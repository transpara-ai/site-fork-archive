package graph

import (
	"net/http"
	"sort"
	"strings"
	"time"
)

// ConsoleConfig is the Config-tab view-model: the hive model-routing
// projection (catalog + per-role assignments + provenance) plus an explicit
// freshness state. It fails closed — a nil, failed, empty, or timestamp-less
// projection renders as unavailable with a human-readable notice and no
// invented routing data.
type ConsoleConfig struct {
	Freshness   ConsoleFreshness
	GeneratedAt string
	Selection   OpsHiveModelSelection
	Notices     []string
}

// buildConsoleConfig maps the hive operator projection (or a fetch error)
// into the Config view-model. Fail-closed by allowlist: only a model
// selection carrying actual data (source, models, or assignments) renders as
// data; an empty selection — even on a fresh projection — resolves to
// unavailable rather than a current-but-empty surface that could read as
// "hive routes nothing".
func buildConsoleConfig(proj *OpsHiveProjection, fetchErr error, now time.Time) ConsoleConfig {
	if fetchErr != nil || proj == nil {
		reason := "operator projection unavailable"
		if fetchErr != nil {
			reason = fetchErr.Error()
		}
		return ConsoleConfig{Freshness: FreshnessUnavailable, Notices: []string{reason}}
	}
	sel := proj.ModelSelection
	notices := append([]string(nil), proj.Errors...)
	notices = append(notices, sel.Errors...)
	if sel.Source == "" && len(sel.Models) == 0 && len(sel.Assignments) == 0 {
		return ConsoleConfig{
			Freshness: FreshnessUnavailable,
			Notices:   append(notices, "hive operator projection carries no model-selection data"),
		}
	}
	freshness := deriveFreshness(proj.GeneratedAt, nil, len(notices) > 0, now, consoleStaleWindow)
	if freshness == FreshnessUnavailable && len(notices) == 0 {
		notices = []string{"operator projection timestamp missing or unparseable"}
	}
	return ConsoleConfig{
		Freshness:   freshness,
		GeneratedAt: proj.GeneratedAt,
		Selection:   sel,
		Notices:     notices,
	}
}

// consoleConfigAssignmentModel prefers the resolved model, then the policy
// model, then an honest placeholder — never an empty cell that reads as data.
func consoleConfigAssignmentModel(item OpsHiveModelRoleAssignment) string {
	if m := strings.TrimSpace(item.Model); m != "" {
		return m
	}
	if m := strings.TrimSpace(item.PolicyModel); m != "" {
		return m
	}
	return "not projected"
}

func consoleConfigAssignmentProvider(item OpsHiveModelRoleAssignment) string {
	if p := strings.TrimSpace(item.Provider); p != "" {
		return p
	}
	if p := strings.TrimSpace(item.PolicyProvider); p != "" {
		return p
	}
	return "not projected"
}

// consoleConfigAssignmentMode renders "Mode · provenance" for a role card,
// reusing the /ops observatory derivation verbatim so /console and /ops can
// never disagree about why a role routes where it does. This is a render
// boundary: internal provenance sentinels (invalid projection) are mapped to
// operator-visible copy.
func consoleConfigAssignmentMode(sel OpsHiveModelSelection, item OpsHiveModelRoleAssignment) string {
	mode, provenance := obsAssignmentModelModeState(sel, item)
	return mode + " · " + obsModeProvenanceDisplay(provenance)
}

// consoleConfigGlobalMode is a render boundary (console.templ prints its
// return directly), so provenance routes through obsModeProvenanceDisplay for
// class-consistency. A no-op today — the global vocabulary cannot produce the
// invalid-projection sentinel — but the class stays closed.
func consoleConfigGlobalMode(sel OpsHiveModelSelection) string {
	mode, provenance, _ := obsHiveProjectionModelModeState(sel)
	return mode + " · " + obsModeProvenanceDisplay(provenance)
}

func consoleConfigCatalogLoadedAt(raw string) string {
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t.UTC().Format("2006-01-02 15:04:05") + " UTC"
	}
	return raw
}

func consoleConfigModelsByAuth(models []OpsHiveModelCatalogEntry, authMode string) []OpsHiveModelCatalogEntry {
	var filtered []OpsHiveModelCatalogEntry
	for _, model := range models {
		if strings.EqualFold(strings.TrimSpace(model.AuthMode), authMode) {
			filtered = append(filtered, model)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		leftProvider := strings.ToLower(strings.TrimSpace(filtered[i].Provider))
		rightProvider := strings.ToLower(strings.TrimSpace(filtered[j].Provider))
		if leftProvider != rightProvider {
			return leftProvider < rightProvider
		}
		return strings.ToLower(filtered[i].ID) < strings.ToLower(filtered[j].ID)
	})
	return filtered
}

func consoleConfigModelsWithOtherAuth(models []OpsHiveModelCatalogEntry) []OpsHiveModelCatalogEntry {
	var filtered []OpsHiveModelCatalogEntry
	for _, model := range models {
		authMode := strings.ToLower(strings.TrimSpace(model.AuthMode))
		if authMode != "subscription" && authMode != "api-key" {
			filtered = append(filtered, model)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		return strings.ToLower(filtered[i].ID) < strings.ToLower(filtered[j].ID)
	})
	return filtered
}

func (h *Handlers) handleConsoleConfig(w http.ResponseWriter, r *http.Request) {
	proj, err := fetchHiveOperatorProjection(r)
	cfg := buildConsoleConfig(proj, err, time.Now().UTC())
	h.renderConsole(w, r, ConsolePageData{Title: "Config", Active: "config", Config: &cfg})
}

func (h *Handlers) handleConsoleConfigFragment(w http.ResponseWriter, r *http.Request) {
	proj, err := fetchHiveOperatorProjection(r)
	cfg := buildConsoleConfig(proj, err, time.Now().UTC())
	consoleConfigFragment(cfg).Render(r.Context(), w)
}
