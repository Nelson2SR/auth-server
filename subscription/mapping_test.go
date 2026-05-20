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
