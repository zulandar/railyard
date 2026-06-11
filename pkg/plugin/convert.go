package plugin

import (
	"fmt"

	protov1 "github.com/zulandar/railyard/pkg/plugin/proto/v1"
)

// decodedEvent is the result of demultiplexing a proto Event into the
// matching pkg/plugin topic+payload pair.
type decodedEvent struct {
	topic   EventType
	payload any
}

// decodeEvent converts a wire Event into its Go counterpart. It uses
// the oneof payload arm rather than the discriminator enum because the
// payload is the source of truth: a malformed Event with a discriminator
// set but no payload arm is treated as an error.
func decodeEvent(ev *protov1.Event) (decodedEvent, error) {
	if ev == nil {
		return decodedEvent{}, fmt.Errorf("nil event")
	}
	switch p := ev.Payload.(type) {
	case *protov1.Event_CarCreated:
		if p.CarCreated == nil {
			return decodedEvent{}, fmt.Errorf("CarCreated payload missing")
		}
		return decodedEvent{
			topic: CarCreated,
			payload: CarCreatedEvent{
				CarID:       p.CarCreated.CarId,
				Track:       p.CarCreated.Track,
				Type:        p.CarCreated.Type,
				Priority:    int(p.CarCreated.Priority),
				RequestedBy: p.CarCreated.RequestedBy,
			},
		}, nil
	case *protov1.Event_CarClaimed:
		if p.CarClaimed == nil {
			return decodedEvent{}, fmt.Errorf("CarClaimed payload missing")
		}
		return decodedEvent{
			topic: CarClaimed,
			payload: CarClaimedEvent{
				CarID:    p.CarClaimed.CarId,
				EngineID: p.CarClaimed.EngineId,
			},
		}, nil
	case *protov1.Event_CarStatusChanged:
		if p.CarStatusChanged == nil {
			return decodedEvent{}, fmt.Errorf("CarStatusChanged payload missing")
		}
		return decodedEvent{
			topic: CarStatusChanged,
			payload: CarStatusChangedEvent{
				CarID:     p.CarStatusChanged.CarId,
				OldStatus: p.CarStatusChanged.OldStatus,
				NewStatus: p.CarStatusChanged.NewStatus,
			},
		}, nil
	case *protov1.Event_CarMerged:
		if p.CarMerged == nil {
			return decodedEvent{}, fmt.Errorf("CarMerged payload missing")
		}
		return decodedEvent{
			topic: CarMerged,
			payload: CarMergedEvent{
				CarID:  p.CarMerged.CarId,
				Branch: p.CarMerged.Branch,
			},
		}, nil
	case *protov1.Event_MergeFailed:
		if p.MergeFailed == nil {
			return decodedEvent{}, fmt.Errorf("MergeFailed payload missing")
		}
		return decodedEvent{
			topic: MergeFailed,
			payload: MergeFailedEvent{
				CarID:  p.MergeFailed.CarId,
				Reason: p.MergeFailed.Reason,
			},
		}, nil
	case *protov1.Event_EngineStarted:
		if p.EngineStarted == nil {
			return decodedEvent{}, fmt.Errorf("EngineStarted payload missing")
		}
		return decodedEvent{
			topic: EngineStarted,
			payload: EngineStartedEvent{
				EngineID: p.EngineStarted.EngineId,
				Track:    p.EngineStarted.Track,
			},
		}, nil
	case *protov1.Event_EngineStopped:
		if p.EngineStopped == nil {
			return decodedEvent{}, fmt.Errorf("EngineStopped payload missing")
		}
		return decodedEvent{
			topic: EngineStopped,
			payload: EngineStoppedEvent{
				EngineID: p.EngineStopped.EngineId,
			},
		}, nil
	case *protov1.Event_EngineStalled:
		if p.EngineStalled == nil {
			return decodedEvent{}, fmt.Errorf("EngineStalled payload missing")
		}
		return decodedEvent{
			topic: EngineStalled,
			payload: EngineStalledEvent{
				EngineID:         p.EngineStalled.EngineId,
				LastActivityUnix: p.EngineStalled.LastActivityUnix,
			},
		}, nil
	case *protov1.Event_YardmasterAction:
		if p.YardmasterAction == nil {
			return decodedEvent{}, fmt.Errorf("YardmasterAction payload missing")
		}
		return decodedEvent{
			topic: YardmasterAction,
			payload: YardmasterActionEvent{
				TargetID:   p.YardmasterAction.TargetId,
				ActionType: p.YardmasterAction.ActionType,
			},
		}, nil
	case *protov1.Event_YardPaused:
		if p.YardPaused == nil {
			return decodedEvent{}, fmt.Errorf("YardPaused payload missing")
		}
		return decodedEvent{
			topic:   YardPaused,
			payload: YardPausedEvent{Reason: p.YardPaused.Reason},
		}, nil
	case *protov1.Event_YardResumed:
		if p.YardResumed == nil {
			return decodedEvent{}, fmt.Errorf("YardResumed payload missing")
		}
		return decodedEvent{
			topic:   YardResumed,
			payload: YardResumedEvent{Reason: p.YardResumed.Reason},
		}, nil
	case *protov1.Event_Custom:
		// Plugin-published dynamic event (railyard-77h.9). The topic is
		// the namespaced string in topic_name; the payload is a
		// map[string]any decoded from the custom Struct. A nil Struct
		// decodes to an empty map.
		if ev.TopicName == "" {
			return decodedEvent{}, fmt.Errorf("custom event missing topic_name")
		}
		var payload map[string]any
		if p.Custom != nil {
			payload = p.Custom.AsMap()
		} else {
			payload = map[string]any{}
		}
		return decodedEvent{
			topic:   EventType(ev.TopicName),
			payload: payload,
		}, nil
	default:
		return decodedEvent{}, fmt.Errorf("unknown event payload type")
	}
}

