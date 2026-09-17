package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func privateFixture(t *testing.T) (*PrivateAccess, *http.ServeMux, string, PrivateDirectory) {
	t.Helper()
	_, db := testAuth(t)
	file := filepath.Join(t.TempDir(), "operators.json")
	d := PrivateDirectory{Origins: []string{"http://localhost:8080"}, Operators: []PrivateOperator{
		{ID: "alice", Name: "Alice", Role: "reviewer", CodeSHA256: privateHash(strings.Repeat("a", 64))},
		{ID: "bob", Name: "Bob", Role: "operator", CodeSHA256: privateHash(strings.Repeat("b", 64))},
		{ID: "eve", Name: "Eve", Role: "viewer", CodeSHA256: privateHash(strings.Repeat("e", 64))},
	}}
	writeDirectory := func(d PrivateDirectory) {
		raw, _ := json.Marshal(d)
		if err := os.WriteFile(file, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeDirectory(d)
	a, err := NewPrivateAccess(db, file)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	a.Register(mux)
	mux.Handle("GET /console/workbench/work/id/artifact", a.RequireAuth(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(UserFromContext(r.Context()).ID)) }))
	mux.Handle("POST /console/workbench/work/id/confirm", a.RequireAuth(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(UserFromContext(r.Context()).ID)) }))
	mux.Handle("POST /app/demo/op", a.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(UserFromContext(r.Context()).ID + ":" + r.FormValue("op")))
	}))
	t.Cleanup(func() { db.Exec(`DELETE FROM private_operator_sessions WHERE operator_id IN ('alice','bob','eve')`) })
	return a, mux, file, d
}

func privateRequest(mux http.Handler, method, path, body, origin string, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://localhost:8080"+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func privateSignIn(t *testing.T, mux http.Handler, id, code string) *http.Cookie {
	t.Helper()
	w := privateRequest(mux, "POST", "/auth/login", url.Values{"operator": {id}, "code": {code}}.Encode(), "http://localhost:8080", nil)
	if w.Code != 303 {
		t.Fatalf("login %s: %d %s", id, w.Code, w.Body.String())
	}
	c := w.Result().Cookies()[0]
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || len(c.Value) != 64 || c.MaxAge != 28800 {
		t.Fatal("unsafe session cookie")
	}
	return c
}

