# Multi-Tenant Auth Server (Casdoor) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stand up a self-hosted, multi-tenant authentication & subscription server by configuring Casdoor, provision one tenant (App A) with WeChat Web QR + Twilio phone-OTP login, and let apps verify subscription expiry offline from JWT claims.

**Architecture:** A single Casdoor instance (Docker Compose + PostgreSQL) is the central OIDC/OAuth2 provider. Tenants/apps/providers/plans are provisioned declaratively via `init_data.json`. Subscription facts are mirrored onto user properties by a small Go sync job so Casdoor emits them as `JWT-Custom` claims; a Go verifier library checks the JWT signature offline (RSA public key) and gates on `subscriptionStatus`/`subscriptionEndTime`.

**Tech Stack:** Casdoor (`casbin/casdoor` image), PostgreSQL 16, Docker Compose, Caddy/Nginx (TLS), Go 1.22 (`github.com/golang-jwt/jwt/v5`), WeChat Open Platform, Twilio.

**Key decisions baked in (from spec, 2026-05-21):**
- Access token TTL = **1 hour**, refresh = **30 days** (Casdoor configures lifetime in whole hours; 1h is the practical minimum and supersedes the original 15-minute proposal).
- Offline verification via an **RSA public-key PEM pinned in the app** (Casdoor's signing cert is long-lived; JWKS-refresh is a documented future enhancement).
- Tenancy: **one organization per application** (this plan delivers the App A → `app-a-org` slice end-to-end; additional apps repeat the same config).
- Subscription claims reach the JWT by **mirroring subscription state into Casdoor user properties** (`plan`, `role`, `subscriptionStatus`, `subscriptionEndTime`) which are selected as `JWT-Custom` token fields.

**Module layout (single Go module at repo root, `module github.com/acme/auth-server`):**

| Path | Responsibility |
|------|----------------|
| `docker-compose.yml`, `.env.example` | Run Casdoor + PostgreSQL (+ reverse proxy) |
| `conf/app.conf` | Casdoor runtime config (DB, origin, init data file) |
| `init_data.json` | Declarative provisioning: org, app, cert, providers, pricing/plans, a seed test user |
| `verifier/` | Go library: offline JWT signature + subscription-claim gate |
| `subscription/` | Go: pure mapping + syncer + Casdoor admin HTTP client |
| `cmd/subsync/` | CLI entrypoint that runs the subscription→user-property sync |
| `examples/appserver/` | Demo HTTP middleware using `verifier` |
| `test/integration/` | End-to-end test against the live Compose stack |

---

## Phase 0 — Repository scaffolding

### Task 0: Initialize repo and Go module

**Files:**
- Create: `.gitignore`
- Create: `go.mod`
- Create: `README.md`

- [ ] **Step 1: Initialize git and Go module**

Run:
```bash
cd /Users/surong/Project/auth-server
git init
go mod init github.com/acme/auth-server
```
Expected: `go.mod` created with `module github.com/acme/auth-server` and a `go 1.22` line (or your installed minor).

- [ ] **Step 2: Write `.gitignore`**

```gitignore
# secrets & local env
.env
certs/*.pem
certs/*.key
# build output
/bin/
subsync
# casdoor local data
*.log
```

- [ ] **Step 3: Write a minimal `README.md`**

```markdown
# Auth Server

Self-hosted multi-tenant auth & subscription server built on Casdoor.
See `docs/superpowers/specs/2026-05-20-auth-server-design.md` for the design.

- `docker-compose up -d` to run Casdoor + PostgreSQL
- `verifier/` — offline JWT + subscription verification library
- `cmd/subsync/` — mirrors subscription state into user properties
```

- [ ] **Step 4: Commit**

```bash
git add .gitignore go.mod README.md
git commit -m "chore: scaffold auth-server repo and Go module"
```

---

## Phase 1 — Run Casdoor + PostgreSQL

### Task 1: Docker Compose stack and Casdoor base config

**Files:**
- Create: `.env.example`
- Create: `docker-compose.yml`
- Create: `conf/app.conf`

- [ ] **Step 1: Write `.env.example`**

```dotenv
# Pin a specific Casdoor release (check https://hub.docker.com/r/casbin/casdoor/tags)
CASDOOR_VERSION=v1.812.0
# PostgreSQL
POSTGRES_USER=casdoor
POSTGRES_PASSWORD=change_me_strong
POSTGRES_DB=casdoor
# Public origin (used in OIDC issuer & redirect validation)
CASDOOR_ORIGIN=http://localhost:8000
```

- [ ] **Step 2: Create the working `.env`**

Run:
```bash
cp .env.example .env
```
Then edit `.env` and set a strong `POSTGRES_PASSWORD`. (`.env` is gitignored.)

- [ ] **Step 3: Write `conf/app.conf`**

```ini
appname = casdoor
httpport = 8000
runmode = prod
copyrequestbody = true
driverName = postgres
dataSourceName = "user=casdoor password=change_me_strong host=db port=5432 sslmode=disable dbname=casdoor"
dbName = casdoor
tableNamePrefix =
showSql = false
redisEndpoint =
defaultStorageProvider =
isCloudIntranet = false
authState = "casdoor"
socks5Proxy = ""
verificationCodeTimeout = 10
initScore = 0
logPostOnly = true
origin = "http://localhost:8000"
originFrontend =
enableErrorMask = false
inactiveTimeoutMinutes =
initDataFile = "./init_data.json"
```
Note: `dataSourceName`'s password must match `.env`'s `POSTGRES_PASSWORD`. For non-local deploys, change `origin` to the HTTPS public URL.

- [ ] **Step 4: Write `docker-compose.yml`**

```yaml
services:
  db:
    image: postgres:16
    restart: unless-stopped
    environment:
      POSTGRES_USER: ${POSTGRES_USER}
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
      POSTGRES_DB: ${POSTGRES_DB}
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U ${POSTGRES_USER}"]
      interval: 5s
      timeout: 5s
      retries: 10

  casdoor:
    image: casbin/casdoor:${CASDOOR_VERSION}
    restart: unless-stopped
    depends_on:
      db:
        condition: service_healthy
    ports:
      - "8000:8000"
    volumes:
      - ./conf/app.conf:/conf/app.conf:ro
      - ./init_data.json:/init_data.json:ro

volumes:
  pgdata:
```
Note: `init_data.json` is created in Task 4. Create an empty placeholder now so the mount works: `echo '{}' > init_data.json`.

- [ ] **Step 5: Create the init_data placeholder and bring up the stack**

Run:
```bash
echo '{}' > init_data.json
docker compose up -d
docker compose ps
```
Expected: both `db` and `casdoor` containers `running` (db `healthy`).

- [ ] **Step 6: Verify Casdoor serves**

Run:
```bash
curl -s -o /dev/null -w "%{http_code}\n" http://localhost:8000/
```
Expected: `200` (the Casdoor login/landing page). If `000`/connection refused, wait ~15s for first-run DB migration and retry; check `docker compose logs casdoor` for migration completion.

- [ ] **Step 7: Commit**

```bash
git add .env.example docker-compose.yml conf/app.conf init_data.json
git commit -m "feat: docker-compose Casdoor + PostgreSQL stack"
```

---

## Phase 2 — Confirm Casdoor capabilities (spike)

This short phase empirically pins down two Casdoor-specific behaviors the later config depends on, so we configure against confirmed facts. Record findings in a committed notes file.

### Task 2: Confirm token-claim emission and admin API auth

**Files:**
- Create: `docs/technical/casdoor-findings.md`

- [ ] **Step 1: Get an admin API credential**

The built-in app `app-built-in` in org `built-in` has a client ID/secret usable for admin REST calls. Retrieve them:
```bash
# Log into the web UI at http://localhost:8000 (default admin/123 on a fresh instance),
# go to Applications -> app-built-in, and copy Client ID and Client Secret.
```
Export for the next steps:
```bash
export CASDOOR=http://localhost:8000
export CID=<client-id>
export CSECRET=<client-secret>
```

- [ ] **Step 2: Confirm admin API auth works (list organizations)**

Run:
```bash
curl -s "$CASDOOR/api/get-organizations?clientId=$CID&clientSecret=$CSECRET" | head -c 400; echo
```
Expected: a JSON body with `"status":"ok"` and a `data` array containing `built-in`. Record in findings whether `clientId`/`clientSecret` query params authenticate admin GETs (alternative: HTTP Basic `-u "$CID:$CSECRET"`). **The confirmed mechanism is what Task 7's client uses.**

- [ ] **Step 3: Confirm a user custom property is emitted as a JWT-Custom claim**

In the web UI: create a temporary application with `Token format = JWT-Custom`, add token fields `plan` and `subscriptionStatus`; on a test user add Properties `plan=pro`, `subscriptionStatus=Active`. Use the app's signin URL or password grant to mint a token, then decode its payload:
```bash
# paste the access_token JWT
echo '<JWT>' | cut -d. -f2 | base64 -d 2>/dev/null; echo
```
Expected: the payload JSON contains `"plan":"pro"` and `"subscriptionStatus":"Active"`. **If custom user properties do NOT appear**, record this and switch the claim source in Task 5 to whichever user attribute Casdoor does emit (e.g. a built-in attribute), keeping the same claim names.

- [ ] **Step 4: Write findings**

```markdown
# Casdoor Capability Findings (2026-05-21)

- Admin API auth: <clientId/clientSecret query | HTTP Basic> — confirmed against /api/get-organizations.
- JWT-Custom emits user custom properties as claims: <yes | no, use attribute X>.
- Token expiry granularity: whole hours (expireInHours). Access = 1h, refresh = 720h (30d).
- Casdoor version: <output of docker compose images casdoor>.
```

- [ ] **Step 5: Commit**

```bash
git add docs/technical/casdoor-findings.md
git commit -m "docs: record Casdoor capability spike findings"
```

---

## Phase 3 — Declarative provisioning (App A tenant)

### Task 3: Author `init_data.json` for the App A tenant

**Files:**
- Modify: `init_data.json` (replace the `{}` placeholder)

- [ ] **Step 1: Replace `init_data.json` with the App A provisioning data**

```json
{
  "organizations": [
    {
      "owner": "admin",
      "name": "app-a-org",
      "displayName": "App A Organization",
      "passwordType": "plain",
      "defaultApplication": "app-a"
    }
  ],
  "certs": [
    {
      "owner": "admin",
      "name": "cert-app-a",
      "type": "x509",
      "cryptoAlgorithm": "RS256",
      "bitSize": 4096,
      "expireInYears": 20
    }
  ],
  "providers": [
    {
      "owner": "admin",
      "name": "provider-wechat-app-a",
      "category": "OAuth",
      "type": "WeChat",
      "clientId": "REPLACE_WECHAT_APPID",
      "clientSecret": "REPLACE_WECHAT_SECRET"
    },
    {
      "owner": "admin",
      "name": "provider-twilio-app-a",
      "category": "SMS",
      "type": "Twilio SMS",
      "clientId": "REPLACE_TWILIO_SID",
      "clientSecret": "REPLACE_TWILIO_AUTH_TOKEN",
      "templateCode": "REPLACE_TWILIO_FROM_NUMBER"
    }
  ],
  "applications": [
    {
      "owner": "admin",
      "name": "app-a",
      "organization": "app-a-org",
      "displayName": "App A",
      "cert": "cert-app-a",
      "enablePassword": true,
      "enableSignUp": true,
      "clientId": "REPLACE_APP_A_CLIENT_ID",
      "clientSecret": "REPLACE_APP_A_CLIENT_SECRET",
      "redirectUris": ["http://localhost:9000/callback"],
      "tokenFormat": "JWT-Custom",
      "tokenFields": ["plan", "role", "subscriptionStatus", "subscriptionEndTime"],
      "expireInHours": 1,
      "refreshExpireInHours": 720,
      "grantTypes": ["authorization_code", "refresh_token"],
      "providers": [
        {"name": "provider-wechat-app-a", "canSignUp": true, "canSignIn": true},
        {"name": "provider-twilio-app-a", "canSignUp": true, "canSignIn": true}
      ],
      "signinMethods": [
        {"name": "Password", "displayName": "Password"},
        {"name": "Verification code", "displayName": "SMS code", "rule": "phone"}
      ]
    }
  ],
  "users": [
    {
      "owner": "app-a-org",
      "name": "test-user",
      "displayName": "Test User",
      "password": "Test123456!",
      "phone": "+15555550100",
      "phoneVerified": true,
      "properties": {
        "plan": "free",
        "role": "free-role",
        "subscriptionStatus": "Expired",
        "subscriptionEndTime": "2000-01-01T00:00:00Z"
      }
    }
  ]
}
```
Notes:
- `REPLACE_*` values are filled from your WeChat Open Platform, Twilio, and chosen App A OAuth client credentials. Keep real secrets out of git: for production, template this file and inject secrets at deploy time. For local dev the placeholders above let the stack import.
- `expireInHours: 1` and `refreshExpireInHours: 720` encode the 1-hour / 30-day decision.
- Field names/shape match what the spike (Task 2) observed from `GetXXX` responses; adjust any field that differs in your Casdoor version per the findings file.

- [ ] **Step 2: Re-import by restarting Casdoor**

Run:
```bash
docker compose restart casdoor
sleep 10
curl -s "$CASDOOR/api/get-organizations?clientId=$CID&clientSecret=$CSECRET" | grep -o '"name":"app-a-org"'
```
Expected: prints `"name":"app-a-org"` (the org was imported). Casdoor imports `init_data.json` idempotently on startup, skipping entities that already exist.

- [ ] **Step 3: Verify the application token config imported**

Run:
```bash
curl -s "$CASDOOR/api/get-application?id=admin/app-a&clientId=$CID&clientSecret=$CSECRET" \
  | python3 -c "import sys,json;d=json.load(sys.stdin)['data'];print(d['tokenFormat'],d['expireInHours'],d['tokenFields'])"
```
Expected: `JWT-Custom 1 ['plan', 'role', 'subscriptionStatus', 'subscriptionEndTime']`.

- [ ] **Step 4: Commit**

```bash
git add init_data.json
git commit -m "feat: provision App A tenant via init_data.json (JWT-Custom, 1h/30d)"
```

### Task 4: Provision pricing & plans for App A

**Files:**
- Modify: `init_data.json` (add `roles`, `plans`, `pricings`)

- [ ] **Step 1: Add roles, plans, and pricing blocks to `init_data.json`**

Add these top-level arrays alongside the existing ones:
```json
  "roles": [
    {"owner": "app-a-org", "name": "free-role", "displayName": "Free", "users": [], "isEnabled": true},
    {"owner": "app-a-org", "name": "pro-role",  "displayName": "Pro",  "users": [], "isEnabled": true}
  ],
  "plans": [
    {"owner": "app-a-org", "name": "free", "displayName": "Free", "price": 0,  "period": "Monthly", "role": "app-a-org/free-role", "isEnabled": true},
    {"owner": "app-a-org", "name": "pro",  "displayName": "Pro",  "price": 9,  "period": "Monthly", "role": "app-a-org/pro-role",  "isEnabled": true}
  ],
  "pricings": [
    {"owner": "app-a-org", "name": "default-pricing", "displayName": "App A Pricing", "application": "app-a", "plans": ["free", "pro"], "isEnabled": true}
  ]
```

- [ ] **Step 2: Re-import and verify plans exist**

Run:
```bash
docker compose restart casdoor
sleep 10
curl -s "$CASDOOR/api/get-plans?owner=app-a-org&clientId=$CID&clientSecret=$CSECRET" | grep -o '"name":"pro"'
```
Expected: prints `"name":"pro"`.

- [ ] **Step 3: Commit**

```bash
git add init_data.json
git commit -m "feat: add App A pricing, plans, and plan->role mapping"
```

---

## Phase 4 — Offline verifier library (TDD)

### Task 5: Subscription claims model

**Files:**
- Create: `verifier/claims.go`
- Test: `verifier/claims_test.go`

- [ ] **Step 1: Write the failing test**

```go
package verifier

import (
	"testing"
	"time"
)

func claimsAt(status, end string) *Claims {
	return &Claims{SubscriptionStatus: status, SubscriptionEndTime: end}
}

func TestSubscriptionActive(t *testing.T) {
	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		status string
		end    string
		want   bool
	}{
		{"active and future", "Active", "2026-12-31T00:00:00Z", true},
		{"active but past", "Active", "2026-01-01T00:00:00Z", false},
		{"expired status", "Expired", "2026-12-31T00:00:00Z", false},
		{"suspended status", "Suspended", "2026-12-31T00:00:00Z", false},
		{"unparseable end", "Active", "not-a-time", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := claimsAt(c.status, c.end).SubscriptionActive(now)
			if got != c.want {
				t.Fatalf("SubscriptionActive=%v want %v", got, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./verifier/ -run TestSubscriptionActive -v`
Expected: FAIL — `undefined: Claims`.

- [ ] **Step 3: Write minimal implementation**

```go
package verifier

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims is the JWT payload: standard registered claims plus the Casdoor
// JWT-Custom subscription fields (emitted from user properties).
type Claims struct {
	jwt.RegisteredClaims
	Plan                string `json:"plan"`
	Role                string `json:"role"`
	SubscriptionStatus  string `json:"subscriptionStatus"`
	SubscriptionEndTime string `json:"subscriptionEndTime"` // RFC3339
}

// SubscriptionActive reports whether the subscription is currently usable.
func (c *Claims) SubscriptionActive(now time.Time) bool {
	if c.SubscriptionStatus != "Active" {
		return false
	}
	end, err := time.Parse(time.RFC3339, c.SubscriptionEndTime)
	if err != nil {
		return false
	}
	return now.Before(end)
}
```

- [ ] **Step 4: Add the jwt dependency and run the test**

Run:
```bash
go get github.com/golang-jwt/jwt/v5
go test ./verifier/ -run TestSubscriptionActive -v
```
Expected: PASS (all 5 sub-cases).

- [ ] **Step 5: Commit**

```bash
git add verifier/claims.go verifier/claims_test.go go.mod go.sum
git commit -m "feat(verifier): subscription claims model with active check"
```

### Task 6: Offline JWT verifier

**Files:**
- Create: `verifier/verifier.go`
- Test: `verifier/verifier_test.go`

- [ ] **Step 1: Write the failing test**

```go
package verifier

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func mustKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func pubPEM(t *testing.T, k *rsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(&k.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

func sign(t *testing.T, k *rsa.PrivateKey, c Claims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, c)
	s, err := tok.SignedString(k)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestVerify_ValidAndActive(t *testing.T) {
	k := mustKey(t)
	v, err := NewFromPEM(pubPEM(t, k))
	if err != nil {
		t.Fatal(err)
	}
	v.now = func() time.Time { return time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC) }
	tokenStr := sign(t, k, Claims{
		RegisteredClaims:    jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Date(2026, 5, 21, 13, 0, 0, 0, time.UTC))},
		Plan:                "pro",
		Role:                "pro-role",
		SubscriptionStatus:  "Active",
		SubscriptionEndTime: "2026-12-31T00:00:00Z",
	})
	claims, err := v.Authorize(tokenStr)
	if err != nil {
		t.Fatalf("Authorize returned error: %v", err)
	}
	if claims.Plan != "pro" {
		t.Fatalf("plan=%q want pro", claims.Plan)
	}
}

func TestVerify_BadSignature(t *testing.T) {
	signer := mustKey(t)
	other := mustKey(t)
	v, _ := NewFromPEM(pubPEM(t, other)) // verify with the wrong key
	tokenStr := sign(t, signer, Claims{
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
	})
	if _, err := v.Verify(tokenStr); err == nil {
		t.Fatal("expected signature error, got nil")
	}
}

func TestVerify_Expired(t *testing.T) {
	k := mustKey(t)
	v, _ := NewFromPEM(pubPEM(t, k))
	tokenStr := sign(t, k, Claims{
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour))},
	})
	if _, err := v.Verify(tokenStr); err == nil {
		t.Fatal("expected expiry error, got nil")
	}
}

func TestAuthorize_InactiveSubscription(t *testing.T) {
	k := mustKey(t)
	v, _ := NewFromPEM(pubPEM(t, k))
	tokenStr := sign(t, k, Claims{
		RegisteredClaims:    jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
		SubscriptionStatus:  "Expired",
		SubscriptionEndTime: "2026-12-31T00:00:00Z",
	})
	if _, err := v.Authorize(tokenStr); !errors.Is(err, ErrInactiveSubscription) {
		t.Fatalf("want ErrInactiveSubscription, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./verifier/ -run TestVerify -v`
Expected: FAIL — `undefined: NewFromPEM`.

- [ ] **Step 3: Write minimal implementation**

```go
package verifier

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ErrInactiveSubscription is returned by Authorize when the token is valid but
// the subscription is not currently active.
var ErrInactiveSubscription = errors.New("subscription is not active")

// Verifier validates Casdoor-issued JWTs offline.
type Verifier struct {
	keyfunc jwt.Keyfunc
	now     func() time.Time
}

// New builds a Verifier from a keyfunc supplying the RSA public key.
func New(keyfunc jwt.Keyfunc) *Verifier {
	return &Verifier{keyfunc: keyfunc, now: time.Now}
}

// NewFromPEM builds a Verifier that trusts a single PKIX RSA public key (PEM).
// Obtain the PEM from Casdoor's signing cert (Admin -> Certs) or its JWKS.
func NewFromPEM(pubPEM []byte) (*Verifier, error) {
	key, err := jwt.ParseRSAPublicKeyFromPEM(pubPEM)
	if err != nil {
		return nil, err
	}
	return New(func(*jwt.Token) (interface{}, error) { return key, nil }), nil
}

// Verify checks the RS256 signature and expiry, returning the parsed claims.
func (v *Verifier) Verify(tokenString string) (*Claims, error) {
	claims := &Claims{}
	if _, err := jwt.ParseWithClaims(tokenString, claims, v.keyfunc,
		jwt.WithValidMethods([]string{"RS256"})); err != nil {
		return nil, err
	}
	return claims, nil
}

// Authorize verifies the token and additionally requires an active subscription.
func (v *Verifier) Authorize(tokenString string) (*Claims, error) {
	claims, err := v.Verify(tokenString)
	if err != nil {
		return nil, err
	}
	if !claims.SubscriptionActive(v.now()) {
		return nil, ErrInactiveSubscription
	}
	return claims, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./verifier/ -v`
Expected: PASS (all claims + verifier tests).

- [ ] **Step 5: Commit**

```bash
git add verifier/verifier.go verifier/verifier_test.go go.sum
git commit -m "feat(verifier): offline RS256 JWT verification with subscription gate"
```

### Task 7: Example app middleware using the verifier

**Files:**
- Create: `examples/appserver/main.go`

- [ ] **Step 1: Write the example middleware**

```go
package main

import (
	"net/http"
	"os"

	"github.com/acme/auth-server/verifier"
)

func main() {
	pem, err := os.ReadFile(os.Getenv("CASDOOR_PUBLIC_KEY_PEM"))
	if err != nil {
		panic(err)
	}
	v, err := verifier.NewFromPEM(pem)
	if err != nil {
		panic(err)
	}

	http.HandleFunc("/pro-feature", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if len(auth) < 8 || auth[:7] != "Bearer " {
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		claims, err := v.Authorize(auth[7:])
		if err == verifier.ErrInactiveSubscription {
			http.Error(w, "subscription required", http.StatusPaymentRequired)
			return
		}
		if err != nil {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		w.Write([]byte("welcome, plan=" + claims.Plan))
	})

	http.ListenAndServe(":9000", nil)
}
```

- [ ] **Step 2: Verify it builds**

Run: `go build ./examples/appserver/`
Expected: builds with no output (exit 0).

- [ ] **Step 3: Commit**

```bash
git add examples/appserver/main.go
git commit -m "docs(verifier): example HTTP middleware gating on subscription"
```

---

## Phase 5 — Subscription→claim sync

### Task 8: Pure subscription→user-property mapping (TDD)

**Files:**
- Create: `subscription/mapping.go`
- Test: `subscription/mapping_test.go`

- [ ] **Step 1: Write the failing test**

```go
package subscription

import (
	"testing"
	"time"
)

func TestMapSubscriptionToUserProps(t *testing.T) {
	sub := Subscription{
		User:    "test-user",
		Plan:    "pro",
		State:   "Active",
		EndDate: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
	}
	props := MapSubscriptionToUserProps(sub, "pro-role")
	want := map[string]string{
		"plan":                "pro",
		"role":                "pro-role",
		"subscriptionStatus":  "Active",
		"subscriptionEndTime": "2026-12-31T00:00:00Z",
	}
	for k, v := range want {
		if props[k] != v {
			t.Errorf("props[%q]=%q want %q", k, props[k], v)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./subscription/ -run TestMap -v`
Expected: FAIL — `undefined: Subscription`.

- [ ] **Step 3: Write minimal implementation**

```go
package subscription

import "time"

// Subscription is the subset of a Casdoor subscription we mirror into claims.
type Subscription struct {
	Owner   string
	Name    string
	User    string // user name within the org
	Plan    string
	State   string // Pending|Active|Upcoming|Suspended|Expired|Error
	EndDate time.Time
}

// UserProps are the Casdoor user properties emitted as JWT-Custom claims.
type UserProps map[string]string

// MapSubscriptionToUserProps converts a subscription (+ its plan's role) into
// the user properties Casdoor will emit as claims.
func MapSubscriptionToUserProps(s Subscription, role string) UserProps {
	return UserProps{
		"plan":                s.Plan,
		"role":                role,
		"subscriptionStatus":  s.State,
		"subscriptionEndTime": s.EndDate.UTC().Format(time.RFC3339),
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./subscription/ -run TestMap -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add subscription/mapping.go subscription/mapping_test.go
git commit -m "feat(subscription): pure subscription->user-property mapping"
```

### Task 9: Syncer orchestration (TDD with a fake client)

**Files:**
- Create: `subscription/syncer.go`
- Test: `subscription/syncer_test.go`

- [ ] **Step 1: Write the failing test**

```go
package subscription

import (
	"testing"
	"time"
)

type fakeClient struct {
	subs    []Subscription
	roles   map[string]string            // plan -> role
	updated map[string]UserProps         // user -> props written
}

func (f *fakeClient) ListSubscriptions(org string) ([]Subscription, error) { return f.subs, nil }
func (f *fakeClient) GetPlanRole(org, plan string) (string, error)         { return f.roles[plan], nil }
func (f *fakeClient) UpdateUserProperties(org, user string, p UserProps) error {
	if f.updated == nil {
		f.updated = map[string]UserProps{}
	}
	f.updated[user] = p
	return nil
}

func TestSyncOrg_WritesPropsPerUser(t *testing.T) {
	fc := &fakeClient{
		subs: []Subscription{
			{User: "u1", Plan: "pro", State: "Active", EndDate: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)},
		},
		roles: map[string]string{"pro": "pro-role"},
	}
	if err := NewSyncer(fc).SyncOrg("app-a-org"); err != nil {
		t.Fatal(err)
	}
	got := fc.updated["u1"]
	if got["plan"] != "pro" || got["role"] != "pro-role" || got["subscriptionStatus"] != "Active" {
		t.Fatalf("unexpected props written: %v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./subscription/ -run TestSyncOrg -v`
Expected: FAIL — `undefined: NewSyncer`.

- [ ] **Step 3: Write minimal implementation**

```go
package subscription

// CasdoorClient is the admin API surface the syncer needs.
type CasdoorClient interface {
	ListSubscriptions(org string) ([]Subscription, error)
	GetPlanRole(org, plan string) (string, error)
	UpdateUserProperties(org, user string, props UserProps) error
}

// Syncer mirrors subscription state into user properties so Casdoor emits them
// as JWT-Custom claims on the next token issuance/refresh.
type Syncer struct{ client CasdoorClient }

func NewSyncer(c CasdoorClient) *Syncer { return &Syncer{client: c} }

// SyncOrg pushes current subscription state into user properties for one org.
func (s *Syncer) SyncOrg(org string) error {
	subs, err := s.client.ListSubscriptions(org)
	if err != nil {
		return err
	}
	for _, sub := range subs {
		role, err := s.client.GetPlanRole(org, sub.Plan)
		if err != nil {
			return err
		}
		if err := s.client.UpdateUserProperties(org, sub.User, MapSubscriptionToUserProps(sub, role)); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./subscription/ -run TestSyncOrg -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add subscription/syncer.go subscription/syncer_test.go
git commit -m "feat(subscription): syncer orchestration over CasdoorClient"
```

### Task 10: Casdoor admin HTTP client

**Files:**
- Create: `subscription/casdoor_client.go`
- Test: `subscription/casdoor_client_test.go`

- [ ] **Step 1: Write the failing test (httptest mock — no live Casdoor)**

```go
package subscription

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPClient_ListSubscriptions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/get-subscriptions" {
			json.NewEncoder(w).Encode(map[string]any{
				"status": "ok",
				"data": []map[string]any{
					{"owner": "app-a-org", "name": "s1", "user": "u1", "plan": "pro", "state": "Active", "endDate": "2026-12-31T00:00:00Z"},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := NewHTTPClient(srv.URL, "cid", "csecret")
	subs, err := c.ListSubscriptions("app-a-org")
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 || subs[0].Plan != "pro" || subs[0].State != "Active" {
		t.Fatalf("unexpected subs: %+v", subs)
	}
	if subs[0].EndDate.Year() != 2026 {
		t.Fatalf("endDate not parsed: %v", subs[0].EndDate)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./subscription/ -run TestHTTPClient -v`
Expected: FAIL — `undefined: NewHTTPClient`.

- [ ] **Step 3: Write minimal implementation**

```go
package subscription

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// HTTPClient talks to Casdoor's admin REST API. Auth uses clientId/clientSecret
// query params (the mechanism confirmed in the Task 2 spike; switch to HTTP
// Basic here if the findings say so).
type HTTPClient struct {
	base   string
	cid    string
	secret string
	http   *http.Client
}

func NewHTTPClient(base, clientID, clientSecret string) *HTTPClient {
	return &HTTPClient{base: base, cid: clientID, secret: clientSecret, http: &http.Client{Timeout: 10 * time.Second}}
}

func (c *HTTPClient) auth(v url.Values) url.Values {
	v.Set("clientId", c.cid)
	v.Set("clientSecret", c.secret)
	return v
}

type subDTO struct {
	Owner   string `json:"owner"`
	Name    string `json:"name"`
	User    string `json:"user"`
	Plan    string `json:"plan"`
	State   string `json:"state"`
	EndDate string `json:"endDate"`
}

func (c *HTTPClient) ListSubscriptions(org string) ([]Subscription, error) {
	q := c.auth(url.Values{"owner": {org}})
	resp, err := c.http.Get(c.base + "/api/get-subscriptions?" + q.Encode())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var body struct {
		Status string   `json:"status"`
		Data   []subDTO `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	out := make([]Subscription, 0, len(body.Data))
	for _, d := range body.Data {
		end, _ := time.Parse(time.RFC3339, d.EndDate)
		out = append(out, Subscription{Owner: d.Owner, Name: d.Name, User: d.User, Plan: d.Plan, State: d.State, EndDate: end})
	}
	return out, nil
}

