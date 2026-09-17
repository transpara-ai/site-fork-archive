package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// testConfigModelSelection returns a populated model-selection projection:
// two catalog models and two role assignments with distinct, deterministic
// provenance paths (policy-event override vs plain-model inferred manual).
func testConfigModelSelection() OpsHiveModelSelection {
	return OpsHiveModelSelection{
		Source:        "hive-operator-projection",
		CatalogSource: "catalog-mixed.yaml",
		Models: []OpsHiveModelCatalogEntry{
			{ID: "claude-opus-4-6", Provider: "claude-cli", AuthMode: "subscription", Tier: "judgment"},
			{ID: "gpt-5.5", Provider: "codex-cli", AuthMode: "subscription", Tier: "execution"},
		},
		Assignments: []OpsHiveModelRoleAssignment{
			// Policy-event assignment → obsAssignmentModelModeState = ("Manual", "override").
			{Role: "strategist", Model: "claude-opus-4-6", Provider: "claude-cli", Source: "hive-model-policy-event", PolicyEventID: "evt_123"},
			// Plain resolved model, no mode metadata → ("Manual", "inferred").
			{Role: "implementer", Model: "gpt-5.5", Provider: "codex-cli"},
		},
	}
}

func TestBuildConsoleConfig(t *testing.T) {
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)

	t.Run("fetch error is unavailable with notice and no invented routing", func(t *testing.T) {
		cfg := buildConsoleConfig(nil, errors.New("HIVE_OPS_API_BASE_URL is not configured"), now)
		if cfg.Freshness != FreshnessUnavailable {
			t.Fatalf("freshness = %q, want unavailable", cfg.Freshness)
		}
		if len(cfg.Notices) == 0 {
			t.Fatal("expected a notice explaining the unavailable state")
		}
		if len(cfg.Selection.Models) != 0 || len(cfg.Selection.Assignments) != 0 {
			t.Fatal("unavailable config must not invent catalog or assignments")
		}
	})

	t.Run("nil projection is unavailable", func(t *testing.T) {
		cfg := buildConsoleConfig(nil, nil, now)
		if cfg.Freshness != FreshnessUnavailable || len(cfg.Notices) == 0 {
			t.Fatalf("nil projection: freshness = %q, notices = %v; want unavailable with notice", cfg.Freshness, cfg.Notices)
		}
	})

	t.Run("fresh projection with EMPTY model selection fails closed", func(t *testing.T) {
		// A fresh timestamp with no model-selection data must NOT render as a
		// current-but-empty surface; only a selection carrying data earns rendering.
		proj := &OpsHiveProjection{GeneratedAt: now.Add(-2 * time.Second).Format(time.RFC3339)}
		cfg := buildConsoleConfig(proj, nil, now)
		if cfg.Freshness != FreshnessUnavailable {
			t.Fatalf("freshness = %q, want unavailable for empty selection", cfg.Freshness)
		}
		if len(cfg.Notices) == 0 || !strings.Contains(strings.Join(cfg.Notices, "; "), "no model-selection") {
			t.Fatalf("empty selection must carry an explicit notice, got %v", cfg.Notices)
		}
	})

	t.Run("populated fresh projection is current and passes selection through", func(t *testing.T) {
		proj := &OpsHiveProjection{
			GeneratedAt:    now.Add(-2 * time.Second).Format(time.RFC3339),
			ModelSelection: testConfigModelSelection(),
		}
		cfg := buildConsoleConfig(proj, nil, now)
		if cfg.Freshness != FreshnessCurrent {
			t.Fatalf("freshness = %q, want current", cfg.Freshness)
		}
		if len(cfg.Selection.Models) != 2 || len(cfg.Selection.Assignments) != 2 {
			t.Fatalf("selection not passed through: %d models, %d assignments", len(cfg.Selection.Models), len(cfg.Selection.Assignments))
		}
	})

	t.Run("stale timestamp is stale", func(t *testing.T) {
		proj := &OpsHiveProjection{
			GeneratedAt:    now.Add(-2 * time.Minute).Format(time.RFC3339), // older than consoleStaleWindow (30s)
			ModelSelection: testConfigModelSelection(),
		}
		if cfg := buildConsoleConfig(proj, nil, now); cfg.Freshness != FreshnessStale {
			t.Fatalf("freshness = %q, want stale", cfg.Freshness)
		}
	})

	t.Run("projection errors downgrade fresh data to partial", func(t *testing.T) {
		proj := &OpsHiveProjection{
			GeneratedAt:    now.Add(-2 * time.Second).Format(time.RFC3339),
			ModelSelection: testConfigModelSelection(),
			Errors:         []string{"telemetry source degraded"},
		}
		cfg := buildConsoleConfig(proj, nil, now)
		if cfg.Freshness != FreshnessPartial {
			t.Fatalf("freshness = %q, want partial", cfg.Freshness)
		}
		if len(cfg.Notices) == 0 || cfg.Notices[0] != "telemetry source degraded" {
			t.Fatalf("projection errors must surface as notices, got %v", cfg.Notices)
		}
	})

	t.Run("model-selection errors also downgrade to partial", func(t *testing.T) {
		sel := testConfigModelSelection()
		sel.Errors = []string{"catalog reload failed"}
		proj := &OpsHiveProjection{
			GeneratedAt:    now.Add(-2 * time.Second).Format(time.RFC3339),
			ModelSelection: sel,
		}
		cfg := buildConsoleConfig(proj, nil, now)
		if cfg.Freshness != FreshnessPartial {
			t.Fatalf("freshness = %q, want partial for selection errors", cfg.Freshness)
		}
	})

	t.Run("populated selection with missing timestamp is unavailable with notice", func(t *testing.T) {
		proj := &OpsHiveProjection{ModelSelection: testConfigModelSelection()} // GeneratedAt empty
		cfg := buildConsoleConfig(proj, nil, now)
		if cfg.Freshness != FreshnessUnavailable {
			t.Fatalf("freshness = %q, want unavailable for missing timestamp", cfg.Freshness)
		}
		if len(cfg.Notices) == 0 {
			t.Fatal("timestamp-less unavailable state must carry an explicit notice")
		}
	})
}

