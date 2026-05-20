package subscription

import (
	"testing"
	"time"
)

type fakeClient struct {
	subs    []Subscription
	roles   map[string]string    // plan -> role
	updated map[string]UserProps // user -> props written
}

func (f *fakeClient) ListSubscriptions(org string) ([]Subscription, error) { return f.subs, nil }
func (f *fakeClient) GetPlanRole(org, plan string) (string, error)         { return f.roles[plan], nil }
func (f *fakeClient) UpdateUserProperties(org, user string, p UserProps) error {
	if f.updated == nil {
		f.updated = map[string]UserProps{}
	}
	f.updated[user] = p
	return nil
}

func TestSyncOrg_WritesPropsPerUser(t *testing.T) {
	fc := &fakeClient{
		subs: []Subscription{
			{User: "u1", Plan: "pro", State: "Active", EndDate: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)},
		},
		roles: map[string]string{"pro": "pro-role"},
	}
	if err := NewSyncer(fc).SyncOrg("app-a-org"); err != nil {
		t.Fatal(err)
	}
	got := fc.updated["u1"]
	if got["plan"] != "pro" || got["role"] != "pro-role" || got["subscriptionStatus"] != "Active" {
		t.Fatalf("unexpected props written: %v", got)
	}
}
