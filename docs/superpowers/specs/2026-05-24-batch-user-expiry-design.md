# Batch user provisioning + time-limited accounts

**Status:** design / spec (auth-server repo)
**Date:** 2026-05-24

## Goal

Let an operator **batch-create phone-OTP accounts in `intent-pulse-org`** (or
any other org) with a per-account expiry date, and have those accounts
**auto-disable when their expiry passes** so they can't be used after the
agreed window. Operator can later **extend** an account (push expiry forward,
re-enable login).

This is "give the client a phone-number-keyed account that works for the next
30 days." Casdoor has no built-in time-limited accounts, so we build it on
top of `user.properties` + a daily sweep.

## Context

- Login flow already gates on Casdoor's allowlist (`enableSignUp=false` on
  `intent-pulse`). No self-signup. New users come from admin action only.
- The auth-server repo is Go, deployed on Render. It already ships a
  `subsync` Go CLI (`cmd/subsync/`) that hits Casdoor's admin HTTP API to
  sync subscription state into `user.Properties`. This work follows the same
  pattern (Go binary + Docker image + Render service).
- `user.Properties` is `map[string]string` and is already used by app-a for
  `plan` / `role` / `subscriptionStatus` — adding another key fits the
  existing data model.
- The dashboard at `intent-pulse-crawler.onrender.com` doesn't need to change
  — when Casdoor disables a user, the auth gate's pattern-B refresh fails
  within ~1 hour and the dashboard kicks them.

## Non-goals

