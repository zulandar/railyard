# Telegraph — Chat Platform Bridge for Railyard

## Overview

Telegraph is a bidirectional communication bridge between Railyard and external chat platforms (Slack, Discord). It enables remote interaction with Dispatch (conversational work planning) and push notifications from Yardmaster (status events, stall alerts, periodic digests) — all without exposing inbound ports.

**Naming:** Railroad companies pioneered telegraph lines along their tracks for long-distance coordination between stations and yards. Telegraph is how the outside world communicates with the yard.

| Telegraph Term | Meaning |
|---|---|
| **Telegraph** | The chat bridge daemon — connects Railyard to Slack/Discord |
| **Adapter** | Platform-specific connector (Slack Socket Mode, Discord Gateway) |
| **Remote Dispatch** | A dispatch session initiated from the chat platform |
| **Dispatch Lock** | DB-level mutex preventing simultaneous car creation |
| **Pulse** | Short-interval status digest (default 30m) |

**CLI namespace:** `ry telegraph` (alias `ry tg`)

---

## Architecture

```
┌─ Chat Platform (Slack/Discord) ─────────────────────┐
│                                                       │
│  #railyard-ops channel                               │
│  ├─ Thread: "Build user auth" (dispatch session)     │
│  ├─ Thread: "Add search feature" (dispatch session)  │
│  └─ Status messages from Yardmaster                  │
│                                                       │
└───────────────────┬───────────────────────────────────┘
                    │ outbound WebSocket (no inbound ports)
                    │
┌───────────────────▼───────────────────────────────────┐
│  Telegraph Daemon (ry telegraph start)                │
│                                                       │
│  ┌─────────────────┐  ┌──────────────────────────┐   │
│  │ Platform Adapter │  │ Event Watcher            │   │
│  │ (Slack/Discord)  │  │ polls Dolt every 10-30s  │   │
│  │ outbound socket  │  │ detects: car changes,    │   │
│  └────────┬────────┘  │ stalls, merges, messages  │   │
│           │            │ to "human" or "telegraph" │   │
│           │            └────────────┬─────────────┘   │
│  ┌────────▼─────────────────────────▼──────────────┐  │
│  │ Router                                          │  │
│  │ - Inbound messages → Dispatch Lock → Session Mgr│  │
│  │ - Outbound events → Format → Platform Adapter   │  │
│  └────────┬─────────────────────────┬──────────────┘  │
│  ┌────────▼────────┐  ┌────────────▼───────────────┐  │
│  │ Session Manager │  │ Conversation Store         │  │
│  │ spawn/resume    │  │ dual-write: Dolt + thread  │  │
│  │ dispatch procs  │  │ recovery from either       │  │
│  └────────┬────────┘  └────────────────────────────┘  │
│           │                                           │
│  ┌────────▼────────┐                                  │
│  │ Remote Dispatch │ (spawned on-demand per thread)   │
│  │ Claude Code     │                                  │
│  │ subprocess      │                                  │
│  └─────────────────┘                                  │
│                                                       │
└───────────────────────┬───────────────────────────────┘
                        │ GORM / MySQL
                ┌───────▼───────┐
                │   Dolt DB     │
                └───────────────┘
```

### Key Constraints

- **No inbound ports:** Telegraph initiates all connections outward. Slack Socket Mode and Discord Gateway are both outbound WebSocket connections.
- **Outbound-only networking:** The machine running Railyard can reach the internet but is not reachable from outside.
- **Platform-agnostic core:** All platform-specific logic lives in adapter packages. Telegraph core is testable with a mock adapter.

---

## Relationship to Existing Components

### Change to `ry start`

Telegraph requires a change to how Dispatch is launched. Previously, `ry start` auto-launched Dispatch in a separate tmux session. With Telegraph, Dispatch becomes on-demand.

**Before:**
```
ry start → launches Dispatch (always-on) + Yardmaster + Engines
```