func TestPrivateSessionIdentityPermissionsAndRevocation(t *testing.T) {
	a, mux, file, d := privateFixture(t)
	alice := privateSignIn(t, mux, "alice", strings.Repeat("a", 64))
	bob := privateSignIn(t, mux, "bob", strings.Repeat("b", 64))
	if alice.Value == bob.Value {
		t.Fatal("sessions shared")
	}
	for _, c := range []*http.Cookie{alice, bob} {
		var rawCount int
		if err := a.db.QueryRow(`SELECT count(*) FROM private_operator_sessions WHERE token_hash=$1`, c.Value).Scan(&rawCount); err != nil || rawCount != 0 {
			t.Fatal("raw session stored")
		}
	}
	for _, tc := range []struct {
		cookie               *http.Cookie
		method, path, origin string
		status               int
		identity             string
	}{
		{nil, "GET", "/console/workbench/work/id/artifact", "", 401, ""},
		{alice, "GET", "/console/workbench/work/id/artifact", "", 200, "alice"},
		{bob, "GET", "/console/workbench/work/id/artifact", "", 200, "bob"},
		{bob, "POST", "/console/workbench/work/id/confirm", "http://localhost:8080", 403, ""},
		{alice, "POST", "/console/workbench/work/id/confirm", "http://localhost:8080", 200, "alice"},
		{alice, "POST", "/console/workbench/work/id/confirm", "http://evil.example", 403, ""},
		{alice, "POST", "/console/workbench/work/id/confirm", "", 403, ""},
		{alice, "POST", "/console/workbench/work/id/confirm", "https://localhost:8080", 403, ""},
	} {
		w := privateRequest(mux, tc.method, tc.path, "reviewed_by=bob", tc.origin, tc.cookie)
		if w.Code != tc.status || (tc.identity != "" && w.Body.String() != tc.identity) {
			t.Fatalf("request %+v: %d %s", tc, w.Code, w.Body.String())
		}
	}
	// A process restart retains the hashed session; a role change revokes it.
	restarted, err := NewPrivateAccess(a.db, file)
	if err != nil {
		t.Fatal(err)
	}
	restartedMux := http.NewServeMux()
	restarted.Register(restartedMux)
	if w := privateRequest(restartedMux, "GET", "/auth/status", "", "", alice); w.Code != 200 || !strings.Contains(w.Body.String(), `"id":"alice"`) {
		t.Fatal("restart lost identity", w.Body.String())
	}
	if w := privateRequest(restartedMux, "GET", "/oauth2/userinfo", "", "", alice); w.Code != 200 || !strings.Contains(w.Body.String(), `"user":"alice"`) || strings.Contains(w.Body.String(), alice.Value) {
		t.Fatalf("userinfo after restart = %d %q", w.Code, w.Body.String())
	}
	d.Operators[0].Role = "operator"
	roleRaw, _ := json.Marshal(d)
	os.WriteFile(file, roleRaw, 0600)
	if w := privateRequest(mux, "GET", "/auth/status", "", "", alice); w.Code != 401 {
		t.Fatal("role change retained session")
	}
	d.Operators[0].Role = "reviewer"
	roleRaw, _ = json.Marshal(d)
	os.WriteFile(file, roleRaw, 0600)
	alice = privateSignIn(t, mux, "alice", strings.Repeat("a", 64))
	// Code rotation invalidates the session immediately, without process restart.
	d.Operators[0].CodeSHA256 = privateHash(strings.Repeat("c", 64))
	raw, _ := json.Marshal(d)
	os.WriteFile(file, raw, 0600)
	if w := privateRequest(mux, "GET", "/auth/status", "", "", alice); w.Code != 401 {
		t.Fatal("rotated credential retained session")
	}
	alice = privateSignIn(t, mux, "alice", strings.Repeat("c", 64))
	if w := privateRequest(mux, "POST", "/auth/logout", "", "http://localhost:8080", alice); w.Code != 303 {
		t.Fatal("logout failed")
	}
	if w := privateRequest(mux, "GET", "/auth/status", "", "", alice); w.Code != 401 {
		t.Fatal("logout retained session")
	}
	// Expiry uses the server clock even if the browser retains the cookie.
	a.now = func() time.Time { return time.Now().Add(9 * time.Hour) }
	if w := privateRequest(mux, "GET", "/auth/status", "", "", bob); w.Code != 401 {
		t.Fatal("expired session accepted")
	}
	a.now = time.Now
	d.Operators = d.Operators[:1]
	raw, _ = json.Marshal(d)
	os.WriteFile(file, raw, 0600)
	if w := privateRequest(mux, "GET", "/auth/status", "", "", bob); w.Code != 401 {
		t.Fatal("removed operator accepted")
	}
	os.WriteFile(file, []byte("invalid"), 0600)
	if w := privateRequest(mux, "GET", "/auth/status", "", "", bob); w.Code != 503 {
		t.Fatal("invalid directory did not fail closed")
	}
}

