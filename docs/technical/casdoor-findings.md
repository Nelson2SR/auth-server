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

## 5. Twilio SMS provider — field mapping (live-verified)

Casdoor's native Twilio uses **Programmable Messaging** (not Verify). Verified against
`casbin/casdoor:v1.812.0` source + a real send. Provider field mapping (from `object/sms.go`):

| Twilio value | Casdoor provider field | DB column |
|---|---|---|
| Account SID | `clientId` | `client_id` |
| Auth Token | `clientSecret` | `client_secret` |
| **Sender (From) number** | `appId` | `app_id` |
| **Message template** (`%s` = code) | `templateCode` | `template_code` |

`init_data.json` carries the mapping with `REPLACE_*` placeholders + the template; **real
secrets are injected at runtime, never committed**. To configure the live provider without
touching git, update the DB row (Casdoor reads provider config per send) or use the admin UI:

```bash
SQL="update provider set client_id='$TWILIO_ACCOUNT_SID', client_secret='$TWILIO_AUTH_TOKEN', \
app_id='$TWILIO_FROM_NUMBER', template_code='Your App A verification code is %s' \
where name='provider-twilio-app-a';"
docker compose exec -T db psql -U casdoor -d casdoor -tAc "$SQL"
```

**Triggering an OTP (no captcha provider linked → `captchaType=none` skips captcha):**
```bash
curl -s -X POST http://localhost:8000/api/send-verification-code \
  --data-urlencode "applicationId=admin/app-a" --data-urlencode "type=phone" \
  --data-urlencode "dest=98653286" --data-urlencode "countryCode=SG" \
  --data-urlencode "method=signup" --data-urlencode "checkType=none" \
  --data-urlencode "captchaType=none"
```
Casdoor expects the **local** number in `dest` plus an ISO `countryCode` (it builds E.164
internally). Result: Casdoor generated a code and delivered it via Twilio with the configured
template (confirmed in the Twilio message log, status `delivered`).

**Trial-account constraints (this Twilio account):** Trial — can send only to **verified**
recipient numbers, messages get a "Sent from your Twilio trial account" prefix, and the free
sender number is **US**. We claimed `+19129128956` (US) and send to the verified `+6598653286`
(SG); US→SG delivery worked. Upgrade the Twilio account to remove the prefix and the
verified-recipient restriction.

## 6. Phone-OTP login flow (phase 1, live-verified)

Full flow that yields a JWT: **send code → user enters code → `/api/login` → auth code →
token exchange**. Verified end-to-end (`TestEndToEnd_PhoneOtpLogin`).

- **User storage:** the user's `phone` holds the **local** number (e.g. `98653286`) and
  `countryCode`/`region` hold the ISO region (`SG`). A full `+E.164` in `phone` does **not**
  match login lookups.
- **`/api/login` for phone code** (JSON body + OAuth query params):
  - Query: `clientId`, `responseType=code`, `redirectUri` (must match app), `scope`, `state`
  - Body: `{"application":"app-a","organization":"app-a-org","username":"<LOCAL PHONE>",
    "countryCode":"SG","code":"<SMS CODE>","signinMethod":"Verification code",
    "type":"code","autoSignin":true}`
  - **`username` must be the phone number**, not the account name: the handler both finds the
    user (`GetUserByFields`) and builds the dest via `GetE164Number(username, countryCode)`.
    A non-phone username fails with "Phone number is invalid in your region".
  - Success returns the OAuth **authorization code** in the `data` field.
- **Token exchange:** `POST /api/login/oauth/access_token` with
  `grant_type=authorization_code`, `client_id`, `client_secret`, `redirect_uri`, `code`.
- **OTP resend cooldown:** a second `send-verification-code` within the window returns
  `status:error`; the previously delivered code stays valid. The test reads the latest Twilio
  message body either way.
- The test-user's real phone is set **at runtime via the admin API** (env `TEST_PHONE_*`), so
  no personal number is committed to `init_data.json`.
