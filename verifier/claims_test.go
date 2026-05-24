package verifier

import (
	"testing"
	"time"
)

func claimsAt(status, end string) *Claims {
	return &Claims{Properties: map[string]string{
		"subscriptionStatus":  status,
		"subscriptionEndTime": end,
	}}
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