**After:**
```
ry start → launches Yardmaster + Engines + Telegraph (if configured)
ry dispatch → on-demand local planning session (acquires lock, releases on exit)
```

`ry dispatch` still creates a separate `railyard-dispatch` tmux session (not co-located with engine/yardmaster panes). The only change is it's no longer auto-launched — the user runs it when they want a local planning session.

This change is necessary because:
- If local Dispatch is always running, it either holds the dispatch lock permanently (blocking Telegraph) or doesn't participate in the lock (allowing conflicting car creation).
- On-demand dispatch means the lock is only held during active planning sessions, whether local or remote.

### Integration with Existing Notification System

The current `internal/messaging/notify.go` uses a shell command template (`notify-send`) for desktop notifications. Telegraph supplements but does not replace this. If both are configured, both fire. Telegraph picks up messages addressed to `"human"` or `"telegraph"` from the messages table.

---

## Package Structure

```
internal/telegraph/
    telegraph.go         # Daemon lifecycle: Start, Stop, Run loop
    adapter.go           # Platform adapter interface
    router.go            # Inbound message routing + dispatch lock
    watcher.go           # Outbound event detection (polls Dolt)
    session.go           # Dispatch session manager (spawn, resume, kill)
    conversation.go      # Conversation persistence (dual-write Dolt + thread)
    format.go            # Message formatting (events → rich chat messages)
    telegraph_test.go    # Daemon integration tests (mock adapter + test DB)
    router_test.go       # Routing logic tests
    watcher_test.go      # Event detection tests
    session_test.go      # Lock and session lifecycle tests
    conversation_test.go # Persistence and recovery tests
    format_test.go       # Formatting pure function tests

internal/telegraph/slack/
    slack.go             # Slack Socket Mode adapter
    slack_test.go        # Slack-specific formatting and SDK mock tests

internal/telegraph/discord/
    discord.go           # Discord Gateway adapter
    discord_test.go      # Discord-specific formatting and SDK mock tests

cmd/ry/
    telegraph.go         # CLI: ry telegraph start/stop/status
```

---

## Platform Adapter Interface

```go
// Adapter is the platform-agnostic interface for chat platforms.
// Implementations connect outbound only (no exposed ports).
type Adapter interface {
    // Connect establishes outbound connection to the platform.
    Connect(ctx context.Context) error

    // Listen returns a channel of inbound messages from the platform.
    Listen() <-chan InboundMessage

    // Send posts a message to a channel or thread.
    Send(ctx context.Context, msg OutboundMessage) error

    // ThreadHistory retrieves message history for a thread (fallback recovery).
    ThreadHistory(ctx context.Context, threadID string) ([]ThreadMessage, error)

    // Close gracefully disconnects.
    Close() error
}

type InboundMessage struct {
    PlatformThreadID string   // Slack thread_ts / Discord thread channel ID
    UserID           string   // Who sent it
    UserName         string   // Display name
    Text             string   // Message content
    ChannelID        string   // Which channel
    IsCommand        bool     // Starts with !ry or ry:
    Timestamp        string   // Platform-specific timestamp
}

type OutboundMessage struct {
    ChannelID   string   // Target channel
    ThreadID    string   // Reply in thread (empty = new message)
    Text        string   // Plain text fallback
    RichText    string   // Markdown/Block Kit/Embed formatted
    MessageType string   // "status", "alert", "dispatch_response", "digest"
}

type ThreadMessage struct {
    UserID    string
    UserName  string
    Text      string
    Timestamp string
    IsBot     bool
}
```

---

## Dispatch Lock & Session Management

### Dolt Schema

