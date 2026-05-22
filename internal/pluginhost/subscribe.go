package pluginhost

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

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
// Topics in req.Topics are taken literally for now; the allow-list
// intersection lands in railyard-fll.4 (Lane F). Each topic is wired to
// the bus through Subscribe; events are multiplexed into a single
// outbound stream with a bounded buffer. On overflow the oldest queued
// event is dropped and a per-(plugin,topic) drop counter is incremented;
// a throttled WARN log is deferred to railyard-fll.5.2 — for now we DEBUG
// every drop and count it.
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

	streamCtx, streamCancel := context.WithCancel(stream.Context())
	defer streamCancel()

	// Register cancel with the launched plugin so Host.Stop can tear us
	// down before draining the plugin's PluginService.Stop.
	if lp := s.host.lookupPluginByName(s.pluginName); lp != nil {
		lp.subMu.Lock()
		lp.subCancels = append(lp.subCancels, streamCancel)
		lp.subMu.Unlock()
	}

	// Multiplexed queue. Buffered so the bus subscriber goroutines can
	// drop into it without blocking even when the gRPC stream is slow.
	queue := make(chan *protov1.Event, subscribeQueueCap)
	drops := newDropCounter(s.pluginName, s.logger)

	// Wire each requested topic to the bus.
	unsubs := make([]events.Unsubscribe, 0, len(req.Topics))
	for _, topic := range req.Topics {
		if topic == "" {
			continue
		}
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
			}
		})
		unsubs = append(unsubs, unsub)
	}

	// Cleanup. Defers fire in LIFO order: unsubscribe → close queue →
	// drain goroutine exits.
	defer func() {
		for _, u := range unsubs {
			u()
		}
		close(queue)
	}()

	// Drain loop on the calling goroutine. Returning from this function
	// causes go-plugin's stream wrapper to close the stream cleanly.
	for {
		select {
		case <-streamCtx.Done():
			return nil
		case ev, ok := <-queue:
			if !ok {
				return nil
			}
			if err := stream.Send(ev); err != nil {
				return err
			}
		}
	}
}

// dropCounter tallies per-topic dropped events with throttled DEBUG
// logging. The throttled WARN that operators care about is wired in
// railyard-fll.5.2; this stub increments the counter and logs at DEBUG
// only.
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

func (d *dropCounter) record(topic string) {
	d.mu.Lock()
	ctr, ok := d.count[topic]
	if !ok {
		ctr = &atomic.Int64{}
		d.count[topic] = ctr
	}
	d.mu.Unlock()
	n := ctr.Add(1)
	d.logger.Debug("pluginhost: subscribe queue overflow",
		slog.String("topic", topic),
		slog.Int64("dropped", n),
	)
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
		return nil, false
	}
	return ev, true
}
