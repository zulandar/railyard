package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gopkg.in/yaml.v3"

	"github.com/zulandar/railyard/pkg/plugin"
	protov1 "github.com/zulandar/railyard/pkg/plugin/proto/v1"
)

// hostService implements [protov1.HostServiceServer]. One instance is
// constructed per launched plugin so the per-plugin identity is part of
// the server's state — we do not need to recover it from
// peer.FromContext on every call.
//
// The pluginName is the stable identifier used for log attribution and
// for looking up the plugin's config block. The allow field is the
// resolved per-plugin capability allow-list (railyard-fll.4) consulted
// on every Subscribe and DispatchCommand for runtime enforcement.
type hostService struct {
	protov1.UnimplementedHostServiceServer

	host       *Host
	pluginName string
	logger     *slog.Logger
	allow      AllowList
}

// newHostService constructs a per-plugin HostService server. It is wired
// into the AcceptAndServe callback in launch.go. The allow-list is
// resolved from config at construction time so the hostService can
// enforce Subscribe / DispatchCommand from the moment the plugin process
// dials back — including during the user's Init, before the launched
// plugin entry has been recorded in the host's registry.
func newHostService(h *Host, pluginName string) *hostService {
	var allow AllowList
	if h != nil {
		allow = h.resolveAllowList(pluginName)
	}
	return &hostService{
		host:       h,
		pluginName: pluginName,
		logger:     slog.Default().With(slog.String("plugin", pluginName)),
		allow:      allow,
	}
}

// YardInfo returns the host's cached yard identity.
func (s *hostService) YardInfo(_ context.Context, _ *protov1.YardInfoRequest) (*protov1.YardInfoResponse, error) {
	yi := s.host.YardInfo()
	resp := &protov1.YardInfoResponse{
		YardId:          yi.YardID,
		Owner:           yi.Owner,
		Project:         yi.Project,
		RepoUrl:         yi.RepoURL,
		RailyardVersion: yi.RailyardVersion,
		BuildCommit:     yi.BuildCommit,
	}
	if !yi.BuildTime.IsZero() {
		resp.BuildTime = timestamppb.New(yi.BuildTime)
	}
	return resp, nil
}

// Snapshot delegates to the host's existing snapshot builder and
// converts the Go struct to its proto twin.
func (s *hostService) Snapshot(ctx context.Context, _ *protov1.SnapshotRequest) (*protov1.SnapshotResponse, error) {
	snap, err := s.host.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return &protov1.SnapshotResponse{
		Snapshot: snapshotToProto(snap),
	}, nil
}

// DispatchCommand routes the call through the host's command surface.
// The host's [Host.DispatchCommand] consults the core allow-list first;
// when that misses we look in the plugin-command registry and forward to
// the owning plugin's PluginService.HandleCommand.
//
// The plugin's per-allow-list (railyard-fll.4) is consulted BEFORE
// routing. Commands not permitted by the allow-list are refused with
// gRPC PermissionDenied — the same Commands list controls both what
// the plugin may expose AND what it may invoke from inside its
// process.
func (s *hostService) DispatchCommand(ctx context.Context, req *protov1.DispatchCommandRequest) (*protov1.DispatchCommandResponse, error) {
	if req == nil || req.Name == "" {
		return nil, errors.New("pluginhost: command name is required")
	}
	if !s.allow.AllowCommand(req.Name) {
		s.logger.Warn(
			"pluginhost: DispatchCommand denied",
			slog.String("plugin", s.pluginName),
			slog.String("command", req.Name),
		)
		return nil, status.Errorf(codes.PermissionDenied,
			"pluginhost: plugin %q is not allowed to dispatch command %q",
			s.pluginName, req.Name,
		)
	}
	args := plugin.CommandArgs{}
	if req.Args != nil {
		args = plugin.CommandArgs(req.Args.AsMap())
	}

	// 1) Core allow-list.
	if _, isCore := s.host.allowed[req.Name]; isCore {
		res, err := s.host.DispatchCommand(ctx, req.Name, args)
		if err != nil {
			return &protov1.DispatchCommandResponse{Success: false, Error: err.Error()}, nil
		}
		// DispatchCommand success: record that the dispatching plugin was just active.
		s.host.bumpActivity(s.pluginName)
		return commandResultToDispatch(res)
	}

	// 2) Plugin-registered surface.
	if owner := s.host.lookupPluginByCommand(req.Name); owner != nil {
		// Host-side arg validation (railyard-77h.16). When the owning
		// plugin declared a typed schema via RegisterCommandSpec, validate
		// the dispatched args against it BEFORE issuing the HandleCommand
		// RPC: required keys must be present and each present value must
		// type-check. A violation returns an in-band failure WITHOUT
		// touching the plugin — the RPC is never issued. A command with no
		// stored spec (a bare RegisterCommand, or an old plugin) has a nil
		// spec and skips validation entirely (unchanged behaviour).
		if spec := s.host.lookupPluginCmdSpec(req.Name); spec != nil {
			if err := validatePluginArgs(spec, args); err != nil {
				return &protov1.DispatchCommandResponse{Success: false, Error: err.Error()}, nil
			}
		}
		hcReq := &protov1.HandleCommandRequest{Name: req.Name, Args: req.Args}
		// Per-plugin command counters (railyard-77h.14). We count
		// handled-by-plugin: every invocation of the owner's
		// HandleCommand bumps commandsHandled and accumulates wall-clock
		// latency; a transport error OR a logical failure
		// (!hcResp.Success) bumps commandsFailed. Atomics only — no h.mu.
		start := time.Now()
		hcResp, err := owner.pluginRPC.HandleCommand(ctx, hcReq)
		owner.commandsHandled.Add(1)
		owner.commandLatencyTotalMicros.Add(uint64(time.Since(start).Microseconds()))
		if err != nil {
			owner.commandsFailed.Add(1)
			return &protov1.DispatchCommandResponse{Success: false, Error: err.Error()}, nil
		}
		if !hcResp.Success {
			owner.commandsFailed.Add(1)
		}
		// DispatchCommand success: bump BOTH the dispatching plugin (it just
		// did an RPC) and the owning plugin (its code just ran) under one
		// lock — handlePermanentDisable could otherwise remove the owner
		// between two separate bumpActivity calls and silently drop the
		// owner's final activity.
		s.host.bumpActivityPair(s.pluginName, owner.name)
		return &protov1.DispatchCommandResponse{
			Success: hcResp.Success,
			Error:   hcResp.Error,
			Data:    hcResp.Data,
		}, nil
	}

	return &protov1.DispatchCommandResponse{
		Success: false,
		Error:   fmt.Sprintf("command not allowed: %s", req.Name),
	}, nil
}

