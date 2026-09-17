package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// PrivateOperator uses a random 256-bit access code, never a human password.
// Removing a record or changing its code/role revokes all existing sessions.
type PrivateOperator struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Role       string `json:"role"`
	CodeSHA256 string `json:"code_sha256"`
}

type PrivateDirectory struct {
	Origins   []string          `json:"origins"`
	Operators []PrivateOperator `json:"operators"`
}

type PrivateAccess struct {
	db                 *sql.DB
	file               string
	secure             bool
	now                func() time.Time
	hiveSiteOpsKeyHash [sha256.Size]byte
	hiveSiteOpsKeySet  bool
}

var operatorIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,63}$`)

type privateRosterKey struct{}
type hiveSiteOpsMachineKey struct{}

// PrivateOperators returns only public operator identity and role fields.
func PrivateOperators(ctx context.Context) []User {
	users, _ := ctx.Value(privateRosterKey{}).([]User)
	return append([]User(nil), users...)
}

// IsHiveSiteOpsMachine reports whether private-access middleware authenticated
// this request with the route-scoped Hive reconciliation credential.
func IsHiveSiteOpsMachine(ctx context.Context) bool {
	authorized, _ := ctx.Value(hiveSiteOpsMachineKey{}).(bool)
	return authorized
}

func privateHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (o PrivateOperator) fingerprint() string {
	return privateHash(o.ID + "\x00" + o.Role + "\x00" + o.CodeSHA256)
}

func loadPrivateDirectory(file string) (PrivateDirectory, error) {
	var d PrivateDirectory
	f, err := os.Open(file)
	if err != nil {
		return d, errors.New("private operator directory unavailable")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 65536 {
		return d, errors.New("private operator directory exceeds size limit")
	}
	decoder := json.NewDecoder(io.LimitReader(f, 65537))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&d); err != nil {
		return d, errors.New("invalid private operator directory")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return d, errors.New("invalid private operator directory trailing data")
	}
	if len(d.Origins) == 0 || len(d.Operators) == 0 {
		return d, errors.New("private origins and operators are required")
	}
	secure := strings.HasPrefix(d.Origins[0], "https://")
	for _, origin := range d.Origins {
		u, err := url.Parse(origin)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || (u.Scheme != "http" && u.Scheme != "https") || (u.Scheme == "https") != secure {
			return d, errors.New("private origins must be consistent absolute HTTP or HTTPS origins")
		}
		ip := net.ParseIP(u.Hostname())
		if u.Scheme == "http" && u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return d, errors.New("private HTTP requires a loopback origin")
		}
	}
	seen := map[string]bool{}
	for _, o := range d.Operators {
		digest, err := hex.DecodeString(o.CodeSHA256)
		if !operatorIDPattern.MatchString(o.ID) || o.ID == "anonymous" || strings.TrimSpace(o.Name) == "" || len(o.Name) > 100 || seen[o.ID] || err != nil || len(digest) != 32 || o.CodeSHA256 != strings.ToLower(o.CodeSHA256) || !privateRoleValid(o.Role) {
			return d, errors.New("invalid private operator record")
		}
		seen[o.ID] = true
	}
	return d, nil
}

func privateRoleValid(role string) bool {
	return role == "viewer" || role == "operator" || role == "reviewer"
}

// PrivateActionAllowed is intentionally an allowlist. Other application
// mutations are unavailable in private Workbench mode.
func PrivateActionAllowed(role, method, path string) bool {
	if !privateRoleValid(role) {
		return false
	}
	if method == http.MethodGet || method == http.MethodHead {
		return true
	}
	if method != http.MethodPost {
		return false
	}
	if path == "/auth/logout" {
		return true
	}
	if role == "viewer" {
		return false
	}
	if path == "/console/workbench/intake" {
		return true
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 5 || parts[0] != "console" || parts[1] != "workbench" || parts[2] != "work" || parts[3] == "" {
		return false
	}
	if len(parts) == 5 {
		switch parts[4] {
		case "run", "human-owner":
			return true
		case "confirm", "result-review":
			return role == "reviewer"
		}
	}
	return len(parts) == 7 && parts[4] == "interventions" && parts[5] != "" && parts[6] == "resolve"
}

func NewPrivateAccess(db *sql.DB, file string) (*PrivateAccess, error) {
	d, err := loadPrivateDirectory(file)
	if err != nil {
		return nil, err
	}
	a := &PrivateAccess{db: db, file: file, secure: strings.HasPrefix(d.Origins[0], "https://"), now: time.Now}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS private_operator_sessions (
 token_hash TEXT PRIMARY KEY, operator_id TEXT NOT NULL, credential_identity TEXT NOT NULL,
 expires_at TIMESTAMPTZ NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`); err != nil {
		return nil, err
	}
	return a, nil
}