// TestConsoleConfigAssignmentModeProjectedVocabulary guards the /console
// render boundary for the hive-projected selection_mode: valid vocabulary
// renders "Mode · explicit"; a present-but-invalid value fails closed to
// "unknown · not projected" — the internal sentinel never reaches copy.
func TestConsoleConfigAssignmentModeProjectedVocabulary(t *testing.T) {
	sel := testConfigModelSelection()
	item := OpsHiveModelRoleAssignment{
		Role:          "strategist",
		Model:         "claude-opus-4-6",
		Source:        "hive-model-policy-event",
		PolicyEventID: "evt_123",
		SelectionMode: "resolver-mode-v2", // present-but-invalid
	}
	if got := consoleConfigAssignmentMode(sel, item); got != "unknown · not projected" {
		t.Fatalf("invalid projected mode = %q, want %q", got, "unknown · not projected")
	}
	item.SelectionMode = "manual-explicit"
	if got := consoleConfigAssignmentMode(sel, item); got != "Manual · explicit" {
		t.Fatalf("manual-explicit = %q, want %q", got, "Manual · explicit")
	}
	item.SelectionMode = "system-default"
	if got := consoleConfigAssignmentMode(sel, item); got != "Auto · explicit" {
		t.Fatalf("system-default = %q, want %q", got, "Auto · explicit")
	}
}

// newConfigHiveServer serves the operator projection with a populated model
// selection, mirroring the health-wall live-upstream test pattern.
func newConfigHiveServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/hive/operator-projection" {
			http.NotFound(w, r)
			return
		}
		proj := OpsHiveProjection{
			GeneratedAt:    time.Now().UTC().Format(time.RFC3339),
			ModelSelection: testConfigModelSelection(),
		}
		if err := json.NewEncoder(w).Encode(proj); err != nil {
			t.Errorf("encode projection: %v", err)
		}
	}))
}

func TestConsoleConfigRendersModelRouting(t *testing.T) {
	srv := newConfigHiveServer(t)
	defer srv.Close()
	t.Setenv("HIVE_OPS_API_BASE_URL", srv.URL)

	h := newConsoleTestHandlers()
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "http://site.test/console/config", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	// Catalog entries, role assignments, provenance, and catalog source all render.
	for _, want := range []string{
		"claude-opus-4-6", "gpt-5.5", // catalog + assignment models
		"strategist", "implementer", // roles
		"Manual · override",  // policy-event provenance (strategist)
		"Manual · inferred",  // plain-model provenance (implementer)
		"catalog-mixed.yaml", // catalog source
	} {
		if !strings.Contains(body, want) {
			t.Errorf("config surface missing %q", want)
		}
	}
}

func TestConsoleConfigTabEnabled(t *testing.T) {
	t.Setenv("HIVE_OPS_API_BASE_URL", "")
	h := newConsoleTestHandlers()
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "http://site.test/console/config", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	body := w.Body.String()
	// The enabled Config tab is an anchor to /console/config, not a disabled span.
	if !strings.Contains(body, `href="/console/config"`) {
		t.Error("Config tab must be enabled (anchor to /console/config)")
	}
	if strings.Contains(body, `title="coming soon"`) {
		t.Error("no console tab may remain in the disabled coming-soon state")
	}
}

