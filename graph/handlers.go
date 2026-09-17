package graph

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/transpara-ai/site/auth"
	"github.com/transpara-ai/site/profile"
)

// anonUserID is the sentinel value returned by userID() when no session exists.
const anonUserID = "anonymous"

// ViewUser holds user info for templates.
type ViewUser struct {
	ID          string
	Role        string
	Name        string
	Picture     string
	UnreadCount int
}

// ViewAPIKey holds API key info for templates.
type ViewAPIKey struct {
	ID        string
	Name      string
	AgentName string
	CreatedAt string
}

// LoopState holds the current hive loop iteration state read from loop files on disk.
type LoopState struct {
	Iteration  int
	Phase      string
	BuildTitle string
	BuildCost  float64 // parsed from build.md
}

// readLoopState collects LoopState for the authed hive views from the
// hive repo's loop/ directory. Iteration and Phase come from
// loop/diagnostics.jsonl (newline count + last entry's phase); build
// title and cost still come from loop/build.md. state.md is no longer
// read — the Reflector's narrative format ("Last updated: Iteration N")
// doesn't match the line-prefix parser the site used, which was
// silently zeroing the display on a working hive.
//
// Iteration count matches the hive's own canonical counter
// (pkg/runner/pipeline_tree.go: countDiagnostics — newline bytes). Guard
// for the edge case where the file's final line isn't newline-terminated
// (crash mid-write, or a future batching change in the hive's writer):
// if data is non-empty and doesn't end in \n, count that trailing line.
// Without the guard the dashboard would silently under-report by one.
//
// Returns zero value if dir is empty or files are missing. This is the
// local-dev path; production supplements with DB-backed counts in the
// handler, so a zero here on Fly is expected and fine.
func readLoopState(dir string) LoopState {
	if dir == "" {
		return LoopState{}
	}
	var s LoopState
	if data, err := os.ReadFile(filepath.Join(dir, "diagnostics.jsonl")); err == nil {
		for _, b := range data {
			if b == '\n' {
				s.Iteration++
			}
		}
		if len(data) > 0 && data[len(data)-1] != '\n' {
			s.Iteration++
		}
		lines := strings.Split(string(data), "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			line := strings.TrimSpace(lines[i])
			if line == "" {
				continue
			}
			var entry struct {
				Phase string `json:"phase"`
			}
			if err := json.Unmarshal([]byte(line), &entry); err == nil {
				s.Phase = entry.Phase
				break
			}
		}
	}
	if data, err := os.ReadFile(filepath.Join(dir, "build.md")); err == nil {
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		for scanner.Scan() {
			line := scanner.Text()
			if after, ok := strings.CutPrefix(line, "# "); ok && s.BuildTitle == "" {
				s.BuildTitle = strings.TrimSpace(after)
			}
		}
		s.BuildCost = parseCostDollars(string(data))
	}
	return s
}

// DiagEntry is a phase diagnostic event read from loop/diagnostics.jsonl.
type DiagEntry struct {
	Phase     string
	Outcome   string
	CostUSD   float64
	Timestamp time.Time
}

// readDiagnostics reads the last limit entries from loop/diagnostics.jsonl, newest first.
// Returns nil if dir is empty or the file does not exist.
func readDiagnostics(dir string, limit int) []DiagEntry {
	if dir == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(dir, "diagnostics.jsonl"))
	if err != nil {
		return nil
	}
	type raw struct {
		Phase     string  `json:"phase"`
		Outcome   string  `json:"outcome"`
		CostUSD   float64 `json:"cost_usd"`
		Timestamp string  `json:"timestamp"`
	}
	var all []DiagEntry
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		var r raw
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue
		}
		e := DiagEntry{Phase: r.Phase, Outcome: r.Outcome, CostUSD: r.CostUSD}
		if r.Timestamp != "" {
			if t, err := time.Parse(time.RFC3339, r.Timestamp); err == nil {
				e.Timestamp = t
			}
		}
		all = append(all, e)
	}
	start := 0
	if len(all) > limit {
		start = len(all) - limit
	}
	tail := make([]DiagEntry, len(all)-start)
	copy(tail, all[start:])
	// Reverse so newest entry is first.
	for i, j := 0, len(tail)-1; i < j; i, j = i+1, j-1 {
		tail[i], tail[j] = tail[j], tail[i]
	}
	return tail
}

// RecentCommit holds a short git commit hash and subject line.
type RecentCommit struct {
	Hash    string
	Subject string
}

// readRecentCommits runs git log in repoDir and returns up to limit commits.
// Returns nil if repoDir is empty or git is unavailable.
func readRecentCommits(repoDir string, limit int) []RecentCommit {
	if repoDir == "" {
		return nil
	}
	out, err := exec.Command("git", "-C", repoDir, "log", "--oneline", fmt.Sprintf("-%d", limit)).Output()
	if err != nil {
		return nil
	}
	var commits []RecentCommit
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) == 2 {
			commits = append(commits, RecentCommit{Hash: parts[0], Subject: parts[1]})
		}
	}
	return commits
}

// hivePhaseClass returns Tailwind badge classes for a pipeline phase name.
func hivePhaseClass(phase string) string {
	switch strings.ToLower(phase) {
	case "scout":
		return "bg-amber-400/10 text-amber-300 border-amber-400/20"
	case "architect":
		return "bg-indigo-400/10 text-indigo-300 border-indigo-400/20"
	case "builder":
		return "bg-emerald-400/10 text-emerald-300 border-emerald-400/20"
	case "critic":
		return "bg-orange-400/10 text-orange-300 border-orange-400/20"
	case "reflector":
		return "bg-violet-400/10 text-violet-300 border-violet-400/20"
	default:
		return "bg-elevated text-warm-muted border-edge"
	}
}

// diagOutcomeIcon returns a symbol representing a diagnostic outcome.
func diagOutcomeIcon(outcome string) string {
	switch outcome {
	case "revise", "revise_blocked":
		return "↻"
	case "failure", "error":
		return "✗"
	case "empty_sections":
		return "○"
	case "":
		return "✓"
	default:
		return "·"
	}
}

// diagOutcomeColor returns a Tailwind text-color class for a diagnostic outcome.
func diagOutcomeColor(outcome string) string {
	switch outcome {
	case "revise", "revise_blocked":
		return "text-amber-400"
	case "failure", "error":
		return "text-red-400"
	case "empty_sections":
		return "text-warm-faint"
	case "":
		return "text-emerald-400"
	default:
		return "text-warm-muted"
	}
}

// maxHiveDiagEntries is the upper bound on diagnostic entries shown on the /hive page.
const maxHiveDiagEntries = 10

// Handlers serves the unified product HTTP endpoints.
type Handlers struct {
	store     *Store
	mind      *Mind                               // optional — triggers auto-reply on conversation messages
	readWrap  func(http.HandlerFunc) http.Handler // optional auth (reads)
	writeWrap func(http.HandlerFunc) http.Handler // required auth (writes)
	loopDir   string                              // optional — path to loop/ dir for reading iteration state
}

// SetLoopDir configures the directory from which loop state files are read.
func (h *Handlers) SetLoopDir(dir string) { h.loopDir = dir }

// NewHandlers creates handlers with auth middleware.
// readWrap allows anonymous access (for public spaces), writeWrap requires auth.
func NewHandlers(store *Store, readWrap, writeWrap func(http.HandlerFunc) http.Handler) *Handlers {
	noop := func(hf http.HandlerFunc) http.Handler { return hf }
	if readWrap == nil {
		readWrap = noop
	}
	if writeWrap == nil {
		writeWrap = noop
	}
	return &Handlers{store: store, readWrap: readWrap, writeWrap: writeWrap}
}

// SetMind enables auto-reply on conversation messages.
func (h *Handlers) SetMind(m *Mind) { h.mind = m }

// Register adds all /app routes to the mux.
func (h *Handlers) Register(mux *http.ServeMux) {
	// Space management (requires auth).
	mux.Handle("GET /app", h.writeWrap(h.handleSpaceIndex))
	mux.Handle("GET /app/notifications", h.writeWrap(h.handleNotifications))
	mux.Handle("POST /app/new", h.writeWrap(h.handleCreateSpace))

	// Space settings (requires auth, owner only).
	mux.Handle("GET /app/{slug}/settings", h.writeWrap(h.handleSpaceSettings))
	mux.Handle("POST /app/{slug}/settings", h.writeWrap(h.handleUpdateSpace))
	mux.Handle("POST /app/{slug}/delete", h.writeWrap(h.handleDeleteSpace))

	// Space invites.
	mux.Handle("POST /app/{slug}/invites", h.writeWrap(h.handleCreateInviteHTMX))
	mux.Handle("DELETE /app/{slug}/invites/{id}", h.writeWrap(h.handleRevokeInvite))
	mux.Handle("GET /join/{code}", h.readWrap(h.handleJoinViaInvite))

	// Space lenses (optional auth — public spaces readable by anyone).
	mux.Handle("GET /app/{slug}", h.readWrap(h.handleSpaceDefault))
	mux.Handle("GET /app/{slug}/board", h.readWrap(h.handleBoard))
	mux.Handle("GET /app/{slug}/refinery", h.readWrap(h.handleRefinery))
	mux.Handle("POST /app/{slug}/refinery/intake", h.writeWrap(h.handleRefineryIntake))
	mux.Handle("POST /app/{slug}/checklist/dismiss", h.writeWrap(h.handleChecklistDismiss))
	mux.Handle("GET /app/{slug}/feed", h.readWrap(h.handleFeed))
	mux.Handle("GET /app/{slug}/threads", h.readWrap(h.handleThreads))
	mux.Handle("GET /app/{slug}/conversations", h.readWrap(h.handleConversations))
	mux.Handle("GET /app/{slug}/people", h.readWrap(h.handlePeople))
	mux.Handle("GET /app/{slug}/agents", h.readWrap(h.handleAgents))
	mux.Handle("PATCH /app/{slug}/agents/{name}/session", h.writeWrap(h.handleAgentSessionUpdate))
	mux.Handle("POST /app/{slug}/agents/{name}/chat", h.writeWrap(h.handleAgentChat))
	mux.Handle("GET /app/{slug}/activity", h.readWrap(h.handleActivity))
	mux.Handle("GET /app/{slug}/knowledge", h.readWrap(h.handleKnowledge))
	mux.Handle("GET /app/{slug}/governance", h.readWrap(h.handleGovernance))
	mux.Handle("GET /app/{slug}/changelog", h.readWrap(h.handleChangelog))
	mux.Handle("GET /app/{slug}/projects", h.readWrap(h.handleProjects))
	mux.Handle("GET /app/{slug}/goals", h.readWrap(h.handleGoals))
	mux.Handle("GET /app/{slug}/goals/{id}", h.readWrap(h.handleGoalDetail))
	mux.Handle("GET /app/{slug}/roles", h.readWrap(h.handleRoles))
	mux.Handle("GET /app/{slug}/teams", h.readWrap(h.handleTeams))
	mux.Handle("GET /app/{slug}/policies", h.readWrap(h.handlePolicies))
	mux.Handle("GET /app/{slug}/documents", h.readWrap(h.handleDocuments))
	mux.Handle("GET /app/{slug}/document/{id}/edit", h.writeWrap(h.handleDocumentEdit))
	mux.Handle("POST /app/{slug}/document/{id}/edit", h.writeWrap(h.handleDocumentEdit))
	mux.Handle("GET /app/{slug}/questions", h.readWrap(h.handleQuestions))
	mux.Handle("GET /app/{slug}/questions/{id}", h.readWrap(h.handleQuestionDetail))
	mux.Handle("GET /app/{slug}/council", h.readWrap(h.handleCouncil))
	mux.Handle("GET /app/{slug}/council/{id}", h.readWrap(h.handleCouncilDetail))

	// Conversation detail (optional auth).
	mux.Handle("GET /app/{slug}/conversation/{id}", h.readWrap(h.handleConversationDetail))
	mux.Handle("GET /app/{slug}/conversation/{id}/messages", h.readWrap(h.handleConversationMessages))

	// Node detail (optional auth — public spaces readable by anyone).
	mux.Handle("GET /app/{slug}/node/{id}", h.readWrap(h.handleNodeDetail))

	// Grammar operations (requires auth).
	mux.Handle("POST /app/{slug}/op", h.writeWrap(h.handleOp))

	// Mind state (requires auth — used by cmd/post to sync loop state).
	mux.Handle("PUT /api/mind-state", h.writeWrap(h.handleSetMindState))

	// Refinery projection (optional auth — public spaces readable by anyone).
	mux.Handle("GET /api/refinery/{slug}/projection", h.readWrap(h.handleRefineryProjection))

	// Hive diagnostics and reconciliation ingestion (requires auth — used by hive runner).
	mux.Handle("POST /api/hive/diagnostic", h.writeWrap(h.handleHiveDiagnostic))
	mux.Handle("POST /api/hive/escalation", h.writeWrap(h.handleHiveEscalation))
	mux.Handle("POST /api/hive/mirror", h.writeWrap(h.handleHiveMirror))
	mux.Handle("GET /api/hive/site-ops", h.writeWrap(h.handleHiveSiteOps))

	// Node children (for HTMX polling).
	mux.Handle("GET /app/{slug}/node/{id}/children", h.readWrap(h.handleNodeChildren))

	// Node mutations (requires auth).
	mux.Handle("POST /app/{slug}/node/{id}/state", h.writeWrap(h.handleNodeState))
	mux.Handle("POST /app/{slug}/node/{id}/update", h.writeWrap(h.handleNodeUpdate))
	mux.Handle("DELETE /app/{slug}/node/{id}", h.writeWrap(h.handleNodeDelete))

	// Hive dashboard — public, no auth required.
	mux.HandleFunc("GET /hive", h.handleHive)
	mux.HandleFunc("GET /hive/feed", h.handleHiveFeed)
	mux.HandleFunc("GET /hive/stats", h.handleHiveStats)
	mux.HandleFunc("GET /hive/status", h.handleHiveStatus)

	// Operator shell — requires auth. Public Hive status stays under /hive*.
	mux.Handle("GET /ops", h.writeWrap(h.handleOps))
	mux.Handle("GET /ops/observation", h.writeWrap(h.handleOpsObservation))
	mux.Handle("GET /ops/control", h.writeWrap(h.handleOpsControl))
	mux.Handle("POST /ops/control/intents", h.writeWrap(h.handleOpsControlIntentCreate))
	mux.Handle("GET /ops/work", h.writeWrap(h.handleOpsWork))
	mux.Handle("GET /ops/telemetry", h.writeWrap(h.handleOpsTelemetry))
	mux.Handle("GET /ops/observatory", h.writeWrap(h.handleOpsObservatory))
	mux.Handle("GET /ops/observatory/events", h.writeWrap(h.handleOpsObservatoryEvents))
	mux.Handle("GET /ops/civilization", h.writeWrap(h.handleOpsCivilization))
	mux.Handle("GET /ops/github-canonical", h.writeWrap(h.handleOpsGitHubCanonical))
	mux.Handle("GET /ops/public-proof", h.writeWrap(h.handleOpsPublicProof))
	mux.Handle("GET /ops/review-console", h.writeWrap(h.handleOpsReviewConsole))
	mux.Handle("GET /ops/hive", h.writeWrap(h.handleOpsHive))
	mux.Handle("POST /ops/hive/model-policy", h.writeWrap(h.handleOpsHiveModelPolicySubmit))
	mux.Handle("GET /ops/hive/intake", h.writeWrap(h.handleOpsHiveIntake))
	mux.Handle("POST /ops/hive/intake/sources", h.writeWrap(h.handleOpsHiveIntakeSourceCreate))
	mux.Handle("POST /ops/hive/intake/launch", h.writeWrap(h.handleOpsHiveIntakeLaunch))
	mux.Handle("GET /ops/hive/runs", h.writeWrap(h.handleOpsHiveRuns))
	mux.Handle("GET /ops/hive/agents", h.writeWrap(h.handleOpsHiveAgents))
	mux.Handle("GET /ops/hive/resources", h.writeWrap(h.handleOpsHiveResources))
	mux.Handle("GET /ops/evidence", h.writeWrap(h.handleOpsEvidence))
	mux.Handle("GET /ops/approvals", h.writeWrap(h.handleOpsApprovals))
	mux.Handle("GET /ops/decision", h.writeWrap(h.handleOpsDecision))
	mux.Handle("POST /ops/decision", h.writeWrap(h.handleOpsDecisionSubmit))
	mux.Handle("GET /ops/refinery", h.writeWrap(h.handleOpsRefinery))
	mux.Handle("GET /factory", h.writeWrap(h.handleFactory))
	mux.Handle("POST /factory/artifacts", h.writeWrap(h.handleFactoryArtifactCreate))

	// Mission Control console — unified read-only overview and focused screens.
	mux.Handle("GET /console/workbench", h.writeWrap(h.handleCivilizationWorkbench))
	mux.Handle("GET /console/workbench/fragment", h.writeWrap(h.handleCivilizationWorkbenchFragment))
	mux.Handle("GET /console/workbench/work-fragment", h.writeWrap(h.handleCivilizationWorkList))
	mux.Handle("GET /console/workbench/runtime", h.writeWrap(h.handleCivilizationRuntime))
	mux.Handle("POST /console/workbench/work/{workID}/human-owner", h.writeWrap(h.handleCivilizationHumanOwner))
	mux.Handle("POST /console/workbench/work/{workID}/confirm", h.writeWrap(h.handleCivilizationConfirm))
	mux.Handle("POST /console/workbench/work/{workID}/result-review", h.writeWrap(h.handleCivilizationResultReview))
	mux.Handle("GET /console/workbench/work/{workID}/artifact", h.writeWrap(h.handleCivilizationArtifact))
	mux.Handle("POST /console/workbench/intake", h.writeWrap(h.handleCivilizationIntake))
	mux.Handle("POST /console/workbench/work/{workID}/run", h.writeWrap(h.handleCivilizationRun))
	mux.Handle("POST /console/workbench/work/{workID}/interventions/{interventionID}/resolve", h.writeWrap(h.handleCivilizationResolve))
	mux.Handle("GET /console", h.writeWrap(h.handleMissionControl))
	mux.Handle("GET /console/mission-control/fragment", h.writeWrap(h.handleMissionControlFragment))
	mux.Handle("GET /console/health", h.writeWrap(h.handleConsoleHealth))
	mux.Handle("GET /console/health/fragment", h.writeWrap(h.handleConsoleHealthFragment))
	mux.Handle("GET /console/kanban", h.writeWrap(h.handleConsoleKanban))
	mux.Handle("GET /console/kanban/fragment", h.writeWrap(h.handleConsoleKanbanFragment))
	mux.Handle("GET /console/kanban/order/{id}", h.writeWrap(h.handleConsoleKanbanOrder))
	mux.Handle("GET /console/intake", h.writeWrap(h.handleConsoleIntake))
	mux.Handle("GET /console/intake/fragment", h.writeWrap(h.handleConsoleIntakeFragment))
	mux.Handle("GET /console/intake/card", h.writeWrap(h.handleConsoleIntakeCard))
	mux.Handle("GET /console/config", h.writeWrap(h.handleConsoleConfig))
	mux.Handle("GET /console/config/fragment", h.writeWrap(h.handleConsoleConfigFragment))
}

