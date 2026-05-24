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
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Date(2026, 5, 21, 13, 0, 0, 0, time.UTC))},
		Properties: map[string]string{
			"plan":                "pro",
			"role":                "pro-role",
			"subscriptionStatus":  "Active",
			"subscriptionEndTime": "2026-12-31T00:00:00Z",
		},
	})
	claims, err := v.Authorize(tokenStr)
	if err != nil {
		t.Fatalf("Authorize returned error: %v", err)
	}
	if claims.Plan() != "pro" {
		t.Fatalf("plan=%q want pro", claims.Plan())
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
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
		Properties: map[string]string{
			"subscriptionStatus":  "Expired",
			"subscriptionEndTime": "2026-12-31T00:00:00Z",
		},
	})
	if _, err := v.Authorize(tokenStr); !errors.Is(err, ErrInactiveSubscription) {
		t.Fatalf("want ErrInactiveSubscription, got %v", err)
	}
}
