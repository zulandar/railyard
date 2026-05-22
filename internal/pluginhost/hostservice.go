package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

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
// for looking up the plugin's config block.
type hostService struct {
	protov1.UnimplementedHostServiceServer

	host       *Host
	pluginName string
	logger     *slog.Logger
}

// newHostService constructs a per-plugin HostService server. It is wired
// into the AcceptAndServe callback in launch.go.
func newHostService(h *Host, pluginName string) *hostService {
	return &hostService{
		host:       h,
		pluginName: pluginName,
		logger:     slog.Default().With(slog.String("plugin", pluginName)),
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
func (s *hostService) DispatchCommand(ctx context.Context, req *protov1.DispatchCommandRequest) (*protov1.DispatchCommandResponse, error) {
	if req == nil || req.Name == "" {
		return nil, errors.New("pluginhost: command name is required")
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
		return commandResultToDispatch(res)
	}

	// 2) Plugin-registered surface.
	if owner := s.host.lookupPluginByCommand(req.Name); owner != nil {
		hcReq := &protov1.HandleCommandRequest{Name: req.Name, Args: req.Args}
		hcResp, err := owner.pluginRPC.HandleCommand(ctx, hcReq)
		if err != nil {
			return &protov1.DispatchCommandResponse{Success: false, Error: err.Error()}, nil
		}
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