// yardInfoFromProto converts a wire YardInfoResponse to the Go struct.
func yardInfoFromProto(resp *protov1.YardInfoResponse) YardInfo {
	yi := YardInfo{
		YardID:          resp.YardId,
		Owner:           resp.Owner,
		Project:         resp.Project,
		RepoURL:         resp.RepoUrl,
		RailyardVersion: resp.RailyardVersion,
		BuildCommit:     resp.BuildCommit,
	}
	if resp.BuildTime != nil {
		yi.BuildTime = resp.BuildTime.AsTime()
	}
	return yi
}

// snapshotFromProto converts a wire Snapshot to the Go Snapshot struct.
func snapshotFromProto(p *protov1.Snapshot) Snapshot {
	snap := Snapshot{}
	if p.Timestamp != nil {
		snap.Timestamp = p.Timestamp.AsTime()
	}
	if len(p.Tracks) > 0 {
		snap.Tracks = make([]TrackSnap, 0, len(p.Tracks))
		for _, t := range p.Tracks {
			if t == nil {
				continue
			}
			snap.Tracks = append(snap.Tracks, TrackSnap{
				Name:          t.Name,
				Language:      t.Language,
				Slots:         int(t.Slots),
				ActiveEngines: append([]string(nil), t.ActiveEngines...),
			})
		}
	}
	if len(p.Engines) > 0 {
		snap.Engines = make([]EngineSnap, 0, len(p.Engines))
		for _, e := range p.Engines {
			if e == nil {
				continue
			}
			es := EngineSnap{
				ID:         e.Id,
				Track:      e.Track,
				Status:     e.Status,
				CurrentCar: e.CurrentCar,
			}
			if e.LastActivity != nil {
				es.LastActivity = e.LastActivity.AsTime()
			}
			snap.Engines = append(snap.Engines, es)
		}
	}
	if p.Cars != nil {
		snap.Cars = CarsSnap{
			Counts: make(map[string]int, len(p.Cars.Counts)),
		}
		for k, v := range p.Cars.Counts {
			snap.Cars.Counts[k] = int(v)
		}
		if len(p.Cars.Active) > 0 {
			snap.Cars.Active = make([]CarSummary, 0, len(p.Cars.Active))
			for _, c := range p.Cars.Active {
				if c == nil {
					continue
				}
				cs := CarSummary{
					ID:          c.Id,
					Title:       c.Title,
					Track:       c.Track,
					Status:      c.Status,
					Type:        c.Type,
					Priority:    int(c.Priority),
					Assignee:    c.Assignee,
					Branch:      c.Branch,
					RequestedBy: c.RequestedBy,
				}
				if c.CreatedAt != nil {
					cs.CreatedAt = c.CreatedAt.AsTime()
				}
				if c.ClaimedAt != nil {
					ct := c.ClaimedAt.AsTime()
					cs.ClaimedAt = &ct
				}
				snap.Cars.Active = append(snap.Cars.Active, cs)
			}
		}
	}
	if p.Yardmaster != nil {
		snap.Yardmaster = YardmasterSnap{
			Status:     p.Yardmaster.Status,
			LastAction: p.Yardmaster.LastAction,
		}
		if p.Yardmaster.LastActionAt != nil {
			snap.Yardmaster.LastActionAt = p.Yardmaster.LastActionAt.AsTime()
		}
	}
	if p.Stats != nil {
		snap.Stats = SnapStats{
			EngineCountsByStatus: make(map[string]int, len(p.Stats.EngineCountsByStatus)),
		}
		for k, v := range p.Stats.EngineCountsByStatus {
			snap.Stats.EngineCountsByStatus[k] = int(v)
		}
	}
	return snap
}