func TestConsoleConfigUnavailableWhenProjectionAbsent(t *testing.T) {
	t.Setenv("HIVE_OPS_API_BASE_URL", "") // no upstream configured -> fetch error

	h := newConsoleTestHandlers()
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "http://site.test/console/config", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "unavailable") {
		t.Error("absent projection must render an explicit unavailable state")
	}
	// No fabricated routing data may appear.
	if strings.Contains(body, "claude-opus-4-6") || strings.Contains(body, "strategist") {
		t.Error("unavailable config must not fabricate routing rows")
	}
}

func TestConsoleConfigFragmentIsShellFree(t *testing.T) {
	t.Setenv("HIVE_OPS_API_BASE_URL", "")
	h := newConsoleTestHandlers()
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "http://site.test/console/config/fragment", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "<html") {
		t.Fatal("fragment must not include the full page shell")
	}
	if !strings.Contains(body, "unavailable") {
		t.Fatal("fragment must render honest staleness")
	}
}

func TestConsoleConfigRoutesInReadOnlyRegistrar(t *testing.T) {
	// Both registrars must serve the config routes: Register (full site) and
	// RegisterReadOnlyConsole (no-DB console deployment).
	t.Setenv("HIVE_OPS_API_BASE_URL", "")
	h := NewHandlers(nil, nil, nil)
	mux := http.NewServeMux()
	h.RegisterReadOnlyConsole(mux)

	for _, path := range []string{"/console/config", "/console/config/fragment"} {
		req := httptest.NewRequest(http.MethodGet, "http://site.test"+path, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200 in read-only registrar", path, w.Code)
		}
		if !strings.Contains(w.Body.String(), "unavailable") {
			t.Errorf("%s: no-DB console must render explicit unavailable state", path)
		}
	}
}

func TestConsoleConfigRendersNoWriteControls(t *testing.T) {
	// READ-ONLY BOUNDARY: the config surface must carry zero write affordances,
	// even fully populated. The governed write (POST /ops/hive/model-policy)
	// lives on /ops and must not leak here in any form.
	srv := newConfigHiveServer(t)
	defer srv.Close()
	t.Setenv("HIVE_OPS_API_BASE_URL", srv.URL)

	h := newConsoleTestHandlers()
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "http://site.test/console/config", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	body := w.Body.String()
	for _, forbidden := range []string{
		"<form", "hx-post", "hx-put", "hx-delete", "hx-patch",
		"<input", "<select", "<textarea", "<button",
		"/ops/hive/model-policy",
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("read-only config surface must not render %q", forbidden)
		}
	}
}

func TestConsoleConfigWriteRouteNotRegistered(t *testing.T) {
	// Belt and braces: no POST route may answer under /console/config in either
	// registrar. ServeMux returns 405 for a method-mismatched pattern — the
	// requirement is only that a POST can never succeed.
	t.Setenv("HIVE_OPS_API_BASE_URL", "")
	for name, register := range map[string]func(h *Handlers, mux *http.ServeMux){
		"Register":                func(h *Handlers, mux *http.ServeMux) { h.Register(mux) },
		"RegisterReadOnlyConsole": func(h *Handlers, mux *http.ServeMux) { h.RegisterReadOnlyConsole(mux) },
	} {
		h := NewHandlers(nil, nil, nil)
		if name == "Register" {
			h = newConsoleTestHandlers()
		}
		mux := http.NewServeMux()
		register(h, mux)
		req := httptest.NewRequest(http.MethodPost, "http://site.test/console/config", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code == http.StatusOK {
			t.Errorf("%s: POST /console/config answered 200; write path must not exist", name)
		}
	}
}

func TestConsoleConfigSurfaceEscapesHostileProjectionData(t *testing.T) {
	// templ's default { } interpolation HTML-escapes everything and this surface
	// uses no templ.Raw/SafeHTML — hostile operator-visible projection strings
	// must come out escaped. Mirrors the intake surface guard.
	hostile := OpsHiveModelSelection{
		Source:        "hive-operator-projection",
		CatalogSource: `<script>catalog()</script>`,
		Models: []OpsHiveModelCatalogEntry{
			{ID: `<script>alert('model')</script>`, Provider: `<img src=x onerror=y>prov`, AuthMode: "subscription", Tier: "judgment"},
		},
		Assignments: []OpsHiveModelRoleAssignment{
			{Role: `<button onclick="x">role</button>`, Model: "m", Error: `<form action="/hive">err</form>`},
		},
		Errors: []string{`<script>selerr()</script>`},
	}
	cfg := buildConsoleConfig(&OpsHiveProjection{
		GeneratedAt:    time.Now().UTC().Format(time.RFC3339),
		ModelSelection: hostile,
	}, nil, time.Now().UTC())

	var buf bytes.Buffer
	if err := consoleConfig(cfg).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()

	for _, raw := range []string{
		`<script>alert('model')</script>`,
		`<script>catalog()</script>`,
		`<script>selerr()</script>`,
		`<button onclick="x">role</button>`,
		`<form action="/hive">err</form>`,
		"<img src=x onerror=y>prov",
	} {
		if strings.Contains(out, raw) {
			t.Errorf("hostile raw markup %q survived escaping in the config surface", raw)
		}
	}
	if !strings.Contains(out, "&lt;script") {
		t.Error("expected escaped form \"&lt;script\" in output; escaping did not occur (data may have vanished instead of being escaped)")
	}
}

func TestConsoleConfigSourceOnlySelectionRendersHonestEmptyStates(t *testing.T) {
	// A selection with Source set but no models/assignments is usable (the
	// allowlist admits it) — the surface must render explicit empty states,
	// not blank tables and not fabricated rows.
	cfg := buildConsoleConfig(&OpsHiveProjection{
		GeneratedAt:    time.Now().UTC().Format(time.RFC3339),
		ModelSelection: OpsHiveModelSelection{Source: "hive-operator-projection"},
	}, nil, time.Now().UTC())
	if cfg.Freshness != FreshnessCurrent {
		t.Fatalf("freshness = %q, want current for source-only selection", cfg.Freshness)
	}
	var buf bytes.Buffer
	if err := consoleConfig(cfg).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "No role assignments projected.") {
		t.Error("empty assignments must render the explicit empty state")
	}
	if !strings.Contains(out, "No catalog models projected.") {
		t.Error("empty catalog must render the explicit empty state")
	}
}

