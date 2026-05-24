# Batch User Provisioning + Time-Limited Accounts — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `cmd/authadmin` CLI to the auth-server repo that batch-creates phone-OTP accounts in Casdoor with a per-account `expireAt` property, plus a Render Cron Job that runs the same binary daily to disable expired accounts.

**Architecture:** A new `expiry/` Go package holds pure date helpers + a `SweepOrg` function. The existing `subscription.HTTPClient` is extended with general-purpose user CRUD methods (`ListUsers`, `GetUser`, `AddUser`, `UpdateUser`) so `expiry/` can reuse the same auth+HTTP boilerplate that `subsync` already uses. The `cmd/authadmin/` binary parses CSV input, dispatches subcommands (`create-batch`, `list`, `extend`, `disable-expired`, `disable`, `enable`), and is also the entrypoint for the new Render Cron Job defined in `render.yaml`.

**Tech Stack:** Go 1.22, stdlib only (`flag`, `encoding/csv`, `encoding/json`, `net/http`, `time`), existing `subscription` package for the Casdoor HTTP client, `httptest` for unit tests, the existing `RUN_INTEGRATION=1` pattern for live-stack tests.

**Design spec:** `docs/superpowers/specs/2026-05-24-batch-user-expiry-design.md`

---

## File map

**Created:**
- `subscription/casdoor_users.go` — new file in existing package: `User` type + `ListUsers`/`GetUser`/`AddUser`/`UpdateUser` methods on `HTTPClient`.
- `subscription/casdoor_users_test.go` — httptest-driven tests.
- `expiry/expiry.go` — `IsExpired`, `ExpireAtString`, `SetExpireAt`.
- `expiry/sweep.go` — `Sweeper`, `SweepReport`, `SweepOrg`, `UserClient` interface.
- `expiry/expiry_test.go` — pure unit tests.
- `expiry/sweep_test.go` — `Sweeper` test with a fake client.
- `cmd/authadmin/main.go` — env + subcommand dispatch.
- `cmd/authadmin/create_batch.go` — CSV in → users out.
- `cmd/authadmin/list.go` — table output.
- `cmd/authadmin/extend.go`, `disable.go`, `enable.go`, `disable_expired.go` — small commands.
- `cmd/authadmin/Dockerfile` — multi-stage Go build.
- `cmd/authadmin/create_batch_test.go` — CSV parsing + stub-client orchestration.
- `test/integration/expiry_sweep_test.go` — live-stack sweep test.
- `docs/admin-users.md` — operator-facing CLI guide.

**Modified:**
- `render.yaml` — add a `type: cron` service for the daily sweep.
- `docs/deploy-render.md` — paragraph + table row for the new cron service.

`go.mod` does not change.

---

## Task 1: User type + ListUsers on subscription.HTTPClient

Smallest piece of HTTP plumbing first. Build it test-first; the rest of the user-CRUD methods follow the same pattern.

**Files:**
- Create: `subscription/casdoor_users.go`
- Create: `subscription/casdoor_users_test.go`

- [ ] **Step 1: Write the failing test**

```go
// subscription/casdoor_users_test.go
package subscription

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestListUsers_authsAndDecodes(t *testing.T) {
	var gotPath string
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		json.NewEncoder(w).Encode(map[string]any{
			"status": "ok",
			"data": []map[string]any{
				{"owner": "org1", "name": "alice", "phone": "98765432", "countryCode": "SG",
					"forbidden": false, "properties": map[string]string{"expireAt": "2026-06-30"}},
				{"owner": "org1", "name": "bob", "phone": "97654321", "countryCode": "SG",
					"forbidden": true, "properties": nil},
			},
		})
	}))
	defer srv.Close()

	c := NewHTTPClient(srv.URL, "cid", "csec")
	users, err := c.ListUsers("org1")
	if err != nil {
		t.Fatal(err)
	}

	if gotPath != "/api/get-users" {
		t.Errorf("path = %q, want /api/get-users", gotPath)
	}
	if gotQuery.Get("owner") != "org1" {
		t.Errorf("owner = %q, want org1", gotQuery.Get("owner"))
	}
	if gotQuery.Get("clientId") != "cid" || gotQuery.Get("clientSecret") != "csec" {
		t.Errorf("missing admin credentials in query: %v", gotQuery)
	}

	if len(users) != 2 {
		t.Fatalf("got %d users, want 2", len(users))
	}
	if users[0].Name != "alice" || users[0].Phone != "98765432" {
		t.Errorf("user[0] = %+v", users[0])
	}
	if users[0].Properties["expireAt"] != "2026-06-30" {
		t.Errorf("expireAt = %q, want 2026-06-30", users[0].Properties["expireAt"])
	}
	if !users[1].Forbidden {
		t.Errorf("bob.Forbidden = false, want true")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestListUsers_authsAndDecodes ./subscription/ -v`
Expected: FAIL with "undefined: User" or "c.ListUsers undefined".

- [ ] **Step 3: Write minimal implementation**

```go
// subscription/casdoor_users.go
package subscription

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// User mirrors the fields we read from Casdoor's admin user APIs. Tagged with
// the JSON names Casdoor uses on the wire. Properties is the bag where we
// stash expireAt and any other per-user metadata.
type User struct {
	Owner             string            `json:"owner"`
	Name              string            `json:"name"`
	DisplayName       string            `json:"displayName,omitempty"`
	Phone             string            `json:"phone,omitempty"`
	CountryCode       string            `json:"countryCode,omitempty"`
	Region            string            `json:"region,omitempty"`
	Email             string            `json:"email,omitempty"`
	Type              string            `json:"type,omitempty"`
	SignupApplication string            `json:"signupApplication,omitempty"`
	Forbidden         bool              `json:"forbidden"`
	Properties        map[string]string `json:"properties,omitempty"`
	CreatedTime       string            `json:"createdTime,omitempty"`
	UpdatedTime       string            `json:"updatedTime,omitempty"`
}

type userListResp struct {
	Status string `json:"status"`
	Msg    string `json:"msg"`
	Data   []User `json:"data"`
}

// ListUsers returns all users in the given organization. Casdoor's
// /api/get-users with owner=<org> returns every row Casdoor owns for that
// org (no pagination needed at our scale).
func (c *HTTPClient) ListUsers(org string) ([]User, error) {
	q := c.auth(url.Values{"owner": {org}})
	u := c.base + "/api/get-users?" + q.Encode()
	resp, err := c.http.Get(u)
	if err != nil {
		return nil, fmt.Errorf("get-users: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get-users: %d %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out userListResp
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("get-users decode: %w", err)
	}
	if out.Status != "ok" {
		return nil, errors.New("get-users: " + out.Msg)
	}
	return out.Data, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -run TestListUsers_authsAndDecodes ./subscription/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add subscription/casdoor_users.go subscription/casdoor_users_test.go
git commit -m "feat(subscription): User type + ListUsers admin call"
```

---

## Task 2: GetUser on subscription.HTTPClient

- [ ] **Step 1: Write the failing test**

Append to `subscription/casdoor_users_test.go`:

