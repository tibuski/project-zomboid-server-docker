# Discord Webhook

Send server events (start, stop, crash, player joins/leaves) to a Discord
channel via webhook, and optionally let the channel restart the server with a
chat command.

## Setup

### 1. Create a Discord Webhook

1. Open your Discord server
2. Go to the channel where you want notifications
3. Click the gear icon (Edit Channel)
4. Select **Integrations** → **Webhooks** → **New Webhook**
5. Name it (e.g., "PZ Server")
6. Click **Copy Webhook URL**

### 2. Configure

Add the URL to your `.env`:

```env
DISCORD_WEBHOOK_URL=https://discord.com/api/webhooks/XXXXXXXX/YYYYYYYYYYYY

DISCORD_NOTIFY_START=true
DISCORD_NOTIFY_STOP=true
DISCORD_NOTIFY_CRASH=true
DISCORD_NOTIFY_UPDATE=true
DISCORD_NOTIFY_JOIN=true
DISCORD_NOTIFY_LEAVE=true
```

### 3. Restart

```bash
docker compose down && docker compose up -d
```

## Notification Types

| Event | Color | Description |
|-------|-------|-------------|
| Server started | Green | Sent when the JVM process launches |
| Server stopped | Red | Sent on graceful shutdown (`docker stop`) |
| Server crashed | Yellow | Sent when the process exits unexpectedly |
| Player joined | Green | Sent when a player joins (tailed from `Logs/*_user.txt`) |
| Player left | Red | Sent when a player disconnects (tailed from `Logs/*_user.txt`) |

Join/leave notifications are enabled by default whenever `DISCORD_WEBHOOK_URL`
is set, and can be disabled with `DISCORD_NOTIFY_JOIN=false` /
`DISCORD_NOTIFY_LEAVE=false`. The entrypoint tails the newest
`Logs/*_user.txt` (PZ logs player logins/disconnects there) and resolves
player names to Steam IDs.

## Example Embeds

**Server Started:**

```
🟢 Server Started
My Awesome Server is now online
Server: servertest | 2026-01-15T19:30:00Z
```

**Server Stopped:**

```
🔴 Server Stopped
My Awesome Server has shut down
Server: servertest | 2026-01-15T20:00:00Z
```

**Server Crashed:**

```
💥 Server Crashed
My Awesome Server exited unexpectedly: exit status 1
Server: servertest | 2026-01-15T19:45:00Z
```

## Restart Command (Discord Bot)

Typing `restart server` (any capitalization) in the channel recreates the
game server on the latest image - the equivalent of `docker compose pull &&
docker compose up -d --force-recreate` for the `zomboid` service.

A webhook can only *send* messages, so this uses a small sidecar container
(`discordbot` service in `docker-compose.yml`) that polls the channel with a
bot token.

### 1. Create the bot

1. Go to the [Discord Developer Portal](https://discord.com/developers/applications) → **New Application**
2. Open **Bot** → **Reset Token** → copy the token (this is `DISCORD_BOT_TOKEN`)
3. On the same page, enable the **Message Content** privileged intent (required, otherwise the bot cannot read message text)
4. Invite the bot to your server with this URL (replace `CLIENT_ID` with the application's **Application ID**):

   ```
   https://discord.com/oauth2/authorize?client_id=CLIENT_ID&scope=bot&permissions=68608
   ```

   (`68608` = View Channel + Send Messages + Read Message History)

### 2. Get the channel ID

In Discord: **Settings → Advanced → Developer Mode** on, then right-click the
channel → **Copy Channel ID**.

### 3. Configure and start

```env
DISCORD_BOT_TOKEN=your-bot-token
DISCORD_CHANNEL_ID=1234567890123456789
```

```bash
docker compose pull && docker compose up -d
```

The sidecar idles quietly when the token/channel are not set, so the service
can stay in the compose file even if you don't use the command.

### How it works

- The channel is polled every 5 seconds (`POLL_INTERVAL`); only messages
  posted *after* the bot started are considered.
- The match is exact (surrounding whitespace ignored), so "please restart
  server" does not trigger anything. Messages from other bots are ignored.
- On a match the bot confirms in the channel, pulls the latest image, and
  force-recreates **only the `zomboid` service** (a full `compose down` would
  kill the sidecar itself mid-command). The usual 🟢 **Server Started**
  webhook message announces when the server is actually back online.
- A cooldown (`RESTART_COOLDOWN`, default `5m`) ignores further commands
  after a restart.

| Variable | Default | Purpose |
|----------|---------|---------|
| `DISCORD_BOT_TOKEN` | (empty) | Bot token from the developer portal |
| `DISCORD_CHANNEL_ID` | (empty) | Channel to watch |
| `POLL_INTERVAL` | `5s` | How often the channel is polled |
| `RESTART_COOLDOWN` | `5m` | Minimum time between two restarts |
| `RESTART_SERVICE` | `zomboid` | Compose service recreated on command |

### Security notes

- **Anyone who can post in the channel can restart the server.** Keep the
  channel private or restrict it to trusted members.
- The sidecar mounts `/var/run/docker.sock` to recreate the container; that
  is root-equivalent access to the host. Only the sidecar gets the socket -
  the game server container stays socket-free.
- Keep the bot token in `.env` secret; it allows posting as the bot.

## Troubleshooting

### Notifications not appearing

- Verify the webhook URL is correct (test with curl)
- Ensure the container can reach Discord's API (no firewall blocking outbound HTTPS)
- Check container logs: `docker compose logs zomboid`

### Test the webhook

```bash
curl -X POST \
  -H "Content-Type: application/json" \
  -d '{"content":"Test from PZ server"}' \
  YOUR_WEBHOOK_URL
```