// Config returns the named plugin's top-level YAML block from
// railyard.yaml. When no block is set the response carries present=false.
func (s *hostService) Config(_ context.Context, req *protov1.ConfigRequest) (*protov1.ConfigResponse, error) {
	if req == nil || req.Name == "" {
		return &protov1.ConfigResponse{Present: false}, nil
	}
	if s.host.deps.Cfg == nil || s.host.deps.Cfg.PluginConfigs == nil {
		return &protov1.ConfigResponse{Present: false}, nil
	}
	node, ok := s.host.deps.Cfg.PluginConfigs[req.Name]
	if !ok || node.Kind == 0 {
		return &protov1.ConfigResponse{Present: false}, nil
	}
	out, err := yaml.Marshal(&node)
	if err != nil {
		return nil, fmt.Errorf("pluginhost: marshalling config block for %q: %w", req.Name, err)
	}
	return &protov1.ConfigResponse{Present: true, ConfigYaml: out}, nil
}

// Log forwards a structured record to the host's slog handler. The
// `plugin=<name>` attribute is attached on this side so the host's logs
// stay consistent even if a plugin omits it.
func (s *hostService) Log(_ context.Context, req *protov1.LogRequest) (*protov1.LogResponse, error) {
	if req == nil {
		return &protov1.LogResponse{}, nil
	}
	level := slog.Level(req.Level)
	attrs := []slog.Attr{slog.String("plugin", s.pluginName)}
	for k, v := range req.Attrs {
		attrs = append(attrs, slog.String(k, v))
	}
	when := time.Now()
	if req.EmittedAt != nil {
		when = req.EmittedAt.AsTime()
	}
	rec := slog.NewRecord(when, level, req.Message, 0)
	rec.AddAttrs(attrs...)
	_ = slog.Default().Handler().Handle(context.Background(), rec)
	return &protov1.LogResponse{}, nil
}

// EmitEvent publishes a plugin-originated event onto the internal bus
// under a namespaced topic (railyard-77h.9).
//
// Security: the topic MUST be prefixed with the caller's own
// "<pluginName>." derived from the connection-bound identity
// (s.pluginName) — NOT from any request field — so a plugin cannot
// publish into another plugin's namespace. The topic is then gated
// against the plugin's allow.publish list (deny-by-default). Only after
// both checks pass is the payload (a map[string]any) published to the
// bus, where it reaches subscribers via the existing Subscribe stream.
func (s *hostService) EmitEvent(_ context.Context, req *protov1.EmitEventRequest) (*protov1.EmitEventResponse, error) {
	if req == nil || req.Topic == "" {
		return nil, status.Error(codes.InvalidArgument, "pluginhost: EmitEvent requires a topic")
	}
	prefix := s.pluginName + "."
	if !strings.HasPrefix(req.Topic, prefix) {
		s.logger.Warn("pluginhost: EmitEvent rejected: topic outside plugin namespace",
			slog.String("plugin", s.pluginName),
			slog.String("topic", req.Topic),
		)
		return nil, status.Errorf(codes.PermissionDenied,
			"pluginhost: plugin %q may only emit topics prefixed %q (got %q)",
			s.pluginName, prefix, req.Topic,
		)
	}
	if !s.allow.AllowPublish(req.Topic) {
		s.logger.Warn("pluginhost: EmitEvent denied by allow.publish",
			slog.String("plugin", s.pluginName),
			slog.String("topic", req.Topic),
		)
		return nil, status.Errorf(codes.PermissionDenied,
			"pluginhost: plugin %q is not allowed to publish topic %q",
			s.pluginName, req.Topic,
		)
	}
	if s.host == nil || s.host.deps.Bus == nil {
		return nil, status.Error(codes.Unavailable, "pluginhost: EmitEvent: event bus not configured")
	}

	// Payload travels the bus as map[string]any so subscribers receive a
	// dynamic map (no static Go struct exists for plugin topics). A nil
	// payload becomes an empty map so subscribe.go's dynamic encoding
	// path engages rather than dropping the event.
	payload := map[string]any{}
	if req.Payload != nil {
		payload = req.Payload.AsMap()
	}
	s.host.deps.Bus.Publish(req.Topic, payload)
	s.host.bumpActivity(s.pluginName)
	return &protov1.EmitEventResponse{}, nil
}