func TestPrivateSpaceMembershipIsSelfServiceAndOperationScoped(t *testing.T) {
	_, mux, _, _ := privateFixture(t)
	alice := privateSignIn(t, mux, "alice", strings.Repeat("a", 64))
	bob := privateSignIn(t, mux, "bob", strings.Repeat("b", 64))
	eve := privateSignIn(t, mux, "eve", strings.Repeat("e", 64))
	for _, tc := range []struct {
		name   string
		cookie *http.Cookie
		body   string
		status int
		want   string
	}{
		{"reviewer joins", alice, "op=join", http.StatusOK, "alice:join"},
		{"reviewer leaves", alice, "op=leave", http.StatusOK, "alice:leave"},
		{"operator joins", bob, "op=join", http.StatusOK, "bob:join"},
		{"viewer cannot join", eve, "op=join", http.StatusForbidden, ""},
		{"other operation remains denied", alice, "op=assign", http.StatusForbidden, ""},
		{"query cannot authorize body", alice, "", http.StatusForbidden, ""},
	} {
		path := "/app/demo/op"
		if tc.name == "query cannot authorize body" {
			path += "?op=join"
		}
		w := privateRequest(mux, http.MethodPost, path, tc.body, "http://localhost:8080", tc.cookie)
		if w.Code != tc.status || (tc.want != "" && w.Body.String() != tc.want) {
			t.Errorf("%s: status=%d body=%q, want status=%d body=%q", tc.name, w.Code, w.Body.String(), tc.status, tc.want)
		}
	}
	if privateSpaceMembershipActionAllowed("reviewer", http.MethodPost, "/app/demo/op/extra", "join") ||
		privateSpaceMembershipActionAllowed("reviewer", http.MethodPost, "/app//op", "join") {
		t.Fatal("malformed membership route allowed")
	}
}

func TestPrivateOriginAndRoleBoundaries(t *testing.T) {
	d := PrivateDirectory{Origins: []string{"http://localhost:8080"}}
	for _, origin := range []string{"null", "http://localhost:8080.evil.test", "http://localhost:8081", "http://localhost:8080/path", "http://evil.test"} {
		r := httptest.NewRequest("POST", "http://localhost:8080/console/workbench/intake", nil)
		r.Header.Set("Origin", origin)
		if privateOriginAllowed(r, d, true) {
			t.Fatalf("origin accepted %s", origin)
		}
	}
	r := httptest.NewRequest("POST", "http://localhost:8080/console/workbench/intake", nil)
	r.Header.Set("Referer", "http://localhost:8080/console/workbench")
	if !privateOriginAllowed(r, d, true) {
		t.Fatal("same-origin referer rejected")
	}
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	if privateOriginAllowed(r, d, true) {
		t.Fatal("cross-site accepted")
	}
	for _, tc := range []struct {
		name, target, origin string
		mutation, allowed    bool
	}{
		{"ephemeral loopback read", "http://localhost:52128/console/workbench", "", false, true},
		{"ephemeral loopback mutation", "http://localhost:52128/console/workbench/intake", "http://localhost:52128", true, true},
		{"ephemeral port mismatch", "http://localhost:52128/console/workbench/intake", "http://localhost:8080", true, false},
		{"loopback name mismatch", "http://127.0.0.1:52128/console/workbench", "", false, false},
		{"lookalike hostname", "http://localhost.evil.test:52128/console/workbench", "", false, false},
		{"origin path", "http://localhost:52128/console/workbench/intake", "http://localhost:52128/path", true, false},
	} {
		r := httptest.NewRequest("GET", tc.target, nil)
		if tc.mutation {
			r.Method = "POST"
			r.Header.Set("Origin", tc.origin)
		}
		if got := privateOriginAllowed(r, d, tc.mutation); got != tc.allowed {
			t.Errorf("%s: allowed=%t, want %t", tc.name, got, tc.allowed)
		}
	}
	remote := PrivateDirectory{Origins: []string{"https://control.example:8080"}}
	for _, tc := range []struct {
		target  string
		allowed bool
	}{
		{"https://control.example:8080/console/workbench", true},
		{"https://control.example:52128/console/workbench", false},
	} {
		r := httptest.NewRequest("GET", tc.target, nil)
		if got := privateOriginAllowed(r, remote, false); got != tc.allowed {
			t.Errorf("remote host %s: allowed=%t, want %t", tc.target, got, tc.allowed)
		}
	}
	for _, path := range []string{"/console/workbench/intake", "/console/workbench/work/id/run", "/console/workbench/work/id/human-owner", "/console/workbench/work/id/interventions/q/resolve"} {
		if !PrivateActionAllowed("operator", "POST", path) || PrivateActionAllowed("viewer", "POST", path) {
			t.Fatal(path)
		}
	}
	for _, path := range []string{"/console/workbench/work/id/confirm", "/console/workbench/work/id/result-review"} {
		if !PrivateActionAllowed("reviewer", "POST", path) || PrivateActionAllowed("operator", "POST", path) {
			t.Fatal(path)
		}
	}
	for _, role := range []string{"", "admin", "agent", "Reviewer"} {
		if PrivateActionAllowed(role, "GET", "/") {
			t.Fatal("unknown role allowed")
		}
	}
	if PrivateActionAllowed("reviewer", "POST", "/ops/hive/runtime/start") {
		t.Fatal("unlisted action allowed")
	}
	for _, role := range []string{"reviewer", "operator"} {
		for _, operation := range []string{"join", "leave"} {
			if !privateSpaceMembershipActionAllowed(role, http.MethodPost, "/app/demo/op", operation) {
				t.Fatalf("%s could not %s space", role, operation)
			}
		}
	}
	if privateSpaceMembershipActionAllowed("viewer", http.MethodPost, "/app/demo/op", "join") ||
		privateSpaceMembershipActionAllowed("reviewer", http.MethodPost, "/app/demo/op", "kick") {
		t.Fatal("space membership authorization escaped its boundary")
	}
}