// SetHiveSiteOpsAPIKey enables one machine-authenticated read boundary for
// Hive reconciliation. The raw key is hashed immediately and is never kept in
// process state. All other private routes continue to require an operator
// session.
func (a *PrivateAccess) SetHiveSiteOpsAPIKey(key string) error {
	key = strings.TrimSpace(key)
	if len(key) < 32 {
		return errors.New("Hive Site-ops API key must contain at least 32 characters")
	}
	a.hiveSiteOpsKeyHash = sha256.Sum256([]byte(key))
	a.hiveSiteOpsKeySet = true
	return nil
}

func (a *PrivateAccess) hiveSiteOpsMachine(r *http.Request) *User {
	if !a.hiveSiteOpsKeySet || r.Method != http.MethodGet || r.URL.Path != "/api/hive/site-ops" {
		return nil
	}
	authorization := r.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, "Bearer ") {
		return nil
	}
	key := strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))
	if len(key) < 32 {
		return nil
	}
	candidate := sha256.Sum256([]byte(key))
	if subtle.ConstantTimeCompare(candidate[:], a.hiveSiteOpsKeyHash[:]) != 1 {
		return nil
	}
	return &User{ID: "hive-reconciliation", Name: "Hive reconciliation", Role: "service", Kind: "agent"}
}

func (a *PrivateAccess) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /auth/login", a.loginPage)
	mux.HandleFunc("POST /auth/login", a.login)
	mux.Handle("POST /auth/logout", a.RequireAuth(a.logout))
	mux.Handle("GET /auth/status", a.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		u := UserFromContext(r.Context())
		_ = json.NewEncoder(w).Encode(map[string]string{"id": u.ID, "name": u.Name, "role": u.Role})
	}))
}

// Gate protects even DB-backed routes registered outside graph's wrappers.
// Static assets and the login form contain no operator or work evidence.
func (a *PrivateAccess) Gate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.URL.Path == "/auth/login" && (r.Method == "GET" || r.Method == "POST")) || ((r.Method == "GET" || r.Method == "HEAD") && (strings.HasPrefix(r.URL.Path, "/static/") || r.URL.Path == "/health")) {
			next.ServeHTTP(w, r)
			return
		}
		a.RequireAuth(next.ServeHTTP).ServeHTTP(w, r)
	})
}

func (a *PrivateAccess) directory() (PrivateDirectory, error) {
	d, err := loadPrivateDirectory(a.file)
	if err == nil && strings.HasPrefix(d.Origins[0], "https://") != a.secure {
		return d, errors.New("private origin scheme changed; restart required")
	}
	return d, err
}