// commandResultToDispatch converts a [plugin.CommandResult] to the
// wire-shape DispatchCommandResponse.
func commandResultToDispatch(res plugin.CommandResult) (*protov1.DispatchCommandResponse, error) {
	resp := &protov1.DispatchCommandResponse{
		Success: res.Success,
		Error:   res.Error,
	}
	if res.Data != nil {
		argStruct, err := structpb.NewStruct(map[string]any(res.Data))
		if err != nil {
			return nil, fmt.Errorf("pluginhost: encoding command result data: %w", err)
		}
		resp.Data = argStruct
	}
	return resp, nil
}

// snapshotToProto converts a Go *plugin.Snapshot to the wire-shape
// *protov1.Snapshot. Mirrors the inverse conversion in
// pkg/plugin/convert.go.
func snapshotToProto(snap *plugin.Snapshot) *protov1.Snapshot {
	if snap == nil {
		return nil
	}
	out := &protov1.Snapshot{}
	if !snap.Timestamp.IsZero() {
		out.Timestamp = timestamppb.New(snap.Timestamp)
	}
	if len(snap.Tracks) > 0 {
		out.Tracks = make([]*protov1.TrackSnap, 0, len(snap.Tracks))
		for _, t := range snap.Tracks {
			out.Tracks = append(out.Tracks, &protov1.TrackSnap{
				Name:          t.Name,
				Language:      t.Language,
				Slots:         int32(t.Slots),
				ActiveEngines: append([]string(nil), t.ActiveEngines...),
			})
		}
	}
	if len(snap.Engines) > 0 {
		out.Engines = make([]*protov1.EngineSnap, 0, len(snap.Engines))
		for _, e := range snap.Engines {
			es := &protov1.EngineSnap{
				Id:         e.ID,
				Track:      e.Track,
				Status:     e.Status,
				CurrentCar: e.CurrentCar,
			}
			if !e.LastActivity.IsZero() {
				es.LastActivity = timestamppb.New(e.LastActivity)
			}
			out.Engines = append(out.Engines, es)
		}
	}
	out.Cars = &protov1.CarsSnap{}
	if snap.Cars.Counts != nil {
		out.Cars.Counts = make(map[string]int32, len(snap.Cars.Counts))
		for k, v := range snap.Cars.Counts {
			out.Cars.Counts[k] = int32(v)
		}
	}
	if len(snap.Cars.Active) > 0 {
		out.Cars.Active = make([]*protov1.CarSummary, 0, len(snap.Cars.Active))
		for _, c := range snap.Cars.Active {
			cs := &protov1.CarSummary{
				Id:          c.ID,
				Title:       c.Title,
				Track:       c.Track,
				Status:      c.Status,
				Type:        c.Type,
				Priority:    int32(c.Priority),
				Assignee:    c.Assignee,
				Branch:      c.Branch,
				RequestedBy: c.RequestedBy,
			}
			if !c.CreatedAt.IsZero() {
				cs.CreatedAt = timestamppb.New(c.CreatedAt)
			}
			if c.ClaimedAt != nil {
				cs.ClaimedAt = timestamppb.New(*c.ClaimedAt)
			}
			out.Cars.Active = append(out.Cars.Active, cs)
		}
	}
	out.Yardmaster = &protov1.YardmasterSnap{
		Status:     snap.Yardmaster.Status,
		LastAction: snap.Yardmaster.LastAction,
	}
	if !snap.Yardmaster.LastActionAt.IsZero() {
		out.Yardmaster.LastActionAt = timestamppb.New(snap.Yardmaster.LastActionAt)
	}
	out.Stats = &protov1.SnapStats{}
	if snap.Stats.EngineCountsByStatus != nil {
		out.Stats.EngineCountsByStatus = make(map[string]int32, len(snap.Stats.EngineCountsByStatus))
		for k, v := range snap.Stats.EngineCountsByStatus {
			out.Stats.EngineCountsByStatus[k] = int32(v)
		}
	}
	return out
}