// RegisterReadOnlyOps adds no-DB operator routes for local/offline read-only
// projection/fallback data. These handlers must not expose mutation paths and
// must only be mounted by the no-DATABASE_URL app branch.
func (h *Handlers) RegisterReadOnlyOps(mux *http.ServeMux) {
	mux.HandleFunc("GET /ops", h.handleOps)
	mux.HandleFunc("GET /ops/observation", h.handleOpsObservation)
	mux.HandleFunc("GET /ops/control", h.handleOpsControl)
	mux.HandleFunc("GET /ops/telemetry", h.handleOpsTelemetry)
	mux.HandleFunc("GET /ops/observatory", h.handleOpsObservatory)
	mux.HandleFunc("GET /ops/observatory/events", h.handleOpsObservatoryEvents)
	mux.HandleFunc("GET /ops/civilization", h.handleOpsCivilization)
	mux.HandleFunc("GET /ops/github-canonical", h.handleOpsGitHubCanonical)
	mux.HandleFunc("GET /ops/public-proof", h.handleOpsPublicProof)
	mux.HandleFunc("GET /ops/review-console", h.handleOpsReviewConsole)
	mux.HandleFunc("GET /ops/hive/intake", h.handleOpsHiveIntake)
	mux.HandleFunc("GET /ops/evidence", h.handleOpsEvidence)
}

// RegisterReadOnlyFactory adds the no-DB human Factory route. It is kept out
// of RegisterReadOnlyOps because /factory is an artifact-intake surface, not an
// ops read-only projection route.
func (h *Handlers) RegisterReadOnlyFactory(mux *http.ServeMux) {
	mux.HandleFunc("GET /factory", h.handleFactory)
}

// RegisterReadOnlyConsole adds no-DB focused console routes. The unified Mission
// Control projection is deliberately excluded because it contains
// authenticated operator data. /console preserves the legacy health
// entry point without mounting the Mission Control acquisition handler.
func (h *Handlers) RegisterReadOnlyConsole(mux *http.ServeMux) {
	mux.HandleFunc("GET /console", h.handleConsoleHealth)
	mux.HandleFunc("GET /console/health", h.handleConsoleHealth)
	mux.HandleFunc("GET /console/health/fragment", h.handleConsoleHealthFragment)
	mux.HandleFunc("GET /console/kanban", h.handleConsoleKanban)
	mux.HandleFunc("GET /console/kanban/fragment", h.handleConsoleKanbanFragment)
	mux.HandleFunc("GET /console/kanban/order/{id}", h.handleConsoleKanbanOrder)
	mux.HandleFunc("GET /console/intake", h.handleConsoleIntake)
	mux.HandleFunc("GET /console/intake/fragment", h.handleConsoleIntakeFragment)
	mux.HandleFunc("GET /console/intake/card", h.handleConsoleIntakeCard)
	mux.HandleFunc("GET /console/config", h.handleConsoleConfig)
	mux.HandleFunc("GET /console/config/fragment", h.handleConsoleConfigFragment)
}

// ────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────

func (h *Handlers) viewUser(r *http.Request) ViewUser {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		return ViewUser{Name: "Anonymous"}
	}
	if h == nil || h.store == nil {
		return ViewUser{ID: u.ID, Name: u.Name, Picture: u.Picture, Role: u.Role}
	}
	uid := h.userID(r)
	unread := h.store.UnreadCount(r.Context(), uid)
	return ViewUser{ID: u.ID, Name: u.Name, Picture: u.Picture, UnreadCount: unread, Role: u.Role}
}

func (h *Handlers) userID(r *http.Request) string {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		return anonUserID
	}
	return u.ID
}

func (h *Handlers) userName(r *http.Request) string {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		return anonUserID
	}
	return u.Name
}

func (h *Handlers) userKind(r *http.Request) string {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		return "human"
	}
	return u.Kind
}

// notify creates a notification for a user (skips if targetID is the actor).
func (h *Handlers) notify(ctx context.Context, targetID, actorName, opID, spaceID, message string) {
	if targetID == "" || targetID == anonUserID {
		return
	}
	h.store.CreateNotification(ctx, targetID, opID, spaceID, actorName+": "+message)
}

// spaceFromRequest returns a space for write operations.
// Owners can always write. Authenticated users can write to public spaces.
func (h *Handlers) spaceFromRequest(r *http.Request) (*Space, error) {
	slug := r.PathValue("slug")
	space, err := h.store.GetSpaceBySlug(r.Context(), slug)
	if err != nil {
		return nil, err
	}
	uid := h.userID(r)
	if space.OwnerID == uid {
		return space, nil
	}
	if space.Visibility == VisibilityPublic && uid != anonUserID {
		return space, nil
	}
	return nil, ErrNotFound
}

// spaceOwnerOnly returns a space only if the current user owns it.
func (h *Handlers) spaceOwnerOnly(r *http.Request) (*Space, error) {
	slug := r.PathValue("slug")
	space, err := h.store.GetSpaceBySlug(r.Context(), slug)
	if err != nil {
		return nil, err
	}
	if space.OwnerID != h.userID(r) {
		return nil, ErrNotFound
	}
	return space, nil
}

// spaceForRead returns a space if the user owns it, it's public, or the user is a member (for reads).
func (h *Handlers) spaceForRead(r *http.Request) (*Space, bool, error) {
	slug := r.PathValue("slug")
	space, err := h.store.GetSpaceBySlug(r.Context(), slug)
	if err != nil {
		return nil, false, err
	}
	uid := h.userID(r)
	isOwner := space.OwnerID == uid
	if isOwner || space.Visibility == VisibilityPublic {
		return space, isOwner, nil
	}
	if uid != anonUserID && h.store.IsMember(r.Context(), space.ID, uid) {
		return space, false, nil
	}
	return nil, false, ErrNotFound
}

func (h *Handlers) canReadSpace(r *http.Request, space *Space) bool {
	uid := h.userID(r)
	if space.OwnerID == uid || space.Visibility == VisibilityPublic {
		return true
	}
	return uid != anonUserID && h.store.IsMember(r.Context(), space.ID, uid)
}

func isHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

// parseMessageSearch extracts from:user operators from a search query.
// Returns the remaining body query and the from-author filter.
// sortTasks sorts tasks in place by the given sort key.
func sortTasks(tasks []Node, sortBy string) {
	switch sortBy {
	case "priority":
		sort.Slice(tasks, func(i, j int) bool {
			return priorityRank(tasks[i].Priority) < priorityRank(tasks[j].Priority)
		})
	case "due":
		sort.Slice(tasks, func(i, j int) bool {
			if tasks[i].DueDate == nil && tasks[j].DueDate == nil {
				return false
			}
			if tasks[i].DueDate == nil {
				return false
			}
			if tasks[j].DueDate == nil {
				return true
			}
			return tasks[i].DueDate.Before(*tasks[j].DueDate)
		})
	case "created":
		sort.Slice(tasks, func(i, j int) bool {
			return tasks[i].CreatedAt.After(tasks[j].CreatedAt)
		})
	case "state":
		sort.Slice(tasks, func(i, j int) bool {
			return stateRank(tasks[i].State) < stateRank(tasks[j].State)
		})
	case "assignee":
		sort.Slice(tasks, func(i, j int) bool {
			return tasks[i].Assignee < tasks[j].Assignee
		})
	default:
		// Default: by priority then created
		sort.Slice(tasks, func(i, j int) bool {
			pi, pj := priorityRank(tasks[i].Priority), priorityRank(tasks[j].Priority)
			if pi != pj {
				return pi < pj
			}
			return tasks[i].CreatedAt.After(tasks[j].CreatedAt)
		})
	}
}

func priorityRank(p string) int {
	switch p {
	case "urgent":
		return 0
	case "high":
		return 1
	case "medium":
		return 2
	case "low":
		return 3
	default:
		return 4
	}
}

func stateRank(s string) int {
	switch s {
	case StateActive:
		return 0
	case StateReview:
		return 1
	case StateOpen:
		return 2
	case StateDone:
		return 3
	default:
		return 4
	}
}

func parseMessageSearch(q string) (body string, fromAuthor string) {
	parts := strings.Fields(q)
	var bodyParts []string
	for _, p := range parts {
		if strings.HasPrefix(p, "from:") {
			fromAuthor = strings.TrimPrefix(p, "from:")
		} else {
			bodyParts = append(bodyParts, p)
		}
	}
	body = strings.Join(bodyParts, " ")
	return
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// populateFormFromJSON parses a JSON request body into r.Form
// so r.FormValue() works for both form-encoded and JSON requests.
// JSON array values (e.g. causes:["id1","id2"]) are converted to CSV
// strings so r.FormValue("causes") returns "id1,id2".
func populateFormFromJSON(r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return
	}
	var m map[string]any
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		return
	}
	if r.Form == nil {
		r.Form = url.Values{}
	}
	for k, v := range m {
		switch val := v.(type) {
		case string:
			r.Form.Set(k, val)
		case []interface{}:
			// Convert JSON array to CSV for form compatibility.
			parts := make([]string, 0, len(val))
			for _, item := range val {
				if s, ok := item.(string); ok {
					parts = append(parts, s)
				}
			}
			r.Form.Set(k, strings.Join(parts, ","))
		case nil:
			// skip
		default:
			r.Form.Set(k, fmt.Sprintf("%v", val))
		}
	}
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugRE.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "space"
	}
	return s
}

// ────────────────────────────────────────────────────────────────────
// Space handlers
// ────────────────────────────────────────────────────────────────────

func (h *Handlers) handleSpaceIndex(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := h.userID(r)

	spaces, err := h.store.ListSpaces(ctx, uid)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{"spaces": spaces})
		return
	}

	p := profile.FromContext(ctx)
	if p == nil {
		p = profile.Default()
	}

	if len(spaces) == 0 {
		// New users: show welcome page to create their first space.
		Welcome(h.viewUser(r), p).Render(ctx, w)
		return
	}

	taskFilter := r.URL.Query().Get("tasks")
	tasks, _ := h.store.ListUserTasks(ctx, uid, taskFilter, 20)
	convos, _ := h.store.ListUserConversations(ctx, uid, 5)
	agentOps, _ := h.store.ListUserAgentActivity(ctx, uid, 10)
	agents, _ := h.store.ListAgentNames(ctx)

	defaultSlug := ""
	if len(spaces) > 0 {
		defaultSlug = spaces[0].Slug
	}

	unread := h.store.UnreadCount(ctx, uid)
	Dashboard(spaces, tasks, convos, agentOps, h.viewUser(r), defaultSlug, agents, unread, taskFilter, p).Render(ctx, w)
}

func (h *Handlers) handleNotifications(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := h.userID(r)

	notifs, _ := h.store.ListNotifications(ctx, uid, 50)
	h.store.MarkNotificationsRead(ctx, uid)

	p := profile.FromContext(ctx)
	if p == nil {
		p = profile.Default()
	}

	NotificationsView(notifs, h.viewUser(r), p).Render(ctx, w)
}