func privateOriginAllowed(r *http.Request, d PrivateDirectory, mutation bool) bool {
	if len(d.Origins) == 0 {
		return false
	}
	configuredOrigin, err := url.Parse(d.Origins[0])
	if err != nil || (configuredOrigin.Scheme != "http" && configuredOrigin.Scheme != "https") {
		return false
	}
	requestHost, err := url.Parse(configuredOrigin.Scheme + "://" + r.Host)
	if err != nil || requestHost.Host == "" || requestHost.User != nil || requestHost.Path != "" || requestHost.RawQuery != "" || requestHost.Fragment != "" {
		return false
	}
	knownHost := false
	for _, origin := range d.Origins {
		u, _ := url.Parse(origin)
		if strings.EqualFold(r.Host, u.Host) {
			knownHost = true
			break
		}
		// Browser bridges and SSH clients can assign an ephemeral local port.
		// Permit that port only for an explicitly configured loopback hostname.
		if privateLoopbackHost(requestHost.Hostname()) && privateLoopbackHost(u.Hostname()) && strings.EqualFold(requestHost.Hostname(), u.Hostname()) {
			knownHost = true
			break
		}
	}
	if !knownHost {
		return false
	}
	if !mutation {
		return true
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	rawOrigin := r.Header.Get("Origin")
	fromReferer := rawOrigin == ""
	if fromReferer {
		rawOrigin = r.Header.Get("Referer")
	}
	u, err := url.Parse(rawOrigin)
	if err != nil || u.Host == "" || u.User != nil || u.Scheme != requestHost.Scheme || !strings.EqualFold(u.Host, r.Host) {
		return false
	}
	if !fromReferer && (u.Path != "" || u.RawQuery != "" || u.Fragment != "") {
		return false
	}
	return true
}

func privateLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func privateHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// no-referrer makes Chromium send an opaque (null) Origin for ordinary
	// form posts. Preserve same-origin evidence while suppressing it externally.
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("X-Frame-Options", "DENY")
}