```go
// DispatchSession tracks active and historical dispatch sessions.
type DispatchSession struct {
    ID               string     `gorm:"primaryKey;size:64"`
    Source           string     `gorm:"size:16;not null"`        // "local", "telegraph"
    UserName         string     `gorm:"size:64"`
    PlatformThreadID string     `gorm:"size:128"`               // chat thread ID (empty for local)
    ChannelID        string     `gorm:"size:128"`
    Status           string     `gorm:"size:16;default:active"` // active, completed, expired, cancelled
    CarsCreated      string     `gorm:"type:json"`              // JSON array of car IDs
    LastHeartbeat    time.Time  `gorm:"index"`
    CreatedAt        time.Time
    CompletedAt      *time.Time
}

// TelegraphConversation stores individual messages in a dispatch session.
type TelegraphConversation struct {
    ID             uint      `gorm:"primaryKey;autoIncrement"`
    SessionID      string    `gorm:"size:64;index;not null"`   // FK to DispatchSession.ID
    Sequence       int       `gorm:"not null"`                 // message order
    Role           string    `gorm:"size:16;not null"`         // "user", "assistant", "system"
    UserName       string    `gorm:"size:64"`
    Content        string    `gorm:"type:text;not null"`
    PlatformMsgID  string    `gorm:"size:128"`                 // slack/discord message ID
    CarsReferenced string    `gorm:"type:json"`                // car IDs mentioned/created
    CreatedAt      time.Time
}
```

### Lock Semantics

The dispatch lock prevents simultaneous car creation from local and remote dispatch. Key properties:

- **Lock is per-session, not per-process.** Telegraph running does NOT hold the lock. The lock is only held when an active dispatch conversation is in progress.
- **Heartbeat expiry.** If a session stops heartbeating for 90s (3 missed heartbeats at 30s interval), the lock auto-expires. Prevents stuck state.
- **Read-only commands bypass the lock.** Status queries (`!ry status`, `!ry car list`) do not require the lock — only car creation/modification.

### Lock Acquisition Flow

```
Inbound message arrives in thread
    │
    ▼
Is there an active DispatchSession for this thread?
    ├─ YES → Route message to existing session (resume)
    │
    └─ NO → Is there ANY active DispatchSession (any source)?
              ├─ YES, heartbeat fresh
              │     → Reply: "Dispatch is busy (local session by alice,
              │       started 3m ago). Your request has been queued."
              │     → Write to pending queue in Dolt
              │
              ├─ YES, heartbeat expired (stale)
              │     → Mark old session as "expired"
              │     → Acquire lock with new session
              │
              └─ NO → Acquire lock, create new DispatchSession
```

### Lock Acquisition Implementation

```go
func AcquireLock(db *gorm.DB, source, userName, threadID, channelID string) (*DispatchSession, *DispatchSession, error) {
    var blocking DispatchSession
    err := db.Where("status = ? AND last_heartbeat > ?", "active",
        time.Now().Add(-heartbeatTimeout)).First(&blocking).Error

    if err == nil {
        return nil, &blocking, nil // someone else holds the lock
    }

    // Expire stale sessions
    db.Model(&DispatchSession{}).
        Where("status = ? AND last_heartbeat < ?", "active",
            time.Now().Add(-heartbeatTimeout)).
        Update("status", "expired")

    // Create new session
    session := &DispatchSession{
        ID:               generateID("ds"),
        Source:           source,
        UserName:         userName,
        PlatformThreadID: threadID,
        ChannelID:        channelID,
        Status:           "active",
        LastHeartbeat:    time.Now(),
    }
    return session, nil, db.Create(session).Error
}
```

### `ry dispatch` Lock Integration

When `ry dispatch` runs:
1. Acquires the dispatch lock with `source=local`
2. Starts heartbeating in a background goroutine (every 30s)
3. Creates the `railyard-dispatch` tmux session and launches Claude Code
4. On exit: releases lock, marks session completed

If Telegraph has an active remote dispatch session:
```
$ ry dispatch
Error: Dispatch lock held by Telegraph (remote session by alice in #railyard-ops, started 2m ago).
Use --force to take over, or wait for the remote session to complete.
```

---

## Remote Dispatch Process