func (c *HTTPClient) GetPlanRole(org, plan string) (string, error) {
	q := c.auth(url.Values{"id": {org + "/" + plan}})
	resp, err := c.http.Get(c.base + "/api/get-plan?" + q.Encode())
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var body struct {
		Data struct {
			Role string `json:"role"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	return body.Data.Role, nil // e.g. "app-a-org/pro-role"
}

func (c *HTTPClient) UpdateUserProperties(org, user string, props UserProps) error {
	// 1. fetch the current user, 2. set properties, 3. update-user.
	gq := c.auth(url.Values{"id": {org + "/" + user}})
	resp, err := c.http.Get(c.base + "/api/get-user?" + gq.Encode())
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var got struct {
		Data map[string]any `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		return err
	}
	if got.Data == nil {
		return fmt.Errorf("user %s/%s not found", org, user)
	}
	existing, _ := got.Data["properties"].(map[string]any)
	if existing == nil {
		existing = map[string]any{}
	}
	for k, v := range props {
		existing[k] = v
	}
	got.Data["properties"] = existing

	payload, err := json.Marshal(got.Data)
	if err != nil {
		return err
	}
	uq := c.auth(url.Values{"id": {org + "/" + user}})
	put, err := c.http.Post(c.base+"/api/update-user?"+uq.Encode(), "application/json", bytesReader(payload))
	if err != nil {
		return err
	}
	defer put.Body.Close()
	if put.StatusCode != http.StatusOK {
		return fmt.Errorf("update-user status %d", put.StatusCode)
	}
	return nil
}
```

Add this small helper at the bottom of the file (avoids importing `bytes` at call sites):
```go
import "bytes"

func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }
```
(Move the `bytes` import into the existing import block; shown separately for clarity.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./subscription/ -v`
Expected: PASS (mapping, syncer, and the ListSubscriptions HTTP test).

