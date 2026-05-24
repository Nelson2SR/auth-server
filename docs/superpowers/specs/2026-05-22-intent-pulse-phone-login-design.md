# Phone-login integration: intent-pulse-crawler → Casdoor

**Status:** design / integration spec (deliverable: design + code snippets; the
intent-pulse-crawler repo is not modified by this work)
**Date:** 2026-05-22

## Goal

Gate the `intent-pulse-crawler` FastAPI dashboard (and its `/api/*` endpoints)
behind **phone-OTP login** served by our Casdoor auth server
(`https://auth-server-msje.onrender.com`). Access is an **allowlist**: only
phone numbers an admin has provisioned in Casdoor can log in; there is no
self-signup.

## Context

- `intent-pulse-crawler` is a server-side FastAPI app (Python) deployed on Render
  at `https://intent-pulse-crawler.onrender.com`. It serves a static dashboard
  plus JSON endpoints (`/api/crawler/*`, `/api/signals`, `/api/config`, an SSE
  log stream, etc.). It currently has **no authentication**.
- Being server-side, it is a **confidential OAuth client**: it can hold a client
  secret and run the authorization-code flow, keeping a normal session cookie.
- Casdoor is OIDC-compliant and exposes
  `…/.well-known/openid-configuration` (issuer = the Render URL) and a JWKS
  endpoint, so a standard OIDC client library works without hand-rolling.
- The hosted phone-OTP login page already works (SMS via Twilio over HTTPS, which
  Render does not block).

## Architecture

Two halves, independent and separately testable:

1. **Casdoor provisioning** — a dedicated org + application + allowlisted users.
   Created via the Casdoor admin API on the live instance. (Init re-seeding is
   disabled there — `INIT_DATA_FILE=/noop_init.json` — so API-created entities
   persist across restarts.)
2. **FastAPI OAuth client** — Authlib OIDC client + Starlette `SessionMiddleware`
   + an auth gate. Code lives in the intent-pulse-crawler repo (snippets below).

```
Browser ──GET /──▶ intent-pulse-crawler (FastAPI)
   │  no session → 302 /auth/login
   ▼
intent-pulse-crawler ──302──▶ Casdoor /login/oauth/authorize?client_id=…&redirect_uri=…&response_type=code&scope=openid profile&state=…
   │
   ▼  hosted phone-OTP page (enter phone + SMS code)  ── allowlist enforced here
Casdoor ──302──▶ /auth/callback?code=…&state=…
   │
   ▼  back-channel POST /api/login/oauth/access_token (code → id_token + access_token)
intent-pulse-crawler  validate id_token via JWKS → session["user"] = {sub,name,phone}
   │
   ▼  302 → original URL
```

The dashboard and its `/api/*` calls are same-origin, so one signed session
cookie authenticates both (XHR/`fetch`/`EventSource` send the cookie
automatically).

## Part A — Casdoor provisioning

All on the live instance (`$BASE = https://auth-server-msje.onrender.com`),
using admin authentication. Values shown are the intended end state.

### A1. Org `intent-pulse-org`

```jsonc
{
  "owner": "admin",
  "name": "intent-pulse-org",
  "displayName": "Intent Pulse",
  "passwordType": "plain",
  "languages": ["en"],          // REQUIRED — null causes a blank login page (known bug)
  "countryCodes": ["SG"]        // phone country selector for the SMS login tab
}
```

### A2. Signing cert `cert-intent-pulse`

Casdoor signs JWTs per-application with a cert. Generate an RSA cert
(`cert-intent-pulse`) in `admin` and reference it from the application. The
application's **public** cert PEM is what any offline verifier would use; with
Authlib we instead rely on the discovery `jwks_uri`, so no PEM handling is
needed on the client.

### A3. Application `intent-pulse`