When Telegraph acquires the lock for a chat thread, it spawns a Remote Dispatch subprocess:

```
Telegraph daemon
    │
    ├─ Spawns: claude --print --system-prompt <dispatch_prompt>
    │          with stdin/stdout piped
    │
    ├─ stdin  ← Telegraph writes user messages from chat thread
    ├─ stdout → Telegraph reads responses, sends to chat thread
    │           AND dual-writes to Dolt conversation store
    │
    ├─ Heartbeat: Telegraph updates DispatchSession.LastHeartbeat
    │             every 30s while subprocess is alive
    │
    └─ On exit: Telegraph marks session completed,
                releases lock, records CarsCreated
```

The dispatch prompt is the same as local Dispatch (from `internal/dispatch/prompt.go`), with an addition telling it to use compact formatting since responses go to a chat platform.

### Conversation Modes

**Conversational mode:** User @mentions the bot with a work request. Telegraph starts a thread, spawns Remote Dispatch, and the conversation flows naturally with follow-up questions and iterative refinement.

**Command mode:** User sends `!ry status`, `!ry car list`, etc. Telegraph executes the equivalent `ry` CLI call, formats the result, and replies. No dispatch lock needed — read-only.

---

## Conversation Persistence & Recovery

### Dual-Write Flow

Every message in a dispatch conversation is written to both Dolt (primary) and the chat platform thread (fallback):

```
User sends message in Slack thread
    │
    ├─ Write to Dolt: TelegraphConversation{Role: "user", ...}
    ├─ Forward to Remote Dispatch subprocess stdin
    │
    ▼
Remote Dispatch responds via stdout
    │
    ├─ Write to Dolt: TelegraphConversation{Role: "assistant", ...}
    └─ Send to Slack thread via adapter.Send()
```

### Recovery Flow

When a message arrives in a thread whose dispatch session has died/expired:

1. Load conversation history from Dolt (`telegraph_conversations` table) — **primary source**
2. If Dolt record is missing, fall back to `adapter.ThreadHistory(threadID)` from the chat platform
3. Load current status of cars created in the original session
4. Build recovery context and inject into a fresh dispatch subprocess
5. Acquire new lock, create new `DispatchSession` linked to the same thread
6. Route the new message to the fresh subprocess

### Recovery Context

```markdown
# Resuming Previous Dispatch Session

## Previous Conversation Summary
{conversation turns from Dolt, condensed if long}

## Cars Created in Previous Session
- car-a1b2c "Add JWT auth middleware" [backend] — status: in_progress
- car-a1b3d "Login page component" [frontend] — status: ready

## New Message from User
{the message that triggered the resume}
```

---

## Event Watcher (Outbound)

The Event Watcher polls Dolt for changes and pushes them to the chat platform.

### Event Categories

**1. Car Lifecycle Events**

Detected by polling `cars` table for status changes since last check. Telegraph maintains a `last_poll_at` timestamp and compares against an in-memory snapshot.

Events emitted:
- Car claimed (`open` → `claimed`/`in_progress`)
- Car completed (`in_progress` → `done`)
- Car merged (`done` → `merged`)
- Car blocked (`*` → `blocked`)
- Car merge failed (`done` → `merge-failed`)

**2. Engine Stalls & Escalations**

Detected by polling engines table and messages addressed to `human` or `telegraph`:

```sql
SELECT * FROM engines WHERE status = 'stalled' AND last_activity > @last_poll_at;

SELECT * FROM messages
WHERE to_agent IN ('human', 'telegraph') AND acknowledged = FALSE
ORDER BY created_at;
```

**3. Periodic Digests — Three Tiers**

| Tier | Default Interval | Content | Suppressed When |
|---|---|---|---|
| **Pulse** | 30 min | Engines active, cars in progress/ready/done since last pulse | Nothing changed AND no active work |
| **Daily** | Configurable time (e.g., 9:00 AM) | Cars completed, merged, created. Token usage. Stalls. Utilization. | No activity in past 24h |
| **Weekly** | Configurable day/time (e.g., Monday 9 AM) | Full report: total closed, merge success rate, token spend, per-track breakdown, stall frequency, avg completion time | No activity in past week |