- [ ] **Step 5: Commit**

```bash
git add subscription/casdoor_client.go subscription/casdoor_client_test.go
git commit -m "feat(subscription): Casdoor admin HTTP client (subs, plan role, user props)"
```

### Task 11: `subsync` CLI entrypoint

**Files:**
- Create: `cmd/subsync/main.go`

- [ ] **Step 1: Write the CLI**

```go
package main

import (
	"log"
	"os"

	"github.com/acme/auth-server/subscription"
)

// subsync mirrors current subscription state into user properties for one org,
// so Casdoor emits fresh subscription claims on the next token issuance.
// Env: CASDOOR_ENDPOINT, CASDOOR_CLIENT_ID, CASDOOR_CLIENT_SECRET, CASDOOR_ORG.
func main() {
	endpoint := os.Getenv("CASDOOR_ENDPOINT")
	cid := os.Getenv("CASDOOR_CLIENT_ID")
	secret := os.Getenv("CASDOOR_CLIENT_SECRET")
	org := os.Getenv("CASDOOR_ORG")
	if endpoint == "" || cid == "" || secret == "" || org == "" {
		log.Fatal("set CASDOOR_ENDPOINT, CASDOOR_CLIENT_ID, CASDOOR_CLIENT_SECRET, CASDOOR_ORG")
	}
	client := subscription.NewHTTPClient(endpoint, cid, secret)
	if err := subscription.NewSyncer(client).SyncOrg(org); err != nil {
		log.Fatalf("sync failed: %v", err)
	}
	log.Printf("synced subscriptions for org %s", org)
}
```