```go
func TestGetUser_authsAndDecodes(t *testing.T) {
	var gotPath string
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		json.NewEncoder(w).Encode(map[string]any{
			"status": "ok",
			"data":   map[string]any{"owner": "org1", "name": "alice", "phone": "98765432"},
		})
	}))
	defer srv.Close()

	c := NewHTTPClient(srv.URL, "cid", "csec")
	u, err := c.GetUser("org1", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/get-user" {
		t.Errorf("path = %q", gotPath)
	}
	if gotQuery.Get("id") != "org1/alice" {
		t.Errorf("id = %q, want org1/alice", gotQuery.Get("id"))
	}
	if u.Name != "alice" || u.Phone != "98765432" {
		t.Errorf("user = %+v", u)
	}
}

func TestGetUser_returnsNilOnNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"status": "ok", "data": nil})
	}))
	defer srv.Close()
	c := NewHTTPClient(srv.URL, "cid", "csec")
	u, err := c.GetUser("org1", "ghost")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if u != nil {
		t.Errorf("user = %+v, want nil", u)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run TestGetUser ./subscription/ -v`
Expected: FAIL (`GetUser` undefined).

- [ ] **Step 3: Write the implementation**

Append to `subscription/casdoor_users.go`:

```go
type userResp struct {
	Status string `json:"status"`
	Msg    string `json:"msg"`
	Data   *User  `json:"data"`
}

// GetUser fetches a single user by org/name. Returns (nil, nil) when Casdoor
// responds status=ok with data=null — that's how Casdoor signals "not found",
// not a 4xx.
func (c *HTTPClient) GetUser(org, name string) (*User, error) {
	q := c.auth(url.Values{"id": {org + "/" + name}})
	u := c.base + "/api/get-user?" + q.Encode()
	resp, err := c.http.Get(u)
	if err != nil {
		return nil, fmt.Errorf("get-user: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get-user: %d %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out userResp
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("get-user decode: %w", err)
	}
	if out.Status != "ok" {
		return nil, errors.New("get-user: " + out.Msg)
	}
	return out.Data, nil // may be nil for "not found"
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -run TestGetUser ./subscription/ -v`
Expected: PASS for both subtests.

- [ ] **Step 5: Commit**

```bash
git add subscription/casdoor_users.go subscription/casdoor_users_test.go
git commit -m "feat(subscription): GetUser admin call with nil-on-not-found"
```

---

## Task 3: AddUser on subscription.HTTPClient

- [ ] **Step 1: Write the failing test**

Append to `subscription/casdoor_users_test.go`:

```go
import "bytes"

func TestAddUser_postsJSONAndAuths(t *testing.T) {
	var gotPath string
	var gotQuery url.Values
	var gotBody bytes.Buffer
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		gotBody.ReadFrom(r.Body)
		json.NewEncoder(w).Encode(map[string]any{"status": "ok", "data": "Affected"})
	}))
	defer srv.Close()

	c := NewHTTPClient(srv.URL, "cid", "csec")
	err := c.AddUser(User{
		Owner: "org1", Name: "alice", DisplayName: "Alice",
		Phone: "98765432", CountryCode: "SG", Region: "SG",
		Type: "normal-user", SignupApplication: "intent-pulse",
		Properties: map[string]string{"expireAt": "2026-06-30"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/add-user" {
		t.Errorf("path = %q", gotPath)
	}
	if gotQuery.Get("clientId") != "cid" {
		t.Errorf("missing clientId: %v", gotQuery)
	}

	var sent map[string]any
	if err := json.Unmarshal(gotBody.Bytes(), &sent); err != nil {
		t.Fatalf("body: %v", err)
	}
	if sent["name"] != "alice" || sent["phone"] != "98765432" {
		t.Errorf("body = %v", sent)
	}
	props, _ := sent["properties"].(map[string]any)
	if props["expireAt"] != "2026-06-30" {
		t.Errorf("expireAt missing from body: %v", props)
	}
}

func TestAddUser_returnsErrorOnNonOkStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"status": "error", "msg": "user already exists"})
	}))
	defer srv.Close()
	c := NewHTTPClient(srv.URL, "cid", "csec")
	err := c.AddUser(User{Owner: "org1", Name: "alice"})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("err = %v, want 'already exists'", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -run TestAddUser ./subscription/ -v`
Expected: FAIL (`AddUser` undefined).

- [ ] **Step 3: Write the implementation**

Append to `subscription/casdoor_users.go`:

```go
type writeResp struct {
	Status string `json:"status"`
	Msg    string `json:"msg"`
	Data   string `json:"data"`
}

// AddUser creates a user. Casdoor returns 200 with status="error" + a message
// for things like duplicate names; we surface that as a Go error.
func (c *HTTPClient) AddUser(u User) error {
	q := c.auth(url.Values{})
	url := c.base + "/api/add-user?" + q.Encode()
	body, err := json.Marshal(u)
	if err != nil {
		return fmt.Errorf("add-user marshal: %w", err)
	}
	resp, err := c.http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("add-user: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("add-user: %d %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out writeResp
	if err := json.Unmarshal(raw, &out); err != nil {
		return fmt.Errorf("add-user decode: %w", err)
	}
	if out.Status != "ok" {
		return errors.New("add-user: " + out.Msg)
	}
	return nil
}
```