func (h *Handlers) handleCreateSpace(w http.ResponseWriter, r *http.Request) {
	populateFormFromJSON(r)
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	description := strings.TrimSpace(r.FormValue("description"))
	kind := r.FormValue("kind")
	if kind == "" {
		kind = SpaceProject
	}
	visibility := r.FormValue("visibility")
	if visibility != VisibilityPublic {
		visibility = VisibilityPrivate
	}

	slug := slugify(name)
	// Ensure unique slug by appending random suffix on conflict.
	space, err := h.store.CreateSpace(r.Context(), slug, name, description, h.userID(r), kind, visibility)
	if err != nil {
		// Try with random suffix.
		slug = slug + "-" + newID()[:6]
		space, err = h.store.CreateSpace(r.Context(), slug, name, description, h.userID(r), kind, visibility)
		if err != nil {
			log.Printf("graph: create space: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Emit agent welcome message into Chat.
	if agentID, agentName, err := h.store.GetFirstAgent(r.Context()); err == nil && agentID != "" {
		userID := h.userID(r)
		convo, cerr := h.store.CreateNode(r.Context(), CreateNodeParams{
			SpaceID:    space.ID,
			Kind:       KindConversation,
			Title:      "Welcome",
			Body:       "Hey, I'm your agent teammate. I can help with tasks, answer questions, and collaborate with your team.",
			Author:     agentName,
			AuthorID:   agentID,
			AuthorKind: "agent",
			Tags:       []string{userID, agentID},
		})
		if cerr == nil {
			h.store.RecordOp(r.Context(), space.ID, convo.ID, agentName, agentID, "converse", nil)
		}
	}

	if wantsJSON(r) {
		writeJSON(w, http.StatusCreated, map[string]any{"space": space})
		return
	}

	http.Redirect(w, r, "/app/"+space.Slug, http.StatusSeeOther)
}

func (h *Handlers) handleSpaceSettings(w http.ResponseWriter, r *http.Request) {
	space, err := h.spaceOwnerOnly(r)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	spaces, _ := h.store.ListSpaces(r.Context(), h.userID(r))
	reports, _ := h.store.ListReports(r.Context(), space.ID)
	members, _ := h.store.ListMembers(r.Context(), space.ID, 50)
	invites, _ := h.store.ListInvites(r.Context(), space.ID)
	SettingsView(*space, spaces, reports, h.viewUser(r), "", members, invites, profile.FromContext(r.Context())).Render(r.Context(), w)
}

func (h *Handlers) handleUpdateSpace(w http.ResponseWriter, r *http.Request) {
	space, err := h.spaceOwnerOnly(r)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		spaces, _ := h.store.ListSpaces(r.Context(), h.userID(r))
		reports, _ := h.store.ListReports(r.Context(), space.ID)
		SettingsView(*space, spaces, reports, h.viewUser(r), "Name cannot be empty.", nil, nil, profile.FromContext(r.Context())).Render(r.Context(), w)
		return
	}

	description := strings.TrimSpace(r.FormValue("description"))
	visibility := r.FormValue("visibility")
	if visibility != VisibilityPublic {
		visibility = VisibilityPrivate
	}

	if err := h.store.UpdateSpace(r.Context(), space.ID, name, description, visibility); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/app/"+space.Slug+"/settings", http.StatusSeeOther)
}

func (h *Handlers) handleDeleteSpace(w http.ResponseWriter, r *http.Request) {
	space, err := h.spaceOwnerOnly(r)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	confirm := strings.TrimSpace(r.FormValue("confirm"))
	if confirm != space.Name {
		spaces, _ := h.store.ListSpaces(r.Context(), h.userID(r))
		reports, _ := h.store.ListReports(r.Context(), space.ID)
		SettingsView(*space, spaces, reports, h.viewUser(r), "Type the space name to confirm deletion.", nil, nil, profile.FromContext(r.Context())).Render(r.Context(), w)
		return
	}

	if err := h.store.DeleteSpace(r.Context(), space.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/app", http.StatusSeeOther)
}

// handleCreateInviteHTMX handles HTMX POST /app/{slug}/invites — creates a new invite code
// and returns an HTML fragment (InviteCodeRow) for inline insertion.
func (h *Handlers) handleCreateInviteHTMX(w http.ResponseWriter, r *http.Request) {
	space, err := h.spaceOwnerOnly(r)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	token, err := h.store.CreateInviteCode(r.Context(), space.ID, h.userID(r), nil, 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	inv := InviteCode{Token: token, SpaceID: space.ID}
	InviteCodeRow(inv, space.Slug).Render(r.Context(), w)
}

// handleRevokeInvite handles DELETE /app/{slug}/invites/{id} — removes an invite code.
// {id} carries the token string (same value as InviteCode.Token sent by the template).
func (h *Handlers) handleRevokeInvite(w http.ResponseWriter, r *http.Request) {
	space, err := h.spaceOwnerOnly(r)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	code := r.PathValue("id")
	// Verify the token belongs to this space before deleting.
	spaceID := h.store.GetInviteSpaceID(r.Context(), code)
	if spaceID != space.ID {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	if err := h.store.RevokeInvite(r.Context(), code); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handlers) handleJoinViaInvite(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")

	uid := h.userID(r)
	if uid == anonUserID {
		// Use readWrap (not writeWrap) so we can redirect with ?next= here,
		// preserving the invite URL across the login flow.
		next := url.QueryEscape(r.URL.Path)
		http.Redirect(w, r, "/auth/login?next="+next, http.StatusSeeOther)
		return
	}

	inv, err := h.store.GetInviteCode(r.Context(), code)
	if err != nil || inv == nil {
		http.Error(w, "invalid or expired invite", http.StatusNotFound)
		return
	}

	uname := h.userName(r)
	if err := h.store.JoinSpace(r.Context(), inv.SpaceID, uid, uname); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.store.UseInviteCode(r.Context(), code, uid); err != nil {
		log.Printf("UseInviteCode %s user %s: %v", code, uid, err)
	}
	h.store.RecordOp(r.Context(), inv.SpaceID, "", uname, uid, "join", nil)

	space, err := h.store.GetSpaceByID(r.Context(), inv.SpaceID)
	if err != nil {
		http.Redirect(w, r, "/app", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/app/"+space.Slug+"/board", http.StatusSeeOther)
}

func (h *Handlers) handleSpaceDefault(w http.ResponseWriter, r *http.Request) {
	space, isOwner, err := h.spaceForRead(r)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{"space": space})
		return
	}

	ctx := r.Context()
	uid := h.userID(r)

	spaces, _ := h.store.ListSpaces(ctx, uid)
	pinned, _ := h.store.ListPinnedNodes(ctx, space.ID)
	memberCount := h.store.MemberCount(ctx, space.ID)
	recentOps, _ := h.store.ListOps(ctx, space.ID, 5)

	// Count tasks by state.
	allTasks, _ := h.store.ListNodes(ctx, ListNodesParams{SpaceID: space.ID, Kind: KindTask})
	openTasks, activeTasks, doneTasks := 0, 0, 0
	for _, t := range allTasks {
		switch t.State {
		case StateOpen:
			openTasks++
		case StateActive, StateReview:
			activeTasks++
		case StateDone:
			doneTasks++
		}
	}

	members, _ := h.store.ListMembers(ctx, space.ID, 10)
	isMember := h.store.IsMember(ctx, space.ID, uid)
	loggedIn := uid != anonUserID
	SpaceOverview(*space, spaces, pinned, recentOps, h.viewUser(r), isOwner,
		memberCount, openTasks, activeTasks, doneTasks, members, isMember, loggedIn, profile.FromContext(ctx)).Render(ctx, w)
}

// ────────────────────────────────────────────────────────────────────
// Lens handlers
// ────────────────────────────────────────────────────────────────────

func (h *Handlers) handleBoard(w http.ResponseWriter, r *http.Request) {
	space, isOwner, err := h.spaceForRead(r)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	spaces, _ := h.store.ListSpaces(r.Context(), h.userID(r))

	// Load projects for filter dropdown.
	projects, _ := h.store.ListNodes(r.Context(), ListNodesParams{
		SpaceID:  space.ID,
		Kind:     KindProject,
		ParentID: "root",
	})

	projectFilter := strings.TrimSpace(r.URL.Query().Get("project"))

	// Load tasks — if project filter is set, load children of that project.
	parentFilter := "root"
	if projectFilter != "" {
		parentFilter = projectFilter
	}
	tasks, err := h.store.ListNodes(r.Context(), ListNodesParams{
		SpaceID:  space.ID,
		Kind:     KindTask,
		ParentID: parentFilter,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Apply text/assignee filters.
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	assigneeFilter := strings.TrimSpace(r.URL.Query().Get("assignee"))
	if q != "" || assigneeFilter != "" {
		var filtered []Node
		for _, t := range tasks {
			if q != "" && !strings.Contains(strings.ToLower(t.Title), q) && !strings.Contains(strings.ToLower(t.Body), q) {
				continue
			}
			if assigneeFilter != "" && t.Assignee != assigneeFilter {
				continue
			}
			filtered = append(filtered, t)
		}
		tasks = filtered
	}

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{"space": space, "nodes": tasks})
		return
	}

	agents, _ := h.store.ListAgentPersonas(r.Context())
	viewMode := r.URL.Query().Get("view") // "list" or "" (board)
	sortBy := r.URL.Query().Get("sort")   // "priority", "due", "created", "state", "assignee"

	if viewMode == "list" {
		// Sort tasks for list view.
		sortTasks(tasks, sortBy)
		ListView(*space, spaces, tasks, h.viewUser(r), isOwner, agents, q, assigneeFilter, sortBy, projects, projectFilter, profile.FromContext(r.Context())).Render(r.Context(), w)
		return
	}

	columns := groupByState(tasks)
	showFirstCompletionToast := r.URL.Query().Get("first_completion") == "1"

	// Getting started checklist: show for authenticated users in new spaces (<1 hour old) that haven't dismissed.
	showChecklist := false
	if h.userID(r) != anonUserID && time.Since(space.CreatedAt) < time.Hour {
		if _, err := r.Cookie("checklist_" + space.ID); err != nil {
			showChecklist = true
		}
	}
	hasTask := false
	hasAgentTask := false
	taskCount := 0
	for _, col := range columns {
		for _, t := range col.Nodes {
			hasTask = true
			taskCount++
			if t.AssigneeKind == "agent" {
				hasAgentTask = true
			}
		}
	}
	hasCompletion := space.FirstCompletionAt != nil
	elapsedStr := formatElapsed(time.Since(space.CreatedAt))

	// Welcome modal: show once to non-owner members on their first board visit.
	uid := h.userID(r)
	showWelcome := false
	var welcomeMembers []SpaceMember
	if uid != anonUserID && uid != space.OwnerID && h.store.IsMember(r.Context(), space.ID, uid) {
		showWelcome = h.store.MarkWelcomed(r.Context(), space.ID, uid)
	}
	if showWelcome {
		welcomeMembers, _ = h.store.ListMembers(r.Context(), space.ID, 10)
	}

	// Invite card: show when checklist is complete and space has fewer than 2 members.
	showInviteCard := false
	inviteToken := ""
	if showChecklist && hasTask && hasAgentTask && hasCompletion {
		memberCount := h.store.MemberCount(r.Context(), space.ID)
		if memberCount < 2 {
			showInviteCard = true
			inviteToken = h.store.GetInviteToken(r.Context(), space.ID)
			if inviteToken == "" {
				inviteToken, _ = h.store.CreateInvite(r.Context(), space.ID, uid)
			}
		}
	}

	BoardView(*space, spaces, columns, h.viewUser(r), isOwner, agents, q, assigneeFilter, projects, projectFilter, showFirstCompletionToast, showChecklist, hasTask, hasAgentTask, hasCompletion, taskCount, elapsedStr, showWelcome, welcomeMembers, showInviteCard, inviteToken, profile.FromContext(r.Context())).Render(r.Context(), w)
}

func formatElapsed(d time.Duration) string {
	minutes := int(d.Minutes())
	if minutes < 2 {
		return "a minute"
	}
	if minutes < 60 {
		return fmt.Sprintf("%d minutes", minutes)
	}
	hours := int(d.Hours())
	if hours == 1 {
		return "about an hour"
	}
	return fmt.Sprintf("about %d hours", hours)
}

func checklistDoneCount(hasTask, hasAgentTask, hasCompletion bool) int {
	n := 0
	if hasTask {
		n++
	}
	if hasAgentTask {
		n++
	}
	if hasCompletion {
		n++
	}
	return n
}

func (h *Handlers) handleChecklistDismiss(w http.ResponseWriter, r *http.Request) {
	space, _, err := h.spaceForRead(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "checklist_" + space.ID,
		Value:    "1",
		Path:     "/",
		MaxAge:   86400 * 30,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/app/"+space.Slug+"/board", http.StatusSeeOther)
}

func (h *Handlers) handleFeed(w http.ResponseWriter, r *http.Request) {
	space, isOwner, err := h.spaceForRead(r)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	spaces, _ := h.store.ListSpaces(r.Context(), h.userID(r))

	searchQuery := r.URL.Query().Get("q")
	feedTab := r.URL.Query().Get("tab") // "following", "foryou", or "" (all)

	var posts []Node
	if feedTab == "foryou" && searchQuery == "" {
		posts, err = h.store.ListPostsByEngagement(r.Context(), space.ID, 50)
	} else if feedTab == "trending" && searchQuery == "" {
		posts, err = h.store.ListPostsByTrending(r.Context(), space.ID, 50)
	} else {
		posts, err = h.store.ListNodes(r.Context(), ListNodesParams{
			SpaceID:  space.ID,
			Kind:     KindPost,
			ParentID: "root",
			Query:    searchQuery,
		})
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Filter by Following tab: show only posts by followed users + reposted by followed users.
	repostedBy := make(map[string]string) // nodeID → reposter display name
	if feedTab == "following" {
		uid := h.userID(r)
		followedIDs := h.store.ListFollowedIDs(r.Context(), uid)
		followSet := make(map[string]bool, len(followedIDs))
		for _, id := range followedIDs {
			followSet[id] = true
		}
		// Also include posts reposted by followed users.
		repostedNodeIDs := h.store.ListRepostedNodeIDs(r.Context(), followedIDs, 100)
		repostSet := make(map[string]bool, len(repostedNodeIDs))
		for _, id := range repostedNodeIDs {
			repostSet[id] = true
		}
		var filtered []Node
		for _, p := range posts {
			if followSet[p.AuthorID] || repostSet[p.ID] {
				filtered = append(filtered, p)
			}
		}
		posts = filtered

		// Build repost attribution: which followed user reposted each post?
		var repostedIDs []string
		for _, p := range posts {
			if !followSet[p.AuthorID] && repostSet[p.ID] {
				repostedIDs = append(repostedIDs, p.ID)
			}
		}
		if len(repostedIDs) > 0 {
			attrMap := h.store.GetRepostAttribution(r.Context(), followedIDs, repostedIDs)
			// Resolve user IDs to display names.
			var userIDs []string
			for _, uid := range attrMap {
				userIDs = append(userIDs, uid)
			}
			nameMap := h.store.ResolveUserNames(r.Context(), userIDs)
			for nodeID, reposterID := range attrMap {
				if name, ok := nameMap[reposterID]; ok {
					repostedBy[nodeID] = name
				}
			}
		}
	}

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{"space": space, "nodes": posts})
		return
	}

	agents, _ := h.store.ListAgentNames(r.Context())

	// Load endorsement + repost data for all posts.
	var postIDs []string
	for _, p := range posts {
		postIDs = append(postIDs, p.ID)
	}
	endorseCounts := h.store.GetBulkEndorsementCounts(r.Context(), postIDs)
	userEndorsed := h.store.GetBulkUserEndorsements(r.Context(), h.userID(r), postIDs)
	repostCounts := h.store.GetBulkRepostCounts(r.Context(), postIDs)
	userReposted := h.store.GetBulkUserReposts(r.Context(), h.userID(r), postIDs)

	// Quote: load the quoted post for compose form preview.
	var quotePost *Node
	if qid := r.URL.Query().Get("quote"); qid != "" {
		quotePost, _ = h.store.GetNode(r.Context(), qid)
	}

	FeedView(*space, spaces, posts, h.viewUser(r), isOwner, len(agents) > 0, searchQuery, feedTab, endorseCounts, userEndorsed, repostCounts, userReposted, repostedBy, quotePost, profile.FromContext(r.Context())).Render(r.Context(), w)
}

func (h *Handlers) handleThreads(w http.ResponseWriter, r *http.Request) {
	space, isOwner, err := h.spaceForRead(r)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	spaces, _ := h.store.ListSpaces(r.Context(), h.userID(r))

	searchQuery := r.URL.Query().Get("q")
	threads, err := h.store.ListNodes(r.Context(), ListNodesParams{
		SpaceID:  space.ID,
		Kind:     KindThread,
		ParentID: "root",
		Query:    searchQuery,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{"space": space, "nodes": threads})
		return
	}

	ThreadsView(*space, spaces, threads, h.viewUser(r), isOwner, searchQuery, profile.FromContext(r.Context())).Render(r.Context(), w)
}

func (h *Handlers) handleConversations(w http.ResponseWriter, r *http.Request) {
	space, _, err := h.spaceForRead(r)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	spaces, _ := h.store.ListSpaces(r.Context(), h.userID(r))

	searchQuery := r.URL.Query().Get("q")
	filterMode := r.URL.Query().Get("filter") // "dm" or "group" or ""
	convos, err := h.store.ListConversations(r.Context(), space.ID, h.userID(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Filter by query and DM/group.
	var filtered []ConversationSummary
	sq := strings.ToLower(searchQuery)
	for _, c := range convos {
		if searchQuery != "" && !strings.Contains(strings.ToLower(c.Title), sq) {
			continue
		}
		if filterMode == "dm" && len(c.Tags) > 2 {
			continue
		}
		if filterMode == "group" && len(c.Tags) <= 2 {
			continue
		}
		filtered = append(filtered, c)
	}
	convos = filtered

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{"space": space, "conversations": convos, "me": h.userName(r)})
		return
	}

	agents, _ := h.store.ListAgentNames(r.Context())
	// Collect all tag IDs and resolve to display names.
	var allIDs []string
	for _, c := range convos {
		allIDs = append(allIDs, c.Tags...)
	}
	nameMap := h.store.ResolveUserNames(r.Context(), allIDs)

	// Build persona map: convo ID → agent persona (nil if no agent in convo).
	// Uses a single batched query to avoid N+1 round-trips.
	personaMap := h.store.GetAgentPersonasForConversations(r.Context(), convos)

	// Message search: when a query is present, also search message bodies.
	var msgResults []MessageSearchResult
	if searchQuery != "" {
		bodyQ, fromAuthor := parseMessageSearch(searchQuery)
		msgResults, _ = h.store.SearchMessages(r.Context(), space.ID, bodyQ, fromAuthor, 20)
	}

	ConversationsView(*space, spaces, convos, h.viewUser(r), agents, nameMap, personaMap, searchQuery, filterMode == "dm", filterMode == "group", msgResults, profile.FromContext(r.Context())).Render(r.Context(), w)
}

func (h *Handlers) handlePeople(w http.ResponseWriter, r *http.Request) {
	// JSON agents endpoint for the hive pipeline.
	if r.URL.Query().Get("format") == "agents" && wantsJSON(r) {
		personas, _ := h.store.ListAgentPersonas(r.Context())
		writeJSON(w, http.StatusOK, map[string]any{"agents": personas})
		return
	}

	space, _, err := h.spaceForRead(r)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	spaces, _ := h.store.ListSpaces(r.Context(), h.userID(r))

	ops, err := h.store.ListOps(r.Context(), space.ID, 1000)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Aggregate by actor.
	memberMap := map[string]*Member{}
	for _, o := range ops {
		m, ok := memberMap[o.Actor]
		if !ok {
			m = &Member{Name: o.Actor, Kind: o.ActorKind}
			memberMap[o.Actor] = m
		}
		m.OpCount++
		m.LastSeen = o.CreatedAt.Format("Jan 2")
	}
	searchQuery := r.URL.Query().Get("q")
	var members []Member
	for _, m := range memberMap {
		if searchQuery != "" && !strings.Contains(strings.ToLower(m.Name), strings.ToLower(searchQuery)) {
			continue
		}
		members = append(members, *m)
	}

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{"space": space, "members": members})
		return
	}

	PeopleView(*space, spaces, members, h.viewUser(r), searchQuery, profile.FromContext(r.Context())).Render(r.Context(), w)
}

func (h *Handlers) handleAgents(w http.ResponseWriter, r *http.Request) {
	space, _, err := h.spaceForRead(r)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	spaces, _ := h.store.ListSpaces(r.Context(), h.userID(r))
	personas, _ := h.store.ListAgentPersonas(r.Context())

	categoryOrder := []string{"care", "governance", "knowledge", "product", "outward", "resource"}
	categoryLabels := map[string]string{
		"care": "Care", "governance": "Governance", "knowledge": "Knowledge",
		"product": "Product", "outward": "Outward", "resource": "Resource",
	}
	grouped := make(map[string][]AppAgentPersona)
	for _, p := range personas {
		grouped[p.Category] = append(grouped[p.Category], AppAgentPersona{
			Name: p.Name, Display: p.Display, Description: p.Description, Category: p.Category,
		})
	}
	var categories []AppAgentCategoryGroup
	for _, cat := range categoryOrder {
		if items, ok := grouped[cat]; ok {
			categories = append(categories, AppAgentCategoryGroup{
				Name: cat, Label: categoryLabels[cat], Personas: items,
			})
		}
	}

	AgentsView(*space, spaces, categories, h.viewUser(r), profile.FromContext(r.Context())).Render(r.Context(), w)
}

func (h *Handlers) handleAgentSessionUpdate(w http.ResponseWriter, r *http.Request) {
	personaName := r.PathValue("name")
	var body struct {
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.SessionID == "" {
		http.Error(w, "session_id required", http.StatusBadRequest)
		return
	}
	if err := h.store.UpdateAgentSession(r.Context(), personaName, body.SessionID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "session_id": body.SessionID})
}

func (h *Handlers) handleAgentChat(w http.ResponseWriter, r *http.Request) {
	personaName := r.PathValue("name")
	ctx := r.Context()
	persona := h.store.GetAgentPersona(ctx, personaName)
	if persona == nil {
		http.NotFound(w, r)
		return
	}
	agentsSpace, err := h.store.GetSpaceBySlug(ctx, AgentsSpaceSlug)
	if err != nil || agentsSpace == nil {
		http.Error(w, "agents space not available", http.StatusServiceUnavailable)
		return
	}
	actorID, actor, actorKind := h.userID(r), h.userName(r), h.userKind(r)
	node, err := h.store.CreateNode(ctx, CreateNodeParams{
		SpaceID:    agentsSpace.ID,
		Kind:       KindConversation,
		Title:      "Chat with " + persona.Display,
		Author:     actor,
		AuthorID:   actorID,
		AuthorKind: actorKind,
		Tags:       []string{actorID, "role:" + persona.Name},
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.store.RecordOp(ctx, agentsSpace.ID, node.ID, actor, actorID, "converse", nil)
	if h.mind != nil {
		go h.mind.OnMessage(agentsSpace.ID, agentsSpace.Slug, node, actorID)
	}
	http.Redirect(w, r, fmt.Sprintf("/app/%s/conversation/%s", agentsSpace.Slug, node.ID), http.StatusSeeOther)
}

func (h *Handlers) handleActivity(w http.ResponseWriter, r *http.Request) {
	space, _, err := h.spaceForRead(r)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	spaces, _ := h.store.ListSpaces(r.Context(), h.userID(r))

	opFilter := r.URL.Query().Get("op")
	ops, err := h.store.ListOps(r.Context(), space.ID, 100)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if opFilter != "" {
		var filtered []Op
		for _, o := range ops {
			if o.Op == opFilter {
				filtered = append(filtered, o)
			}
		}
		ops = filtered
	}

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{"space": space, "ops": ops})
		return
	}

	ActivityView(*space, spaces, ops, h.viewUser(r), opFilter, profile.FromContext(r.Context())).Render(r.Context(), w)
}

func (h *Handlers) handleKnowledge(w http.ResponseWriter, r *http.Request) {
	space, _, err := h.spaceForRead(r)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// ?op=max_lesson — server-side aggregate for NextLessonNumber (O(1), no truncation).
	if r.URL.Query().Get("op") == "max_lesson" {
		max, err := h.store.MaxLessonNumber(r.Context(), space.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"max_lesson": max})
		return
	}

	spaces, _ := h.store.ListSpaces(r.Context(), h.userID(r))

	searchQuery := r.URL.Query().Get("q")
	claims, err := h.store.ListNodes(r.Context(), ListNodesParams{
		SpaceID:  space.ID,
		Kind:     KindClaim,
		ParentID: "root",
		Query:    searchQuery,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Count challenges per claim.
	challengeCounts := make(map[string]int, len(claims))
	for _, c := range claims {
		challengeCounts[c.ID] = h.store.CountChallenges(r.Context(), c.ID)
	}

	// Fetch documents and questions for the unified knowledge view (BOUNDED: Limit 50 each).
	docs, _ := h.store.ListDocuments(r.Context(), space.ID, 50)
	questions, _ := h.store.ListQuestions(r.Context(), space.ID, 50)

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{
			"space":     space,
			"claims":    claims,
			"documents": docs,
			"questions": questions,
		})
		return
	}

	tab := r.URL.Query().Get("tab")
	if tab != "docs" && tab != "qa" && tab != "claims" {
		tab = "docs"
	}

	KnowledgeView(*space, spaces, claims, challengeCounts, h.viewUser(r), searchQuery, tab, docs, questions, profile.FromContext(r.Context())).Render(r.Context(), w)
}

func (h *Handlers) handleChangelog(w http.ResponseWriter, r *http.Request) {
	space, _, err := h.spaceForRead(r)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	spaces, _ := h.store.ListSpaces(r.Context(), h.userID(r))
	searchQuery := r.URL.Query().Get("q")
	entries, err := h.store.ListChangelog(r.Context(), space.ID, 50)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if searchQuery != "" {
		var filtered []ChangelogEntry
		sq := strings.ToLower(searchQuery)
		for _, e := range entries {
			if strings.Contains(strings.ToLower(e.Title), sq) {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
	}

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{"space": space, "changelog": entries})
		return
	}

	ChangelogView(*space, spaces, entries, h.viewUser(r), searchQuery, profile.FromContext(r.Context())).Render(r.Context(), w)
}

func (h *Handlers) handleProjects(w http.ResponseWriter, r *http.Request) {
	space, isOwner, err := h.spaceForRead(r)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	spaces, _ := h.store.ListSpaces(r.Context(), h.userID(r))
	searchQuery := r.URL.Query().Get("q")

	projects, err := h.store.ListNodes(r.Context(), ListNodesParams{
		SpaceID:  space.ID,
		Kind:     KindProject,
		ParentID: "root",
		Query:    searchQuery,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{"space": space, "projects": projects})
		return
	}

	ProjectsView(*space, spaces, projects, h.viewUser(r), isOwner, searchQuery, profile.FromContext(r.Context())).Render(r.Context(), w)
}

// GoalWithProjects pairs a goal with its child projects.
type GoalWithProjects struct {
	Goal     Node
	Projects []Node
}

// GoalDetail is the full goal view: goal + projects + direct tasks + aggregated counts.
type GoalDetail struct {
	Goal        Node
	Projects    []Node
	DirectTasks []Node
	TotalTasks  int
	DoneTasks   int
}

func (h *Handlers) handleGoals(w http.ResponseWriter, r *http.Request) {
	space, isOwner, err := h.spaceForRead(r)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	spaces, _ := h.store.ListSpaces(r.Context(), h.userID(r))
	searchQuery := r.URL.Query().Get("q")

	goals, err := h.store.ListNodes(r.Context(), ListNodesParams{
		SpaceID:  space.ID,
		Kind:     KindGoal,
		ParentID: "root",
		Query:    searchQuery,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	goalsWithProjects := make([]GoalWithProjects, 0, len(goals))
	for _, goal := range goals {
		projects, _ := h.store.ListNodes(r.Context(), ListNodesParams{
			SpaceID:  space.ID,
			Kind:     KindProject,
			ParentID: goal.ID,
		})
		goalsWithProjects = append(goalsWithProjects, GoalWithProjects{
			Goal:     goal,
			Projects: projects,
		})
	}

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{"space": space, "goals": goalsWithProjects})
		return
	}

	GoalsView(*space, spaces, goalsWithProjects, h.viewUser(r), isOwner, searchQuery, profile.FromContext(r.Context())).Render(r.Context(), w)
}

func (h *Handlers) handleGoalDetail(w http.ResponseWriter, r *http.Request) {
	space, isOwner, err := h.spaceForRead(r)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	spaces, _ := h.store.ListSpaces(r.Context(), h.userID(r))
	id := r.PathValue("id")

	goal, err := h.store.GetNode(r.Context(), id)
	if errors.Is(err, ErrNotFound) || (err == nil && goal.Kind != KindGoal) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if goal.SpaceID != space.ID {
		http.NotFound(w, r)
		return
	}

	projects, err := h.store.ListNodes(r.Context(), ListNodesParams{
		SpaceID:  space.ID,
		Kind:     KindProject,
		ParentID: goal.ID,
		Limit:    200,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	directTasks, err := h.store.ListNodes(r.Context(), ListNodesParams{
		SpaceID:  space.ID,
		Kind:     KindTask,
		ParentID: goal.ID,
		Limit:    200,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	totalTasks := len(directTasks)
	doneTasks := 0
	for _, t := range directTasks {
		if t.State == StateDone {
			doneTasks++
		}
	}
	for _, proj := range projects {
		totalTasks += proj.ChildCount
		doneTasks += proj.ChildDone
	}

	detail := GoalDetail{
		Goal:        *goal,
		Projects:    projects,
		DirectTasks: directTasks,
		TotalTasks:  totalTasks,
		DoneTasks:   doneTasks,
	}

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{"space": space, "goal": detail})
		return
	}

	GoalDetailView(*space, spaces, detail, h.viewUser(r), isOwner, profile.FromContext(r.Context())).Render(r.Context(), w)
}

func (h *Handlers) handleRoles(w http.ResponseWriter, r *http.Request) {
	space, isOwner, err := h.spaceForRead(r)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	spaces, _ := h.store.ListSpaces(r.Context(), h.userID(r))
	searchQuery := r.URL.Query().Get("q")

	roles, err := h.store.ListNodes(r.Context(), ListNodesParams{
		SpaceID:  space.ID,
		Kind:     KindRole,
		ParentID: "root",
		Query:    searchQuery,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{"space": space, "roles": roles})
		return
	}

	RolesView(*space, spaces, roles, h.viewUser(r), isOwner, searchQuery, profile.FromContext(r.Context())).Render(r.Context(), w)
}

func (h *Handlers) handleTeams(w http.ResponseWriter, r *http.Request) {
	space, isOwner, err := h.spaceForRead(r)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	spaces, _ := h.store.ListSpaces(r.Context(), h.userID(r))
	searchQuery := r.URL.Query().Get("q")

	teams, err := h.store.ListNodes(r.Context(), ListNodesParams{
		SpaceID:  space.ID,
		Kind:     KindTeam,
		ParentID: "root",
		Query:    searchQuery,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{"space": space, "teams": teams})
		return
	}

	ctx := r.Context()
	uid := h.userID(r)
	memberCounts := make(map[string]int, len(teams))
	isMember := make(map[string]bool, len(teams))
	for _, t := range teams {
		memberCounts[t.ID] = h.store.NodeMemberCount(ctx, t.ID)
		if uid != "" {
			isMember[t.ID] = h.store.IsNodeMember(ctx, t.ID, uid)
		}
	}

	TeamsView(*space, spaces, teams, h.viewUser(r), isOwner, searchQuery, memberCounts, isMember, profile.FromContext(r.Context())).Render(r.Context(), w)
}

func (h *Handlers) handlePolicies(w http.ResponseWriter, r *http.Request) {
	space, isOwner, err := h.spaceForRead(r)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	spaces, _ := h.store.ListSpaces(r.Context(), h.userID(r))
	searchQuery := r.URL.Query().Get("q")

	policies, err := h.store.ListNodes(r.Context(), ListNodesParams{
		SpaceID:  space.ID,
		Kind:     KindPolicy,
		ParentID: "root",
		Query:    searchQuery,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{"space": space, "policies": policies})
		return
	}

	PoliciesView(*space, spaces, policies, h.viewUser(r), isOwner, searchQuery, profile.FromContext(r.Context())).Render(r.Context(), w)
}

func (h *Handlers) handleDocuments(w http.ResponseWriter, r *http.Request) {
	space, isOwner, err := h.spaceForRead(r)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	spaces, _ := h.store.ListSpaces(r.Context(), h.userID(r))
	searchQuery := r.URL.Query().Get("q")

	documents, err := h.store.ListNodes(r.Context(), ListNodesParams{
		SpaceID:  space.ID,
		Kind:     KindDocument,
		ParentID: "root",
		Query:    searchQuery,
		Limit:    50,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{"space": space, "documents": documents})
		return
	}

	DocumentsView(*space, spaces, documents, h.viewUser(r), isOwner, searchQuery, profile.FromContext(r.Context())).Render(r.Context(), w)
}

func (h *Handlers) handleDocumentEdit(w http.ResponseWriter, r *http.Request) {
	space, err := h.spaceFromRequest(r)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	nodeID := r.PathValue("id")
	node, err := h.store.GetNode(r.Context(), nodeID)
	if errors.Is(err, ErrNotFound) || node == nil {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if node.Kind != KindDocument {
		http.NotFound(w, r)
		return
	}

	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		title := r.FormValue("title")
		body := r.FormValue("body")
		if err := h.store.UpdateNode(r.Context(), nodeID, &title, &body, nil, nil, nil); err != nil {
			if errors.Is(err, ErrNotFound) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if wantsJSON(r) {
			updated, _ := h.store.GetNode(r.Context(), nodeID)
			writeJSON(w, http.StatusOK, map[string]any{"node": updated})
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/app/%s/node/%s", space.Slug, nodeID), http.StatusSeeOther)
		return
	}

	spaces, _ := h.store.ListSpaces(r.Context(), h.userID(r))
	DocumentEditView(*space, spaces, *node, h.viewUser(r), profile.FromContext(r.Context())).Render(r.Context(), w)
}

func (h *Handlers) handleQuestions(w http.ResponseWriter, r *http.Request) {
	space, isOwner, err := h.spaceForRead(r)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	spaces, _ := h.store.ListSpaces(r.Context(), h.userID(r))
	searchQuery := r.URL.Query().Get("q")

	questions, err := h.store.ListNodes(r.Context(), ListNodesParams{
		SpaceID:  space.ID,
		Kind:     KindQuestion,
		ParentID: "root",
		Query:    searchQuery,
		Limit:    50,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{"space": space, "questions": questions})
		return
	}

	QuestionsView(*space, spaces, questions, h.viewUser(r), isOwner, searchQuery, profile.FromContext(r.Context())).Render(r.Context(), w)
}

func (h *Handlers) handleQuestionDetail(w http.ResponseWriter, r *http.Request) {
	space, isOwner, err := h.spaceForRead(r)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	spaces, _ := h.store.ListSpaces(r.Context(), h.userID(r))
	id := r.PathValue("id")

	question, err := h.store.GetNode(r.Context(), id)
	if errors.Is(err, ErrNotFound) || (err == nil && question.Kind != KindQuestion) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if question.SpaceID != space.ID {
		http.NotFound(w, r)
		return
	}

	answers, err := h.store.ListNodes(r.Context(), ListNodesParams{
		SpaceID:  space.ID,
		ParentID: id,
		Limit:    200,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{"space": space, "question": question, "answers": answers})
		return
	}

	QuestionDetailView(*space, spaces, *question, answers, h.viewUser(r), isOwner, profile.FromContext(r.Context())).Render(r.Context(), w)
}

func (h *Handlers) handleCouncil(w http.ResponseWriter, r *http.Request) {
	space, _, err := h.spaceForRead(r)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	spaces, _ := h.store.ListSpaces(r.Context(), h.userID(r))

	sessions, err := h.store.ListCouncilSessions(r.Context(), space.ID, 50)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{"space": space, "sessions": sessions})
		return
	}

	agents, _ := h.store.ListAgentPersonas(r.Context())
	CouncilListView(*space, spaces, sessions, h.viewUser(r), agents, profile.FromContext(r.Context())).Render(r.Context(), w)
}

func (h *Handlers) handleCouncilDetail(w http.ResponseWriter, r *http.Request) {
	space, _, err := h.spaceForRead(r)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	spaces, _ := h.store.ListSpaces(r.Context(), h.userID(r))
	id := r.PathValue("id")

	session, err := h.store.GetNode(r.Context(), id)
	if errors.Is(err, ErrNotFound) || (err == nil && session.Kind != KindCouncil) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if session.SpaceID != space.ID {
		http.NotFound(w, r)
		return
	}

	responses, err := h.store.ListNodes(r.Context(), ListNodesParams{
		SpaceID:  space.ID,
		ParentID: id,
		Limit:    200,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{"space": space, "session": session, "responses": responses})
		return
	}

	CouncilDetailView(*space, spaces, *session, responses, h.viewUser(r), profile.FromContext(r.Context())).Render(r.Context(), w)
}

func (h *Handlers) handleGovernance(w http.ResponseWriter, r *http.Request) {
	space, isOwner, err := h.spaceForRead(r)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	spaces, _ := h.store.ListSpaces(r.Context(), h.userID(r))
	stateFilter := r.URL.Query().Get("state")
	searchQuery := r.URL.Query().Get("q")
	proposals, err := h.store.ListProposals(r.Context(), space.ID, stateFilter, 50)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if searchQuery != "" {
		var filtered []ProposalWithVotes
		sq := strings.ToLower(searchQuery)
		for _, p := range proposals {
			if strings.Contains(strings.ToLower(p.Title), sq) || strings.Contains(strings.ToLower(p.Body), sq) {
				filtered = append(filtered, p)
			}
		}
		proposals = filtered
	}

	actorID := h.userID(r)
	_, delegateName, hasDelegated := h.store.GetUserDelegation(r.Context(), space.ID, actorID)
	delegations, _ := h.store.ListDelegations(r.Context(), space.ID, 20)

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{"space": space, "proposals": proposals})
		return
	}

	GovernanceView(*space, spaces, proposals, h.viewUser(r), isOwner, stateFilter, searchQuery, hasDelegated, delegateName, delegations, profile.FromContext(r.Context())).Render(r.Context(), w)
}

// ────────────────────────────────────────────────────────────────────
// Node detail
// ────────────────────────────────────────────────────────────────────

func (h *Handlers) handleConversationDetail(w http.ResponseWriter, r *http.Request) {
	space, _, err := h.spaceForRead(r)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	nodeID := r.PathValue("id")
	node, err := h.store.GetNode(r.Context(), nodeID)
	if errors.Is(err, ErrNotFound) || (err == nil && node.Kind != KindConversation) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	messages, err := h.store.ListNodes(r.Context(), ListNodesParams{
		SpaceID:  space.ID,
		ParentID: nodeID,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{"space": space, "conversation": node, "messages": messages})
		return
	}

	hasAgent, _ := h.store.HasAgentParticipant(r.Context(), node.Tags)
	agentPersona := h.store.GetAgentPersonaForConversation(r.Context(), node.Tags)

	// Mark conversation as read for current user.
	uid := h.userID(r)
	if uid != "" {
		h.store.MarkConversationRead(r.Context(), uid, nodeID)
	}

	// Load reactions for all messages.
	var msgIDs []string
	for _, m := range messages {
		msgIDs = append(msgIDs, m.ID)
	}
	rxnMap := h.store.GetBulkReactions(r.Context(), msgIDs)

	user := h.viewUser(r)
	nameMap := h.store.ResolveUserNames(r.Context(), node.Tags)
	ConversationDetailView(*space, *node, messages, user, h.userID(r), hasAgent, agentPersona, nameMap, rxnMap, profile.FromContext(r.Context())).Render(r.Context(), w)
}

// handleConversationMessages returns new messages since the given timestamp.
// Used by HTMX polling: GET /app/{slug}/conversation/{id}/messages?after=RFC3339
func (h *Handlers) handleConversationMessages(w http.ResponseWriter, r *http.Request) {
	space, _, err := h.spaceForRead(r)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	nodeID := r.PathValue("id")
	afterStr := r.URL.Query().Get("after")
	if afterStr == "" {
		w.WriteHeader(http.StatusOK)
		return
	}

	after, err := time.Parse(time.RFC3339Nano, afterStr)
	if err != nil {
		http.Error(w, "invalid after timestamp", http.StatusBadRequest)
		return
	}

	messages, err := h.store.ListNodes(r.Context(), ListNodesParams{
		SpaceID:  space.ID,
		ParentID: nodeID,
		After:    &after,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{"messages": messages})
		return
	}

	// Return chatMessage fragments for HTMX polling.
	uid := h.userID(r)
	var msgIDs []string
	for _, m := range messages {
		msgIDs = append(msgIDs, m.ID)
	}
	rxnMap := h.store.GetBulkReactions(r.Context(), msgIDs)
	for _, msg := range messages {
		chatMessage(msg, uid, space.Slug, rxnMap[msg.ID]).Render(r.Context(), w)
	}
}

func (h *Handlers) handleNodeDetail(w http.ResponseWriter, r *http.Request) {
	space, isOwner, err := h.spaceForRead(r)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	nodeID := r.PathValue("id")
	node, err := h.store.GetNode(r.Context(), nodeID)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	children, err := h.store.ListNodes(r.Context(), ListNodesParams{
		SpaceID:  space.ID,
		ParentID: nodeID,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	ops, err := h.store.ListNodeOps(r.Context(), nodeID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Fetch dependencies (what this node depends on) and dependents (what depends on this).
	dependencies, _ := h.store.ListDependencies(r.Context(), nodeID)
	dependents, _ := h.store.ListDependents(r.Context(), nodeID)

	// Fetch space tasks for dependency dropdown (tasks only, exclude self + existing deps).
	var spaceTasks []Node
	if node.Kind == KindTask {
		all, _ := h.store.ListNodes(r.Context(), ListNodesParams{SpaceID: space.ID, Kind: KindTask})
		depSet := make(map[string]bool, len(dependencies))
		depSet[nodeID] = true // exclude self
		for _, d := range dependencies {
			depSet[d.ID] = true
		}
		for _, t := range all {
			if !depSet[t.ID] {
				spaceTasks = append(spaceTasks, t)
			}
		}
	}

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{"space": space, "node": node, "children": children, "ops": ops, "dependencies": dependencies, "dependents": dependents})
		return
	}

	// Build parent chain for breadcrumbs.
	var parents []Node
	cur := node
	for cur.ParentID != "" && len(parents) < 5 {
		p, err := h.store.GetNode(r.Context(), cur.ParentID)
		if err != nil || p == nil {
			break
		}
		parents = append(parents, *p)
		cur = p
	}
	// Reverse so root parent is first.
	for i, j := 0, len(parents)-1; i < j; i, j = i+1, j-1 {
		parents[i], parents[j] = parents[j], parents[i]
	}

	// Engagement data for the node.
	uid := h.userID(r)
	endorseCount := h.store.CountEndorsements(r.Context(), nodeID)
	endorsed := uid != "" && h.store.HasEndorsed(r.Context(), uid, nodeID)
	repostCounts := h.store.GetBulkRepostCounts(r.Context(), []string{nodeID})
	reposted := uid != "" && h.store.HasReposted(r.Context(), uid, nodeID)
	agents, _ := h.store.ListAgentPersonas(r.Context())

	NodeDetailView(*space, *node, children, ops, h.viewUser(r), isOwner, parents, dependencies, dependents, spaceTasks, agents, endorseCount, endorsed, repostCounts[nodeID], reposted, profile.FromContext(r.Context())).Render(r.Context(), w)
}

// ────────────────────────────────────────────────────────────────────
// Grammar operation dispatcher
// ────────────────────────────────────────────────────────────────────

func (h *Handlers) handleOp(w http.ResponseWriter, r *http.Request) {
	populateFormFromJSON(r)

	space, err := h.spaceFromRequest(r)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	op := r.FormValue("op")
	ctx := r.Context()
	actor := h.userName(r)
	actorID := h.userID(r)
	actorKind := h.userKind(r)

	switch op {
	case "intend":
		title := strings.TrimSpace(r.FormValue("title"))
		if title == "" {
			http.Error(w, "title required", http.StatusBadRequest)
			return
		}
		assigneeName := strings.TrimSpace(r.FormValue("assignee"))
		assigneeID := ""
		if assigneeName != "" {
			assigneeID = h.store.ResolveUserID(ctx, assigneeName)
		}
		var dueDate *time.Time
		if ds := r.FormValue("due_date"); ds != "" {
			if t, err := time.Parse("2006-01-02", ds); err == nil {
				dueDate = &t
			}
		}
		nodeKind := r.FormValue("kind")
		if nodeKind != KindProject && nodeKind != KindGoal && nodeKind != KindRole && nodeKind != KindTeam && nodeKind != KindPolicy && nodeKind != KindDocument && nodeKind != KindQuestion && nodeKind != KindProposal {
			nodeKind = KindTask // default
		}
		var intentCauses []string
		if causesStr := r.FormValue("causes"); causesStr != "" {
			for _, c := range strings.Split(causesStr, ",") {
				if c = strings.TrimSpace(c); c != "" {
					intentCauses = append(intentCauses, c)
				}
			}
		}
		intentBody := strings.TrimSpace(r.FormValue("body"))
		if intentBody == "" {
			intentBody = strings.TrimSpace(r.FormValue("description"))
		}
		node, err := h.store.CreateNode(ctx, CreateNodeParams{
			SpaceID:    space.ID,
			ParentID:   strings.TrimSpace(r.FormValue("parent_id")),
			Kind:       nodeKind,
			Title:      title,
			Body:       intentBody,
			Priority:   r.FormValue("priority"),
			Assignee:   assigneeName,
			AssigneeID: assigneeID,
			Author:     actor,
			AuthorID:   actorID,
			AuthorKind: actorKind,
			DueDate:    dueDate,
			Causes:     intentCauses,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		intentPayload, _ := json.Marshal(map[string]string{
			"body":     intentBody,
			"priority": node.Priority,
		})
		op, _ := h.store.RecordOp(ctx, space.ID, node.ID, actor, actorID, "intend", intentPayload)

		// Notify assignee if task was created with one.
		if assigneeID != "" && assigneeID != actorID && op != nil {
			h.notify(ctx, assigneeID, actor, op.ID, space.ID, "created a task for you: "+node.Title)
		}

		// Trigger Mind if task was created with an agent assignee.
		if h.mind != nil && assigneeID != "" {
			go h.mind.OnTaskAssigned(space.ID, space.Slug, node, assigneeID)
		}

		// Trigger Mind to auto-answer questions created via intend.
		if h.mind != nil && nodeKind == KindQuestion {
			go h.mind.OnQuestionAsked(space.ID, space.Slug, node)
		}

		if wantsJSON(r) {
			writeJSON(w, http.StatusCreated, map[string]any{"node": node, "op": "intend"})
			return
		}
		if isHTMX(r) {
			TaskCard(*node, space.Slug).Render(ctx, w)
			return
		}
		if returnTo := safeLocalReturn(r.FormValue("return_to")); returnTo != "" {
			http.Redirect(w, r, returnTo, http.StatusSeeOther)
			return
		}
		boardURL := "/app/" + space.Slug + "/board"
		if assigneeID != "" {
			if isAgent, _ := h.store.HasAgentParticipant(ctx, []string{assigneeID}); isAgent {
				boardURL += "?aha_agent=1"
			}
		}
		http.Redirect(w, r, boardURL, http.StatusSeeOther)

	case "decompose":
		title := strings.TrimSpace(r.FormValue("title"))
		parentID := r.FormValue("parent_id")
		if title == "" || parentID == "" {
			http.Error(w, "title and parent_id required", http.StatusBadRequest)
			return
		}
		node, err := h.store.CreateNode(ctx, CreateNodeParams{
			SpaceID:    space.ID,
			ParentID:   parentID,
			Kind:       KindTask,
			Title:      title,
			Author:     actor,
			AuthorID:   actorID,
			AuthorKind: actorKind,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		op, _ := h.store.RecordOp(ctx, space.ID, node.ID, actor, actorID, "decompose", nil)

		// Notify parent task author when an agent decomposes their task.
		if actorKind == "agent" && op != nil {
			if parent, _ := h.store.GetNode(ctx, parentID); parent != nil && parent.AuthorID != actorID {
				h.notify(ctx, parent.AuthorID, actor, op.ID, space.ID, "broke down your task: "+parent.Title)
			}
		}

		if wantsJSON(r) {
			writeJSON(w, http.StatusCreated, map[string]any{"node": node, "op": "decompose"})
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/app/%s/node/%s", space.Slug, parentID), http.StatusSeeOther)

	case "express":
		title := strings.TrimSpace(r.FormValue("title"))
		body := strings.TrimSpace(r.FormValue("body"))
		if title == "" && body == "" {
			http.Error(w, "title or body required", http.StatusBadRequest)
			return
		}

		// express with kind=question creates a KindQuestion and triggers auto-answer.
		if r.FormValue("kind") == KindQuestion {
			node, err := h.store.CreateNode(ctx, CreateNodeParams{
				SpaceID:    space.ID,
				Kind:       KindQuestion,
				Title:      title,
				Body:       body,
				Author:     actor,
				AuthorID:   actorID,
				AuthorKind: actorKind,
			})
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			h.store.RecordOp(ctx, space.ID, node.ID, actor, actorID, "express", nil)

			if h.mind != nil {
				go h.mind.OnQuestionAsked(space.ID, space.Slug, node)
			}

			if wantsJSON(r) {
				writeJSON(w, http.StatusCreated, map[string]any{"node": node, "op": "express"})
				return
			}
			http.Redirect(w, r, fmt.Sprintf("/app/%s/questions/%s", space.Slug, node.ID), http.StatusSeeOther)
			return
		}

		quoteOfID := strings.TrimSpace(r.FormValue("quote_of_id"))
		node, err := h.store.CreateNode(ctx, CreateNodeParams{
			SpaceID:    space.ID,
			Kind:       KindPost,
			Title:      title,
			Body:       body,
			Author:     actor,
			AuthorID:   actorID,
			AuthorKind: actorKind,
			QuoteOfID:  quoteOfID,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h.store.RecordOp(ctx, space.ID, node.ID, actor, actorID, "express", nil)

		if wantsJSON(r) {
			writeJSON(w, http.StatusCreated, map[string]any{"node": node, "op": "express"})
			return
		}
		if isHTMX(r) {
			FeedCard(*node, space.Slug, 0, false, 0, false, "").Render(ctx, w)
			return
		}
		http.Redirect(w, r, "/app/"+space.Slug+"/feed", http.StatusSeeOther)

	case "discuss":
		title := strings.TrimSpace(r.FormValue("title"))
		body := strings.TrimSpace(r.FormValue("body"))
		if title == "" {
			http.Error(w, "title required", http.StatusBadRequest)
			return
		}
		node, err := h.store.CreateNode(ctx, CreateNodeParams{
			SpaceID:    space.ID,
			Kind:       KindThread,
			Title:      title,
			Body:       body,
			Author:     actor,
			AuthorID:   actorID,
			AuthorKind: actorKind,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h.store.RecordOp(ctx, space.ID, node.ID, actor, actorID, "discuss", nil)

		if wantsJSON(r) {
			writeJSON(w, http.StatusCreated, map[string]any{"node": node, "op": "discuss"})
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/app/%s/node/%s", space.Slug, node.ID), http.StatusSeeOther)

	case "respond":
		parentID := r.FormValue("parent_id")
		body := strings.TrimSpace(r.FormValue("body"))
		if parentID == "" || body == "" {
			http.Error(w, "parent_id and body required", http.StatusBadRequest)
			return
		}
		replyToID := strings.TrimSpace(r.FormValue("reply_to_id"))
		node, err := h.store.CreateNode(ctx, CreateNodeParams{
			SpaceID:    space.ID,
			ParentID:   parentID,
			Kind:       KindComment,
			Body:       body,
			Author:     actor,
			AuthorID:   actorID,
			AuthorKind: actorKind,
			ReplyToID:  replyToID,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		op, _ := h.store.RecordOp(ctx, space.ID, node.ID, actor, actorID, "respond", nil)

		// Notify conversation participants (except the sender).
		if op != nil {
			if parent, _ := h.store.GetNode(ctx, parentID); parent != nil && parent.Kind == KindConversation {
				for _, tagID := range parent.Tags {
					if tagID != actorID {
						h.notify(ctx, tagID, actor, op.ID, space.ID, "sent a message")
					}
				}
				h.store.UpdateLastMessagePreview(ctx, parentID, body)
			}
		}

		// Trigger Mind auto-reply if a non-agent messaged in a conversation.
		if h.mind != nil && actorKind != "agent" {
			if parent, _ := h.store.GetNode(ctx, parentID); parent != nil && parent.Kind == KindConversation {
				go h.mind.OnMessage(space.ID, space.Slug, parent, actorID)
			}
		}

		if wantsJSON(r) {
			writeJSON(w, http.StatusCreated, map[string]any{"node": node, "op": "respond"})
			return
		}
		if isHTMX(r) {
			parent, _ := h.store.GetNode(ctx, parentID)
			if parent != nil && parent.Kind == KindConversation {
				chatMessage(*node, actorID, space.Slug, nil).Render(ctx, w)
			} else {
				CommentItem(*node).Render(ctx, w)
			}
			return
		}
		if parent, _ := h.store.GetNode(ctx, parentID); parent != nil && parent.Kind == KindConversation {
			http.Redirect(w, r, fmt.Sprintf("/app/%s/conversation/%s", space.Slug, parentID), http.StatusSeeOther)
		} else {
			http.Redirect(w, r, fmt.Sprintf("/app/%s/node/%s", space.Slug, parentID), http.StatusSeeOther)
		}

	case "claim":
		nodeID := r.FormValue("node_id")
		if nodeID == "" {
			http.Error(w, "node_id required", http.StatusBadRequest)
			return
		}
		if err := h.store.ClaimNode(ctx, nodeID, actor, actorID); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		op, _ := h.store.RecordOp(ctx, space.ID, nodeID, actor, actorID, "claim", nil)
		// Notify task author.
		if op != nil {
			if node, _ := h.store.GetNode(ctx, nodeID); node != nil && node.AuthorID != actorID {
				h.notify(ctx, node.AuthorID, actor, op.ID, space.ID, "claimed your task: "+node.Title)
			}
		}
		// Trigger Mind if an agent claims.
		if h.mind != nil {
			if node, _ := h.store.GetNode(ctx, nodeID); node != nil {
				go h.mind.OnTaskAssigned(space.ID, space.Slug, node, actorID)
			}
		}
		if wantsJSON(r) {
			node, _ := h.store.GetNode(ctx, nodeID)
			writeJSON(w, http.StatusOK, map[string]any{"node": node, "op": "claim"})
			return
		}
		http.Redirect(w, r, r.Header.Get("Referer"), http.StatusSeeOther)

	case "complete":
		nodeID := r.FormValue("node_id")
		if nodeID == "" {
			http.Error(w, "node_id required", http.StatusBadRequest)
			return
		}
		if err := h.store.UpdateNodeState(ctx, nodeID, StateDone); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		op, _ := h.store.RecordOp(ctx, space.ID, nodeID, actor, actorID, "complete", nil)

		// Notify task author/assignee when someone completes their task.
		if op != nil {
			if node, _ := h.store.GetNode(ctx, nodeID); node != nil {
				if node.AuthorID != actorID {
					h.notify(ctx, node.AuthorID, actor, op.ID, space.ID, "completed your task: "+node.Title)
				}
				if node.AssigneeID != "" && node.AssigneeID != actorID && node.AssigneeID != node.AuthorID {
					h.notify(ctx, node.AssigneeID, actor, op.ID, space.ID, "completed task: "+node.Title)
				}
			}
		}

		// Recompute reputation for the actor who completed the task.
		h.store.ComputeAndUpdateReputation(ctx, actorID)

		// Track first completion in this space.
		isFirstCompletion, _ := h.store.MarkFirstCompletion(ctx, space.ID)

		if wantsJSON(r) {
			node, _ := h.store.GetNode(ctx, nodeID)
			writeJSON(w, http.StatusOK, map[string]any{"node": node, "op": "complete", "first_completion": isFirstCompletion})
			return
		}

		node, _ := h.store.GetNode(ctx, nodeID)
		if isHTMX(r) && node != nil {
			TaskCard(*node, space.Slug).Render(ctx, w)
			return
		}
		boardURL := "/app/" + space.Slug + "/board"
		if isFirstCompletion {
			boardURL += "?first_completion=1"
		}
		http.Redirect(w, r, boardURL, http.StatusSeeOther)

	case "assign":
		nodeID := r.FormValue("node_id")
		assignee := strings.TrimSpace(r.FormValue("assignee"))
		if nodeID == "" {
			http.Error(w, "node_id required", http.StatusBadRequest)
			return
		}
		// Resolve assignee name to ID.
		assigneeID := h.store.ResolveUserID(ctx, assignee)
		if err := h.store.UpdateNode(ctx, nodeID, nil, nil, nil, &assignee, &assigneeID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		op, _ := h.store.RecordOp(ctx, space.ID, nodeID, actor, actorID, "assign", nil)

		// Notify assignee.
		if assigneeID != actorID && op != nil {
			h.notify(ctx, assigneeID, actor, op.ID, space.ID, "assigned you a task")
		}

		// Trigger Mind if task was assigned to an agent.
		if h.mind != nil && assigneeID != "" {
			if node, _ := h.store.GetNode(ctx, nodeID); node != nil {
				go h.mind.OnTaskAssigned(space.ID, space.Slug, node, assigneeID)
			}
		}

		if wantsJSON(r) {
			node, _ := h.store.GetNode(ctx, nodeID)
			writeJSON(w, http.StatusOK, map[string]any{"node": node, "op": "assign"})
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/app/%s/node/%s", space.Slug, nodeID), http.StatusSeeOther)

	case "prioritize":
		nodeID := r.FormValue("node_id")
		priority := r.FormValue("priority")
		if nodeID == "" || priority == "" {
			http.Error(w, "node_id and priority required", http.StatusBadRequest)
			return
		}
		if err := h.store.UpdateNode(ctx, nodeID, nil, nil, &priority, nil, nil); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h.store.RecordOp(ctx, space.ID, nodeID, actor, actorID, "prioritize", nil)

		if wantsJSON(r) {
			node, _ := h.store.GetNode(ctx, nodeID)
			writeJSON(w, http.StatusOK, map[string]any{"node": node, "op": "prioritize"})
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/app/%s/node/%s", space.Slug, nodeID), http.StatusSeeOther)

	case "converse":
		title := strings.TrimSpace(r.FormValue("title"))
		if title == "" {
			http.Error(w, "title required", http.StatusBadRequest)
			return
		}
		// Participants: store ONLY user IDs in tags.
		// Resolve participant names to IDs via lookup.
		participants := []string{actorID}
		if p := strings.TrimSpace(r.FormValue("participants")); p != "" {
			for _, name := range strings.Split(p, ",") {
				name = strings.TrimSpace(name)
				if name == "" || name == actor || name == actorID {
					continue
				}
				// Try to resolve name to user ID.
				resolved := h.store.ResolveUserID(ctx, name)
				if resolved != "" {
					if resolved != actorID {
						participants = append(participants, resolved)
					}
				} else {
					// Fallback: store raw string for invited users who don't exist yet.
					participants = append(participants, name)
				}
			}
		}
		// Route agent conversations to the agents space.
		// Use identity system (users table, kind='agent') — not content heuristics.
		targetSpace := space
		if hasAgent, err := h.store.HasAgentParticipant(ctx, participants); err != nil {
			log.Printf("converse: check agent participants: %v", err)
		} else if hasAgent {
			agentsSpace, err := h.store.GetSpaceBySlug(ctx, AgentsSpaceSlug)
			if err != nil {
				log.Printf("converse: get agents space %q: %v", AgentsSpaceSlug, err)
			} else if agentsSpace != nil {
				targetSpace = agentsSpace
			}
		}

		node, err := h.store.CreateNode(ctx, CreateNodeParams{
			SpaceID:    targetSpace.ID,
			Kind:       KindConversation,
			Title:      title,
			Body:       strings.TrimSpace(r.FormValue("body")),
			Author:     actor,
			AuthorID:   actorID,
			AuthorKind: actorKind,
			Tags:       participants,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h.store.RecordOp(ctx, targetSpace.ID, node.ID, actor, actorID, "converse", nil)

		// Trigger Mind if a human created a conversation with an agent.
		if h.mind != nil && actorKind != "agent" {
			go h.mind.OnMessage(targetSpace.ID, targetSpace.Slug, node, actorID)
		}

		if wantsJSON(r) {
			writeJSON(w, http.StatusCreated, map[string]any{"node": node, "op": "converse"})
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/app/%s/conversation/%s", targetSpace.Slug, node.ID), http.StatusSeeOther)

	case "join":
		if err := h.store.JoinSpace(ctx, space.ID, actorID, actor); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h.store.RecordOp(ctx, space.ID, "", actor, actorID, "join", nil)
		if wantsJSON(r) {
			writeJSON(w, http.StatusOK, map[string]string{"op": "join", "status": "joined"})
			return
		}
		http.Redirect(w, r, "/app/"+space.Slug, http.StatusSeeOther)

	case "leave":
		if err := h.store.LeaveSpace(ctx, space.ID, actorID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h.store.RecordOp(ctx, space.ID, "", actor, actorID, "leave", nil)
		if wantsJSON(r) {
			writeJSON(w, http.StatusOK, map[string]string{"op": "leave", "status": "left"})
			return
		}
		http.Redirect(w, r, "/app/"+space.Slug, http.StatusSeeOther)

	case OpJoinTeam:
		nodeID := r.FormValue("node_id")
		if nodeID == "" {
			http.Error(w, "node_id required", http.StatusBadRequest)
			return
		}
		if actorID == "" {
			http.Error(w, "must be logged in to join a team", http.StatusUnauthorized)
			return
		}
		if !h.store.IsMember(ctx, space.ID, actorID) {
			http.Error(w, "must be a space member to join a team", http.StatusForbidden)
			return
		}
		if err := h.store.JoinNodeMember(ctx, nodeID, actorID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h.store.RecordOp(ctx, space.ID, nodeID, actor, actorID, OpJoinTeam, nil)
		if wantsJSON(r) {
			writeJSON(w, http.StatusOK, map[string]string{"op": OpJoinTeam, "status": "joined"})
			return
		}
		http.Redirect(w, r, "/app/"+space.Slug+"/teams", http.StatusSeeOther)

	case OpLeaveTeam:
		nodeID := r.FormValue("node_id")
		if nodeID == "" {
			http.Error(w, "node_id required", http.StatusBadRequest)
			return
		}
		if actorID == "" {
			http.Error(w, "must be logged in to leave a team", http.StatusUnauthorized)
			return
		}
		// Self or space owner can remove.
		targetID := r.FormValue("user_id")
		if targetID == "" {
			targetID = actorID
		}
		if targetID != actorID && space.OwnerID != actorID {
			http.Error(w, "only space owner can remove others from a team", http.StatusForbidden)
			return
		}
		if err := h.store.LeaveNodeMember(ctx, nodeID, targetID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h.store.RecordOp(ctx, space.ID, nodeID, actor, actorID, OpLeaveTeam, nil)
		if wantsJSON(r) {
			writeJSON(w, http.StatusOK, map[string]string{"op": OpLeaveTeam, "status": "left"})
			return
		}
		http.Redirect(w, r, "/app/"+space.Slug+"/teams", http.StatusSeeOther)

	case "kick":
		memberID := r.FormValue("member_id")
		if memberID == "" {
			http.Error(w, "member_id required", http.StatusBadRequest)
			return
		}
		if space.OwnerID != actorID {
			http.Error(w, "only space owner can remove members", http.StatusForbidden)
			return
		}
		if memberID == actorID {
			http.Error(w, "cannot remove yourself", http.StatusBadRequest)
			return
		}
		if err := h.store.LeaveSpace(ctx, space.ID, memberID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h.store.RecordOp(ctx, space.ID, "", actor, actorID, "kick", nil)
		if wantsJSON(r) {
			writeJSON(w, http.StatusOK, map[string]string{"op": "kick"})
			return
		}
		http.Redirect(w, r, "/app/"+space.Slug+"/settings", http.StatusSeeOther)

	case "report":
		nodeID := r.FormValue("node_id")
		reason := strings.TrimSpace(r.FormValue("reason"))
		if nodeID == "" {
			http.Error(w, "node_id required", http.StatusBadRequest)
			return
		}
		if reason == "" {
			reason = "flagged by user"
		}
		payload, _ := json.Marshal(map[string]string{"reason": reason})
		h.store.RecordOp(ctx, space.ID, nodeID, actor, actorID, "report", payload)

		if wantsJSON(r) {
			writeJSON(w, http.StatusOK, map[string]string{"op": "report", "status": "recorded"})
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/app/%s/node/%s", space.Slug, nodeID), http.StatusSeeOther)

	case "resolve":
		nodeID := r.FormValue("node_id")
		action := r.FormValue("action") // "dismiss" or "remove"
		if nodeID == "" {
			http.Error(w, "node_id required", http.StatusBadRequest)
			return
		}
		// Only space owner can resolve reports.
		if space.OwnerID != actorID {
			http.Error(w, "only space owner can resolve reports", http.StatusForbidden)
			return
		}
		payload, _ := json.Marshal(map[string]string{"action": action})
		h.store.RecordOp(ctx, space.ID, nodeID, actor, actorID, "resolve", payload)

		if action == "remove" {
			h.store.DeleteNode(ctx, nodeID)
		}

		if wantsJSON(r) {
			writeJSON(w, http.StatusOK, map[string]string{"op": "resolve", "action": action})
			return
		}
		http.Redirect(w, r, "/app/"+space.Slug+"/settings", http.StatusSeeOther)

	case "depend":
		nodeID := r.FormValue("node_id")
		dependsOn := r.FormValue("depends_on")
		if nodeID == "" || dependsOn == "" {
			http.Error(w, "node_id and depends_on required", http.StatusBadRequest)
			return
		}
		if err := h.store.AddDependency(ctx, nodeID, dependsOn); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h.store.RecordOp(ctx, space.ID, nodeID, actor, actorID, "depend", nil)

		if wantsJSON(r) {
			node, _ := h.store.GetNode(ctx, nodeID)
			writeJSON(w, http.StatusOK, map[string]any{"node": node, "op": "depend"})
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/app/%s/node/%s", space.Slug, nodeID), http.StatusSeeOther)

	case "undepend":
		nodeID := r.FormValue("node_id")
		dependsOn := r.FormValue("depends_on")
		if nodeID == "" || dependsOn == "" {
			http.Error(w, "node_id and depends_on required", http.StatusBadRequest)
			return
		}
		if err := h.store.RemoveDependency(ctx, nodeID, dependsOn); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if wantsJSON(r) {
			writeJSON(w, http.StatusOK, map[string]string{"op": "undepend"})
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/app/%s/node/%s", space.Slug, nodeID), http.StatusSeeOther)

	case "assert":
		title := strings.TrimSpace(r.FormValue("title"))
		body := strings.TrimSpace(r.FormValue("body"))
		if title == "" {
			http.Error(w, "title required", http.StatusBadRequest)
			return
		}
		var causes []string
		if causesStr := r.FormValue("causes"); causesStr != "" {
			for _, c := range strings.Split(causesStr, ",") {
				if c = strings.TrimSpace(c); c != "" {
					causes = append(causes, c)
				}
			}
		}
		node, err := h.store.CreateNode(ctx, CreateNodeParams{
			SpaceID:    space.ID,
			Kind:       KindClaim,
			Title:      title,
			Body:       body,
			State:      ClaimClaimed,
			Author:     actor,
			AuthorID:   actorID,
			AuthorKind: actorKind,
			Causes:     causes,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h.store.RecordOp(ctx, space.ID, node.ID, actor, actorID, "assert", nil)

		if wantsJSON(r) {
			writeJSON(w, http.StatusCreated, map[string]any{"node": node, "op": "assert"})
			return
		}
		if isHTMX(r) {
			KnowledgeCard(*node, space.Slug, 0).Render(ctx, w)
			return
		}
		http.Redirect(w, r, "/app/"+space.Slug+"/knowledge", http.StatusSeeOther)

	case "challenge":
		nodeID := r.FormValue("node_id")
		reason := strings.TrimSpace(r.FormValue("reason"))
		if nodeID == "" {
			http.Error(w, "node_id required", http.StatusBadRequest)
			return
		}
		// Only claims can be challenged.
		node, err := h.store.GetNode(ctx, nodeID)
		if err != nil || node == nil {
			http.NotFound(w, r)
			return
		}
		if node.Kind != KindClaim {
			http.Error(w, "can only challenge claims", http.StatusBadRequest)
			return
		}
		if reason == "" {
			reason = "challenged"
		}
		payload, _ := json.Marshal(map[string]string{"reason": reason})
		op, _ := h.store.RecordOp(ctx, space.ID, nodeID, actor, actorID, "challenge", payload)

		// Notify claim author.
		if node.AuthorID != actorID && op != nil {
			h.notify(ctx, node.AuthorID, actor, op.ID, space.ID, "challenged your claim: "+node.Title)
		}

		// Update claim state to challenged.
		if err := h.store.UpdateNodeState(ctx, nodeID, ClaimChallenged); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if wantsJSON(r) {
			writeJSON(w, http.StatusOK, map[string]string{"op": "challenge", "status": "recorded"})
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/app/%s/node/%s", space.Slug, nodeID), http.StatusSeeOther)

	case "verify":
		nodeID := r.FormValue("node_id")
		reason := strings.TrimSpace(r.FormValue("reason"))
		if nodeID == "" {
			http.Error(w, "node_id required", http.StatusBadRequest)
			return
		}
		node, err := h.store.GetNode(ctx, nodeID)
		if err != nil || node == nil {
			http.NotFound(w, r)
			return
		}
		if node.Kind != KindClaim {
			http.Error(w, "can only verify claims", http.StatusBadRequest)
			return
		}
		if err := h.store.UpdateNodeState(ctx, nodeID, ClaimVerified); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var payload json.RawMessage
		if reason != "" {
			payload, _ = json.Marshal(map[string]string{"reason": reason})
		}
		op, _ := h.store.RecordOp(ctx, space.ID, nodeID, actor, actorID, "verify", payload)
		if node.AuthorID != actorID && op != nil {
			h.notify(ctx, node.AuthorID, actor, op.ID, space.ID, "verified your claim: "+node.Title)
		}
		if wantsJSON(r) {
			writeJSON(w, http.StatusOK, map[string]string{"op": "verify"})
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/app/%s/node/%s", space.Slug, nodeID), http.StatusSeeOther)

	case "retract":
		nodeID := r.FormValue("node_id")
		reason := strings.TrimSpace(r.FormValue("reason"))
		if nodeID == "" {
			http.Error(w, "node_id required", http.StatusBadRequest)
			return
		}
		node, err := h.store.GetNode(ctx, nodeID)
		if err != nil || node == nil {
			http.NotFound(w, r)
			return
		}
		if node.Kind != KindClaim {
			http.Error(w, "can only retract claims", http.StatusBadRequest)
			return
		}
		if err := h.store.UpdateNodeState(ctx, nodeID, ClaimRetracted); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var payload json.RawMessage
		if reason != "" {
			payload, _ = json.Marshal(map[string]string{"reason": reason})
		}
		op, _ := h.store.RecordOp(ctx, space.ID, nodeID, actor, actorID, "retract", payload)
		if node.AuthorID != actorID && op != nil {
			h.notify(ctx, node.AuthorID, actor, op.ID, space.ID, "retracted your claim: "+node.Title)
		}
		if wantsJSON(r) {
			writeJSON(w, http.StatusOK, map[string]string{"op": "retract"})
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/app/%s/node/%s", space.Slug, nodeID), http.StatusSeeOther)

	case "reflect":
		body := strings.TrimSpace(r.FormValue("body"))
		if body == "" {
			http.Error(w, "body required", http.StatusBadRequest)
			return
		}
		node, err := h.store.CreateNode(ctx, CreateNodeParams{
			SpaceID:    space.ID,
			Kind:       KindPost,
			Title:      "Reflection",
			Body:       body,
			Author:     actor,
			AuthorID:   actorID,
			AuthorKind: actorKind,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h.store.RecordOp(ctx, space.ID, node.ID, actor, actorID, "reflect", nil)
		if wantsJSON(r) {
			writeJSON(w, http.StatusCreated, map[string]any{"node": node, "op": "reflect"})
			return
		}
		http.Redirect(w, r, "/app/"+space.Slug+"/feed", http.StatusSeeOther)

	case "edit":
		nodeID := r.FormValue("node_id")
		body := strings.TrimSpace(r.FormValue("body"))
		causesStr := r.FormValue("causes")
		if nodeID == "" || (body == "" && causesStr == "") {
			http.Error(w, "node_id and body or causes required", http.StatusBadRequest)
			return
		}
		// Only the author can edit.
		node, err := h.store.GetNode(ctx, nodeID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if node.AuthorID != actorID {
			http.Error(w, "only the author can edit", http.StatusForbidden)
			return
		}
		if body != "" {
			oldBody := node.Body
			if err := h.store.EditNodeBody(ctx, nodeID, body); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			h.store.RecordOp(ctx, space.ID, nodeID, actor, actorID, "edit", json.RawMessage(`{"old_body":`+strconv.Quote(oldBody)+`}`))
		}
		if causesStr != "" {
			var causes []string
			for _, c := range strings.Split(causesStr, ",") {
				if c = strings.TrimSpace(c); c != "" {
					causes = append(causes, c)
				}
			}
			if err := h.store.UpdateNodeCauses(ctx, nodeID, causes); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			meta, _ := json.Marshal(map[string]string{"causes": causesStr})
			h.store.RecordOp(ctx, space.ID, nodeID, actor, actorID, "edit", json.RawMessage(meta))
		}
		if wantsJSON(r) {
			writeJSON(w, http.StatusOK, map[string]string{"op": "edit"})
			return
		}
		http.Redirect(w, r, r.Header.Get("Referer"), http.StatusSeeOther)

	case "delete":
		nodeID := r.FormValue("node_id")
		if nodeID == "" {
			http.Error(w, "node_id required", http.StatusBadRequest)
			return
		}
		// Only the author can delete.
		node, err := h.store.GetNode(ctx, nodeID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if node.AuthorID != actorID {
			http.Error(w, "only the author can delete", http.StatusForbidden)
			return
		}
		if err := h.store.SoftDeleteNode(ctx, nodeID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h.store.RecordOp(ctx, space.ID, nodeID, actor, actorID, "delete", json.RawMessage(`{"old_body":`+strconv.Quote(node.Body)+`}`))
		if wantsJSON(r) {
			writeJSON(w, http.StatusOK, map[string]string{"op": "delete"})
			return
		}
		http.Redirect(w, r, r.Header.Get("Referer"), http.StatusSeeOther)

	case "react":
		nodeID := r.FormValue("node_id")
		emoji := r.FormValue("emoji")
		if nodeID == "" || emoji == "" {
			http.Error(w, "node_id and emoji required", http.StatusBadRequest)
			return
		}
		added, err := h.store.ToggleReaction(ctx, nodeID, actorID, emoji)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if added {
			h.store.RecordOp(ctx, space.ID, nodeID, actor, actorID, "react", json.RawMessage(`{"emoji":"`+emoji+`"}`))
		}
		if wantsJSON(r) {
			writeJSON(w, http.StatusOK, map[string]any{"op": "react", "added": added, "emoji": emoji})
			return
		}
		if isHTMX(r) {
			reactions := h.store.GetNodeReactions(ctx, nodeID)
			reactionBar(nodeID, space.Slug, reactions, actorID).Render(ctx, w)
			return
		}
		http.Redirect(w, r, r.Header.Get("Referer"), http.StatusSeeOther)

	case "endorse":
		nodeID := r.FormValue("node_id")
		if nodeID == "" {
			http.Error(w, "node_id required", http.StatusBadRequest)
			return
		}
		// Toggle: endorse if not endorsed, unendorse if already endorsed.
		endorsed := h.store.HasEndorsed(ctx, actorID, nodeID)
		if endorsed {
			h.store.Unendorse(ctx, actorID, nodeID)
		} else {
			h.store.Endorse(ctx, actorID, nodeID)
			h.store.RecordOp(ctx, space.ID, nodeID, actor, actorID, "endorse", nil)
			// Notify the post author.
			if node, err := h.store.GetNode(ctx, nodeID); err == nil && node.AuthorID != actorID {
				h.notify(ctx, node.AuthorID, actor, nodeID, space.ID, "endorsed your post")
			}
		}
		if wantsJSON(r) {
			writeJSON(w, http.StatusOK, map[string]any{"op": "endorse", "endorsed": !endorsed})
			return
		}
		if isHTMX(r) {
			count := h.store.CountEndorsements(ctx, nodeID)
			endorseButton(nodeID, space.Slug, count, !endorsed).Render(ctx, w)
			return
		}
		http.Redirect(w, r, r.Header.Get("Referer"), http.StatusSeeOther)

	case "repost":
		nodeID := r.FormValue("node_id")
		if nodeID == "" {
			http.Error(w, "node_id required", http.StatusBadRequest)
			return
		}
		reposted := h.store.HasReposted(ctx, actorID, nodeID)
		if reposted {
			h.store.Unrepost(ctx, actorID, nodeID)
		} else {
			h.store.Repost(ctx, actorID, nodeID)
			h.store.RecordOp(ctx, space.ID, nodeID, actor, actorID, "repost", nil)
			if node, err := h.store.GetNode(ctx, nodeID); err == nil && node.AuthorID != actorID {
				h.notify(ctx, node.AuthorID, actor, nodeID, space.ID, "reposted your post")
			}
		}
		if wantsJSON(r) {
			writeJSON(w, http.StatusOK, map[string]any{"op": "repost", "reposted": !reposted})
			return
		}
		if isHTMX(r) {
			counts := h.store.GetBulkRepostCounts(ctx, []string{nodeID})
			repostButton(nodeID, space.Slug, counts[nodeID], !reposted).Render(ctx, w)
			return
		}
		http.Redirect(w, r, r.Header.Get("Referer"), http.StatusSeeOther)

	case "pin":
		nodeID := r.FormValue("node_id")
		if nodeID == "" {
			http.Error(w, "node_id required", http.StatusBadRequest)
			return
		}
		if err := h.store.SetPinned(ctx, nodeID, true); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h.store.RecordOp(ctx, space.ID, nodeID, actor, actorID, "pin", nil)
		if wantsJSON(r) {
			writeJSON(w, http.StatusOK, map[string]string{"op": "pin"})
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/app/%s/node/%s", space.Slug, nodeID), http.StatusSeeOther)

	case "unpin":
		nodeID := r.FormValue("node_id")
		if nodeID == "" {
			http.Error(w, "node_id required", http.StatusBadRequest)
			return
		}
		if err := h.store.SetPinned(ctx, nodeID, false); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h.store.RecordOp(ctx, space.ID, nodeID, actor, actorID, "unpin", nil)
		if wantsJSON(r) {
			writeJSON(w, http.StatusOK, map[string]string{"op": "unpin"})
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/app/%s/node/%s", space.Slug, nodeID), http.StatusSeeOther)

	case "propose":
		title := strings.TrimSpace(r.FormValue("title"))
		if title == "" {
			http.Error(w, "title required", http.StatusBadRequest)
			return
		}
		params := CreateNodeParams{
			SpaceID:    space.ID,
			Kind:       KindProposal,
			Title:      title,
			Body:       strings.TrimSpace(r.FormValue("description")),
			State:      ProposalOpen,
			Author:     actor,
			AuthorID:   actorID,
			AuthorKind: actorKind,
		}
		if dl := r.FormValue("deadline"); dl != "" {
			if t, err := time.Parse("2006-01-02", dl); err == nil {
				params.DueDate = &t
			}
		}
		node, err := h.store.CreateNode(ctx, params)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Optional quorum configuration.
		if qp := r.FormValue("quorum_pct"); qp != "" {
			var pct int
			if _, err := fmt.Sscanf(qp, "%d", &pct); err == nil && pct > 0 && pct <= 100 {
				vb := r.FormValue("voting_body")
				if vb == "" {
					vb = VotingBodyAll
				}
				h.store.SetProposalConfig(ctx, node.ID, pct, vb)
			}
		}
		h.store.RecordOp(ctx, space.ID, node.ID, actor, actorID, "propose", nil)

		if wantsJSON(r) {
			writeJSON(w, http.StatusCreated, map[string]any{"node": node, "op": "propose"})
			return
		}
		http.Redirect(w, r, "/app/"+space.Slug+"/governance", http.StatusSeeOther)

	case "vote":
		nodeID := r.FormValue("node_id")
		vote := r.FormValue("vote") // "yes" or "no"
		if nodeID == "" || (vote != "yes" && vote != "no") {
			http.Error(w, "node_id and vote (yes/no) required", http.StatusBadRequest)
			return
		}
		// Only proposals can be voted on.
		node, err := h.store.GetNode(ctx, nodeID)
		if err != nil || node == nil {
			http.NotFound(w, r)
			return
		}
		if node.Kind != KindProposal {
			http.Error(w, "can only vote on proposals", http.StatusBadRequest)
			return
		}
		if node.State != ProposalOpen {
			http.Error(w, "proposal is no longer open", http.StatusBadRequest)
			return
		}
		// Delegated users cannot vote directly — they must undelegate first.
		if h.store.HasDelegated(ctx, space.ID, actorID) {
			http.Error(w, "you have delegated your vote; undelegate before voting directly", http.StatusConflict)
			return
		}
		// One vote per user per proposal.
		if h.store.HasVoted(ctx, nodeID, actorID) {
			http.Error(w, "already voted", http.StatusConflict)
			return
		}
		payload, _ := json.Marshal(map[string]string{"vote": vote})
		op, _ := h.store.RecordOp(ctx, space.ID, nodeID, actor, actorID, "vote", payload)

		// Notify proposal author.
		if node.AuthorID != actorID && op != nil {
			h.notify(ctx, node.AuthorID, actor, op.ID, space.ID, "voted "+vote+" on your proposal: "+node.Title)
		}

		// Auto-close proposal if quorum is now met.
		h.store.CheckAndAutoCloseProposal(ctx, space.ID, nodeID)

		if wantsJSON(r) {
			writeJSON(w, http.StatusOK, map[string]string{"op": "vote", "vote": vote})
			return
		}
		http.Redirect(w, r, "/app/"+space.Slug+"/governance", http.StatusSeeOther)

	case "progress":
		// Submit work for review: active → review, with optional progress note.
		nodeID := r.FormValue("node_id")
		body := strings.TrimSpace(r.FormValue("body"))
		if nodeID == "" {
			http.Error(w, "node_id required", http.StatusBadRequest)
			return
		}
		node, err := h.store.GetNode(ctx, nodeID)
		if err != nil || node == nil {
			http.NotFound(w, r)
			return
		}
		if node.Kind != KindTask {
			http.Error(w, "progress op only valid for tasks", http.StatusBadRequest)
			return
		}
		if node.State != StateActive {
			http.Error(w, "task must be in active state to submit for review", http.StatusBadRequest)
			return
		}
		if err := h.store.UpdateNodeState(ctx, nodeID, StateReview); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var progressPayload []byte
		if body != "" {
			progressPayload, _ = json.Marshal(map[string]string{"body": body})
		}
		op, _ := h.store.RecordOp(ctx, space.ID, nodeID, actor, actorID, "progress", progressPayload)
		// Notify task author that work is ready for review.
		if op != nil && node.AuthorID != actorID {
			h.notify(ctx, node.AuthorID, actor, op.ID, space.ID, "submitted for review: "+node.Title)
		}
		if wantsJSON(r) {
			node, _ := h.store.GetNode(ctx, nodeID)
			writeJSON(w, http.StatusOK, map[string]any{"node": node, "op": "progress"})
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/app/%s/node/%s", space.Slug, nodeID), http.StatusSeeOther)

	case "review":
		// Structured review with verdict: approve / revise / reject.
		nodeID := r.FormValue("node_id")
		verdict := r.FormValue("verdict") // "approve", "revise", "reject"
		body := strings.TrimSpace(r.FormValue("body"))
		if nodeID == "" || (verdict != "approve" && verdict != "revise" && verdict != "reject") {
			http.Error(w, "node_id and verdict (approve/revise/reject) required", http.StatusBadRequest)
			return
		}
		node, err := h.store.GetNode(ctx, nodeID)
		if err != nil || node == nil {
			http.NotFound(w, r)
			return
		}
		if node.Kind != KindTask {
			http.Error(w, "review op only valid for tasks", http.StatusBadRequest)
			return
		}
		if node.State != StateReview {
			http.Error(w, "task must be in review state", http.StatusBadRequest)
			return
		}
		var newState string
		switch verdict {
		case "approve":
			newState = StateDone
		case "revise":
			newState = StateActive
		default: // "reject"
			newState = StateClosed
		}
		if err := h.store.UpdateNodeState(ctx, nodeID, newState); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		reviewPayload, _ := json.Marshal(map[string]string{"verdict": verdict, "body": body})
		op, _ := h.store.RecordOp(ctx, space.ID, nodeID, actor, actorID, "review", reviewPayload)
		// Notify assignee of review verdict.
		if op != nil && node.AssigneeID != "" && node.AssigneeID != actorID {
			var msg string
			switch verdict {
			case "approve":
				msg = "approved your task: " + node.Title
			case "revise":
				msg = "requested revisions on: " + node.Title
			default:
				msg = "rejected your task: " + node.Title
			}
			h.notify(ctx, node.AssigneeID, actor, op.ID, space.ID, msg)
		}
		// Recompute reputation for the task's assignee (the worker being reviewed).
		if node.AssigneeID != "" {
			h.store.ComputeAndUpdateReputation(ctx, node.AssigneeID)
		}

		if wantsJSON(r) {
			node, _ := h.store.GetNode(ctx, nodeID)
			writeJSON(w, http.StatusOK, map[string]any{"node": node, "op": "review", "verdict": verdict})
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/app/%s/node/%s", space.Slug, nodeID), http.StatusSeeOther)

	case "close_proposal":
		nodeID := r.FormValue("node_id")
		outcome := r.FormValue("outcome") // "passed" or "rejected"
		if nodeID == "" || (outcome != "passed" && outcome != "rejected") {
			http.Error(w, "node_id and outcome (passed/rejected) required", http.StatusBadRequest)
			return
		}
		// Only space owner can close proposals.
		if space.OwnerID != actorID {
			http.Error(w, "only space owner can close proposals", http.StatusForbidden)
			return
		}
		node, err := h.store.GetNode(ctx, nodeID)
		if err != nil || node == nil {
			http.NotFound(w, r)
			return
		}
		if node.Kind != KindProposal {
			http.Error(w, "can only close proposals", http.StatusBadRequest)
			return
		}
		newState := ProposalPassed
		if outcome == "rejected" {
			newState = ProposalFailed
		}
		if err := h.store.UpdateNodeState(ctx, nodeID, newState); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		payload, _ := json.Marshal(map[string]string{"outcome": outcome})
		op, _ := h.store.RecordOp(ctx, space.ID, nodeID, actor, actorID, "close_proposal", payload)

		// Notify proposal author.
		if node.AuthorID != actorID && op != nil {
			h.notify(ctx, node.AuthorID, actor, op.ID, space.ID, outcome+" your proposal: "+node.Title)
		}

		if wantsJSON(r) {
			writeJSON(w, http.StatusOK, map[string]string{"op": "close_proposal", "outcome": outcome})
			return
		}
		http.Redirect(w, r, "/app/"+space.Slug+"/governance", http.StatusSeeOther)

	case "delegate":
		delegateID := strings.TrimSpace(r.FormValue("delegate_id"))
		if delegateID == "" {
			http.Error(w, "delegate_id required", http.StatusBadRequest)
			return
		}
		if err := h.store.Delegate(ctx, space.ID, actorID, delegateID); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		payload, _ := json.Marshal(map[string]string{"delegate_id": delegateID})
		h.store.RecordOp(ctx, space.ID, "", actor, actorID, OpDelegate, payload)
		if wantsJSON(r) {
			writeJSON(w, http.StatusOK, map[string]string{"op": OpDelegate, "delegate_id": delegateID})
			return
		}
		http.Redirect(w, r, "/app/"+space.Slug+"/governance", http.StatusSeeOther)

	case "undelegate":
		if err := h.store.Undelegate(ctx, space.ID, actorID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h.store.RecordOp(ctx, space.ID, "", actor, actorID, OpUndelegate, nil)
		if wantsJSON(r) {
			writeJSON(w, http.StatusOK, map[string]string{"op": OpUndelegate})
			return
		}
		http.Redirect(w, r, "/app/"+space.Slug+"/governance", http.StatusSeeOther)

	case "convene":
		title := strings.TrimSpace(r.FormValue("title"))
		body := strings.TrimSpace(r.FormValue("body"))
		if title == "" {
			http.Error(w, "title required", http.StatusBadRequest)
			return
		}
		// Resolve agent IDs from the agents field. Browser forms send one value per
		// selected dropdown option; older clients may still send comma-separated text.
		var agentIDs []string
		for _, raw := range r.Form["agents"] {
			if a := strings.TrimSpace(raw); a != "" {
				for _, name := range strings.Split(a, ",") {
					name = strings.TrimSpace(name)
					if name == "" {
						continue
					}
					if resolved := h.store.ResolveUserID(ctx, name); resolved != "" {
						agentIDs = append(agentIDs, resolved)
					}
					// Unresolvable names are skipped — storing a display name where an ID
					// is required violates invariant 11 (IDENTITY).
				}
			}
		}
		node, err := h.store.CreateNode(ctx, CreateNodeParams{
			SpaceID:    space.ID,
			Kind:       KindCouncil,
			Title:      title,
			Body:       body,
			Author:     actor,
			AuthorID:   actorID,
			AuthorKind: actorKind,
			Tags:       agentIDs,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h.store.RecordOp(ctx, space.ID, node.ID, actor, actorID, "convene", nil)

		// Trigger Mind to call each tagged agent persona asynchronously.
		if h.mind != nil {
			go h.mind.OnCouncilConvened(space.ID, space.Slug, node)
		}

		if wantsJSON(r) {
			writeJSON(w, http.StatusCreated, map[string]any{"node": node, "op": "convene"})
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/app/%s/council/%s", space.Slug, node.ID), http.StatusSeeOther)

	default:
		http.Error(w, fmt.Sprintf("unknown op: %s", op), http.StatusBadRequest)
	}
}

func safeLocalReturn(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return ""
	}
	return path
}

// ────────────────────────────────────────────────────────────────────
// Node mutation handlers (non-op convenience routes)
// ────────────────────────────────────────────────────────────────────

func (h *Handlers) handleNodeState(w http.ResponseWriter, r *http.Request) {
	populateFormFromJSON(r)
	space, err := h.spaceFromRequest(r)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	nodeID := r.PathValue("id")
	newState := r.FormValue("state")

	node, err := h.store.GetNode(r.Context(), nodeID)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := h.store.UpdateNodeState(r.Context(), nodeID, newState); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Record as appropriate grammar op.
	opName := "progress"
	if newState == StateDone {
		opName = "complete"
	} else if newState == StateReview {
		opName = "review"
	}
	op, _ := h.store.RecordOp(r.Context(), space.ID, nodeID, h.userName(r), h.userID(r), opName, nil)

	// Notify assignee on state change.
	uid := h.userID(r)
	if op != nil && node != nil && node.AssigneeID != "" && node.AssigneeID != uid {
		h.notify(r.Context(), node.AssigneeID, h.userName(r), op.ID, space.ID, opName+" task: "+node.Title)
	}

	// Track first completion in this space.
	isFirstCompletion := false
	if newState == StateDone {
		isFirstCompletion, _ = h.store.MarkFirstCompletion(r.Context(), space.ID)
	}

	node, _ = h.store.GetNode(r.Context(), nodeID)
	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{"node": node, "op": opName, "first_completion": isFirstCompletion})
		return
	}
	if isHTMX(r) && node != nil {
		TaskCard(*node, space.Slug).Render(r.Context(), w)
		return
	}
	boardURL := "/app/" + space.Slug + "/board"
	if isFirstCompletion {
		boardURL += "?first_completion=1"
	}
	http.Redirect(w, r, boardURL, http.StatusSeeOther)
}

func (h *Handlers) handleNodeUpdate(w http.ResponseWriter, r *http.Request) {
	populateFormFromJSON(r)
	space, err := h.spaceFromRequest(r)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	nodeID := r.PathValue("id")

	var title, body, priority, assignee *string
	if v := r.FormValue("title"); v != "" {
		title = &v
	}
	if r.Form != nil && r.Form.Has("body") {
		v := r.FormValue("body")
		body = &v
	}
	if v := r.FormValue("priority"); v != "" {
		priority = &v
	}
	var assigneeIDPtr *string
	if r.Form != nil && r.Form.Has("assignee") {
		v := r.FormValue("assignee")
		assignee = &v
		aid := h.store.ResolveUserID(r.Context(), v)
		assigneeIDPtr = &aid
	}

	if err := h.store.UpdateNode(r.Context(), nodeID, title, body, priority, assignee, assigneeIDPtr); err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	node, _ := h.store.GetNode(r.Context(), nodeID)
	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{"node": node})
		return
	}
	if isHTMX(r) && node != nil {
		TaskCard(*node, space.Slug).Render(r.Context(), w)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/app/%s/node/%s", space.Slug, nodeID), http.StatusSeeOther)
}

func (h *Handlers) handleNodeDelete(w http.ResponseWriter, r *http.Request) {
	space, err := h.spaceFromRequest(r)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	nodeID := r.PathValue("id")
	if err := h.store.DeleteNode(r.Context(), nodeID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{"deleted": nodeID})
		return
	}
	if isHTMX(r) {
		w.Header().Set("HX-Redirect", "/app/"+space.Slug+"/board")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/app/"+space.Slug+"/board", http.StatusSeeOther)
}

// ────────────────────────────────────────────────────────────────────
// Board helpers
// ────────────────────────────────────────────────────────────────────

// BoardColumn holds nodes grouped by state for the kanban board.
type BoardColumn struct {
	State string
	Label string
	Nodes []Node
}

func groupByState(nodes []Node) []BoardColumn {
	columns := []BoardColumn{
		{State: StateOpen, Label: "Open"},
		{State: StateActive, Label: "Active"},
		{State: StateBlocked, Label: "Blocked"},
		{State: StateReview, Label: "Review"},
		{State: StateDone, Label: "Done"},
	}
	byState := map[string]*BoardColumn{}
	for i := range columns {
		byState[columns[i].State] = &columns[i]
	}
	for _, n := range nodes {
		if col, ok := byState[n.State]; ok {
			col.Nodes = append(col.Nodes, n)
		}
	}
	// Done column: newest completions first (updated_at DESC).
	if done, ok := byState[StateDone]; ok {
		sort.Slice(done.Nodes, func(i, j int) bool {
			return done.Nodes[i].UpdatedAt.After(done.Nodes[j].UpdatedAt)
		})
	}
	return columns
}

// Member holds aggregated activity data for the People lens.
type Member struct {
	Name     string
	Kind     string // "human" or "agent"
	OpCount  int
	LastSeen string
}

// handleNodeChildren returns child nodes for HTMX polling.
func (h *Handlers) handleNodeChildren(w http.ResponseWriter, r *http.Request) {
	space, _, err := h.spaceForRead(r)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	nodeID := r.PathValue("id")
	children, err := h.store.ListNodes(r.Context(), ListNodesParams{
		SpaceID:  space.ID,
		ParentID: nodeID,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{"children": children})
		return
	}

	// Render children as HTML fragments for HTMX.
	for _, child := range children {
		if child.Kind == KindComment {
			CommentItem(child).Render(r.Context(), w)
		} else {
			childRow(child, space.Slug).Render(r.Context(), w)
		}
	}
}

// handleSetMindState sets a key-value pair for the Mind's context.
// PUT /api/mind-state with JSON body {"key": "...", "value": "..."}.
func (h *Handlers) handleSetMindState(w http.ResponseWriter, r *http.Request) {
	populateFormFromJSON(r)
	key := strings.TrimSpace(r.FormValue("key"))
	value := r.FormValue("value")
	if key == "" {
		http.Error(w, "key required", http.StatusBadRequest)
		return
	}
	if err := h.store.SetMindState(r.Context(), key, value); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"key": key, "status": "ok"})
}

// maxHivePosts is the upper bound on posts fetched and processed by the /hive
// dashboard. Invariant 13 (BOUNDED): every operation has defined scope.
const maxHivePosts = 20

// activeRoleThreshold is the window within which a pipeline role is considered
// active. A post within this window sets the role's Active = true.
const activeRoleThreshold = 30 * time.Minute

// HiveStats holds aggregated metrics parsed from hive agent post bodies.
type HiveStats struct {
	Features  int
	TotalCost float64
	AvgCost   float64
}

var (
	reCost     = regexp.MustCompile(`\$(\d+\.\d+)`)
	reDuration = regexp.MustCompile(`Duration:\s*(\d+m(?:\d+s)?)`)
)

// parseCostDollars extracts the first dollar amount from a post body (e.g. "Cost: $0.53").
func parseCostDollars(body string) float64 {
	m := reCost.FindStringSubmatch(body)
	if m == nil {
		return 0
	}
	v, _ := strconv.ParseFloat(m[1], 64)
	return v
}

// parseDurationStr extracts the duration string from a post body (e.g. "Duration: 3m28s" → "3m28s").
func parseDurationStr(body string) string {
	m := reDuration.FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return m[1]
}

// hiveCostStr returns a formatted cost string for a node's body, e.g. "$0.42".
// Returns empty string if no cost is found or cost is zero.
func hiveCostStr(n Node) string {
	c := parseCostDollars(n.Body)
	if c <= 0 {
		return ""
	}
	return fmt.Sprintf("$%.2f", c)
}

// hiveDurationStr returns the duration string from a node's body, e.g. "3m28s".
// Returns empty string if no duration is found.
func hiveDurationStr(n Node) string {
	return parseDurationStr(n.Body)
}

// computeHiveStats aggregates cost metrics across hive agent posts.
// posts must be pre-bounded (callers must pass at most maxHivePosts entries).
// Features counts only posts that include a cost line (cost > 0); posts without
// a cost entry are excluded from all three metrics. This is intentional: a post
// without a cost line is not a verified build iteration.
func computeHiveStats(posts []Node) HiveStats {
	var total float64
	var features int
	for _, p := range posts {
		c := parseCostDollars(p.Body)
		if c > 0 {
			total += c
			features++
		}
	}
	avg := 0.0
	if features > 0 {
		avg = total / float64(features)
	}
	return HiveStats{Features: features, TotalCost: total, AvgCost: avg}
}

// PipelineRole holds display state for a pipeline role on the /hive dashboard.
type PipelineRole struct {
	Name       string    // "Scout", "Builder", "Critic", "Reflector"
	LastActive time.Time // zero = never seen in fetched posts
	Active     bool      // true = post within last 30 minutes
}

// pipelineRoleDefs maps display names to the title prefix used by each role.
var pipelineRoleDefs = []struct {
	display string
	prefix  string
}{
	{"Scout", "[hive:scout]"},
	{"Architect", "[hive:architect]"},
	{"Builder", "[hive:builder]"},
	{"Critic", "[hive:critic]"},
	{"Reflector", "[hive:reflector]"},
}

// computePipelineRoles extracts last-active timestamps for Scout, Builder, Critic, and Reflector
// by scanning post titles for the standard [hive:role] prefix.
func computePipelineRoles(posts []Node) []PipelineRole {
	last := make(map[string]time.Time, len(pipelineRoleDefs))
	for _, p := range posts {
		lower := strings.ToLower(p.Title)
		for _, rd := range pipelineRoleDefs {
			if strings.HasPrefix(lower, rd.prefix) {
				if t, ok := last[rd.display]; !ok || p.CreatedAt.After(t) {
					last[rd.display] = p.CreatedAt
				}
			}
		}
	}
	now := time.Now()
	roles := make([]PipelineRole, len(pipelineRoleDefs))
	for i, rd := range pipelineRoleDefs {
		t := last[rd.display]
		roles[i] = PipelineRole{
			Name:       rd.display,
			LastActive: t,
			Active:     !t.IsZero() && now.Sub(t) < activeRoleThreshold,
		}
	}
	return roles
}

// maxHiveAgentTasks is the upper bound on open tasks shown on the /hive dashboard.
// Invariant 13 (BOUNDED).
const maxHiveAgentTasks = 10

// parseIterFromPosts returns the highest iteration number found in post titles,
// or 0 if none are found. Post titles use the format "[hive:role] iter N: ...".
func parseIterFromPosts(posts []Node) int {
	re := regexp.MustCompile(`\biter\s+(\d+)\b`)
	best := 0
	for _, p := range posts {
		m := re.FindStringSubmatch(strings.ToLower(p.Title))
		if len(m) < 2 {
			continue
		}
		n := 0
		for _, c := range m[1] {
			if c >= '0' && c <= '9' {
				n = n*10 + int(c-'0')
			}
		}
		if n > best {
			best = n
		}
	}
	return best
}

// handleHive renders the public /hive dashboard.
// Reads from the DATABASE first (works on Fly), falls back to local files (dev).
func (h *Handlers) handleHive(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Diagnostics: DB first (works on Fly), fall back to local files (dev).
	entries, _ := h.store.ListHiveDiagnostics(ctx, maxHiveDiagEntries)
	if len(entries) == 0 {
		entries = readDiagnostics(h.loopDir, maxHiveDiagEntries)
	}

	// Local file data (dev only — empty on Fly, and that's fine).
	ls := readLoopState(h.loopDir)
	repoDir := ""
	if h.loopDir != "" {
		repoDir = filepath.Dir(h.loopDir)
	}
	commits := readRecentCommits(repoDir, 10)

	// Supplement with database data so deployed instances show real numbers.
	agentID := h.store.GetHiveAgentID(ctx)
	if agentID != "" {
		totalOps, lastActive, _ := h.store.GetHiveTotals(ctx, agentID)
		posts, _ := h.store.ListHiveActivity(ctx, agentID, maxHivePosts)

		// Iteration count: try "iter N" in post titles, fall back to done task count.
		if ic := parseIterFromPosts(posts); ic > ls.Iteration {
			ls.Iteration = ic
		}
		if ls.Iteration == 0 {
			// Count done tasks as iteration proxy.
			tasks, _ := h.store.ListHiveAgentTasks(ctx, agentID, 1000)
			doneCount := 0
			for _, t := range tasks {
				if t.State == "done" {
					doneCount++
				}
			}
			if doneCount > ls.Iteration {
				ls.Iteration = doneCount
			}
		}

		if totalOps > 0 && ls.Phase == "" {
			ls.Phase = "idle"
		}
		if !lastActive.IsZero() && ls.BuildTitle == "" {
			ls.BuildTitle = "Last active: " + lastActive.Format("2006-01-02 15:04")
		}
	}

	HivePage(ls, entries, commits, h.viewUser(r), profile.FromContext(ctx)).Render(ctx, w)
}

// handleHiveStatus renders the main content partial for HTMX polling (every 5s).
// It re-fetches posts and tasks and returns the #hive-content element only,
// with no surrounding HTML shell.
func (h *Handlers) handleHiveStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	agentID := h.store.GetHiveAgentID(ctx)
	posts, err := h.store.ListHiveActivity(ctx, agentID, maxHivePosts)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	stats := computeHiveStats(posts)
	roles := computePipelineRoles(posts)
	tasks, err := h.store.ListHiveAgentTasks(ctx, agentID, maxHiveAgentTasks)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	totalOps, lastActive, err := h.store.GetHiveTotals(ctx, agentID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	iterCount := parseIterFromPosts(posts)
	ls := readLoopState(h.loopDir)
	if ls.Iteration > iterCount {
		iterCount = ls.Iteration
	}
	p := profile.FromContext(ctx)
	if p == nil {
		p = profile.Default()
	}
	HiveStatusPartial(posts, stats, roles, tasks, totalOps, lastActive, iterCount, ls, p).Render(ctx, w)
}

// handleHiveDiagnostic accepts POST /api/hive/diagnostic from the hive runner.
// Stores the phase event so /hive/feed works in production where loop files are absent.
func (h *Handlers) handleHiveDiagnostic(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	var ev struct {
		Phase   string  `json:"phase"`
		Outcome string  `json:"outcome"`
		CostUSD float64 `json:"cost_usd"`
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.store.AppendHiveDiagnostic(r.Context(), ev.Phase, ev.Outcome, ev.CostUSD, body); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "ok"})
}

// handleHiveSiteOps exposes human-authored site operations for Hive
// reconciliation. It enriches intend ops with the linked node's Markdown body so
// Refinery intake can be used as the kickoff input while Hive is already idle.
func (h *Handlers) handleHiveSiteOps(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	slug := strings.TrimSpace(r.URL.Query().Get("space"))
	if slug == "" {
		http.Error(w, "space required", http.StatusBadRequest)
		return
	}
	space, err := h.store.GetSpaceBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.Error(w, "space not found", http.StatusNotFound)
			return
		}
		http.Error(w, "get space: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if !h.canReadSpace(r, space) && !auth.IsHiveSiteOpsMachine(r.Context()) {
		http.Error(w, "space not found", http.StatusNotFound)
		return
	}

	var since *time.Time
	if raw := strings.TrimSpace(r.URL.Query().Get("since")); raw != "" {
		t, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			http.Error(w, "invalid since: "+err.Error(), http.StatusBadRequest)
			return
		}
		since = &t
	}

	ops, err := h.store.ListOpsSince(ctx, space.ID, since, 100)
	if err != nil {
		http.Error(w, "list ops: "+err.Error(), http.StatusInternalServerError)
		return
	}

	type siteOp struct {
		ID        string          `json:"id"`
		SpaceID   string          `json:"space_id"`
		NodeID    string          `json:"node_id,omitempty"`
		NodeTitle string          `json:"node_title,omitempty"`
		Actor     string          `json:"actor"`
		ActorID   string          `json:"actor_id"`
		ActorKind string          `json:"actor_kind"`
		Op        string          `json:"op"`
		Payload   json.RawMessage `json:"payload"`
		CreatedAt time.Time       `json:"created_at"`
	}

	out := make([]siteOp, 0, len(ops))
	for _, op := range ops {
		out = append(out, siteOp{
			ID:        op.ID,
			SpaceID:   op.SpaceID,
			NodeID:    op.NodeID,
			NodeTitle: op.NodeTitle,
			Actor:     op.Actor,
			ActorID:   op.ActorID,
			ActorKind: op.ActorKind,
			Op:        op.Op,
			Payload:   hiveSiteOpPayload(op),
			CreatedAt: op.CreatedAt,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"ops": out})
}

func hiveSiteOpPayload(op Op) json.RawMessage {
	var payload map[string]any
	if len(op.Payload) > 0 && string(op.Payload) != "{}" {
		_ = json.Unmarshal(op.Payload, &payload)
	}
	if payload == nil {
		payload = map[string]any{}
	}
	if op.Op == "intend" {
		if _, ok := payload["body"]; !ok && op.NodeBody != "" {
			payload["body"] = op.NodeBody
		}
		if _, ok := payload["priority"]; !ok && op.NodePriority != "" {
			payload["priority"] = op.NodePriority
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return data
}

// handleHiveMirror accepts Hive execution updates for Site-originated tasks.
// It stamps the Site node with Hive chain references and records an audit op so
// dashboards can trace the displayed task status back to Hive execution.
func (h *Handlers) handleHiveMirror(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NodeID       string `json:"node_id"`
		HiveTaskID   string `json:"hive_task_id"`
		HiveChainRef string `json:"hive_chain_ref"`
		EventType    string `json:"event_type"`
		State        string `json:"state"`
		Summary      string `json:"summary"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	req.NodeID = strings.TrimSpace(req.NodeID)
	req.HiveTaskID = strings.TrimSpace(req.HiveTaskID)
	req.HiveChainRef = strings.TrimSpace(req.HiveChainRef)
	req.EventType = strings.TrimSpace(req.EventType)
	req.State = strings.TrimSpace(req.State)
	if req.NodeID == "" || req.HiveChainRef == "" {
		http.Error(w, "node_id and hive_chain_ref required", http.StatusBadRequest)
		return
	}
	if req.HiveTaskID == "" {
		req.HiveTaskID = req.HiveChainRef
	}

	ctx := r.Context()
	node, err := h.store.GetNode(ctx, req.NodeID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.Error(w, "node not found", http.StatusNotFound)
			return
		}
		http.Error(w, "get node: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.store.StampNodeHiveMirror(ctx, req.NodeID, req.HiveTaskID, req.HiveChainRef, req.EventType); err != nil {
		if errors.Is(err, ErrNotFound) {
			http.Error(w, "node not found", http.StatusNotFound)
			return
		}
		http.Error(w, "stamp mirror: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if req.State != "" {
		if err := h.store.UpdateNodeState(ctx, req.NodeID, req.State); err != nil {
			http.Error(w, "update node state: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	payload, _ := json.Marshal(req)
	op, err := h.store.RecordOp(ctx, node.SpaceID, req.NodeID, "hive", h.userID(r), "mirror", payload)
	if err != nil {
		http.Error(w, "record mirror op: "+err.Error(), http.StatusInternalServerError)
		return
	}
	opID := ""
	if op != nil {
		opID = op.ID
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status":         "mirrored",
		"node_id":        req.NodeID,
		"hive_task_id":   req.HiveTaskID,
		"hive_chain_ref": req.HiveChainRef,
		"op_id":          opID,
	})
}

// handleHiveEscalation accepts POST /api/hive/escalation from the hive pipeline.
// Sets the task state to "escalated", creates a notification for the space owner,
// and records an escalate op for auditability.
func (h *Handlers) handleHiveEscalation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SpaceSlug  string `json:"space_slug"`
		TaskID     string `json:"task_id"`
		Reason     string `json:"reason"`
		AssigneeID string `json:"assignee_id"` // optional: specific user to notify
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.TaskID == "" || req.SpaceSlug == "" {
		http.Error(w, "task_id and space_slug required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// Set task state to escalated.
	if err := h.store.UpdateNodeState(ctx, req.TaskID, "escalated"); err != nil {
		http.Error(w, "update task state: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Look up the space to find the owner for notification.
	space, err := h.store.GetSpaceBySlug(ctx, req.SpaceSlug)
	if err != nil {
		http.Error(w, "space not found: "+err.Error(), http.StatusNotFound)
		return
	}

	// Record an escalate op for the audit trail.
	payload, _ := json.Marshal(map[string]string{"reason": req.Reason})
	op, _ := h.store.RecordOp(ctx, space.ID, req.TaskID, "hive", h.userID(r), "escalate", payload)

	// Notify the assignee, or fall back to the space owner.
	notifyTarget := req.AssigneeID
	if notifyTarget == "" {
		notifyTarget = space.OwnerID
	}
	opID := ""
	if op != nil {
		opID = op.ID
	}
	h.store.CreateNotification(ctx, notifyTarget, opID, space.ID,
		"Hive ESCALATION: "+req.Reason)

	writeJSON(w, http.StatusOK, map[string]string{"status": "escalated", "task_id": req.TaskID})
}

// handleHiveFeed renders the phase timeline partial for HTMX polling — public, no auth required.
// Reads from the database first (works on Fly); falls back to local files (dev without DB).
func (h *Handlers) handleHiveFeed(w http.ResponseWriter, r *http.Request) {
	entries, _ := h.store.ListHiveDiagnostics(r.Context(), maxHiveDiagEntries)
	if len(entries) == 0 {
		entries = readDiagnostics(h.loopDir, maxHiveDiagEntries)
	}
	HiveDiagFeed(entries).Render(r.Context(), w)
}

// handleHiveStats renders the live stats bar partial for HTMX polling.
func (h *Handlers) handleHiveStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	agentID := h.store.GetHiveAgentID(ctx)
	totalOps, lastActive, err := h.store.GetHiveTotals(ctx, agentID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	HiveStatsBar(totalOps, lastActive).Render(ctx, w)
}

// groundedLabel returns "grounded in N doc(s)" for agent messages that used document context,
// or "" if no documents were used. Tags use the format "grounded:N".
func groundedLabel(tags []string) string {
	for _, t := range tags {
		if strings.HasPrefix(t, "grounded:") {
			n, err := strconv.Atoi(strings.TrimPrefix(t, "grounded:"))
			if err == nil && n > 0 {
				if n == 1 {
					return "grounded in 1 doc"
				}
				return fmt.Sprintf("grounded in %d docs", n)
			}
		}
	}
	return ""
}