- [ ] **Step 2: Verify it builds**

Run: `go build ./cmd/subsync/`
Expected: builds, exit 0.

- [ ] **Step 3: Commit**

```bash
git add cmd/subsync/main.go
git commit -m "feat(subsync): CLI to sync subscription state into user properties"
```

---

## Phase 6 — End-to-end integration test

### Task 12: Live login + claim + verify integration test

**Files:**
- Create: `test/integration/login_flow_test.go`

This test runs only against a live stack (Phases 1–3 applied). It is gated behind `RUN_INTEGRATION=1` so unit runs stay hermetic.

- [ ] **Step 1: Write the integration test**

```go
//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/acme/auth-server/subscription"
	"github.com/acme/auth-server/verifier"
)

func env(t *testing.T, k string) string {
	t.Helper()
	v := os.Getenv(k)
	if v == "" {
		t.Skipf("%s not set; skipping integration test", k)
	}
	return v
}

// Exercises: provision (already applied) -> set an Active subscription on the
// test user via the admin client -> run sync -> obtain a token via the OAuth
// password grant -> verify offline that the subscription claim is Active.
func TestEndToEnd_OfflineSubscriptionGate(t *testing.T) {
	if os.Getenv("RUN_INTEGRATION") != "1" {
		t.Skip("set RUN_INTEGRATION=1 to run")
	}
	base := env(t, "CASDOOR_ENDPOINT")
	cid := env(t, "CASDOOR_CLIENT_ID")
	secret := env(t, "CASDOOR_CLIENT_SECRET")
	appCID := env(t, "APP_A_CLIENT_ID")
	appSecret := env(t, "APP_A_CLIENT_SECRET")
	pubKeyPath := env(t, "CASDOOR_PUBLIC_KEY_PEM")
	org := "app-a-org"
	user := "test-user"

	// 1. Make the test user's subscription Active by writing properties directly.
	client := subscription.NewHTTPClient(base, cid, secret)
	props := subscription.MapSubscriptionToUserProps(subscription.Subscription{
		User: user, Plan: "pro", State: "Active",
		EndDate: time.Now().Add(24 * time.Hour).UTC(),
	}, "app-a-org/pro-role")
	if err := client.UpdateUserProperties(org, user, props); err != nil {
		t.Fatalf("seed props: %v", err)
	}

	// 2. Obtain a token via the OAuth password grant for app-a.
	form := url.Values{
		"grant_type":    {"password"},
		"client_id":     {appCID},
		"client_secret": {appSecret},
		"username":      {org + "/" + user},
		"password":      {"Test123456!"},
	}
	resp, err := http.PostForm(base+"/api/login/oauth/access_token", form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken == "" || strings.Count(tok.AccessToken, ".") != 2 {
		t.Fatalf("no JWT access token returned")
	}

	// 3. Verify offline that the subscription is Active.
	pem, err := os.ReadFile(pubKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	v, err := verifier.NewFromPEM(pem)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := v.Authorize(tok.AccessToken)
	if err != nil {
		t.Fatalf("Authorize failed: %v", err)
	}
	if claims.Plan != "pro" || claims.SubscriptionStatus != "Active" {
		t.Fatalf("claims plan=%q status=%q want pro/Active", claims.Plan, claims.SubscriptionStatus)
	}
}
```