func (a *PrivateAccess) RequireAuth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		privateHeaders(w)
		d, err := a.directory()
		if err != nil {
			http.Error(w, "Private access configuration unavailable.", 503)
			return
		}
		if !privateOriginAllowed(r, d, r.Method != http.MethodGet && r.Method != http.MethodHead) {
			http.Error(w, "Request origin is not allowed.", 403)
			return
		}
		if machine := a.hiveSiteOpsMachine(r); machine != nil {
			ctx := ContextWithUser(r.Context(), machine)
			ctx = context.WithValue(ctx, hiveSiteOpsMachineKey{}, true)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		cookie, err := r.Cookie("civilization_session")
		var id, fingerprint string
		var expires time.Time
		if err == nil && len(cookie.Value) == 64 {
			err = a.db.QueryRowContext(r.Context(), `SELECT operator_id, credential_identity, expires_at FROM private_operator_sessions WHERE token_hash=$1`, privateHash(cookie.Value)).Scan(&id, &fingerprint, &expires)
		} else {
			err = errors.New("no session")
		}
		var operator *PrivateOperator
		if err == nil && expires.After(a.now()) {
			for _, o := range d.Operators {
				if o.ID == id && o.fingerprint() == fingerprint {
					copy := o
					operator = &copy
					break
				}
			}
		}
		if operator == nil {
			a.clearCookie(w)
			if r.Header.Get("HX-Request") == "true" {
				w.Header().Set("HX-Redirect", "/auth/login")
				http.Error(w, "Sign in to continue.", 401)
			} else if r.Method == http.MethodGet && !strings.HasSuffix(r.URL.Path, "/artifact") && r.URL.Path != "/auth/status" {
				http.Redirect(w, r, "/auth/login", 303)
			} else {
				http.Error(w, "Sign in to continue.", 401)
			}
			return
		}
		if !PrivateActionAllowed(operator.Role, r.Method, r.URL.Path) {
			http.Error(w, "Your role cannot perform this action.", 403)
			return
		}
		u := &User{ID: operator.ID, Name: operator.Name, Role: operator.Role, Kind: "human"}
		roster := make([]User, 0, len(d.Operators))
		for _, o := range d.Operators {
			roster = append(roster, User{ID: o.ID, Name: o.Name, Role: o.Role, Kind: "human"})
		}
		ctx := context.WithValue(ContextWithUser(r.Context(), u), privateRosterKey{}, roster)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

var privateLoginTemplate = template.Must(template.New("login").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Sign in · Civilization</title><style>body{background:#101113;color:#eee;font:16px system-ui;margin:0;display:grid;min-height:100vh;place-items:center}main{width:min(24rem,85vw)}label{display:block;margin:1rem 0 .4rem}input,button{box-sizing:border-box;width:100%;padding:.8rem;border:1px solid #555;border-radius:.5rem;background:#202329;color:inherit;font:inherit}button{background:#64d8ec;color:#101113;margin-top:1.5rem;cursor:pointer}p{color:#bbb;line-height:1.6}.error{color:#ffa2a2}</style><main><h1>Civilization</h1><p>Sign in with your operator ID and private access code.</p>{{if .}}<p class="error" role="alert">{{.}}</p>{{end}}<form method="post" action="/auth/login"><label for="operator">Operator ID</label><input id="operator" name="operator" autocomplete="username" required maxlength="64"><label for="code">Access code</label><input id="code" name="code" type="password" autocomplete="current-password" required maxlength="64"><button>Sign in</button></form><p>Your administrator supplies your access code through your private access channel.</p></main></html>`))

func (a *PrivateAccess) loginPage(w http.ResponseWriter, r *http.Request) {
	privateHeaders(w)
	d, err := a.directory()
	if err != nil {
		http.Error(w, "Private access configuration unavailable.", 503)
		return
	}
	if !privateOriginAllowed(r, d, false) {
		http.Error(w, "Request host is not allowed.", 403)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = privateLoginTemplate.Execute(w, "")
}

func (a *PrivateAccess) login(w http.ResponseWriter, r *http.Request) {
	privateHeaders(w)
	d, err := a.directory()
	if err != nil {
		http.Error(w, "Private access configuration unavailable.", 503)
		return
	}
	if !privateOriginAllowed(r, d, true) {
		http.Error(w, "Request origin is not allowed.", 403)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if r.ParseForm() != nil {
		http.Error(w, "Invalid sign-in request.", 400)
		return
	}
	code := r.PostFormValue("code")
	decoded, err := hex.DecodeString(code)
	var operator *PrivateOperator
	if err == nil && len(decoded) == 32 {
		digest := privateHash(code)
		for _, o := range d.Operators {
			if subtle.ConstantTimeCompare([]byte(digest), []byte(o.CodeSHA256)) == 1 && o.ID == r.PostFormValue("operator") {
				copy := o
				operator = &copy
			}
		}
	}
	if operator == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(401)
		_ = privateLoginTemplate.Execute(w, "Operator ID or access code is incorrect.")
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		http.Error(w, "Sign-in unavailable.", 503)
		return
	}
	token := hex.EncodeToString(raw)
	expires := a.now().Add(8 * time.Hour)
	if _, err := a.db.ExecContext(r.Context(), `INSERT INTO private_operator_sessions(token_hash,operator_id,credential_identity,expires_at) VALUES($1,$2,$3,$4)`, privateHash(token), operator.ID, operator.fingerprint(), expires); err != nil {
		http.Error(w, "Sign-in unavailable.", 503)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "civilization_session", Value: token, Path: "/", HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteStrictMode, Expires: expires, MaxAge: 8 * 60 * 60})
	http.Redirect(w, r, "/console/workbench", 303)
}

func (a *PrivateAccess) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: "civilization_session", Path: "/", HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteStrictMode, MaxAge: -1})
}

func (a *PrivateAccess) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("civilization_session"); err == nil {
		if _, err := a.db.ExecContext(r.Context(), `DELETE FROM private_operator_sessions WHERE token_hash=$1`, privateHash(cookie.Value)); err != nil {
			http.Error(w, "Sign-out unavailable; try again.", 503)
			return
		}
	}
	a.clearCookie(w)
	http.Redirect(w, r, "/auth/login", 303)
}
