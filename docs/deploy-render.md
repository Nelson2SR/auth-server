# Deploying to Render

This deploys **Casdoor + managed PostgreSQL** via the `render.yaml` Blueprint.
Render terminates TLS and assigns a public URL, so the Caddy reverse proxy from
`docker-compose.prod.yml` is not used here.

## What the Blueprint creates

- **`auth-server-db`** — managed PostgreSQL (database `casdoor`, user `casdoor`).
- **`auth-server`** — a Docker Web Service built from `./Dockerfile` (the
  `casbin/casdoor` image with our `conf/app.conf` + `init_data.json` baked in).

Connection wiring (no manual values needed for these):
- The DB host/port/user/password/name are injected into the service as `DB_*`
  env vars. `deploy/render-entrypoint.sh` assembles them into a single
  `CASDOOR_DSN`, which `conf/app.conf` reads as `${CASDOOR_DSN||<local default>}`.
- The OIDC issuer / `origin` comes from Render's `RENDER_EXTERNAL_URL` env var
  automatically (`origin = "${RENDER_EXTERNAL_URL||http://localhost:8000}"`), so
  there is no chicken-and-egg with the public URL.

> Verified locally: building the image and pointing it at a fresh Postgres DB
> boots Casdoor, seeds `init_data.json`, serves `/api/health`, and reports the
> issuer from `RENDER_EXTERNAL_URL`.

## Deploy steps

1. In the Render dashboard: **New → Blueprint**.
2. Connect the GitHub repo `Nelson2SR/auth-server` and select the branch.
3. Render reads `render.yaml`, shows the DB + web service, and provisions them.
   First build pulls the Casdoor image and seeds the DB on first boot.
4. When the service is live, open `https://<service>.onrender.com` — you should
   get the Casdoor UI, and `/.well-known/openid-configuration` should show your
   Render URL as the `issuer`.

## Required post-deploy steps (do these immediately)

The Blueprint brings the service up, but it is **not production-safe until you:**

1. **Change the Casdoor admin password.** Casdoor creates a built-in
   `admin` / `123` super-admin. Log in and change it before anything else, or
   the public instance is wide open.
2. **Stop init re-seeding, THEN inject provider secrets.** This order matters —
   see the "Provider secrets" section below.
3. **Fix redirect URIs.** `app-a.redirectUris` is `http://localhost:9000/callback`
   for local dev. Set it to your real client's callback URL.

## Provider secrets (Twilio / Gmail) — the re-seed gotcha

Casdoor **re-applies `initDataFile` on every boot and overwrites existing rows.**
It does not expand env vars inside `init_data.json`, and the repo is public, so
real secrets can't live there. Net effect: any secret you inject (DB or admin UI)
is reverted to the `REPLACE_*` placeholder on the next restart/redeploy — *unless*
you first disable re-seeding.

The image bakes an empty `deploy/noop_init.json` and reads
`initDataFile = "${INIT_DATA_FILE||./init_data.json}"`. To configure providers
durably:

1. **Disable re-seeding:** set service env var `INIT_DATA_FILE=/noop_init.json`,
   then trigger a **full deploy** (a *restart* does NOT pick up env-var changes —
   only a deploy does). The first boot already seeded the structure;
   subsequent boots now import nothing.
2. **Inject the secrets** on the seeded provider rows (Casdoor UI → Providers, or
   SQL — Render blocks external DB connections by default, so temporarily add your
   IP to the database's IP allow list, then remove it):
   - `provider-twilio-app-a`: `client_id` (SID), `client_secret` (auth token),
     `app_id` (sender number).
   - `provider-email-app-a`: `client_secret` (Gmail App Password).
3. **Restart** so Casdoor reloads the providers. The values now persist across
   restarts/redeploys because re-seeding is off.

Verified live: with `INIT_DATA_FILE=/noop_init.json`, injected Twilio creds
survive a restart and `POST /api/send-verification-code` returns `status: ok`.

## Notes & gotchas

- **Region:** the DB and web service must share a region (both `singapore` in
  `render.yaml`) so the private DB host resolves. Change both together.
- **SSL:** `DB_SSLMODE=require` is set for Render. (Local docker-compose uses the
  built-in default DSN with `sslmode=disable`.)
- **Free tier:** free Postgres expires after ~30 days and free web services
  sleep after ~15 min idle (first request after sleep is slow). Bump the `plan`
  values in `render.yaml` for anything beyond evaluation.
- **DB persistence:** state lives in the managed Postgres, not the container, so
  redeploys/restarts keep your data and injected secrets.