- [ ] **Step 2: Export the App A signing public key**

Fetch App A's cert public key (PEM) from Casdoor and save it:
```bash
curl -s "$CASDOOR/api/get-cert?id=admin/cert-app-a&clientId=$CID&clientSecret=$CSECRET" \
  | python3 -c "import sys,json;print(json.load(sys.stdin)['data']['certificate'])" > certs/app-a-public.pem
head -1 certs/app-a-public.pem
```
Expected: `-----BEGIN CERTIFICATE-----` (an X.509 cert PEM; `jwt.ParseRSAPublicKeyFromPEM` accepts a cert PEM and extracts the public key).

- [ ] **Step 3: Run the integration test**

Run:
```bash
RUN_INTEGRATION=1 \
CASDOOR_ENDPOINT=http://localhost:8000 \
CASDOOR_CLIENT_ID=$CID CASDOOR_CLIENT_SECRET=$CSECRET \
APP_A_CLIENT_ID=<app-a client id> APP_A_CLIENT_SECRET=<app-a client secret> \
CASDOOR_PUBLIC_KEY_PEM=certs/app-a-public.pem \
go test -tags integration ./test/integration/ -run TestEndToEnd -v
```
Expected: PASS. If the password grant is rejected, confirm `enablePassword: true` on app-a and that the user/password match Task 3; if the claim is empty, recheck the Task 2 finding on custom-property emission.