Pulse digests are **suppressed when idle** — if all cars are closed out, no engines are working, and nothing changed since the last pulse, no notification is sent. Digests resume automatically when new work appears.

### Watcher Loop

```go
func (w *Watcher) Run(ctx context.Context) error {
    pollTicker := time.NewTicker(w.cfg.PollInterval)
    pulseTicker := time.NewTicker(w.cfg.Digest.Pulse.Interval)
    dailyScheduler := newDailyScheduler(w.cfg.Digest.Daily)
    weeklyScheduler := newWeeklyScheduler(w.cfg.Digest.Weekly)

    for {
        select {
        case <-ctx.Done():
            return nil
        case <-pollTicker.C:
            events := w.detectCarEvents()
            events = append(events, w.detectStalls()...)
            events = append(events, w.detectEscalations()...)
            for _, e := range events {
                w.adapter.Send(ctx, w.format(e))
            }
        case <-pulseTicker.C:
            if status := w.buildPulse(); status != nil {
                w.adapter.Send(ctx, w.formatPulse(status))
            }
        case <-dailyScheduler.C:
            if report := w.buildDailyReport(); report != nil {
                w.adapter.Send(ctx, w.formatDaily(report))
            }
        case <-weeklyScheduler.C:
            if report := w.buildWeeklyReport(); report != nil {
                w.adapter.Send(ctx, w.formatWeekly(report))
            }
        }
    }
}
```

### Message Formatting

Telegraph produces a platform-neutral rich format that adapters translate to native formatting (Slack Block Kit, Discord Embeds):

```go
type FormattedEvent struct {
    ChannelID string
    ThreadID  string   // empty = post to channel
    Title     string   // bold header
    Body      string   // detail text (markdown)
    Severity  string   // "info", "warning", "critical"
    Fields    []Field  // structured key-value pairs
}

type Field struct {
    Label string
    Value string
    Short bool   // side-by-side if platform supports it
}
```

### Example Chat Output

**Car completed:**
```
Car completed: car-a1b2c "Add JWT auth middleware"
Track: backend | Engine: eng-03 | Branch: ry/alice/backend/car-a1b2c
Yardmaster will run tests and merge.
```

**Engine stalled:**
```
Engine stalled: eng-05
Car: car-f02 "Login page component" (frontend)
Reason: No stdout for 120s
Yardmaster attempting reassignment.
```

**Merged to main:**
```
Merged to main: car-a1b2c "Add JWT auth middleware"
Branch ry/alice/backend/car-a1b2c → main
3 downstream cars unblocked.
```

**Pulse digest (30m):**
```
Railyard Status
Engines: 3 active, 0 stalled
Cars:    2 in progress, 5 ready, 12 done
Tracks:  backend (3 ready), frontend (2 ready)
Merged:  2 cars since last pulse
Tokens:  142,350 total (48,200 input / 94,150 output)
```

**Weekly report:**
```
Railyard Weekly Report (Feb 14 - Feb 21)

SUMMARY
  Cars created:     18
  Cars completed:   14
  Cars merged:      12
  Cars still open:  6 (4 ready, 2 blocked)
  Merge success:    92% (12/13 attempts, 1 conflict)

TRACKS
  backend:    8 completed, 2 open    avg completion: 23m
  frontend:   5 completed, 3 open    avg completion: 31m
  infra:      1 completed, 1 open    avg completion: 15m

ENGINES
  Total uptime:     142h across 3 engines
  Stalls:           2 (both recovered automatically)
  /clear cycles:    avg 1.8 per car

TOKENS
  Input:      284,500
  Output:     512,300
  Total:      796,800
  Per car:    ~56,900 avg
```

---

## Slack Adapter

### Connection Model