- Web admin UI (operator uses CLI from laptop; cron runs on Render).
- Email/SMS notification before expiry.
- Multi-org per invocation (each command takes one `--org`).
- Username+password or magic-link credentials (phone-OTP only).
- Soft-delete / audit log of disabled users (Casdoor already records
  `updatedTime`; that's enough for v1).

## Data model

`expireAt` stored in `user.Properties["expireAt"]` as an ISO date string,
e.g. `"2026-06-30"`. Comparison done on the date alone, in UTC.

A user is **expired** when `Properties["expireAt"]` parses to a date strictly
before today (UTC). Missing or unparseable `expireAt` → not expired (so
fixture users like `admin` are never swept).

A user is **disabled** when Casdoor's `forbidden=true`. The sweep flips
`forbidden=true` on expired users; `extend` flips it back to false.

## Components

### 1. `expiry/` package (new)

Pure functions, easy to unit-test. Wraps the existing `subscription`
package's `CasdoorClient` for the IO.

```
expiry/
├── expiry.go        // IsExpired(user, now) bool
                     // ExpireAtString(days int, now time.Time) string
                     // SetExpireAt(user *User, days int, now time.Time)
├── sweep.go         // SweepOrg(ctx, client, org, now) → SweepReport
└── expiry_test.go
```

`SweepReport`:
```go
type SweepReport struct {
    Scanned     int
    Expired     int
    Disabled    int       // newly forbidden=true this run
    AlreadyOff  int       // already forbidden, no action
    Errors      []SweepError
}
```

### 2. `cmd/authadmin/` CLI (new)

One Go binary, subcommands implemented with the stdlib `flag` package (no new
deps — matches existing `cmd/subsync/` style). Reads Casdoor admin creds from
env (`CASDOOR_ENDPOINT`, `CASDOOR_CLIENT_ID`, `CASDOOR_CLIENT_SECRET`).

```
authadmin create-batch    --org <org> --csv <path> --days <N> [--out <path>]
authadmin list            --org <org> [--expired | --expiring-in <N>]
authadmin extend          --org <org> --name <user> --by <N>
authadmin disable-expired --org <org>
authadmin disable         --org <org> --name <user>
authadmin enable          --org <org> --name <user>
```

All `--days`, `--by`, `--expiring-in` flags take a **plain integer number of
days** (no `d` suffix). Keeps parsing trivial (`strconv.Atoi`) and consistent
across the three commands.

**`create-batch`** input CSV (header row required):
```
name,displayName,phone,countryCode
alice,Alice Sun,98765432,SG
bob,Bob Lim,97654321,SG
```
For each row:
1. `add-user` with `phone`, `countryCode`, `region=countryCode`,
   `properties.expireAt = today + days`.
2. Failure on a row (e.g. duplicate name, 4xx from Casdoor) → record in the
   result CSV, continue with the next row, set the exit code to non-zero at
   the end.
3. Result CSV (written to `--out` or stdout if omitted):
   ```
   name,phone,countryCode,expireAt,status,error
   alice,98765432,SG,2026-06-23,ok,
   bob,97654321,SG,,error,user already exists
   ```

**`list`** output: a fixed-width table — `name`, `phone`, `expireAt`, `state`:
- `active` — has expireAt, in the future, not forbidden.
- `expiring` — expireAt within `--expiring-in N` days from today.
- `expired` — expireAt in the past, not yet swept (forbidden=false).
- `disabled` — forbidden=true.
- `no-expiry` — no `expireAt` property; never swept (fixture / admin users).

No paging beyond Casdoor's natural `get-users?owner=...` response.

**`extend`** behavior:
1. Fetch the user.
2. Set `properties.expireAt = today + N days`.
3. Set `forbidden=false`.
4. `update-user`.

**`disable-expired`** is exactly `expiry.SweepOrg(...)`; prints the
`SweepReport`; exits zero unless the whole call failed (per-user errors
counted but tolerated).

**`disable` / `enable`** are tiny wrappers over `update-user` with
`forbidden=true|false`. Exist so the operator never has to touch the Casdoor
UI for the most common manual ops.

### 3. Render Cron Job (new service)

Add to `render.yaml`:
```yaml
- type: cron
  name: auth-server-expiry-sweep
  runtime: docker
  schedule: "0 1 * * *"          # 01:00 UTC daily
  region: singapore
  dockerfilePath: ./cmd/authadmin/Dockerfile
  dockerContext: .
  dockerCommand: "/authadmin disable-expired --org intent-pulse-org"
  envVars:
    - key: CASDOOR_ENDPOINT
      value: https://auth-server-msje.onrender.com
    - key: CASDOOR_CLIENT_ID
      sync: false
    - key: CASDOOR_CLIENT_SECRET
      sync: false
```

Cron Jobs on Render are billed per run (~$1/mo for once-daily). The same
Dockerfile is what the operator would build locally for the CLI.

### `cmd/authadmin/Dockerfile`

Mirrors `cmd/subsync/Dockerfile` — multi-stage Go build, alpine runtime:

```Dockerfile
FROM golang:1.22 AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o /authadmin ./cmd/authadmin

FROM alpine:3
COPY --from=build /authadmin /authadmin
ENTRYPOINT ["/authadmin"]
```

The cron's `dockerCommand` supplies the subcommand args at runtime; running
the same image locally with no args prints help.

## Data flow

**create-batch (operator → Casdoor):**
```
operator runs authadmin create-batch --csv ./batch.csv --days 30
  → for each row: POST /api/add-user with properties.expireAt = today+30d
  → write result.csv with status per row
  → exit 0 if all ok, 1 if any row failed
```

**daily sweep (Render Cron → Casdoor):**
```
cron fires "/authadmin disable-expired --org intent-pulse-org"
  → GET /api/get-users?owner=intent-pulse-org
  → for each user: if IsExpired(user, now) && !forbidden:
       POST /api/update-user with forbidden=true
  → log SweepReport (counts), exit 0
```

**extend (operator):**
```
operator runs authadmin extend --name alice --by 30d
  → GET /api/get-user?id=intent-pulse-org/alice
  → set properties.expireAt = today+30d, forbidden=false
  → POST /api/update-user
  → user can log in again immediately
```

**dashboard side (passive — no change to intent-pulse-crawler):**
When a user is disabled, their next dashboard request triggers pattern B's
silent refresh against Casdoor. Casdoor refuses the refresh (forbidden=true),
the auth gate clears the session and bounces them to `/auth/login`. Casdoor
won't issue a new login code for a forbidden user, so they can't get back in.

## Error handling

- **CLI:** non-zero exit on any per-row failure in `create-batch`, on lookup
  failure in `extend`, on env-var missing or Casdoor unreachable. Other
  per-user failures inside `disable-expired` are logged but don't fail the
  command.
- **Sweep:** per-user errors recorded in `SweepReport.Errors`, sweep
  continues to the next user. Pattern matches `subsync`.
- **Idempotent:** running `disable-expired` twice is safe; second run does
  nothing because already-disabled users are skipped.
- **Cron failure visibility:** Render shows cron job exit code + stdout in
  the dashboard. Sweep prints the `SweepReport` so the operator can scan
  daily.

## Testing

**Unit (no Casdoor):**
- `TestIsExpired` — table of (`expireAt` string, `now`, expected bool):
  missing / future / today / past / malformed.
- `TestExpireAtString` — `days=0` → today, `days=30` → today+30, in UTC.

**Integration (`RUN_INTEGRATION=1`, env-gated like the existing
`TestEndToEnd_PhoneOtpLogin`):**
- Create a user via `expiry.SetExpireAt(..., -1, now)` (yesterday) via the
  admin API.
- Run `expiry.SweepOrg(ctx, client, "intent-pulse-org", now)`.
- Assert: user is now `forbidden=true`; `SweepReport.Disabled == 1`.
- Tear down: delete the test user.

The test uses the same `CASDOOR_*` env contract as the existing integration
test; runs in the same `test/integration/` package.

## File layout

```
auth-server/
├── cmd/
│   ├── subsync/                       (existing)
│   └── authadmin/                     (new)
│       ├── Dockerfile
│       └── main.go                    # arg parsing + subcommand dispatch
├── expiry/                            (new package)
│   ├── expiry.go                      # pure helpers
│   ├── sweep.go                       # SweepOrg
│   └── expiry_test.go
├── test/integration/
│   └── expiry_sweep_test.go           (new)
├── docs/
│   ├── admin-users.md                 (new) — CLI usage, CSV format, cron
│   └── deploy-render.md               (update) — mention the new cron service
└── render.yaml                        (update) — add the cron_job service
```

`go.mod` doesn't change — we use stdlib `flag`, `encoding/csv`, `time`,
existing `subscription` package, existing `verifier` if needed.

## Out of scope (v1)

- Notification before expiry (would need email/SMS provider + a per-user
  notification state).
- Per-user TTL different from days (date input).
- Admin web UI inside auth-server.
- Bulk disable/enable across an org (one-off per user via CLI is fine).
- Self-service "extend my access" by the client (admin-only).
- Locking the auth gate based on `expireAt` directly (we rely on
  `forbidden=true` propagating via refresh; one ~1h window of access after
  expiry is acceptable).

## Open questions (none blocking implementation)

- Should `create-batch` print the would-be `expireAt` and prompt for
  confirmation when run interactively? → No, keep non-interactive; result
  CSV is the audit trail.
- Should the CLI honor an `AUTHADMIN_ORG` env var so `--org` can be omitted?
  → Nice-to-have, can add later; keep `--org` required in v1 for clarity.
