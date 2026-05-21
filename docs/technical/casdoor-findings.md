# Casdoor Capability Findings

Empirically confirmed against **Casdoor `v1.812.0`** (Docker image `casbin/casdoor:v1.812.0`)
on 2026-05-21, by running the live stack and the end-to-end integration test.

## 1. Admin API authentication — clientId/clientSecret query params ✓

Admin REST calls authenticate with the **built-in app** credentials
(`built-in/app-built-in`, retrievable from the DB or the UI) passed as
`?clientId=...&clientSecret=...` query parameters. Confirmed working for
`/api/get-user`, `/api/update-user`, `/api/get-subscriptions`, `/api/get-plan`.
→ `subscription.HTTPClient` uses this mechanism (no change needed).

## 2. Token expiry granularity — whole hours ✓

`application.expireInHours` is an integer. `expireInHours: 1` yields tokens with
an exactly **1.0-hour** lifetime (verified by decoding `exp - iat`). 15 minutes is
not expressible; **1 hour** is the practical minimum (matches the design decision).

## 3. JWT-Custom emits User *struct fields*, not arbitrary property keys ⚠ (design-shaping)

This is the key finding. With `tokenFormat: JWT-Custom`, `tokenFields` selects fields
of the Casdoor **User struct** (e.g. `Id`, `Name`, `DisplayName`, `Properties`). It does
**not** accept arbitrary custom keys.

- `tokenFields: ["plan","role","subscriptionStatus","subscriptionEndTime"]` → **no** such
  claims appear (they are not User struct fields).
- `tokenFields: ["Id","Name","DisplayName","Properties"]` → the token carries a nested
  **`properties`** claim:
  ```json
  "properties": {
    "plan": "pro", "role": "pro-role",
    "subscriptionStatus": "Active", "subscriptionEndTime": "2026-12-31T00:00:00Z"
  }
  ```
- The field name must be PascalCase `Properties` (lowercase `properties` is ignored).

**Implications applied:**
- `init_data.json` app `tokenFields` = `["Id","Name","DisplayName","Properties"]`.
- The `verifier.Claims` reads the nested `properties` map (accessors `Plan()`, `Role()`,
  `SubscriptionStatus()`, `SubscriptionActive()`), not top-level claims.
- The subscription sync was already correct: it writes the keys into the user's
  **Properties**, which is exactly where Casdoor sources the nested claim.

> Note: the design spec/diagrams describe these as `plan` / `subscriptionStatus` claims.
> They ARE in the token and carry the same names — just nested one level under `properties`.

## 4. Password grant (used by the integration test)

- Requires `"password"` in the application `grantTypes` (added to `init_data.json`).
- The `username` is the **bare user name** (`test-user`); the organization is inferred from
  the application. `"<org>/<user>"` is rejected with `the user does not exist`.

## Running the integration test

`go test` runs in the package directory, so pass an **absolute** path for the public key:
```bash
ADMIN_ID=$(docker compose exec -T db psql -U casdoor -d casdoor -tAc "select client_id from application where name='app-built-in';")
ADMIN_SECRET=$(docker compose exec -T db psql -U casdoor -d casdoor -tAc "select client_secret from application where name='app-built-in';")
docker compose exec -T db psql -U casdoor -d casdoor -tAc "select certificate from cert where name='cert-app-a';" > certs/app-a-public.pem

RUN_INTEGRATION=1 \
CASDOOR_ENDPOINT=http://localhost:8000 \
CASDOOR_CLIENT_ID="$ADMIN_ID" CASDOOR_CLIENT_SECRET="$ADMIN_SECRET" \
APP_A_CLIENT_ID=REPLACE_APP_A_CLIENT_ID APP_A_CLIENT_SECRET=REPLACE_APP_A_CLIENT_SECRET \
CASDOOR_PUBLIC_KEY_PEM="$(pwd)/certs/app-a-public.pem" \
go test -tags integration ./test/integration/ -run TestEndToEnd -v
```
Result: **PASS** — admin client sets the subscription, password grant mints a JWT, the
verifier validates it offline and reads `plan=pro`, `subscriptionStatus=Active`.