Add `"bytes"` to the imports block at the top of `casdoor_users.go`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -run TestAddUser ./subscription/ -v`
Expected: PASS for both subtests.

- [ ] **Step 5: Commit**

```bash
git add subscription/casdoor_users.go subscription/casdoor_users_test.go
git commit -m "feat(subscription): AddUser admin call"
```

---

## Task 4: UpdateUser on subscription.HTTPClient

- [ ] **Step 1: Write the failing test**

Append to `subscription/casdoor_users_test.go`:

```go
func TestUpdateUser_sendsIdQueryAndFullBody(t *testing.T) {
	var gotPath string
	var gotQuery url.Values
	var gotBody bytes.Buffer
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		gotBody.ReadFrom(r.Body)
		json.NewEncoder(w).Encode(map[string]any{"status": "ok", "data": "Affected"})
	}))
	defer srv.Close()

	c := NewHTTPClient(srv.URL, "cid", "csec")
	err := c.UpdateUser(User{Owner: "org1", Name: "alice", Forbidden: true,
		Properties: map[string]string{"expireAt": "2026-05-01"}})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/update-user" {
		t.Errorf("path = %q", gotPath)
	}
	if gotQuery.Get("id") != "org1/alice" {
		t.Errorf("id = %q, want org1/alice", gotQuery.Get("id"))
	}

	var sent map[string]any
	if err := json.Unmarshal(gotBody.Bytes(), &sent); err != nil {
		t.Fatalf("body: %v", err)
	}
	if sent["forbidden"] != true {
		t.Errorf("forbidden in body = %v, want true", sent["forbidden"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestUpdateUser ./subscription/ -v`
Expected: FAIL (`UpdateUser` undefined).

- [ ] **Step 3: Write the implementation**

Append to `subscription/casdoor_users.go`:

```go
// UpdateUser replaces a user's record. Caller passes the FULL object
// (typically: GetUser → mutate fields → UpdateUser). Casdoor identifies the
// row to update via the `id` query (org/name), not via the body.
func (c *HTTPClient) UpdateUser(u User) error {
	q := c.auth(url.Values{"id": {u.Owner + "/" + u.Name}})
	url := c.base + "/api/update-user?" + q.Encode()
	body, err := json.Marshal(u)
	if err != nil {
		return fmt.Errorf("update-user marshal: %w", err)
	}
	resp, err := c.http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("update-user: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("update-user: %d %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out writeResp
	if err := json.Unmarshal(raw, &out); err != nil {
		return fmt.Errorf("update-user decode: %w", err)
	}
	if out.Status != "ok" {
		return errors.New("update-user: " + out.Msg)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./subscription/ -v`
Expected: ALL existing subscription tests still pass + new `TestUpdateUser_*` passes.

- [ ] **Step 5: Commit**

```bash
git add subscription/casdoor_users.go subscription/casdoor_users_test.go
git commit -m "feat(subscription): UpdateUser admin call"
```

---

## Task 5: expiry.IsExpired and friends (pure helpers)

**Files:**
- Create: `expiry/expiry.go`
- Create: `expiry/expiry_test.go`

- [ ] **Step 1: Write the failing test**

```go
// expiry/expiry_test.go
package expiry

import (
	"testing"
	"time"

	"github.com/acme/auth-server/subscription"
)

func TestIsExpired(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	tt := []struct {
		name      string
		expireAt  string
		wantExpired bool
	}{
		{"missing property -> never expired", "", false},
		{"date in the future", "2026-06-30", false},
		{"date is today", "2026-06-01", false},
		{"date is yesterday", "2026-05-31", true},
		{"date in the far past", "2025-01-01", true},
		{"malformed -> never expired", "next tuesday", false},
	}
	for _, c := range tt {
		t.Run(c.name, func(t *testing.T) {
			u := subscription.User{}
			if c.expireAt != "" {
				u.Properties = map[string]string{"expireAt": c.expireAt}
			}
			got := IsExpired(u, now)
			if got != c.wantExpired {
				t.Errorf("IsExpired(%q) = %v, want %v", c.expireAt, got, c.wantExpired)
			}
		})
	}
}

func TestExpireAtString_addsCalendarDaysInUTC(t *testing.T) {
	now := time.Date(2026, 5, 24, 23, 30, 0, 0, time.UTC)
	got := ExpireAtString(30, now)
	want := "2026-06-23"
	if got != want {
		t.Errorf("ExpireAtString(30, %v) = %q, want %q", now, got, want)
	}
}

func TestSetExpireAt_initialisesPropertiesIfNil(t *testing.T) {
	now := time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC)
	u := subscription.User{Name: "alice"}
	SetExpireAt(&u, 7, now)
	if u.Properties == nil || u.Properties["expireAt"] != "2026-05-31" {
		t.Errorf("Properties = %v", u.Properties)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./expiry/ -v`
Expected: FAIL (no package).

- [ ] **Step 3: Write the implementation**

```go
// expiry/expiry.go
// Package expiry implements per-account time-limited access for users
// provisioned in Casdoor. The expiry timestamp lives on the user as
// Properties["expireAt"] in ISO-8601 date form (YYYY-MM-DD) and is
// interpreted in UTC.
package expiry

import (
	"time"

	"github.com/acme/auth-server/subscription"
)

// dateLayout is the ISO-8601 date form we store in user.Properties["expireAt"].
const dateLayout = "2006-01-02"

// PropKey is the key under user.Properties where the expiry date lives.
const PropKey = "expireAt"

// IsExpired reports whether the user's expireAt is strictly before today (in UTC).
// A missing or unparseable expireAt counts as "never expires" — this lets
// fixture users (like the admin account) coexist safely in the same org.
func IsExpired(u subscription.User, now time.Time) bool {
	raw := ""
	if u.Properties != nil {
		raw = u.Properties[PropKey]
	}
	if raw == "" {
		return false
	}
	t, err := time.ParseInLocation(dateLayout, raw, time.UTC)
	if err != nil {
		return false
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return t.Before(today)
}

// ExpireAtString returns the date `days` calendar days after `now`, formatted
// for Properties["expireAt"]. Always normalises to UTC midnight first so the
// result doesn't depend on the time-of-day component of `now`.
func ExpireAtString(days int, now time.Time) string {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return today.AddDate(0, 0, days).Format(dateLayout)
}

// SetExpireAt writes Properties["expireAt"] = today+days, initialising the
// Properties map if needed. Does not touch any other fields on the user.
func SetExpireAt(u *subscription.User, days int, now time.Time) {
	if u.Properties == nil {
		u.Properties = map[string]string{}
	}
	u.Properties[PropKey] = ExpireAtString(days, now)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./expiry/ -v`
Expected: PASS for all subtests of `TestIsExpired`, `TestExpireAtString_addsCalendarDaysInUTC`, `TestSetExpireAt_initialisesPropertiesIfNil`.

- [ ] **Step 5: Commit**

```bash
git add expiry/expiry.go expiry/expiry_test.go
git commit -m "feat(expiry): pure date helpers for the expireAt user property"
```

---

## Task 6: expiry.Sweeper + SweepReport with a fake client

**Files:**
- Create: `expiry/sweep.go`
- Create: `expiry/sweep_test.go`

- [ ] **Step 1: Write the failing test**

```go
// expiry/sweep_test.go
package expiry

import (
	"errors"
	"testing"
	"time"

	"github.com/acme/auth-server/subscription"
)

// fakeClient is a stand-in for the subset of subscription.HTTPClient that
// Sweeper actually uses. We can drive every code path without httptest.
type fakeClient struct {
	users      []subscription.User
	updates    []subscription.User
	updateErr  map[string]error // by user name
	listErr    error
}

func (f *fakeClient) ListUsers(org string) ([]subscription.User, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.users, nil
}

func (f *fakeClient) UpdateUser(u subscription.User) error {
	if err := f.updateErr[u.Name]; err != nil {
		return err
	}
	f.updates = append(f.updates, u)
	return nil
}

func TestSweepOrg_disablesOnlyExpiredAndNotAlreadyForbidden(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	c := &fakeClient{users: []subscription.User{
		{Owner: "org", Name: "active",        Properties: map[string]string{"expireAt": "2026-06-30"}},
		{Owner: "org", Name: "expired",       Properties: map[string]string{"expireAt": "2026-05-31"}},
		{Owner: "org", Name: "already-off",   Forbidden: true, Properties: map[string]string{"expireAt": "2026-05-31"}},
		{Owner: "org", Name: "fixture-admin"}, // no expireAt -> never swept
	}}

	rep, err := NewSweeper(c).SweepOrg("org", now)
	if err != nil {
		t.Fatal(err)
	}

	if rep.Scanned != 4 || rep.Expired != 2 || rep.Disabled != 1 || rep.AlreadyOff != 1 {
		t.Errorf("report = %+v", rep)
	}
	if len(c.updates) != 1 || c.updates[0].Name != "expired" || !c.updates[0].Forbidden {
		t.Errorf("update calls = %+v", c.updates)
	}
}

func TestSweepOrg_continuesOnPerUserError(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	c := &fakeClient{
		users: []subscription.User{
			{Owner: "org", Name: "a", Properties: map[string]string{"expireAt": "2026-05-30"}},
			{Owner: "org", Name: "b", Properties: map[string]string{"expireAt": "2026-05-30"}},
			{Owner: "org", Name: "c", Properties: map[string]string{"expireAt": "2026-05-30"}},
		},
		updateErr: map[string]error{"b": errors.New("nope")},
	}
	rep, err := NewSweeper(c).SweepOrg("org", now)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Disabled != 2 || len(rep.Errors) != 1 || rep.Errors[0].Name != "b" {
		t.Errorf("report = %+v", rep)
	}
}

func TestSweepOrg_returnsErrorWhenListFails(t *testing.T) {
	c := &fakeClient{listErr: errors.New("network")}
	_, err := NewSweeper(c).SweepOrg("org", time.Now())
	if err == nil {
		t.Errorf("err = nil, want network failure")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./expiry/ -v -run TestSweep`
Expected: FAIL (`NewSweeper`, `SweepOrg`, `SweepReport` undefined).

- [ ] **Step 3: Write the implementation**

```go
// expiry/sweep.go
package expiry

import (
	"fmt"
	"time"

	"github.com/acme/auth-server/subscription"
)

// UserClient is the slice of subscription.HTTPClient the Sweeper depends on.
// Defined as a tiny interface so tests can substitute an in-memory fake.
type UserClient interface {
	ListUsers(org string) ([]subscription.User, error)
	UpdateUser(u subscription.User) error
}

// Sweeper disables expired users in an org by setting forbidden=true.
type Sweeper struct {
	client UserClient
}

func NewSweeper(c UserClient) *Sweeper { return &Sweeper{client: c} }

// SweepError records that one user couldn't be processed; the sweep
// continues with the next user so a single bad row doesn't strand the rest.
type SweepError struct {
	Name string
	Err  error
}

func (e SweepError) Error() string { return fmt.Sprintf("%s: %v", e.Name, e.Err) }

// SweepReport summarises a single sweep run for logging.
type SweepReport struct {
	Scanned    int          // total users in the org
	Expired    int          // users with expireAt < today
	Disabled   int          // expired users newly switched to forbidden=true
	AlreadyOff int          // expired users that were already forbidden
	Errors     []SweepError // per-user failures (sweep still completed)
}

// SweepOrg lists every user in `org`, identifies the expired ones, and
// flips `forbidden=true` on those that aren't already disabled. Idempotent:
// running twice is safe.
func (s *Sweeper) SweepOrg(org string, now time.Time) (SweepReport, error) {
	users, err := s.client.ListUsers(org)
	if err != nil {
		return SweepReport{}, fmt.Errorf("list users in %s: %w", org, err)
	}
	rep := SweepReport{Scanned: len(users)}
	for _, u := range users {
		if !IsExpired(u, now) {
			continue
		}
		rep.Expired++
		if u.Forbidden {
			rep.AlreadyOff++
			continue
		}
		u.Forbidden = true
		if err := s.client.UpdateUser(u); err != nil {
			rep.Errors = append(rep.Errors, SweepError{Name: u.Name, Err: err})
			continue
		}
		rep.Disabled++
	}
	return rep, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./expiry/ -v`
Expected: PASS for all `TestSweepOrg_*` and the earlier expiry tests.

- [ ] **Step 5: Commit**

```bash
git add expiry/sweep.go expiry/sweep_test.go
git commit -m "feat(expiry): Sweeper that disables expired users in an org"
```

---

## Task 7: cmd/authadmin scaffold (env + subcommand dispatch + help)

**Files:**
- Create: `cmd/authadmin/main.go`

- [ ] **Step 1: Write the implementation**

There's no behaviour to TDD yet — this task is purely the wiring; subcommands plug in over the next tasks. Each subcommand will get its own test alongside it.

```go
// cmd/authadmin/main.go
// authadmin — operator CLI for batch-creating phone-OTP accounts in Casdoor
// with a per-account expireAt property, plus the daily expiry sweep.
//
// All commands authenticate against Casdoor via the admin app-built-in
// credentials, read from env:
//   CASDOOR_ENDPOINT      e.g. https://auth-server-msje.onrender.com
//   CASDOOR_CLIENT_ID     admin app-built-in client id
//   CASDOOR_CLIENT_SECRET admin app-built-in client secret
package main

import (
	"fmt"
	"os"

	"github.com/acme/auth-server/subscription"
)

const usage = `authadmin — manage time-limited phone-OTP accounts in Casdoor

USAGE
  authadmin <command> [flags]

COMMANDS
  create-batch    Create users in bulk from a CSV, setting expireAt = today+N days
  list            List users in an org with their expireAt + state
  extend          Push a user's expireAt forward and re-enable them
  disable-expired Disable (forbidden=true) any users whose expireAt is in the past
  disable         Manually disable a single user
  enable          Manually enable a single user

Run "authadmin <command> -h" for the flags each command accepts.

Environment:
  CASDOOR_ENDPOINT, CASDOOR_CLIENT_ID, CASDOOR_CLIENT_SECRET
`

// newClient builds a subscription.HTTPClient from env, or fatally errors out
// if any required variable is missing. All subcommands route their Casdoor
// I/O through it.
func newClient() *subscription.HTTPClient {
	endpoint := os.Getenv("CASDOOR_ENDPOINT")
	cid := os.Getenv("CASDOOR_CLIENT_ID")
	secret := os.Getenv("CASDOOR_CLIENT_SECRET")
	if endpoint == "" || cid == "" || secret == "" {
		fmt.Fprintln(os.Stderr, "set CASDOOR_ENDPOINT, CASDOOR_CLIENT_ID, CASDOOR_CLIENT_SECRET")
		os.Exit(2)
	}
	return subscription.NewHTTPClient(endpoint, cid, secret)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	switch cmd {
	case "create-batch":
		runCreateBatch(args)
	case "list":
		runList(args)
	case "extend":
		runExtend(args)
	case "disable-expired":
		runDisableExpired(args)
	case "disable":
		runDisable(args)
	case "enable":
		runEnable(args)
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n%s", cmd, usage)
		os.Exit(2)
	}
}
```

The compile will fail at this point because the `run*` functions don't exist yet — they're added in the next tasks. That's expected and intentional: we add each subcommand + its tests one task at a time so the plan stays bite-sized. Each subsequent task ends with a compile-and-tests-pass step that confirms the scaffold remains intact.

- [ ] **Step 2: Commit**

```bash
git add cmd/authadmin/main.go
git commit -m "feat(authadmin): CLI scaffold (env + subcommand dispatch + help)"
```

The repo is now in a state where `go build ./cmd/authadmin/` fails — fine; the very next task gets it green.

---

## Task 8: `disable-expired` subcommand

Simplest subcommand and the one the cron runs — wire this first to get the binary compiling.

**Files:**
- Create: `cmd/authadmin/disable_expired.go`

- [ ] **Step 1: Write the implementation**

```go
// cmd/authadmin/disable_expired.go
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/acme/auth-server/expiry"
)

// runDisableExpired is the entrypoint for the daily Render Cron Job.
// Lists users in --org, disables (forbidden=true) any whose expireAt is in
// the past. Per-user failures are logged but don't fail the whole run.
func runDisableExpired(args []string) {
	fs := flag.NewFlagSet("disable-expired", flag.ExitOnError)
	org := fs.String("org", "", "Casdoor organization (required)")
	_ = fs.Parse(args)
	if *org == "" {
		fmt.Fprintln(os.Stderr, "disable-expired: --org is required")
		os.Exit(2)
	}

	rep, err := expiry.NewSweeper(newClient()).SweepOrg(*org, time.Now().UTC())
	if err != nil {
		fmt.Fprintf(os.Stderr, "sweep failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("sweep %s: scanned=%d expired=%d disabled=%d already_off=%d errors=%d\n",
		*org, rep.Scanned, rep.Expired, rep.Disabled, rep.AlreadyOff, len(rep.Errors))
	for _, e := range rep.Errors {
		fmt.Printf("  err: %s\n", e.Error())
	}
}
```

- [ ] **Step 2: Verify the binary builds**

Run: `go build ./cmd/authadmin/`
Expected: success (no output).

- [ ] **Step 3: Smoke-test help output**

Run: `./authadmin disable-expired -h`
Expected: usage block listing `-org` with description.

Cleanup: `rm authadmin`

- [ ] **Step 4: Commit**

```bash
git add cmd/authadmin/disable_expired.go
git commit -m "feat(authadmin): disable-expired subcommand (used by cron)"
```

---

## Task 9: `disable` + `enable` subcommands (single-user toggles)

Tiny commands sharing one helper — bundled into one task because each is ~10 lines.

**Files:**
- Create: `cmd/authadmin/toggle.go`

- [ ] **Step 1: Write the implementation**

```go
// cmd/authadmin/toggle.go
package main

import (
	"flag"
	"fmt"
	"os"
)

func runDisable(args []string) { setForbidden(args, "disable", true) }
func runEnable(args []string)  { setForbidden(args, "enable", false) }

// setForbidden looks up the user, sets Forbidden, and writes them back.
// Errors out if the user doesn't exist (so a typo doesn't silently no-op).
// Uses Go's structural typing — neither subscription.User nor the package name
// appears in this file; we just call methods on the value newClient() returns.
func setForbidden(args []string, cmdName string, forbidden bool) {
	fs := flag.NewFlagSet(cmdName, flag.ExitOnError)
	org := fs.String("org", "", "Casdoor organization (required)")
	name := fs.String("name", "", "user name (required)")
	_ = fs.Parse(args)
	if *org == "" || *name == "" {
		fmt.Fprintf(os.Stderr, "%s: --org and --name are required\n", cmdName)
		os.Exit(2)
	}
	c := newClient()
	u, err := c.GetUser(*org, *name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", cmdName, err)
		os.Exit(1)
	}
	if u == nil {
		fmt.Fprintf(os.Stderr, "%s: user %s/%s not found\n", cmdName, *org, *name)
		os.Exit(1)
	}
	u.Forbidden = forbidden
	if err := c.UpdateUser(*u); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", cmdName, err)
		os.Exit(1)
	}
	fmt.Printf("%s: %s/%s forbidden=%v\n", cmdName, *org, *name, forbidden)
}
```

- [ ] **Step 2: Verify the binary builds**

Run: `go build ./cmd/authadmin/`
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add cmd/authadmin/toggle.go
git commit -m "feat(authadmin): disable / enable single-user toggles"
```

---

## Task 10: `extend` subcommand

**Files:**
- Create: `cmd/authadmin/extend.go`

- [ ] **Step 1: Write the implementation**

```go
// cmd/authadmin/extend.go
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/acme/auth-server/expiry"
)

// runExtend pushes a user's expireAt forward by --by days from today AND
// clears forbidden=true, so a "renewed" account can log in again immediately.
func runExtend(args []string) {
	fs := flag.NewFlagSet("extend", flag.ExitOnError)
	org := fs.String("org", "", "Casdoor organization (required)")
	name := fs.String("name", "", "user name (required)")
	by := fs.Int("by", 0, "days to extend by (required, integer > 0)")
	_ = fs.Parse(args)
	if *org == "" || *name == "" || *by <= 0 {
		fmt.Fprintln(os.Stderr, "extend: --org, --name, and --by (>0) are required")
		os.Exit(2)
	}
	c := newClient()
	u, err := c.GetUser(*org, *name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "extend: %v\n", err)
		os.Exit(1)
	}
	if u == nil {
		fmt.Fprintf(os.Stderr, "extend: user %s/%s not found\n", *org, *name)
		os.Exit(1)
	}
	now := time.Now().UTC()
	expiry.SetExpireAt(u, *by, now)
	u.Forbidden = false
	if err := c.UpdateUser(*u); err != nil {
		fmt.Fprintf(os.Stderr, "extend: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("extend: %s/%s expireAt=%s forbidden=false\n",
		*org, *name, u.Properties[expiry.PropKey])
}
```

- [ ] **Step 2: Verify the binary builds**

Run: `go build ./cmd/authadmin/`
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add cmd/authadmin/extend.go
git commit -m "feat(authadmin): extend subcommand (push expireAt + clear forbidden)"
```

---

## Task 11: `list` subcommand

**Files:**
- Create: `cmd/authadmin/list.go`

- [ ] **Step 1: Write the implementation**

```go
// cmd/authadmin/list.go
package main

import (
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/acme/auth-server/expiry"
	"github.com/acme/auth-server/subscription"
)

// runList prints a fixed-width table of all users in --org with their
// expireAt + state. Optionally narrows to just expired users or users
// expiring within --expiring-in days.
func runList(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	org := fs.String("org", "", "Casdoor organization (required)")
	expired := fs.Bool("expired", false, "only show users past their expireAt")
	expiringIn := fs.Int("expiring-in", 0, "only show users with expireAt within this many days")
	_ = fs.Parse(args)
	if *org == "" {
		fmt.Fprintln(os.Stderr, "list: --org is required")
		os.Exit(2)
	}
	c := newClient()
	users, err := c.ListUsers(*org)
	if err != nil {
		fmt.Fprintf(os.Stderr, "list: %v\n", err)
		os.Exit(1)
	}
	now := time.Now().UTC()
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tPHONE\tEXPIREAT\tSTATE")
	for _, u := range users {
		st := state(u, now)
		// "expiring" is filter-conditional: it overlays "active" when
		// --expiring-in is set and the user falls within the window.
		if st == "active" && *expiringIn > 0 && isExpiringWithin(u, now, *expiringIn) {
			st = "expiring"
		}
		if *expired && st != "expired" {
			continue
		}
		if *expiringIn > 0 && st != "expiring" {
			continue
		}
		ea := ""
		if u.Properties != nil {
			ea = u.Properties[expiry.PropKey]
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", u.Name, u.Phone, ea, st)
	}
	tw.Flush()
}

// state collapses (forbidden, expireAt, now) into one of the documented
// state labels.
func state(u subscription.User, now time.Time) string {
	if u.Forbidden {
		return "disabled"
	}
	if u.Properties == nil || u.Properties[expiry.PropKey] == "" {
		return "no-expiry"
	}
	if expiry.IsExpired(u, now) {
		return "expired"
	}
	return "active"
}

// isExpiringWithin returns true when the user's expireAt is between today and
// today+days (inclusive). Returns false for missing/unparseable expireAt.
func isExpiringWithin(u subscription.User, now time.Time, days int) bool {
	if u.Properties == nil {
		return false
	}
	raw := u.Properties[expiry.PropKey]
	if raw == "" {
		return false
	}
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return false
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	limit := today.AddDate(0, 0, days)
	return !t.Before(today) && !t.After(limit)
}
```

- [ ] **Step 2: Verify the binary builds**

Run: `go build ./cmd/authadmin/`
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add cmd/authadmin/list.go
git commit -m "feat(authadmin): list subcommand with state column + filters"
```

---

## Task 12: `create-batch` subcommand (TDD — pure logic separated from main)

The CSV parsing + per-row orchestration is non-trivial, so we split it into a pure helper that's unit-testable and a thin `main`-side wrapper.

**Files:**
- Create: `cmd/authadmin/create_batch.go`
- Create: `cmd/authadmin/create_batch_test.go`

- [ ] **Step 1: Write the failing test**

```go
// cmd/authadmin/create_batch_test.go
package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/acme/auth-server/subscription"
)

type stubCreator struct {
	added []subscription.User
	errAt map[string]error // name → error to return
}

func (s *stubCreator) AddUser(u subscription.User) error {
	if err := s.errAt[u.Name]; err != nil {
		return err
	}
	s.added = append(s.added, u)
	return nil
}

func TestRunCreateBatch_setsExpireAtAndOrgPerRow(t *testing.T) {
	in := strings.NewReader("name,displayName,phone,countryCode\nalice,Alice,98765432,SG\nbob,Bob,97654321,SG\n")
	var out bytes.Buffer
	stub := &stubCreator{}
	now := time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC)

	if err := createBatch(in, &out, stub, createBatchOpts{
		Org: "intent-pulse-org", Days: 30, App: "intent-pulse", Now: now,
	}); err != nil {
		t.Fatal(err)
	}

	if len(stub.added) != 2 {
		t.Fatalf("added %d, want 2", len(stub.added))
	}
	a := stub.added[0]
	if a.Owner != "intent-pulse-org" || a.Name != "alice" || a.Phone != "98765432" ||
		a.CountryCode != "SG" || a.Region != "SG" || a.SignupApplication != "intent-pulse" ||
		a.Type != "normal-user" || a.Properties["expireAt"] != "2026-06-23" {
		t.Errorf("user[0] = %+v", a)
	}
	if !strings.Contains(out.String(), "alice,98765432,SG,2026-06-23,ok,") {
		t.Errorf("result CSV missing alice row: %s", out.String())
	}
	if !strings.Contains(out.String(), "bob,97654321,SG,2026-06-23,ok,") {
		t.Errorf("result CSV missing bob row: %s", out.String())
	}
}

func TestRunCreateBatch_reportsPerRowErrorsAndReturnsError(t *testing.T) {
	in := strings.NewReader("name,displayName,phone,countryCode\nalice,Alice,98765432,SG\nbob,Bob,97654321,SG\n")
	var out bytes.Buffer
	stub := &stubCreator{errAt: map[string]error{"bob": errors.New("user already exists")}}
	now := time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC)

	err := createBatch(in, &out, stub, createBatchOpts{
		Org: "org", Days: 7, App: "app", Now: now,
	})
	if err == nil {
		t.Errorf("err = nil, want batch failed")
	}
	if !strings.Contains(out.String(), "bob,97654321,SG,,error,user already exists") {
		t.Errorf("result CSV missing bob error row: %s", out.String())
	}
	if len(stub.added) != 1 || stub.added[0].Name != "alice" {
		t.Errorf("expected only alice to have been added, got %+v", stub.added)
	}
}

func TestRunCreateBatch_rejectsMissingHeaderColumns(t *testing.T) {
	in := strings.NewReader("name,phone\nalice,98765432\n")
	stub := &stubCreator{}
	err := createBatch(in, &bytes.Buffer{}, stub, createBatchOpts{
		Org: "o", Days: 1, App: "a", Now: time.Now(),
	})
	if err == nil || !strings.Contains(err.Error(), "header") {
		t.Errorf("err = %v, want header error", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/authadmin/ -v`
Expected: FAIL (`createBatch` and `createBatchOpts` undefined).

- [ ] **Step 3: Write the implementation**

```go
// cmd/authadmin/create_batch.go
package main

import (
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/acme/auth-server/expiry"
	"github.com/acme/auth-server/subscription"
	"time"
)

// userCreator is the subset of subscription.HTTPClient that createBatch uses.
// Defined inline so tests can pass a stub; *subscription.HTTPClient satisfies
// it automatically because it has a matching AddUser method.
type userCreator interface {
	AddUser(u subscription.User) error
}

type createBatchOpts struct {
	Org  string
	App  string // value for SignupApplication
	Days int
	Now  time.Time
}

var requiredHeaders = []string{"name", "displayName", "phone", "countryCode"}

// createBatch reads a CSV of users from `in`, creates each one via `client`,
// writes a result CSV to `out`, and returns a non-nil error if any row failed
// (so the CLI can exit non-zero). Pure I/O — no env reads, no os.Exit; easy
// to unit-test.
func createBatch(in io.Reader, out io.Writer, client userCreator, opts createBatchOpts) error {
	rdr := csv.NewReader(in)
	rdr.TrimLeadingSpace = true
	hdr, err := rdr.Read()
	if err != nil {
		return fmt.Errorf("read header: %w", err)
	}
	idx := map[string]int{}
	for i, h := range hdr {
		idx[h] = i
	}
	for _, want := range requiredHeaders {
		if _, ok := idx[want]; !ok {
			return fmt.Errorf("header: missing required column %q (need %v)", want, requiredHeaders)
		}
	}

	expireAt := expiry.ExpireAtString(opts.Days, opts.Now)
	w := csv.NewWriter(out)
	defer w.Flush()
	_ = w.Write([]string{"name", "phone", "countryCode", "expireAt", "status", "error"})

	var hadFailure bool
	for {
		row, err := rdr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read row: %w", err)
		}
		u := subscription.User{
			Owner:             opts.Org,
			Name:              row[idx["name"]],
			DisplayName:       row[idx["displayName"]],
			Phone:             row[idx["phone"]],
			CountryCode:       row[idx["countryCode"]],
			Region:            row[idx["countryCode"]],
			Type:              "normal-user",
			SignupApplication: opts.App,
			Properties:        map[string]string{expiry.PropKey: expireAt},
		}
		if err := client.AddUser(u); err != nil {
			hadFailure = true
			_ = w.Write([]string{u.Name, u.Phone, u.CountryCode, "", "error", err.Error()})
			continue
		}
		_ = w.Write([]string{u.Name, u.Phone, u.CountryCode, expireAt, "ok", ""})
	}
	if hadFailure {
		return errors.New("one or more rows failed; see result CSV")
	}
	return nil
}

// runCreateBatch is the main-side wrapper: parses flags, opens files, calls
// createBatch, exits non-zero on partial failures.
func runCreateBatch(args []string) {
	fs := flag.NewFlagSet("create-batch", flag.ExitOnError)
	org := fs.String("org", "", "Casdoor organization (required)")
	app := fs.String("app", "intent-pulse", "signupApplication for each user")
	csvPath := fs.String("csv", "", "input CSV path (required)")
	outPath := fs.String("out", "", "result CSV path (default: stdout)")
	days := fs.Int("days", 0, "days until expiry (required, integer > 0)")
	_ = fs.Parse(args)
	if *org == "" || *csvPath == "" || *days <= 0 {
		fmt.Fprintln(os.Stderr, "create-batch: --org, --csv, --days (>0) are required")
		os.Exit(2)
	}

	in, err := os.Open(*csvPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create-batch: open csv: %v\n", err)
		os.Exit(1)
	}
	defer in.Close()
	var out io.Writer = os.Stdout
	if *outPath != "" {
		f, err := os.Create(*outPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "create-batch: open out: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		out = f
	}

	err = createBatch(in, out, newClient(), createBatchOpts{
		Org: *org, App: *app, Days: *days, Now: time.Now().UTC(),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "create-batch: %v\n", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/authadmin/ -v`
Expected: PASS for all `TestRunCreateBatch_*`.

- [ ] **Step 5: Verify the full binary still builds + vets clean**

Run: `go build ./cmd/authadmin/ && go vet ./cmd/authadmin/`
Expected: success, no output from `go vet`.

- [ ] **Step 6: Commit**

```bash
git add cmd/authadmin/create_batch.go cmd/authadmin/create_batch_test.go
git commit -m "feat(authadmin): create-batch subcommand with CSV in/out + tests"
```

---

## Task 13: cmd/authadmin Dockerfile (for both local build + Render Cron Job)

**Files:**
- Create: `cmd/authadmin/Dockerfile`

- [ ] **Step 1: Write the Dockerfile**

```dockerfile
# Multi-stage build for the authadmin CLI. Same shape as cmd/subsync/Dockerfile.
# The image is invoked two ways:
#   1. Render Cron Job runs `authadmin disable-expired --org <org>` daily.
#   2. Operators can `docker run ... authadmin <subcommand>` locally for batch
#      provisioning without installing Go.

FROM golang:1.22 AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o /authadmin ./cmd/authadmin

FROM alpine:3
COPY --from=build /authadmin /authadmin
ENTRYPOINT ["/authadmin"]
```

- [ ] **Step 2: Verify the image builds**

Run: `docker build -f cmd/authadmin/Dockerfile -t authadmin:test .`
Expected: builds successfully, image tagged.

- [ ] **Step 3: Smoke-test help output via Docker**

Run: `docker run --rm authadmin:test help`
Expected: usage block.

Cleanup: `docker rmi authadmin:test`

- [ ] **Step 4: Commit**

```bash
git add cmd/authadmin/Dockerfile
git commit -m "feat(authadmin): Dockerfile for local + Render-cron use"
```

---

## Task 14: render.yaml — add the cron service

**Files:**
- Modify: `render.yaml`

- [ ] **Step 1: Read the current render.yaml**

Confirm the file ends with the existing web service definition; we'll append the cron service under the same top-level `services:` list.

```bash
cat render.yaml
```

- [ ] **Step 2: Append the cron service**

Append the following block at the bottom of `services:` in `render.yaml` (indent with 2 spaces under `services:`):

```yaml
  # Daily sweep that disables users in intent-pulse-org whose expireAt is in
  # the past. Same Dockerfile as the authadmin CLI; the dockerCommand selects
  # the subcommand at run time. CASDOOR_CLIENT_ID / CASDOOR_CLIENT_SECRET are
  # the admin app-built-in credentials, set in the Render dashboard (sync:
  # false keeps them out of source).
  - type: cron
    name: auth-server-expiry-sweep
    runtime: docker
    region: singapore
    schedule: "0 1 * * *"
    dockerfilePath: ./cmd/authadmin/Dockerfile
    dockerContext: .
    dockerCommand: "/authadmin disable-expired --org intent-pulse-org"
    envVars:
      - key: CASDOOR_ENDPOINT
        value: https://auth-server-msje.onrender.com
      - key: CASDOOR_CLIENT_ID
        sync: false
      - key: CASDOOR_CLIENT_SECRET
        sync: false
```

- [ ] **Step 3: Validate YAML**

Run: `/usr/local/bin/python3.9 -c 'import yaml; yaml.safe_load(open("render.yaml"))' && echo OK`
Expected: `OK`

If your local `python3` doesn't have PyYAML, run instead: `docker run --rm -v "$PWD/render.yaml:/r.yaml:ro" mikefarah/yq:4 . /r.yaml > /dev/null && echo OK`.

- [ ] **Step 4: Commit**

```bash
git add render.yaml
git commit -m "feat(deploy): Render Cron Job for the daily expiry sweep"
```

---

## Task 15: Integration test — round-trip a live sweep

Mirrors the existing `TestEndToEnd_PhoneOtpLogin` pattern: only runs when `RUN_INTEGRATION=1` and Casdoor admin creds are set. Creates a temporary user with an expireAt yesterday, runs the sweep, asserts forbidden=true, then deletes the user.

**Files:**
- Create: `test/integration/expiry_sweep_test.go`

- [ ] **Step 1: Write the test**

```go
//go:build integration

package integration

import (
	"os"
	"testing"
	"time"

	"github.com/acme/auth-server/expiry"
	"github.com/acme/auth-server/subscription"
)

// End-to-end sweep test against the live Casdoor.
//
// Env (same contract as TestEndToEnd_PhoneOtpLogin):
//   RUN_INTEGRATION=1
//   CASDOOR_ENDPOINT, CASDOOR_CLIENT_ID, CASDOOR_CLIENT_SECRET (admin app-built-in)
//   EXPIRY_TEST_ORG (e.g. intent-pulse-org)
func TestEndToEnd_ExpirySweep(t *testing.T) {
	if os.Getenv("RUN_INTEGRATION") != "1" {
		t.Skip("set RUN_INTEGRATION=1 to run")
	}
	base := env(t, "CASDOOR_ENDPOINT")
	cid := env(t, "CASDOOR_CLIENT_ID")
	secret := env(t, "CASDOOR_CLIENT_SECRET")
	org := env(t, "EXPIRY_TEST_ORG")

	c := subscription.NewHTTPClient(base, cid, secret)
	name := "expiry-test-" + time.Now().UTC().Format("20060102-150405")

	// 1. Create a user whose expireAt is yesterday (already past).
	yesterday := time.Now().UTC().AddDate(0, 0, -1)
	u := subscription.User{
		Owner: org, Name: name, DisplayName: name,
		Type: "normal-user", SignupApplication: "intent-pulse",
		Properties: map[string]string{expiry.PropKey: expiry.ExpireAtString(0, yesterday)},
	}
	if err := c.AddUser(u); err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	t.Cleanup(func() {
		// best-effort cleanup — leave the row forbidden=true if delete fails
		got, _ := c.GetUser(org, name)
		if got != nil {
			got.Forbidden = true
			_ = c.UpdateUser(*got)
		}
	})

	// 2. Sweep.
	rep, err := expiry.NewSweeper(c).SweepOrg(org, time.Now().UTC())
	if err != nil {
		t.Fatalf("SweepOrg: %v", err)
	}
	if rep.Disabled < 1 {
		t.Errorf("Disabled = %d, want >= 1 (report: %+v)", rep.Disabled, rep)
	}

	// 3. Verify the row is now forbidden=true.
	got, err := c.GetUser(org, name)
	if err != nil {
		t.Fatalf("GetUser after sweep: %v", err)
	}
	if got == nil || !got.Forbidden {
		t.Errorf("after sweep: user.Forbidden=false (user=%+v)", got)
	}

	// 4. Test extend re-enables: push expireAt forward by 7 days + clear forbidden.
	expiry.SetExpireAt(got, 7, time.Now().UTC())
	got.Forbidden = false
	if err := c.UpdateUser(*got); err != nil {
		t.Fatalf("UpdateUser (extend): %v", err)
	}
	after, _ := c.GetUser(org, name)
	if after == nil || after.Forbidden || after.Properties[expiry.PropKey] !=
		expiry.ExpireAtString(7, time.Now().UTC()) {
		t.Errorf("after extend: %+v", after)
	}
}
```

- [ ] **Step 2: Compile-check (no run — needs live Casdoor)**

Run: `go test -tags integration -run TestEndToEnd_ExpirySweep ./test/integration/ -count=0`
Expected: `ok ... [no tests to run]` — proves the file compiles without needing the live stack.

- [ ] **Step 3: Optionally run against live Casdoor**

```bash
set -a; . ./.env; set +a
RUN_INTEGRATION=1 \
CASDOOR_ENDPOINT=https://auth-server-msje.onrender.com \
CASDOOR_CLIENT_ID="$ADMIN_CLIENT_ID" CASDOOR_CLIENT_SECRET="$ADMIN_CLIENT_SECRET" \
EXPIRY_TEST_ORG=intent-pulse-org \
go test -tags integration -run TestEndToEnd_ExpirySweep ./test/integration/ -v -timeout 60s
```
Expected: `PASS`. Optional step — skip if you don't have admin creds handy; CI/CD can run it.

- [ ] **Step 4: Commit**

```bash
git add test/integration/expiry_sweep_test.go
git commit -m "test(integration): end-to-end expiry sweep against live Casdoor"
```

---

## Task 16: Operator docs (`docs/admin-users.md`)

**Files:**
- Create: `docs/admin-users.md`

- [ ] **Step 1: Write the doc**

```markdown
# Admin user management (authadmin CLI)

Phone-OTP accounts in Casdoor with per-account expiry. The CLI lives in
`cmd/authadmin/`; the same binary runs as the daily sweep cron on Render.

## When to use what

| Need | Command |
|------|---------|
| Onboard a batch of clients | `authadmin create-batch --csv batch.csv --days 30` |
| See who's expiring soon | `authadmin list --expiring-in 7` |
| Re-enable a client whose access lapsed | `authadmin extend --name alice --by 30` |
| Disable a specific client now | `authadmin disable --name alice` |
| Run the daily sweep manually | `authadmin disable-expired --org intent-pulse-org` |

## Setup

Set three env vars in your shell (these are the `app-built-in` admin
credentials — same ones the integration test uses; never commit them):

```
export CASDOOR_ENDPOINT=https://auth-server-msje.onrender.com
export CASDOOR_CLIENT_ID=<admin app-built-in clientId>
export CASDOOR_CLIENT_SECRET=<admin app-built-in clientSecret>
```

Run from source: `go run ./cmd/authadmin <subcommand> [flags]`
Or build: `go build -o ./bin/authadmin ./cmd/authadmin && ./bin/authadmin ...`

## Create a batch from CSV

CSV format — header row required, in this exact order or with `name`,
`displayName`, `phone`, `countryCode` columns in any order:

```csv
name,displayName,phone,countryCode
alice,Alice Sun,98765432,SG
bob,Bob Lim,97654321,SG
```

```bash
authadmin create-batch --org intent-pulse-org --csv ./onboarding.csv --days 30
```

A result CSV is printed to stdout (or to `--out result.csv`):

```csv
name,phone,countryCode,expireAt,status,error
alice,98765432,SG,2026-06-23,ok,
bob,97654321,SG,,error,user already exists
```

The command exits with code `1` if any row failed; code `0` if all rows
succeeded. Use the result CSV as the audit trail of who you created.

> **Twilio trial reminder:** until you upgrade your Twilio account, every
> phone number you add must already be in Twilio's "Verified Caller IDs" list,
> or the SMS will silently fail when that user tries to log in.

## List + filter

```bash
authadmin list --org intent-pulse-org                    # everyone
authadmin list --org intent-pulse-org --expired          # past expireAt
authadmin list --org intent-pulse-org --expiring-in 7    # expires in <=7 days
```

State column:
- `active` — has expireAt, in the future, not forbidden.
- `expiring` — currently shown when `--expiring-in N` is set and within range.
- `expired` — expireAt in the past, not yet swept.
- `disabled` — `forbidden=true` (whether by sweep or by `disable`).
- `no-expiry` — no expireAt set (e.g. the seeded `admin` account).

## Extend / re-enable

```bash
authadmin extend --org intent-pulse-org --name alice --by 30
```

Pushes `expireAt = today + 30` AND clears `forbidden=true`, so a previously-
disabled account can log in again immediately.

## Manual disable / enable

```bash
authadmin disable --org intent-pulse-org --name alice
authadmin enable  --org intent-pulse-org --name alice
```

Both leave `expireAt` untouched — use `extend` if you want to push it forward.

## The daily sweep (cron)

`authadmin disable-expired --org intent-pulse-org` is what the Render Cron
Job runs at 01:00 UTC daily. It's idempotent — running it twice in a row is
safe. To run manually:

```bash
authadmin disable-expired --org intent-pulse-org
# sweep intent-pulse-org: scanned=12 expired=2 disabled=1 already_off=1 errors=0
```

## What "expired" means downstream

When the sweep sets `forbidden=true`, the user's existing dashboard session
keeps working until their access token's TTL elapses (~1h). At the next
silent-refresh attempt, Casdoor refuses the refresh, the session is cleared,
and they're bounced to the login page. Casdoor won't issue a new OTP for a
forbidden user, so they can't get back in until you `extend`.
```

- [ ] **Step 2: Commit**

```bash
git add docs/admin-users.md
git commit -m "docs: operator guide for the authadmin CLI"
```

---

## Task 17: Update `docs/deploy-render.md` to mention the new cron service

**Files:**
- Modify: `docs/deploy-render.md`

- [ ] **Step 1: Read the current file**

```bash
cat docs/deploy-render.md
```

Find the "What the Blueprint creates" table — that's where the cron service should be added as a new row.

- [ ] **Step 2: Append a row to the table + a short paragraph**

In the **"What the Blueprint creates"** section, add this row at the bottom of the table:

```markdown
- **`auth-server-expiry-sweep`** — a Render Cron Job that runs `authadmin
  disable-expired --org intent-pulse-org` once a day at 01:00 UTC. Same
  Dockerfile (`cmd/authadmin/Dockerfile`) you'd use to run the CLI locally.
  See `docs/admin-users.md` for what the sweep does and how to extend a
  user's access.
```

In the **"Required post-deploy steps"** section, add:

```markdown
4. **Set `CASDOOR_CLIENT_ID` and `CASDOOR_CLIENT_SECRET` on the
   `auth-server-expiry-sweep` cron service** — same admin app-built-in
   credentials you used to provision the `intent-pulse` application. Without
   them the daily sweep will exit non-zero in its logs but won't disrupt the
   web service.
```

- [ ] **Step 3: Commit**

```bash
git add docs/deploy-render.md
git commit -m "docs(deploy): mention the new expiry-sweep cron service"
```

---

## Task 18: Final regression — `go test ./...` + `go vet ./...`

- [ ] **Step 1: Run the full test suite**

Run: `go test ./...`
Expected: all packages PASS (`expiry`, `subscription`, `cmd/authadmin`, plus any pre-existing packages). Integration tests are tag-gated and won't run.

- [ ] **Step 2: Run go vet**

Run: `go vet ./...`
Expected: no output (clean).

- [ ] **Step 3: Push the branch and open the PR**

```bash
git push origin feat/auth-server-v1
gh pr view --json url --jq .url   # or `gh pr create` if no PR yet
```

If no PR exists yet, the existing PR #1 (`feat/auth-server-v1` → `main`) on the auth-server repo already covers this branch — these commits will appear in it.

- [ ] **Step 4 (optional): Apply via the Render API**

Once the PR is merged into `main` (or whatever branch the live service tracks), Render's autoDeploy will see the new `render.yaml`. The cron service is provisioned automatically. If autoDeploy is off, you can trigger via the Render dashboard "Sync Blueprint" or via API.

Set `CASDOOR_CLIENT_ID` / `CASDOOR_CLIENT_SECRET` on the new cron service before it first runs (it'll exit non-zero otherwise — visible in cron-job logs).

- [ ] **Step 5: Smoke-test on Render**

In the Render dashboard for `auth-server-expiry-sweep`, click **"Trigger run"** to fire the cron manually. The job logs should print one line like:

```
sweep intent-pulse-org: scanned=N expired=0 disabled=0 already_off=0 errors=0
```

If `errors > 0`, inspect the per-error lines that follow.

---

## Notes for the implementer

- **TDD discipline.** Every code task starts with a failing test you can copy verbatim. Don't skip "Run test to verify it fails" — confirming the failure proves the test actually exercises the code you're about to write, not something unrelated.
- **Don't introduce new imports beyond stdlib.** The spec calls this out; the plan honours it. `subscription` and `expiry` packages are the only intra-repo dependencies.
- **Idempotency matters.** The sweep is designed to be safe to re-run; the integration test relies on that. Don't add side-effects that break re-runs.
- **Casdoor responds 200 + status="error" for many failure modes.** All the new HTTP methods check `out.Status != "ok"` and surface it as a Go error — keep that pattern when extending.
- **The integration test creates real users in the live `intent-pulse-org`.** It uses a timestamped name (`expiry-test-YYYYMMDD-HHMMSS`) so reruns don't clash, and `t.Cleanup` disables the row even if the test asserts fail. If you abort mid-test, run `authadmin list --org intent-pulse-org` and clean up by hand or with `authadmin disable`.