- [ ] **Step 4: Commit**

```bash
git add test/integration/login_flow_test.go
git commit -m "test: end-to-end offline subscription-gate integration test"
```

---

## Phase 7 — Operational hardening

### Task 13: Reverse proxy + secrets + scheduled sync

**Files:**
- Create: `Caddyfile`
- Modify: `docker-compose.yml` (add `caddy` service + scheduled `subsync`)
- Create: `docs/technical/operations.md`

- [ ] **Step 1: Write `Caddyfile` (TLS termination)**

```
auth.example.com {
    reverse_proxy casdoor:8000
}
```

- [ ] **Step 2: Add Caddy to `docker-compose.yml`**

Add this service (and remove the public `8000` port mapping from `casdoor` so only Caddy is exposed):
```yaml
  caddy:
    image: caddy:2
    restart: unless-stopped
    depends_on: [casdoor]
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - caddy_data:/data
```
And add `caddy_data:` under the top-level `volumes:` key. Update `conf/app.conf` `origin` to `https://auth.example.com`.

- [ ] **Step 3: Add a scheduled subsync runner**

Add a container that runs `subsync` on a schedule (build a tiny image from the Go binary). Append to `docker-compose.yml`:
```yaml
  subsync:
    build:
      context: .
      dockerfile: cmd/subsync/Dockerfile
    restart: unless-stopped
    depends_on: [casdoor]
    environment:
      CASDOOR_ENDPOINT: http://casdoor:8000
      CASDOOR_CLIENT_ID: ${ADMIN_CLIENT_ID}
      CASDOOR_CLIENT_SECRET: ${ADMIN_CLIENT_SECRET}
      CASDOOR_ORG: app-a-org
      SYNC_INTERVAL: "300"
```
Create `cmd/subsync/Dockerfile`:
```dockerfile
FROM golang:1.22 AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o /subsync ./cmd/subsync

FROM alpine:3
COPY --from=build /subsync /subsync
# loop the one-shot CLI on an interval
ENTRYPOINT ["sh","-c","while true; do /subsync; sleep ${SYNC_INTERVAL:-300}; done"]
```
Add `ADMIN_CLIENT_ID` / `ADMIN_CLIENT_SECRET` to `.env.example` (the admin app credentials from Task 2).

