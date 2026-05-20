package subscription

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPClient_ListSubscriptions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/get-subscriptions" {
			json.NewEncoder(w).Encode(map[string]any{
				"status": "ok",
				"data": []map[string]any{
					{"owner": "app-a-org", "name": "s1", "user": "u1", "plan": "pro", "state": "Active", "endDate": "2026-12-31T00:00:00Z"},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := NewHTTPClient(srv.URL, "cid", "csecret")
	subs, err := c.ListSubscriptions("app-a-org")
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 || subs[0].Plan != "pro" || subs[0].State != "Active" {
		t.Fatalf("unexpected subs: %+v", subs)
	}
	if subs[0].EndDate.Year() != 2026 {
		t.Fatalf("endDate not parsed: %v", subs[0].EndDate)
	}
}
