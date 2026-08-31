# Discord Setup

This guide explains how to create the Discord bot and tracking server
this service needs. It covers the three `DISCORD_*` environment
variables from the [README](../README.md).

## Why a Tracking Server Exists

Discord sends presence only over a bot's Gateway connection, and only
for members of a shared server. There is no polling API, unlike Steam.

The tracking server exists to satisfy that rule. It has no purpose
beyond that. `@everyone` has no channel access, so members cannot
browse or read anything there. Onboarding happens by direct message on
join, not in a shared channel.

See the [design doc](superpowers/specs/2026-08-30-discord-game-status-sync-design.md)
for the full privacy rationale.

## Steps

### 1. Create a Discord Application

Go to the [Discord Developer Portal](https://discord.com/developers/applications).
Click "New Application" and give it a name.

### 2. Add a Bot to the Application

Open the "Bot" tab. Click "Reset Token" to generate a token. Copy it.
This value is `DISCORD_BOT_TOKEN`.

Treat this token as a secret. Anyone with it can control the bot.

### 3. Enable Two Privileged Intents

Stay on the "Bot" tab. Find "Privileged Gateway Intents". Enable
"Presence Intent". Enable "Server Members Intent".

You need both intents. Without them, the bot cannot receive presence
updates or member events.

### 4. Create a Tracking Server

Create a new, empty Discord server. Use any name; members never see
it in a meaningful way.

### 5. Lock Down the Tracking Server

Open "Server Settings" then "Roles". Select the `@everyone` role.
Remove the "View Channels" permission for every channel.

This step matters. It keeps the server empty from a member's point of
view, even though the bot still receives their presence.

### 6. Invite the Bot to the Tracking Server

Return to the Developer Portal. Open "OAuth2" then "URL Generator".
Under "Scopes", check "bot". The bot needs no extra permissions.

Open the generated URL. Select your tracking server. Authorize the
bot.

### 7. Get the Server ID

In Discord, open "User Settings" then "Advanced". Enable "Developer
Mode".

Right-click the tracking server's icon. Click "Copy Server ID". This
value is `DISCORD_GUILD_ID`.

### 8. Create a Permanent Invite Link

Right-click any channel in the tracking server. Click "Invite People".
Click "Edit invite link". Set "Expire After" to "Never". Set "Max
Number of Uses" to "No limit".

Copy the link. This value is `DISCORD_INVITE_URL`. The service shows
this link to users so they can join and link their account.

## Result

You now have all three values:

| Variable | Source |
| --- | --- |
| `DISCORD_BOT_TOKEN` | Step 2 |
| `DISCORD_GUILD_ID` | Step 7 |
| `DISCORD_INVITE_URL` | Step 8 |

Set them as environment variables for the service.