```jsonc
{
  "owner": "admin",
  "name": "intent-pulse",
  "organization": "intent-pulse-org",
  "displayName": "Intent Pulse Crawler",
  "cert": "cert-intent-pulse",
  "enableSignUp": false,                       // ← THE ALLOWLIST: no self-registration
  "enablePassword": false,                     // phone-OTP only
  "clientId": "<generated>",
  "clientSecret": "<generated>",
  "redirectUris": [
    "https://intent-pulse-crawler.onrender.com/auth/callback",
    "http://localhost:8000/auth/callback"      // local dev
  ],
  "grantTypes": ["authorization_code", "refresh_token"],
  "tokenFormat": "JWT",
  "expireInHours": 1,
  "refreshExpireInHours": 720,
  "signinMethods": [
    {"name": "Verification code", "displayName": "SMS code", "rule": "Phone only"}
  ],
  "providers": [
    {"name": "provider-twilio-app-a", "canSignUp": false, "canSignIn": true}
  ]
}
```

Notes:
- `rule` MUST be `"Phone only"` (valid Casdoor key); `"phone"` silently drops the
  method and the SMS tab won't render (known bug).
- The existing Twilio provider is shared (a provider is owned by `admin` and
  linked per app), so no new Twilio account is needed.
- With only one signin method, the login page shows the phone form directly.

### A4. Provision allowlisted users

For each allowed person, create a user in `intent-pulse-org` with their phone
(local number) + `countryCode`/`region` = `SG`. No password needed. Example:

```jsonc
{
  "owner": "intent-pulse-org",
  "name": "<username>",
  "displayName": "<name>",
  "phone": "<local number, e.g. 98653286>",
  "countryCode": "SG",
  "region": "SG"
}
```

Because `enableSignUp=false`, a phone that isn't provisioned cannot obtain a login
code — the allowlist is enforced entirely at Casdoor, before any token is issued.

## Part B — FastAPI integration (intent-pulse-crawler)

### B1. Dependencies (`requirements.txt`)

```
authlib>=1.3
itsdangerous>=2.0        # required by Starlette SessionMiddleware
httpx>=0.27              # Authlib's async transport
```

### B2. Configuration (env vars)

| Var | Example | Notes |
|-----|---------|-------|
| `CASDOOR_ENDPOINT` | `https://auth-server-msje.onrender.com` | issuer base |
| `CASDOOR_CLIENT_ID` | `<intent-pulse clientId>` | from A3 |
| `CASDOOR_CLIENT_SECRET` | `<intent-pulse clientSecret>` | from A3 |
| `OIDC_REDIRECT_URI` | `https://intent-pulse-crawler.onrender.com/auth/callback` | MUST exactly match a registered redirect URI |
| `SESSION_SECRET` | `<32+ random bytes>` | signs the session cookie |

### B3. Auth module (`src/intent_pulse/api/auth.py`, new)

```python
import os
from authlib.integrations.starlette_client import OAuth
from starlette.middleware.sessions import SessionMiddleware
from starlette.requests import Request
from starlette.responses import RedirectResponse, JSONResponse

CASDOOR_ENDPOINT = os.environ["CASDOOR_ENDPOINT"].rstrip("/")
OIDC_REDIRECT_URI = os.environ["OIDC_REDIRECT_URI"]

oauth = OAuth()
oauth.register(
    name="casdoor",
    server_metadata_url=f"{CASDOOR_ENDPOINT}/.well-known/openid-configuration",
    client_id=os.environ["CASDOOR_CLIENT_ID"],
    client_secret=os.environ["CASDOOR_CLIENT_SECRET"],
    client_kwargs={"scope": "openid profile"},
)

# Paths reachable without a session.
PUBLIC_PREFIXES = ("/health", "/auth/")

def install_auth(app):
    app.add_middleware(
        SessionMiddleware,
        secret_key=os.environ["SESSION_SECRET"],
        https_only=True,        # set False for local http dev
        same_site="lax",
    )

    @app.middleware("http")
    async def require_login(request: Request, call_next):
        path = request.url.path
        if path.startswith(PUBLIC_PREFIXES) or "user" in request.session:
            return await call_next(request)
        # Unauthenticated:
        accept = request.headers.get("accept", "")
        if "text/html" in accept:                       # top-level navigation
            request.session["next"] = str(request.url)
            return RedirectResponse("/auth/login")
        return JSONResponse({"detail": "unauthenticated"}, status_code=401)

    @app.get("/auth/login")
    async def login(request: Request):
        return await oauth.casdoor.authorize_redirect(request, OIDC_REDIRECT_URI)

    @app.get("/auth/callback")
    async def callback(request: Request):
        token = await oauth.casdoor.authorize_access_token(request)  # exchanges code + validates id_token via JWKS
        info = token.get("userinfo") or await oauth.casdoor.userinfo(token=token)
        request.session["user"] = {
            "sub": info.get("sub"),
            "name": info.get("name") or info.get("preferred_username"),
            "phone": info.get("phone"),
        }
        return RedirectResponse(request.session.pop("next", "/"))

    @app.get("/auth/logout")
    async def logout(request: Request):
        request.session.clear()
        return RedirectResponse("/")
```

