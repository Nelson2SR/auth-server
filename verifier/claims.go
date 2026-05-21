package verifier

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims is the JWT payload: standard registered claims plus the Casdoor user
// Properties map.
//
// Casdoor's JWT-Custom format emits selected *User struct* fields. The custom
// subscription data (plan, role, subscriptionStatus, subscriptionEndTime) lives
// in the user's Properties map, which appears as a nested "properties" claim
// when "Properties" is included in the application's token fields. (Empirically
// confirmed against Casdoor v1.812.0 — see docs/technical/casdoor-findings.md.)
type Claims struct {
	jwt.RegisteredClaims
	Properties map[string]string `json:"properties"`
}

func (c *Claims) prop(k string) string {
	if c.Properties == nil {
		return ""
	}
	return c.Properties[k]
}

// Plan returns the subscription plan name (e.g. "pro").
func (c *Claims) Plan() string { return c.prop("plan") }

// Role returns the Casdoor role backing the plan.
func (c *Claims) Role() string { return c.prop("role") }

// SubscriptionStatus returns the subscription state (e.g. "Active", "Expired").
func (c *Claims) SubscriptionStatus() string { return c.prop("subscriptionStatus") }

// SubscriptionActive reports whether the subscription is currently usable.
func (c *Claims) SubscriptionActive(now time.Time) bool {
	if c.prop("subscriptionStatus") != "Active" {
		return false
	}
	end, err := time.Parse(time.RFC3339, c.prop("subscriptionEndTime"))
	if err != nil {
		return false
	}
	return now.Before(end)
}