- [ ] **Step 4: Write `docs/technical/operations.md`**

```markdown
# Operations

- **Secrets:** WeChat/Twilio/admin credentials and the cert live in `.env` and Casdoor's DB — never committed. For prod, template `init_data.json` and inject `REPLACE_*` at deploy.
- **Signing cert:** root of trust. Generated as `cert-app-a` (RS256). Rotate by issuing a new cert, re-pointing the app, and redistributing the public PEM to apps.
- **Backups:** `docker compose exec db pg_dump -U casdoor casdoor > backup.sql` on a schedule.
- **Admin MFA:** enable TOTP for admin accounts in the Casdoor UI.
- **Subscription freshness:** `subsync` runs every 5 min; claims are at most 1h stale in issued tokens (access TTL) plus one sync interval in user properties.
```

- [ ] **Step 5: Validate the compose config and build the subsync image**

Run:
```bash
docker compose config >/dev/null && echo "compose OK"
docker compose build subsync
```
Expected: `compose OK` and a successful image build.

- [ ] **Step 6: Commit**

```bash
git add Caddyfile docker-compose.yml cmd/subsync/Dockerfile docs/technical/operations.md .env.example conf/app.conf
git commit -m "feat(ops): TLS reverse proxy, scheduled subsync, operations runbook"
```

