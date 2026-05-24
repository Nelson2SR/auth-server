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
// test user via the admin client -> obtain a token via the OAuth password
// grant -> verify offline that the subscription claim is Active.
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
		// Casdoor infers the organization from the application, so the username
		// is the bare user name (NOT "<org>/<user>").
		"username": {user},
		"password": {"Test123456!"},
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
	if claims.Plan() != "pro" || claims.SubscriptionStatus() != "Active" {
		t.Fatalf("claims plan=%q status=%q want pro/Active", claims.Plan(), claims.SubscriptionStatus())
	}
}
