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
