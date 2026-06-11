package pluginhost

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/zulandar/railyard/internal/events"
	"github.com/zulandar/railyard/pkg/plugin"
	protov1 "github.com/zulandar/railyard/pkg/plugin/proto/v1"
)

// subscribeQueueCap is the per-stream bounded buffer. It matches the
// underlying events.Bus capacity so a slow plugin contributes the same
// backpressure shape end-to-end.
const subscribeQueueCap = 256

// Subscribe streams events from internal/events.Bus to the plugin. The
// plugin's identity is the per-server pluginName supplied at
// construction time.
//
// Topics in req.Topics are intersected with the plugin's allow-list
// (railyard-fll.4) before wiring up bus subscriptions. Topics not in
// the allow-list are silently filtered out — this is defense in depth
// against a plugin asking for a cap that was already denied at Init.
// If EVERY requested topic is denied (and at least one was requested),
// the RPC returns gRPC PermissionDenied with a structured message
// naming the rejected topics. An empty topic list passes through
// cleanly (the plugin saying "I want nothing").
//
// Each surviving topic is wired to the bus; events are multiplexed
// into a single outbound stream with a bounded buffer. On overflow the
// oldest queued event is dropped and a per-(plugin,topic) drop counter
// is incremented. The counter increment is unconditional (other
// metrics may consume it); WARN-level logging is throttled to at most
// 1/sec per (plugin, topic) via [dropWarner] (railyard-fll.5.2).
//
// The function returns when the client cancels the stream context or
// when [Host.Stop] cancels the stream during shutdown.
func (s *hostService) Subscribe(req *protov1.SubscribeRequest, stream protov1.HostService_SubscribeServer) error {
	if req == nil {
		req = &protov1.SubscribeRequest{}
	}
	if s.host.deps.Bus == nil {
		return fmt.Errorf("pluginhost: subscribe requires a non-nil bus")
	}

	// Allow-list filter (railyard-fll.4). Defense in depth: the Init
	// handshake already filtered the plugin's declared subscriptions,
	// but a plugin might dynamically Subscribe to a topic outside its
	// allow list at runtime. We drop denied topics here and, when EVERY
	// requested topic is denied, surface a PermissionDenied so the
	// plugin sees a clear failure rather than an event stream that
	// produces nothing.
	allowedTopics := make([]string, 0, len(req.Topics))
	deniedTopics := make([]string, 0)
	for _, topic := range req.Topics {
		if topic == "" {
			continue
		}
		if s.allow.AllowEvent(topic) {
			allowedTopics = append(allowedTopics, topic)
		} else {
			deniedTopics = append(deniedTopics, topic)
		}
	}
	if len(deniedTopics) > 0 {
		s.logger.Warn(
			"pluginhost: Subscribe denied topics",
			slog.String("plugin", s.pluginName),
			slog.Any("denied", deniedTopics),
		)
	}
	if len(allowedTopics) == 0 && len(req.Topics) > 0 {
		return status.Errorf(codes.PermissionDenied,
			"pluginhost: plugin %q is not allowed to subscribe to: %s",
			s.pluginName, strings.Join(deniedTopics, ","),
		)
	}

	streamCtx, streamCancel := context.WithCancel(stream.Context())
	defer streamCancel()

	// Register cancel with the launched plugin so Host.Stop can tear us
	// down before draining the plugin's PluginService.Stop.
	if lp := s.host.lookupPluginByName(s.pluginName); lp != nil {
		lp.subMu.Lock()
		lp.subCancels = append(lp.subCancels, streamCancel)
		lp.subMu.Unlock()
	}

	// Subscribe setup success: record that the plugin was just active.
	s.host.bumpActivity(s.pluginName)

	// Multiplexed queue. Buffered so the bus subscriber goroutines can
	// drop into it without blocking even when the gRPC stream is slow.
	queue := make(chan *protov1.Event, subscribeQueueCap)
	drops := newDropCounter(s.pluginName, s.logger)
	warner := newDropWarner(s.logger)

	// droppedTotal is the per-subscription cumulative drop count stamped
	// onto every outgoing Event so the plugin can detect gaps and
	// reconcile via Snapshot (railyard-77h.10). It is bumped from the
	// bus-callback goroutines (one per topic) on the drop-oldest path, so
	// it MUST be atomic; the drain loop reads it with a plain Load. No
	// mutex is taken on the event hot path.
	var droppedTotal atomic.Uint64

	// Wire each allowed topic to the bus. incrSubscription per topic
	// matches the in-process Host.Subscribe shim's per-topic accounting,
	// so Status() reports the right SubscriptionCount for subprocess
	// plugins (the gRPC path used to bypass the counter — see
	// railyard-vdp).
	unsubs := make([]events.Unsubscribe, 0, len(allowedTopics))
	for _, topic := range allowedTopics {
		t := topic // capture
		unsub := s.host.deps.Bus.Subscribe(t, func(payload any) {
			ev, ok := payloadToProto(t, payload)
			if !ok {
				return
			}
			select {
			case queue <- ev:
			default:
				// Drop oldest, push new. Match the bus's drop-oldest
				// semantics so a slow plugin doesn't permanently wedge.
				select {
				case <-queue:
				default:
				}
				select {
				case queue <- ev:
				default:
				}
				drops.record(t)
				droppedTotal.Add(1)
				warner.recordDrop(s.pluginName, t)
			}
		})
		unsubs = append(unsubs, unsub)
		s.host.incrSubscription(s.pluginName)
	}

	// Cleanup. Defers fire in LIFO order: unsubscribe → close queue →
	// drain goroutine exits. Decrement the per-plugin counter for every
	// topic we incremented so Status() returns to zero when the stream
	// ends.
	defer func() {
		for _, u := range unsubs {
			u()
			s.host.decrSubscription(s.pluginName)
		}
		close(queue)
	}()

	// Drain loop on the calling goroutine. Returning from this function
	// causes go-plugin's stream wrapper to close the stream cleanly.
	//
	// seq counts events DELIVERED on this stream (starting at 1); it is
	// touched only here, on a single goroutine, so it needs no
	// synchronization. dropped is the cumulative drop count at delivery
	// time (railyard-77h.10).
	var seq uint64
	for {
		select {
		case <-streamCtx.Done():
			return nil
		case ev, ok := <-queue:
			if !ok {
				return nil
			}
			seq++
			ev.Seq = seq
			ev.Dropped = droppedTotal.Load()
			if err := stream.Send(ev); err != nil {
				return err
			}
		}
	}
}