Slack Socket Mode — the app connects outbound to Slack's WebSocket endpoint. No HTTP server, no public URLs.

**Bot Token Scopes:**
- `chat:write` — post messages and replies
- `channels:history` — read thread history (recovery fallback)
- `channels:read` — list channels
- `app_mentions:read` — respond when @mentioned
- `users:read` — resolve user display names

**Socket Mode:** Enabled (no Request URL needed)

**Event Subscriptions (via Socket Mode):**
- `message.channels` — messages in channels the bot is in
- `app_mention` — when someone @mentions the bot

**Go dependency:** `github.com/slack-go/slack`

### Message Routing

```
Message arrives in #railyard-ops
    │
    ├─ From bot itself? → Ignore (prevent loops)
    ├─ Thread reply in existing dispatch thread? → Session Manager
    ├─ @mention of bot? → Router (new dispatch or command)
    ├─ Starts with "!ry"? → Router (command mode)
    └─ Regular channel chatter → Ignore
```

### Rich Formatting

Slack Block Kit is used for structured output. The `FormattedEvent` type maps to Block Kit sections, context blocks, and fields.

---

## Discord Adapter

### Connection Model

Discord Gateway API — outbound WebSocket, same constraint compliance as Slack.

**Bot Permissions:**
- Send Messages
- Create Public Threads (for dispatch sessions)
- Read Message History
- Embed Links

**Go dependency:** `github.com/bwmarrin/discordgo`

### Key Differences from Slack

- **Threads:** Slack threads use `thread_ts` (timestamp-based). Discord threads are channel objects — created via `MessageThreadStart` for new dispatch sessions.
- **Rich formatting:** Discord uses Embeds instead of Slack Block Kit. Different structure, same concept.
- **Channel references:** Discord uses numeric IDs, not channel names.

---

## Daemon Lifecycle

### Main Loop

```go
type Daemon struct {
    db         *gorm.DB
    cfg        *config.Config
    adapter    Adapter
    router     *Router
    watcher    *Watcher
    sessionMgr *SessionManager
    convStore  *ConversationStore
}

func (d *Daemon) Run(ctx context.Context) error {
    if err := d.adapter.Connect(ctx); err != nil {
        return fmt.Errorf("telegraph: connect: %w", err)
    }
    defer d.adapter.Close()

    d.adapter.Send(ctx, OutboundMessage{
        ChannelID: d.cfg.Telegraph.Channel,
        Text:      "Telegraph online. Railyard connected.",
    })

    go d.watcher.Run(ctx)

    for {
        select {
        case <-ctx.Done():
            d.adapter.Send(ctx, OutboundMessage{
                ChannelID: d.cfg.Telegraph.Channel,
                Text:      "Telegraph shutting down.",
            })
            return nil
        case msg := <-d.adapter.Listen():
            d.router.Handle(ctx, msg)
        }
    }
}
```

### CLI Commands

```bash
ry telegraph start -c railyard.yaml     # Start daemon (standalone)
ry start -c railyard.yaml --telegraph   # Start as part of full orchestration
ry telegraph status                     # Health check
ry telegraph stop                       # Graceful shutdown
ry tg start                             # Alias
```

### Integration with `ry start`

Telegraph runs as another pane in the `railyard` tmux session (alongside Yardmaster and engines), or as a standalone process. Auto-detected from `telegraph:` presence in config.

### Integration with `ry stop`

`Stop()` sends shutdown signal to Telegraph, which:
1. Posts "Telegraph shutting down" to the channel
2. Marks any active dispatch sessions as cancelled
3. Closes the chat platform connection gracefully

---

## Testing Strategy

### Mock Adapter

All Telegraph core logic is tested against a mock adapter — no chat platform connection needed.

