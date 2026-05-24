package subscription

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// HTTPClient talks to Casdoor's admin REST API. Auth uses clientId/clientSecret
// query params (the mechanism confirmed in the capability spike; switch to HTTP
// Basic here if the findings say so).
type HTTPClient struct {
	base   string
	cid    string
	secret string
	http   *http.Client
}

func NewHTTPClient(base, clientID, clientSecret string) *HTTPClient {
	return &HTTPClient{base: base, cid: clientID, secret: clientSecret, http: &http.Client{Timeout: 10 * time.Second}}
}

func (c *HTTPClient) auth(v url.Values) url.Values {
	v.Set("clientId", c.cid)
	v.Set("clientSecret", c.secret)
	return v
}

type subDTO struct {
	Owner   string `json:"owner"`
	Name    string `json:"name"`
	User    string `json:"user"`
	Plan    string `json:"plan"`
	State   string `json:"state"`
	EndDate string `json:"endDate"`
}

func (c *HTTPClient) ListSubscriptions(org string) ([]Subscription, error) {
	q := c.auth(url.Values{"owner": {org}})
	resp, err := c.http.Get(c.base + "/api/get-subscriptions?" + q.Encode())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var body struct {
		Status string   `json:"status"`
		Data   []subDTO `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	out := make([]Subscription, 0, len(body.Data))
	for _, d := range body.Data {
		end, _ := time.Parse(time.RFC3339, d.EndDate)
		out = append(out, Subscription{Owner: d.Owner, Name: d.Name, User: d.User, Plan: d.Plan, State: d.State, EndDate: end})
	}
	return out, nil
}

func (c *HTTPClient) GetPlanRole(org, plan string) (string, error) {
	q := c.auth(url.Values{"id": {org + "/" + plan}})
	resp, err := c.http.Get(c.base + "/api/get-plan?" + q.Encode())
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var body struct {
		Data struct {
			Role string `json:"role"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	return body.Data.Role, nil // e.g. "app-a-org/pro-role"
}

func (c *HTTPClient) UpdateUserProperties(org, user string, props UserProps) error {
	// 1. fetch the current user, 2. set properties, 3. update-user.
	gq := c.auth(url.Values{"id": {org + "/" + user}})
	resp, err := c.http.Get(c.base + "/api/get-user?" + gq.Encode())
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var got struct {
		Data map[string]any `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		return err
	}
	if got.Data == nil {
		return fmt.Errorf("user %s/%s not found", org, user)
	}
	existing, _ := got.Data["properties"].(map[string]any)
	if existing == nil {
		existing = map[string]any{}
	}
	for k, v := range props {
		existing[k] = v
	}
	got.Data["properties"] = existing

	payload, err := json.Marshal(got.Data)
	if err != nil {
		return err
	}
	uq := c.auth(url.Values{"id": {org + "/" + user}})
	put, err := c.http.Post(c.base+"/api/update-user?"+uq.Encode(), "application/json", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer put.Body.Close()
	if put.StatusCode != http.StatusOK {
		return fmt.Errorf("update-user status %d", put.StatusCode)
	}
	return nil
}
