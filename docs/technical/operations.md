# Operations

## Running

- **Local dev:** `docker compose up -d` (Casdoor on `http://localhost:8000`, PostgreSQL alongside).
- **Production:** `docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d`
  — adds Caddy (TLS at `auth.example.com`) and the scheduled `subsync`, and stops exposing
  Casdoor's port directly. Update `conf/app.conf` `origin` to `https://auth.example.com` and
  point the `Caddyfile` at your real domain first.

## Secrets

WeChat/Twilio/admin credentials and the signing cert live in `.env` and Casdoor's DB — never
committed. For prod, template `init_data.json` and inject the `REPLACE_*` values at deploy time.
Fill `ADMIN_CLIENT_ID` / `ADMIN_CLIENT_SECRET` in `.env` from **Applications → app-built-in**
after first boot (needed by the `subsync` service).

## Signing certificate

`cert-app-a` (RS256) is the root of trust for offline JWT verification. Rotate by issuing a new
cert, re-pointing the application, and redistributing the public PEM to apps (the verifier
library pins the public key via `NewFromPEM`).

## Backups

Schedule `docker compose exec db pg_dump -U casdoor casdoor > backup.sql`.

## Admin MFA

Enable TOTP for admin accounts in the Casdoor UI.

## Subscription freshness

`subsync` runs every 5 minutes (`SYNC_INTERVAL`), mirroring subscription state into user
properties. Worst-case staleness of subscription claims in a live token = the access-token TTL
(1 hour) plus one sync interval in user properties.