```go
type MockAdapter struct {
    inbound   chan telegraph.InboundMessage
    sent      []telegraph.OutboundMessage
    history   map[string][]telegraph.ThreadMessage
    connected bool
}

func (m *MockAdapter) SimulateInbound(msg telegraph.InboundMessage) {
    m.inbound <- msg
}

func (m *MockAdapter) LastSent() telegraph.OutboundMessage {
    return m.sent[len(m.sent)-1]
}

func (m *MockAdapter) SentCount() int {
    return len(m.sent)
}
```

### Unit Tests Per Component

| File | What's Tested |
|---|---|
| `router_test.go` | Thread replies route to session mgr, commands route to handler, @mentions create new sessions, bot self-messages ignored |
| `watcher_test.go` | Car status change detection, stall detection, escalation pickup, pulse suppression when idle, daily/weekly scheduling |
| `session_test.go` | Lock acquisition, lock contention, heartbeat expiry, session resume, context recovery from Dolt, fallback to thread history |
| `conversation_test.go` | Dual-write to Dolt, sequence numbering, recovery load, max turns enforcement, lookback window |
| `format_test.go` | Event formatting for all event types, pulse/daily/weekly digest content, idle suppression logic |
| `daemon_test.go` | Full lifecycle: start → receive message → dispatch → response → shutdown |

### Adapter-Specific Tests

Each adapter gets tests that mock the platform SDK:

| File | What's Tested |
|---|---|
| `slack/slack_test.go` | Block Kit formatting, thread history pagination, self-message filtering, Socket Mode reconnect |
| `discord/discord_test.go` | Embed formatting, thread creation for new sessions, Gateway reconnect |

### Dispatch Lock Concurrency Tests

```go
TestDispatchLock_ConcurrentAcquisition    // Two goroutines race — exactly one wins
TestDispatchLock_HeartbeatExpiry          // Session stops heartbeating → lock expires
TestDispatchLock_LocalBlocksRemote       // Local dispatch holds lock → Telegraph gets busy
TestDispatchLock_RemoteBlocksLocal       // Telegraph holds lock → ry dispatch gets error
TestDispatchLock_QueueDrain              // Queued request processed when lock releases
```

### Coverage Target

- `internal/telegraph/` core: >90%
- Adapter packages: >80%

---

## Configuration Reference

```yaml
telegraph:
  platform: slack                       # "slack" or "discord"
  channel: "#railyard-ops"              # slack channel name or discord channel ID

  slack:
    bot_token: "${SLACK_BOT_TOKEN}"     # xoxb-...
    app_token: "${SLACK_APP_TOKEN}"     # xapp-... (Socket Mode)

  discord:
    bot_token: "${DISCORD_BOT_TOKEN}"
    guild_id: "${DISCORD_GUILD_ID}"
    channel_id: "${DISCORD_CHANNEL_ID}"

  dispatch_lock:
    heartbeat_interval_sec: 30
    heartbeat_timeout_sec: 90           # 3 missed heartbeats = stale
    queue_max: 10                       # max pending requests while locked

  events:
    car_lifecycle: true                 # car status changes
    engine_stalls: true                 # stall alerts
    escalations: true                   # yardmaster → human messages
    poll_interval_sec: 15               # how often to check Dolt

  digest:
    pulse:
      enabled: true
      interval_min: 30
      suppress_when_idle: true          # no notification if nothing to report
    daily:
      enabled: true
      time: "09:00"                     # local time
      timezone: "America/New_York"
    weekly:
      enabled: true
      day: "monday"
      time: "09:00"
      timezone: "America/New_York"
      # channel: "#railyard-weekly"     # optional: different channel for weekly

  conversations:
    max_turns_per_session: 100          # safety limit per dispatch session
    recovery_lookback_days: 30          # how far back to look for resumable threads

  # routing:                            # future: per-severity channel routing
  #   critical: "#railyard-alerts"
  #   info: "#railyard-ops"
```

Tokens support environment variable references (`${VAR}` syntax) resolved at config load time. Tokens should never be committed to the repo.

---

## Implementation Phases