---

## Self-Review (completed during planning)

**Spec coverage:**
- Multi-tenant model → Tasks 3–4 (org-per-app provisioning). ✓
- WeChat Web QR + Twilio phone OTP → Task 3 providers + app signin methods. ✓
- Hosted login page / OIDC auth code flow → Casdoor default; exercised in Task 12. ✓
- JWT-Custom subscription claims → Tasks 2 (confirm), 3 (token fields), 8–11 (populate). ✓
- Offline verification (sig + status + endTime) → Tasks 5–6, demoed Task 7, asserted Task 12. ✓
- 1h / 30d TTLs → Task 3 (`expireInHours: 1`, `refreshExpireInHours: 720`). ✓
- Version-controlled provisioning → Task 3–4 `init_data.json`. ✓
- Per-app verification helper → `verifier/` package (Tasks 5–7). ✓
- Subscription lifecycle / granting via API → admin client Task 10; sync Tasks 8–9, 11. ✓
- Deployment, secrets, cert, backups, MFA, testing → Tasks 1, 13, and 12. ✓

**Placeholder scan:** `REPLACE_*` tokens in `init_data.json` are intentional credential slots (documented), not plan gaps. No "TBD"/"add error handling"-style placeholders remain.

**Type consistency:** `Claims`, `Subscription`, `UserProps`, `CasdoorClient`, `Verifier` and their methods (`SubscriptionActive`, `Verify`, `Authorize`, `MapSubscriptionToUserProps`, `SyncOrg`, `NewHTTPClient`) are used consistently across Tasks 5–12.

**Known external dependency:** Tasks 3, 10, and 12 depend on Casdoor field/endpoint shapes that the Task 2 spike confirms against the running version; later tasks instruct adjusting to the findings if a field differs.