### B4. Wire into the app (`src/intent_pulse/api/routes.py`)

After `app = FastAPI(...)` and the existing CORS middleware:

```python
from intent_pulse.api.auth import install_auth
install_auth(app)
```

`install_auth` adds the session middleware, the global auth gate, and the three
`/auth/*` routes. All existing endpoints become protected automatically; only
`/health` and `/auth/*` stay public.

### B5. Front-end: handle 401 on XHR

The dashboard's JS calls `/api/*`. When the session expires, those return `401`.
Add one guard to the fetch layer so the SPA bounces to login:

```js
async function api(path, opts) {
  const r = await fetch(path, opts);
  if (r.status === 401) { window.location = "/auth/login"; return; }
  return r;
}
```

(The SSE log stream uses `EventSource`, which also sends the cookie; on 401 it
errors — reload to `/auth/login`.)

## Configuration / deployment

- **Casdoor:** add the production + dev redirect URIs to the `intent-pulse` app
  (A3). They must match `OIDC_REDIRECT_URI` byte-for-byte.
- **intent-pulse-crawler on Render:** set the five env vars from B2 on the
  service. `https_only=True` is correct on Render (TLS terminated at the edge,
  forwarded as HTTPS).
- No outbound-SMTP concern here — login uses SMS (Twilio HTTPS), already working.

## Testing

1. **Local:** run intent-pulse-crawler with `CASDOOR_ENDPOINT` pointing at the
   deployed Casdoor and `OIDC_REDIRECT_URI=http://localhost:8000/auth/callback`
   (registered in A3), `https_only=False`. Hit a protected route → expect a
   redirect to the Casdoor phone-OTP page.
2. Log in with an **allowlisted** phone → expect redirect back to the dashboard
   with a working session; `/api/*` calls succeed.
3. Try a **non-allowlisted** phone → Casdoor refuses to issue a login code; no
   token is ever minted.
4. `/auth/logout` clears the session → protected routes redirect to login again.
5. Confirm `/health` stays open (Render health checks must not require auth).

## Security notes

- **Allowlist** is enforced server-side at Casdoor (`enableSignUp=false` + only
  provisioned users). The app additionally trusts only Casdoor-issued, JWKS-
  validated tokens.
- **Session cookie** is signed (`SESSION_SECRET`), `https_only`, `same_site=lax`.
  Rotate `SESSION_SECRET` invalidates all sessions.
- **Redirect URI** allowlist in Casdoor prevents open-redirect/code interception.
- **id_token validation** (signature, issuer, audience, expiry, nonce) is handled
  by Authlib using the discovery document — do not skip it.
- Access-token lifetime is 1h; add refresh-token handling later if silent renewal
  is desired (out of scope for v1).

## Out of scope (v1)

- Per-user authorization/roles inside the dashboard (everyone who logs in has full
  access).
- Token refresh / silent renewal.
- Email login (Render blocks SMTP; phone-only for now).
- Modifying the intent-pulse-crawler repo (this is a spec; code is applied by the
  app owner).
