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
