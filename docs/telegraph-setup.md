# Telegraph Setup Guide

Telegraph is Railyard's chat bridge that connects to **Slack** or **Discord**. It provides:

- **Inbound command routing** — query Railyard status from chat (`!ry status`, `!ry car list`)
- **Outbound event posting** — car lifecycle changes, engine stalls, escalation messages
- **Dispatch via chat** — @mention the bot to start a dispatch session (creates cars from natural language)
- **Scheduled digests** — daily and weekly summary reports posted to your channel

## Slack Setup

### 1. Create a Slack App

1. Go to [api.slack.com/apps](https://api.slack.com/apps) and click **Create New App** > **From scratch**
2. Name it (e.g. "Railyard") and select your workspace

### 2. Enable Socket Mode

Telegraph uses Socket Mode so the bot connects via WebSocket (no public URL needed).

1. Go to **Settings** > **Socket Mode** and toggle it **on**
2. Create an app-level token with the `connections:write` scope
3. Save the token — it starts with `xapp-...`

### 3. Add Bot Scopes

Go to **OAuth & Permissions** > **Scopes** > **Bot Token Scopes** and add:

| Scope | Purpose |
|-------|---------|
| `chat:write` | Send messages and event notifications |
| `channels:history` | Read channel messages for thread history |
| `users:read` | Resolve user display names |
| `app_mentions:read` | Detect @mentions for dispatch conversations |

### 4. Subscribe to Bot Events

Go to **Event Subscriptions** > **Subscribe to bot events** and add:

| Event | Purpose |
|-------|---------|
| `message.channels` | Receive channel messages for `!ry` commands |
| `app_mention` | Receive @mentions for dispatch conversations |

### 5. Install to Workspace

1. Go to **Install App** and click **Install to Workspace**
2. Copy the **Bot User OAuth Token** — it starts with `xoxb-...`

### 6. Find Your Channel ID

Right-click the channel in Slack > **View channel details** > copy the Channel ID at the bottom (e.g. `C0123456789`).

### 7. Configure

Add to your `railyard.yaml`:

```yaml
telegraph:
  platform: slack
  channel: C0123456789
  slack:
    bot_token: ${SLACK_BOT_TOKEN}
    app_token: ${SLACK_APP_TOKEN}
```

Set the environment variables:

```bash
export SLACK_BOT_TOKEN="xoxb-your-token-here"
export SLACK_APP_TOKEN="xapp-your-token-here"
```

## Discord Setup

### 1. Create a Discord Application

1. Go to [discord.com/developers/applications](https://discord.com/developers/applications) and click **New Application**
2. Name it (e.g. "Railyard")

### 2. Create a Bot

1. Go to the **Bot** tab and click **Add Bot**
2. Copy the bot **Token**
3. Under **Privileged Gateway Intents**, enable:
   - **Message Content Intent** — required to read message text for `!ry` commands
   - **Server Members Intent** — required to resolve usernames

### 3. Invite the Bot

1. Go to **OAuth2** > **URL Generator**
2. Select the `bot` scope
3. Select these bot permissions:
   - Send Messages
   - Read Message History
   - Create Public Threads
   - Manage Threads
4. Copy the generated URL and open it in your browser to invite the bot to your server

### 4. Find Your Guild ID and Channel ID

1. Enable **Developer Mode** in Discord: User Settings > Advanced > Developer Mode
2. Right-click your server name > **Copy Server ID** (this is the Guild ID)
3. Right-click the channel > **Copy Channel ID**

### 5. Configure

Add to your `railyard.yaml`:

```yaml
telegraph:
  platform: discord
  channel: "123456789012345678"
  discord:
    bot_token: ${DISCORD_BOT_TOKEN}
    guild_id: "123456789012345678"
    channel_id: "123456789012345678"
```

Set the environment variable:

```bash
export DISCORD_BOT_TOKEN="your-bot-token-here"
```

## Configuration Reference

Full annotated `telegraph` config block with all options and defaults:

```yaml
telegraph:
  platform: slack                    # Required: "slack" or "discord"
  channel: C0123456789               # Required: default channel ID for posting

  # --- Channel allowlist (optional) ---
  # Restrict the bot to only respond in these channels. Messages from
  # other channels are silently dropped. Threads inside allowed channels
  # are always permitted. Omit or leave empty to allow all channels.
  allowed_channels:
    - C0123456789                    # e.g. #railyard
    - C9876543210                    # e.g. #ops

  # --- Slack credentials (required when platform: slack) ---
  slack:
    bot_token: ${SLACK_BOT_TOKEN}    # xoxb-... bot token
    app_token: ${SLACK_APP_TOKEN}    # xapp-... app-level token for Socket Mode

  # --- Discord credentials (required when platform: discord) ---
  discord:
    bot_token: ${DISCORD_BOT_TOKEN}  # Discord bot token
    guild_id: "123456789"            # Discord server (guild) ID
    channel_id: "123456789"          # Discord channel ID

  # --- Outbound event posting ---
  events:
    car_lifecycle: true              # Post car status changes (default: true)
    engine_stalls: true              # Post engine stall alerts (default: true)
    escalations: true                # Post escalation messages (default: true)
    poll_interval_sec: 15            # How often to poll for events (default: 15)

  # --- Scheduled digests ---
  digest:
    daily:
      enabled: true                  # Enable daily digest (default: false)
      cron: "0 9 * * *"             # Cron schedule (default: 9am daily)
    weekly:
      enabled: true                  # Enable weekly digest (default: false)
      cron: "0 9 * * 1"             # Cron schedule (default: 9am Monday)

  # --- Dispatch lock (prevents concurrent dispatch sessions) ---
  dispatch_lock:
    heartbeat_interval_sec: 30       # How often sessions send heartbeats (default: 30)
    heartbeat_timeout_sec: 90        # Stale heartbeat threshold (default: 90)
    queue_max: 5                     # Max queued dispatch requests (default: 5)

  # --- Conversation settings ---
  conversations:
    max_turns: 20                    # Max turns per dispatch conversation (default: 20)
    recovery_lookback_days: 7        # Days to look back for session recovery (default: 7)
```

Token fields support `${ENV_VAR}` substitution — set secrets as environment variables rather than hardcoding them.

## Running Telegraph

```bash
ry telegraph start -c railyard.yaml   # Start the Telegraph daemon
ry telegraph status                    # Check if the daemon is running
ry telegraph stop                      # Stop the daemon
```

The `telegraph` command is also aliased as `tg`:

```bash
ry tg start -c railyard.yaml
ry tg status
ry tg stop
```

Telegraph runs in a dedicated tmux session (`railyard-telegraph`). You can attach directly:

```bash
tmux attach -t railyard-telegraph
```

## Chat Commands

All read-only commands use the `!ry` prefix:

| Command | Description |
|---------|-------------|
| `!ry status` | Railyard dashboard (engines, cars, tracks) |
| `!ry car list [--track X] [--status X]` | List cars with optional filters |
| `!ry car show <id>` | Show details for a specific car |
| `!ry engine list` | List active engines with status |
| `!ry help` | Show available commands |

To start a **dispatch conversation** (create cars from natural language), @mention the bot:

> @Railyard Add authentication middleware to the backend

This acquires a dispatch lock and starts an interactive session in a thread.

## Outbound Events

When enabled, Telegraph automatically posts to your channel:

- **Car status changes** — when cars move between statuses (open, in_progress, done, merged)
- **Engine stalls** — when an engine stops responding (no stdout, repeated errors)
- **Escalations** — when agents send messages to "human" or "telegraph" recipients
- **Pulse updates** — periodic snapshots when orchestration state changes

## Troubleshooting

### Slack: "slack: app token is required for socket mode"

Socket Mode is not enabled, or the app-level token is missing. Go to **Settings** > **Socket Mode** in your Slack app configuration and ensure it's toggled on. Then create an app-level token with `connections:write` scope.

### Slack: "slack: auth test" error

The bot token is invalid or expired. Reinstall the app to your workspace to get a fresh `xoxb-...` token.

### Slack: Bot doesn't respond to messages

- Verify the bot is invited to the channel (`/invite @Railyard`)
- Check that `message.channels` is listed under Event Subscriptions
- Confirm Socket Mode is enabled (not just "Request URL" mode)

### Discord: "discord: create session" error

The bot token is invalid. Go to the Bot tab in the Discord Developer Portal and reset the token.

### Discord: Bot ignores messages

- **Message Content Intent** must be enabled in the Bot tab under Privileged Gateway Intents — without this, the bot receives empty message content
- Verify the bot has **Read Message History** and **Send Messages** permissions in the channel

### Discord: "Disallowed intent(s)" error on connect

Your bot needs **Message Content Intent** and/or **Server Members Intent** enabled. Go to the Discord Developer Portal > your app > **Bot** > **Privileged Gateway Intents** and toggle them on.

### Events not posting

- Check that `events.car_lifecycle`, `events.engine_stalls`, or `events.escalations` are `true` in your config
- Verify the channel ID is correct
- Check Telegraph logs: `tmux attach -t railyard-telegraph`

### Digests not posting

- Verify `digest.daily.enabled: true` or `digest.weekly.enabled: true`
- The cron expression must be valid (standard 5-field cron format)
- Digests are suppressed when there's no activity in the period