### Phase 1: Core Telegraph + Slack Adapter

- `internal/telegraph/` package scaffold (adapter interface, daemon, router, watcher, session manager, conversation store, formatting)
- `internal/telegraph/slack/` Slack Socket Mode adapter
- Dolt schema: `dispatch_sessions`, `telegraph_conversations` tables
- Event watcher: car lifecycle, stalls, escalations, pulse digest
- Mock adapter + full unit test suite for telegraph core
- Slack adapter tests with SDK mocks
- `cmd/ry/telegraph.go` CLI commands
- Modify `ry start` to not auto-launch Dispatch; add `--telegraph` flag
- Modify `ry dispatch` to acquire/release dispatch lock
- Configuration schema additions to `railyard.yaml`

### Phase 2: Discord Adapter

- `internal/telegraph/discord/` Discord Gateway adapter
- Discord thread creation for dispatch sessions
- Discord Embed formatting
- Discord adapter tests with SDK mocks

### Phase 3: Advanced Digests

- Daily digest with time-range Dolt queries
- Weekly report with full statistics
- Digest suppression logic (idle detection)
- Optional per-severity channel routing

### Phase 4: Multi-User Enhancements

- Per-user dispatch session isolation (Slack thread per user)
- User display name resolution
- Optional user allowlisting (restrict who can dispatch)

---

## Decision Log

| # | Decision | Alternatives Considered | Rationale |
|---|---|---|---|
| 1 | **Name: Telegraph** | Signal Tower, Depot, Callboard | Railroad telegraph fits remote communication metaphor. Clean CLI namespace (`ry telegraph` / `ry tg`). |
| 2 | **Platform-agnostic adapter interface** | Slack-only, Discord-only | Team may switch platforms. Adapter interface is low overhead, enables future platforms. |
| 3 | **Build both Slack and Discord adapters** | Single platform only | Slack for corporate use, Discord for personal project testing. Validates adapter interface works across platforms. |
| 4 | **Slack as first implementation** | Discord first | Socket Mode fits no-inbound-ports constraint. Block Kit for rich formatting. Corporate adoption. |
| 5 | **Discord as second implementation** | Defer indefinitely | User has Discord server ready for testing on personal projects. Validates adapter abstraction. |
| 6 | **Hybrid Remote Dispatch (Option C)** | Direct tmux relay, structured DB-only | Clean process boundary. No coupling to tmux. Local and remote dispatch coexist. Supports both conversational and command modes. |
| 7 | **DB-level dispatch lock with heartbeat** | No lock, always-on lock, tmux detection | Prevents simultaneous car creation. Heartbeat expiry prevents stuck state. Lock is per-session not per-process. |
| 8 | **`ry start` no longer auto-launches Dispatch** | Keep current auto-launch | Dispatch lock only works if local dispatch is on-demand. `ry dispatch` launches separate tmux session when needed. |
| 9 | **Dual-write conversations: Dolt primary, chat fallback** | Chat-only, Dolt-only | Dolt gives audit trail + version control + platform independence. Chat fallback covers edge cases where Dolt write failed. |
| 10 | **Multi-tier digests: pulse/daily/weekly** | Single interval only | Pulse for operational awareness, daily for summary, weekly for planning/reporting. Pulse suppressed when idle to reduce noise. |
| 11 | **Single daemon process (Approach 1)** | Split watcher + handler, notifications-only | Right complexity for 2-5 users, one railyard. Internal components logically separated for future split if needed. |
| 12 | **Read-only commands bypass dispatch lock** | All commands go through lock | Status queries are harmless reads. Only car creation/modification needs the lock. |
| 13 | **Mock adapter for core testing** | Test against real APIs | Enables fast, deterministic, offline testing. Platform-specific tests mock SDK separately. |
| 14 | **>90% coverage for core, >80% for adapters** | Lower threshold | Telegraph is a new critical path for human-system interaction. Lock concurrency and conversation recovery must be solid. |
