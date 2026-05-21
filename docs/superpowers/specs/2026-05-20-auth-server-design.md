# Auth Server Design — Multi-Tenant Identity & Subscription Service

**Date:** 2026-05-20
**Status:** Approved design, ready for implementation planning

## Summary

A self-hosted, multi-tenant authentication and authorization server built by adopting
[Casdoor](https://casdoor.org) (open-source, Go-based IAM/SSO). It serves multiple
applications from one central instance. Users log in via **WeChat Web QR (Open Platform)**
or **phone OTP (Twilio SMS)** through Casdoor's hosted login page. After login, applications
receive signed JWTs whose claims carry the user's **plan, role, subscription status, and
expiry**, so each app can gate features and check subscription expiration **offline** by
verifying the JWT signature — no per-request call back to the auth server.

We adopt and configure Casdoor rather than building auth from scratch. Custom code is limited
to a version-controlled bootstrap/provisioning layer and small per-app token-verification
helpers. We extend Casdoor's source only if a required claim or flow cannot be configured.

## Goals

- One central identity provider serving multiple applications (multi-tenant).
- Login via WeChat Web QR and phone OTP (SMS).
- User management (provided by Casdoor's admin UI and API).
- Issue tokens whose claims let apps verify subscription expiration offline.
- Reproducible, version-controlled setup (not click-ops).

## Non-Goals (v1)

- Payment-provider integration / automatic billing-driven renewals (API is exposed so a
  billing service can drive subscriptions later, but no payment integration is built now).
- Instant (sub-token-TTL) revocation for individual actions; offline verification accepts a
  bounded staleness window equal to the access-token TTL.
- WeChat surfaces other than Web QR (Official Account, Mini Program). WeChat is configured as
  a provider so other surfaces can be added later.
- High-availability / multi-node clustering (single-host Docker Compose for v1).

## Architecture

A single Casdoor instance is the central IdP for all applications. Apps never store passwords
or talk to WeChat/Twilio directly; they delegate login to Casdoor via the OIDC authorization
code flow and verify the resulting JWTs offline.

```
                         ┌─────────────────────────────┐
   App A (web/mobile) ──▶│  Casdoor (Go)               │
   App B ...          ──▶│  - hosted login page        │──▶ WeChat Open Platform (Web QR)
                         │  - OIDC/OAuth2 provider     │──▶ Twilio (SMS OTP)
                         │  - user mgmt + admin UI     │
                         │  - pricing/plan/subscription│
                         └──────────────┬──────────────┘
                                        │
                                   PostgreSQL
```

Components:

- **Casdoor** — the IdP: hosted login page, OIDC/OAuth2 provider, user management, and the
  pricing/plan/subscription engine.
- **PostgreSQL** — Casdoor's datastore (users, apps, orgs, plans, subscriptions, tokens).
- **Reverse proxy (Caddy or Nginx)** — TLS termination in front of Casdoor.
- **Bootstrap/provisioning layer (custom)** — declarative config or an init script using the
  Casdoor Go SDK that provisions organizations, applications, providers, and plans
  reproducibly.
- **Per-app verification helper (custom, small)** — a snippet/SDK per app language that
  verifies the JWT against Casdoor's JWKS and evaluates subscription validity.

## Multi-Tenant Model

Casdoor hierarchy → our concepts:

- **Organization** = tenant boundary. Owns its users, its providers (WeChat, Twilio), and its
  pricing/plans.
- **Application** = a registered app ("App A", "App B"). Belongs to an organization; defines
  enabled login methods, redirect URLs, client ID/secret, and token/claim format.
- **User** = belongs to an organization; can be used across that organization's applications.

**Decision (v1):** **one organization per application** — App A → Org A with its own users and
plans. This gives each app fully isolated users and subscription rules.

**Migration path:** if shared SSO users across apps are later desired, move to "one
organization, many applications" so users in that org sign in to all its apps. This is a known
Casdoor topology; the change is configuration plus user data migration, not a redesign.

## Login Flows

Both methods use the OIDC **authorization code flow** and Casdoor's hosted login page.

### WeChat Web QR (Open Platform)

1. App redirects the user to the Casdoor login page.
2. User selects "WeChat"; Casdoor renders the QR code.
3. User scans with the WeChat app; WeChat calls back to Casdoor.
4. Casdoor creates or links the user, then redirects to the app with an authorization code.
5. App exchanges the code for tokens at Casdoor's token endpoint.

### Phone OTP (Twilio)

1. On the same login page, user enters a phone number.
2. Casdoor sends a one-time code via the Twilio SMS provider.
3. User enters the code; Casdoor verifies it.
4. Casdoor creates or links the user, redirects with an authorization code; app exchanges for
   tokens.

### Account linking

If the same person uses both methods, they should resolve to one identity. v1 rule: link by
verified phone number where available; otherwise treat as distinct users. Exact linking
behavior is configured in Casdoor and documented during implementation.

## Token & Subscription Model

The core requirement: apps check subscription expiration offline.

- Application token format = **JWT-Custom**, with token fields
  `["Id", "Name", "DisplayName", "Properties"]`.
- **Claim shape (verified against Casdoor v1.812.0):** Casdoor's `JWT-Custom` token fields
  select *User struct fields*, not arbitrary keys. The subscription data is stored in the
  user's **Properties** map and surfaces as a single nested **`properties`** claim:
  - `sub` — user identity; `iss`, `aud`, `iat`, `exp` — standard OIDC claims
  - `properties.plan` — e.g. `free`, `pro`
  - `properties.role` — the Casdoor role backing the plan
  - `properties.subscriptionStatus` — one of `Pending | Active | Upcoming | Suspended | Expired | Error`
  - `properties.subscriptionEndTime` — expiry timestamp (RFC3339)
  (The claim *names* are unchanged from the original design; they are nested one level under
  `properties` rather than top-level. See `docs/technical/casdoor-findings.md`.)
- **Access-token TTL: 1 hour** (decision, 2026-05-21; bounds staleness of subscription state).
  Casdoor configures token lifetime in whole hours (`expireInHours`), so 1 hour is the
  practical minimum; this supersedes the originally-proposed 15 minutes.
- **Refresh-token TTL: 30 days** (decision); refresh tokens are stored server-side and
  revocable.
- **App-side check:** verify the JWT signature against Casdoor's public signing key, then
  require `properties.subscriptionStatus == Active` **and** `now < properties.subscriptionEndTime`.
- **Keeping claims current:** a small sync job mirrors each user's active Subscription into
  their Casdoor user Properties, so the next issued/refreshed token carries up-to-date values.

**Accepted trade-off:** a user who cancels or expires mid-token retains access for up to the
1-hour access-token TTL. This is the cost of offline verification. Instant revocation for
specific sensitive actions can be added later via an online check at those points; out of
scope for v1.

## Subscription Lifecycle

- Define **Pricing → Plans** in Casdoor (e.g. Free / Pro / Pro-Annual). Each plan maps to a
  **Role** carrying permissions.
- A **Subscription** ties a user to a plan with start/end dates and the state machine
  `Pending → Active → Expired`, plus `Suspended`.
- **Granting/renewing (v1):** via Casdoor admin UI or API. The API is exposed so a future
  billing service can drive subscription creation/renewal; no payment integration is built in
  v1.
- On any subscription change, the updated state and expiry flow into the next issued or
  refreshed token.

## Configuration vs. Custom Code

**Configuration (the majority):** organizations, applications, WeChat + Twilio providers,
plans/pricing, token claim fields, and TTLs.

**Custom code (small, deliberate):**

- **Bootstrap/provisioning layer** — declarative config or an init script (Casdoor Go SDK)
  that provisions orgs, apps, providers, and plans reproducibly and under version control.
- **Per-app verification helper** — JWT signature + subscription validity check, one small
  snippet per app language.
- Casdoor source extension only if a required claim or flow cannot be configured (avoided
  unless forced).

## Deployment, Operations, Security

- **Docker Compose**: `casdoor` + `postgres`, behind a reverse proxy (Caddy/Nginx) for TLS.
- **Secrets** (WeChat appid/secret, Twilio keys, JWT signing cert, DB credentials) supplied via
  environment/secret files; never committed to the repo.
- **JWT signing certificate** is the root of trust: generate a dedicated cert and plan
  rotation.
- **Backups**: regular PostgreSQL backups.
- **Admin protection**: admin accounts secured with MFA (Casdoor supports TOTP).

## Testing Strategy

Integration tests that stand up the Docker Compose stack, run the bootstrap layer, then assert:

- An application can complete the OIDC login flow (WeChat and Twilio providers mocked/stubbed).
- Issued tokens carry the correct subscription claims (`plan`, `role`, `subscriptionStatus`,
  `subscriptionEndTime`).
- The per-app verification helper accepts an active subscription and **rejects** an expired or
  suspended one.
- Refresh-token rotation works and a revoked refresh token is rejected.

## Open Decisions (defaulted, easy to revisit)

1. **Tenancy topology** — defaulted to one-org-per-app; migration path to shared-SSO documented.
2. **Token TTLs** — set to 1 hour access / 30 day refresh (Casdoor hour-granularity constraint).
3. **Account-linking rule** — defaulted to link-by-verified-phone; finalize during
   implementation.

## References

- Casdoor subscription: https://casdoor.org/docs/pricing/subscription/
- Casdoor plan/pricing: https://casdoor.org/docs/pricing/plan/
- Casdoor token formats (JWT-Custom): https://casdoor.org/docs/token/overview/