func TestConsoleConfigGroupsModelsByAccessAndExplainsTiers(t *testing.T) {
	cfg := buildConsoleConfig(&OpsHiveProjection{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		ModelSelection: OpsHiveModelSelection{
			Source:   "hive-operator-projection",
			LoadedAt: "2026-09-17T11:22:12-07:00",
			Models: []OpsHiveModelCatalogEntry{
				{ID: "api-model", Provider: "anthropic", AuthMode: "api-key", Tier: "judgment", Metadata: map[string]string{"verified_at": "2026-09-08"}},
				{ID: "subscription-model", Provider: "codex-cli", AuthMode: "subscription", Tier: "execution", Metadata: map[string]string{"verified_at": "2026-09-08", "subscription_verified_at": "2026-09-17"}},
				{ID: "local-model", Provider: "ollama", AuthMode: "local", Tier: "volume"},
			},
		},
	}, nil, time.Now().UTC())

	var buf bytes.Buffer
	if err := consoleConfig(cfg).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()
	subscription := strings.Index(out, `data-model-access="subscription"`)
	apiKey := strings.Index(out, `data-model-access="api-key"`)
	other := strings.Index(out, `data-model-access="other"`)
	if subscription < 0 || apiKey < 0 || other < 0 || !(subscription < apiKey && apiKey < other) {
		t.Fatalf("access groups missing or out of order: subscription=%d api=%d other=%d", subscription, apiKey, other)
	}
	for _, id := range []string{"subscription-model", "api-model", "local-model"} {
		if count := strings.Count(out, id); count != 1 {
			t.Errorf("model %q rendered %d times, want once", id, count)
		}
	}
	for _, authMode := range []string{"subscription", "api-key", "local"} {
		if !strings.Contains(out, `data-model-auth="`+authMode+`"`) {
			t.Errorf("missing exact auth mode %q", authMode)
		}
	}
	for _, text := range []string{"Catalog loaded 2026-09-17 18:22:12 UTC", "Subscription verified 2026-09-17", "live access checks", "ambiguous, high-impact", "implementation and tool use", "routine, high-throughput"} {
		if !strings.Contains(out, text) {
			t.Errorf("missing explanatory copy %q", text)
		}
	}
	if strings.Contains(out, "Catalog checked") {
		t.Error("per-model metadata review must not be presented as a catalog-wide check")
	}
	if strings.Contains(out, "verified 2026-09-08") {
		t.Error("catalog metadata must not be presented as runtime access verification")
	}
}

func TestConsoleConfigEmptyStatesCarryContext(t *testing.T) {
	// A selection with Source set but no models/assignments is usable — the
	// empty states must carry enough context to explain WHY they're empty,
	// not just state the fact.
	cfg := buildConsoleConfig(&OpsHiveProjection{
		GeneratedAt:    time.Now().UTC().Format(time.RFC3339),
		ModelSelection: OpsHiveModelSelection{Source: "hive-operator-projection"},
	}, nil, time.Now().UTC())
	if cfg.Freshness != FreshnessCurrent {
		t.Fatalf("freshness = %q, want current for source-only selection", cfg.Freshness)
	}
	var buf bytes.Buffer
	if err := consoleConfig(cfg).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"No role assignments projected.",
		"No catalog models projected.",
		"Role routing appears once",
		"hot-reloaded",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("config empty state missing %q; body: %s", want, out)
		}
	}
}
