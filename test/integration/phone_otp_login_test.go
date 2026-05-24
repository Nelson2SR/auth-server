//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/acme/auth-server/verifier"
)

// End-to-end phone-OTP login (phase 1):
//   set user phone + Active subscription -> Casdoor sends OTP via Twilio ->
//   read the code from the Twilio message log -> /api/login with phone+code ->
//   exchange the auth code for a JWT -> verify offline that the subscription is Active.
//
// Requires the live stack and Twilio creds. Env:
//   RUN_INTEGRATION=1
//   CASDOOR_ENDPOINT, CASDOOR_CLIENT_ID, CASDOOR_CLIENT_SECRET   (admin app-built-in)
//   APP_A_CLIENT_ID, APP_A_CLIENT_SECRET
//   CASDOOR_PUBLIC_KEY_PEM (absolute path to app-a cert PEM)
//   TWILIO_ACCOUNT_SID, TWILIO_AUTH_TOKEN
//   TEST_PHONE_LOCAL (e.g. 98653286), TEST_PHONE_COUNTRY (e.g. SG), TEST_PHONE_E164 (e.g. +6598653286)
func TestEndToEnd_PhoneOtpLogin(t *testing.T) {
	if os.Getenv("RUN_INTEGRATION") != "1" {
		t.Skip("set RUN_INTEGRATION=1 to run")
	}
	base := env(t, "CASDOOR_ENDPOINT")
	adminID := env(t, "CASDOOR_CLIENT_ID")
	adminSecret := env(t, "CASDOOR_CLIENT_SECRET")
	appID := env(t, "APP_A_CLIENT_ID")
	appSecret := env(t, "APP_A_CLIENT_SECRET")
	pemPath := env(t, "CASDOOR_PUBLIC_KEY_PEM")
	twSID := env(t, "TWILIO_ACCOUNT_SID")
	twTok := env(t, "TWILIO_AUTH_TOKEN")
	phoneLocal := env(t, "TEST_PHONE_LOCAL")
	phoneCountry := env(t, "TEST_PHONE_COUNTRY")
	phoneE164 := env(t, "TEST_PHONE_E164")

	org, user := "app-a-org", "test-user"
	hc := &http.Client{Timeout: 15 * time.Second}

	// 1. Set the user's phone + an Active subscription via the admin API.
	setUser(t, hc, base, adminID, adminSecret, org, user, map[string]any{
		"phone":       phoneLocal,
		"countryCode": phoneCountry,
		"region":      phoneCountry,
		"properties": map[string]string{
			"plan":                "pro",
			"role":                "app-a-org/pro-role",
			"subscriptionStatus":  "Active",
			"subscriptionEndTime": time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339),
		},
	})

	// 2. Trigger the OTP (method=login). Cooldown may return error; the active
	//    code is still the latest delivered, which step 3 reads.
	_ = postForm(t, hc, base+"/api/send-verification-code", url.Values{
		"applicationId": {"admin/app-a"}, "type": {"phone"},
		"dest": {phoneLocal}, "countryCode": {phoneCountry}, "method": {"login"},
		"checkType": {"none"}, "captchaType": {"none"},
	})

	// 3. Read the verification code from the latest Twilio message to the recipient.
	code := readTwilioCode(t, hc, twSID, twTok, phoneE164)
	t.Logf("OTP code read from Twilio: %s", code)

	// 4. Phone-code login -> OAuth authorization code (returned in "data").
	loginBody, _ := json.Marshal(map[string]any{
		"application": "app-a", "organization": org,
		"username": phoneLocal, "countryCode": phoneCountry, "code": code,
		"signinMethod": "Verification code", "type": "code", "autoSignin": true,
	})
	loginURL := base + "/api/login?" + url.Values{
		"clientId": {appID}, "responseType": {"code"},
		"redirectUri": {"http://localhost:9000/callback"}, "scope": {"openid"}, "state": {"itest"},
	}.Encode()
	var login struct {
		Status string `json:"status"`
		Msg    string `json:"msg"`
		Data   string `json:"data"`
	}
	postJSON(t, hc, loginURL, loginBody, &login)
	if login.Status != "ok" || login.Data == "" {
		t.Fatalf("phone-code login failed: status=%s msg=%s", login.Status, login.Msg)
	}

	// 5. Exchange the authorization code for a token.
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	postFormInto(t, hc, base+"/api/login/oauth/access_token", url.Values{
		"grant_type": {"authorization_code"}, "client_id": {appID}, "client_secret": {appSecret},
		"redirect_uri": {"http://localhost:9000/callback"}, "code": {login.Data},
	}, &tok)
	if strings.Count(tok.AccessToken, ".") != 2 {
		t.Fatalf("no JWT access token from code exchange")
	}

	// 6. Verify offline: signature + Active subscription.
	pem, err := os.ReadFile(pemPath)
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
	if claims.Plan() != "pro" || claims.SubscriptionStatus() != "Active" {
		t.Fatalf("claims plan=%q status=%q want pro/Active", claims.Plan(), claims.SubscriptionStatus())
	}
}

// --- helpers ---

func setUser(t *testing.T, hc *http.Client, base, cid, secret, org, user string, fields map[string]any) {
	t.Helper()
	q := url.Values{"id": {org + "/" + user}, "clientId": {cid}, "clientSecret": {secret}}.Encode()
	resp, err := hc.Get(base + "/api/get-user?" + q)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got struct {
		Data map[string]any `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil || got.Data == nil {
		t.Fatalf("get-user failed: %v", err)
	}
	for k, val := range fields {
		got.Data[k] = val
	}
	payload, _ := json.Marshal(got.Data)
	put, err := hc.Post(base+"/api/update-user?"+q, "application/json", strings.NewReader(string(payload)))
	if err != nil {
		t.Fatal(err)
	}
	defer put.Body.Close()
	if put.StatusCode != http.StatusOK {
		t.Fatalf("update-user status %d", put.StatusCode)
	}
}

func readTwilioCode(t *testing.T, hc *http.Client, sid, token, toE164 string) string {
	t.Helper()
	re := regexp.MustCompile(`\d{5,6}`)
	u := fmt.Sprintf("https://api.twilio.com/2010-04-01/Accounts/%s/Messages.json?To=%s&PageSize=1",
		sid, url.QueryEscape(toE164))
	for i := 0; i < 8; i++ {
		time.Sleep(2 * time.Second)
		req, _ := http.NewRequest("GET", u, nil)
		req.SetBasicAuth(sid, token)
		resp, err := hc.Do(req)
		if err != nil {
			continue
		}
		var body struct {
			Messages []struct {
				Body string `json:"body"`
			} `json:"messages"`
		}
		json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if len(body.Messages) > 0 {
			if m := re.FindString(body.Messages[0].Body); m != "" {
				return m
			}
		}
	}
	t.Fatal("no OTP code found in Twilio messages")
	return ""
}

func postForm(t *testing.T, hc *http.Client, u string, form url.Values) string {
	t.Helper()
	resp, err := hc.PostForm(u, form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func postFormInto(t *testing.T, hc *http.Client, u string, form url.Values, out any) {
	t.Helper()
	resp, err := hc.PostForm(u, form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatalf("decode form response: %v", err)
	}
}

func postJSON(t *testing.T, hc *http.Client, u string, body []byte, out any) {
	t.Helper()
	resp, err := hc.Post(u, "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatalf("decode json response: %v", err)
	}
}