// dropCounter tallies per-topic dropped events. The counter survives
// independent of the WARN log throttling in [dropWarner] because other
// metrics consumers (e.g. future Prometheus exporters) may want the
// raw total without any time-windowed rollup applied.
type dropCounter struct {
	pluginName string
	logger     *slog.Logger
	mu         sync.Mutex
	count      map[string]*atomic.Int64
}

func newDropCounter(pluginName string, logger *slog.Logger) *dropCounter {
	return &dropCounter{
		pluginName: pluginName,
		logger:     logger,
		count:      make(map[string]*atomic.Int64),
	}
}

// record increments the running total of drops for `topic`. The
// throttled WARN log is emitted by [dropWarner.recordDrop]; this
// method intentionally does NOT log so the counter stays cheap.
func (d *dropCounter) record(topic string) {
	d.mu.Lock()
	ctr, ok := d.count[topic]
	if !ok {
		ctr = &atomic.Int64{}
		d.count[topic] = ctr
	}
	d.mu.Unlock()
	ctr.Add(1)
}

// payloadToProto converts a bus event payload to its wire shape. The
// topic string is matched against the [plugin.EventType] constants; an
// unknown topic returns ok=false and the caller drops the record.
func payloadToProto(topic string, payload any) (*protov1.Event, bool) {
	ev := &protov1.Event{
		EmittedAt: timestamppb.New(time.Now()),
	}
	switch plugin.EventType(topic) {
	case plugin.CarCreated:
		p, ok := payload.(plugin.CarCreatedEvent)
		if !ok {
			return nil, false
		}
		ev.Type = protov1.EventType_EVENT_TYPE_CAR_CREATED
		ev.Payload = &protov1.Event_CarCreated{CarCreated: &protov1.CarCreatedEvent{
			CarId:       p.CarID,
			Track:       p.Track,
			Type:        p.Type,
			Priority:    int32(p.Priority),
			RequestedBy: p.RequestedBy,
		}}
	case plugin.CarClaimed:
		p, ok := payload.(plugin.CarClaimedEvent)
		if !ok {
			return nil, false
		}
		ev.Type = protov1.EventType_EVENT_TYPE_CAR_CLAIMED
		ev.Payload = &protov1.Event_CarClaimed{CarClaimed: &protov1.CarClaimedEvent{
			CarId:    p.CarID,
			EngineId: p.EngineID,
		}}
	case plugin.CarStatusChanged:
		p, ok := payload.(plugin.CarStatusChangedEvent)
		if !ok {
			return nil, false
		}
		ev.Type = protov1.EventType_EVENT_TYPE_CAR_STATUS_CHANGED
		ev.Payload = &protov1.Event_CarStatusChanged{CarStatusChanged: &protov1.CarStatusChangedEvent{
			CarId:     p.CarID,
			OldStatus: p.OldStatus,
			NewStatus: p.NewStatus,
		}}
	case plugin.CarMerged:
		p, ok := payload.(plugin.CarMergedEvent)
		if !ok {
			return nil, false
		}
		ev.Type = protov1.EventType_EVENT_TYPE_CAR_MERGED
		ev.Payload = &protov1.Event_CarMerged{CarMerged: &protov1.CarMergedEvent{
			CarId:  p.CarID,
			Branch: p.Branch,
		}}
	case plugin.MergeFailed:
		p, ok := payload.(plugin.MergeFailedEvent)
		if !ok {
			return nil, false
		}
		ev.Type = protov1.EventType_EVENT_TYPE_MERGE_FAILED
		ev.Payload = &protov1.Event_MergeFailed{MergeFailed: &protov1.MergeFailedEvent{
			CarId:  p.CarID,
			Reason: p.Reason,
		}}
	case plugin.EngineStarted:
		p, ok := payload.(plugin.EngineStartedEvent)
		if !ok {
			return nil, false
		}
		ev.Type = protov1.EventType_EVENT_TYPE_ENGINE_STARTED
		ev.Payload = &protov1.Event_EngineStarted{EngineStarted: &protov1.EngineStartedEvent{
			EngineId: p.EngineID,
			Track:    p.Track,
		}}
	case plugin.EngineStopped:
		p, ok := payload.(plugin.EngineStoppedEvent)
		if !ok {
			return nil, false
		}
		ev.Type = protov1.EventType_EVENT_TYPE_ENGINE_STOPPED
		ev.Payload = &protov1.Event_EngineStopped{EngineStopped: &protov1.EngineStoppedEvent{
			EngineId: p.EngineID,
		}}
	case plugin.EngineStalled:
		p, ok := payload.(plugin.EngineStalledEvent)
		if !ok {
			return nil, false
		}
		ev.Type = protov1.EventType_EVENT_TYPE_ENGINE_STALLED
		ev.Payload = &protov1.Event_EngineStalled{EngineStalled: &protov1.EngineStalledEvent{
			EngineId:         p.EngineID,
			LastActivityUnix: p.LastActivityUnix,
		}}
	case plugin.YardmasterAction:
		p, ok := payload.(plugin.YardmasterActionEvent)
		if !ok {
			return nil, false
		}
		ev.Type = protov1.EventType_EVENT_TYPE_YARDMASTER_ACTION
		ev.Payload = &protov1.Event_YardmasterAction{YardmasterAction: &protov1.YardmasterActionEvent{
			TargetId:   p.TargetID,
			ActionType: p.ActionType,
		}}
	case plugin.YardPaused:
		p, ok := payload.(plugin.YardPausedEvent)
		if !ok {
			return nil, false
		}
		ev.Type = protov1.EventType_EVENT_TYPE_YARD_PAUSED
		ev.Payload = &protov1.Event_YardPaused{YardPaused: &protov1.YardPausedEvent{Reason: p.Reason}}
	case plugin.YardResumed:
		p, ok := payload.(plugin.YardResumedEvent)
		if !ok {
			return nil, false
		}
		ev.Type = protov1.EventType_EVENT_TYPE_YARD_RESUMED
		ev.Payload = &protov1.Event_YardResumed{YardResumed: &protov1.YardResumedEvent{Reason: p.Reason}}
	default:
		// Plugin-published dynamic event (railyard-77h.9). The topic is
		// not one of the core EventType constants; its payload is a
		// map[string]any emitted via HostService.EmitEvent. Carry the
		// namespaced topic in topic_name and the map in the custom Struct
		// arm. Any other payload shape on an unknown topic is dropped.
		m, ok := payload.(map[string]any)
		if !ok {
			return nil, false
		}
		st, err := structpb.NewStruct(m)
		if err != nil {
			return nil, false
		}
		ev.Type = protov1.EventType_EVENT_TYPE_UNSPECIFIED
		ev.TopicName = topic
		ev.Payload = &protov1.Event_Custom{Custom: st}
	}
	return ev, true
}