func TestPrivateHiveSiteOpsMachineCredentialIsReadOnlyAndRouteScoped(t *testing.T) {
	a, _, _, _ := privateFixture(t)
	key := strings.Repeat("h", 64)
	if err := a.SetHiveSiteOpsAPIKey(key); err != nil {
		t.Fatal(err)
	}
	handler := a.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		u := UserFromContext(r.Context())
		if u == nil || !IsHiveSiteOpsMachine(r.Context()) {
			t.Fatal("machine identity missing")
		}
		_, _ = w.Write([]byte(u.ID + ":" + u.Kind))
	})
	request := func(method, path, authorization, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://localhost:8080"+path, nil)
		r.Header.Set("Authorization", authorization)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	if w := request(http.MethodGet, "/api/hive/site-ops?space=hive", "Bearer "+key, ""); w.Code != http.StatusOK || w.Body.String() != "hive-reconciliation:agent" {
		t.Fatalf("valid machine read: status=%d body=%q", w.Code, w.Body.String())
	}
	if w := request(http.MethodGet, "/api/hive/site-ops?space=hive", "Bearer "+strings.Repeat("x", 64), ""); w.Code != http.StatusSeeOther {
		t.Fatalf("wrong machine credential status=%d, want login redirect", w.Code)
	}
	if w := request(http.MethodGet, "/console/config", "Bearer "+key, ""); w.Code != http.StatusSeeOther {
		t.Fatalf("machine credential escaped route boundary: status=%d", w.Code)
	}
	if w := request(http.MethodPost, "/api/hive/site-ops", "Bearer "+key, "http://localhost:8080"); w.Code != http.StatusUnauthorized {
		t.Fatalf("machine credential gained write access: status=%d", w.Code)
	}
}

func TestPrivateHiveSiteOpsMachineCredentialRejectsWeakKey(t *testing.T) {
	a, _, _, _ := privateFixture(t)
	if err := a.SetHiveSiteOpsAPIKey("short"); err == nil {
		t.Fatal("weak Hive Site-ops API key accepted")
	}
}

func TestPrivateGateProtectsUnwrappedRoutes(t *testing.T) {
	a, _, _, _ := privateFixture(t)
	called := false
	gate := a.Gate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	for _, path := range []string{"/vision", "/api/graph", "/console/workbench/work/id/artifact"} {
		privateRequest(gate, "GET", path, "", "", nil)
		if called {
			t.Fatal("unwrapped data route exposed", path)
		}
	}
	w := privateRequest(gate, "GET", "/health", "", "", nil)
	if !called || w.Code != 200 {
		t.Fatal("health probe gated")
	}
}
