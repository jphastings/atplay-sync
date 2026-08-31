# Steam Setup

This guide explains how to get the Steam Web API key this service
needs. It covers the `STEAM_API_KEY` and `STEAM_DAILY_CALL_BUDGET`
environment variables from the [README](../README.md).

## Why This Service Needs Only One Key

Steam offers a public API for a user's current play status, unlike
Discord. The service polls this API on a schedule for every linked
account.

Users do not link Steam through this service directly. Instead, each
user creates a signed [keytrace](https://keytrace.dev) claim that
proves ownership of their Steam profile. This service reads that
claim and polls the linked SteamID64 on the user's behalf.

## Steps

### 1. Get a Steam Web API Key

Go to the [Steam Web API key page](https://steamcommunity.com/dev/apikey).
Sign in with a Steam account.

Enter a domain name. For local development, enter `localhost`. For a
deployed service, enter your service's domain.

Agree to the terms. Click "Register". Copy the key. This value is
`STEAM_API_KEY`.

### 2. Set the Daily Call Budget (Optional)

Steam does not publish an official rate limit. The commonly cited,
unofficial ceiling is 100,000 calls per day per key.

The service defaults `STEAM_DAILY_CALL_BUDGET` to `100000`. Set a
different value only if your own key has a different limit.

The service spreads its polling evenly across the rest of the day as
this budget runs low. It stops calling Steam once it exhausts the
budget, or once Steam itself returns a rate-limit error. It resumes
the next day.

## Result

| Variable | Source |
| --- | --- |
| `STEAM_API_KEY` | Step 1 |
| `STEAM_DAILY_CALL_BUDGET` | Step 2 (optional, defaults to `100000`) |

Set them as environment variables for the service.
