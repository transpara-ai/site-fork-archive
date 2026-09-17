package graph

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	sitepersonas "github.com/transpara-ai/site/graph/personas"
	"github.com/transpara-ai/site/profile"
)

const opsHiveIntakeMaxContentBytes = 20000
const opsHiveLaunchableIntakeListLimit = 100

const (
	opsControlIntentSourceKind    = "control_intent"
	opsMarkdownArtifactSourceKind = "markdown_artifact"
)

const (
	opsPublicProofOperationRepo           = "transpara-ai/operation"
	opsPublicProofOperationPacketCommit   = "7ab3929ff88d0b75c48d53a80e92db74ec523482"
	opsPublicProofOperationPacketID       = "OPERATION-PUBLIC-PROOF-REFERENCE-2026-06-29"
	opsPublicProofOperationPacketRepoPath = "docs/operations/public-proof-evidence/public-proof-reference-2026-06-29.md"
	opsPublicProofOperationPacketPath     = opsPublicProofOperationRepo + "/" + opsPublicProofOperationPacketRepoPath
)

var (
	opsPublicProofOperationPacketGeneratedAt = time.Date(2026, 6, 29, 8, 10, 49, 0, time.UTC)
	opsPublicProofOperationPacketFreshUntil  = opsPublicProofOperationPacketGeneratedAt.Add(7 * 24 * time.Hour)
)

type OpsSurface struct {
	ID          string
	Label       string
	Description string
	Href        string
	Target      string
	Owner       string
	Status      string
}

type OpsPageData struct {
	Title           string
	Description     string
	Active          string
	View            string // optional sub-view selector; "forensic" shows the full evidence tiers
	Surfaces        []OpsSurface
	EmbedURL        string
	EmbedLabel      string
	Telemetry       *OpsTelemetryData
	Work            *OpsWorkData
	Hive            *OpsHiveData
	HiveShell       *OpsHiveShellData
	Evidence        *OpsEvidenceData
	PublicProof     *OpsPublicProofData
	Decision        *OpsDecisionData
	Approvals       *OpsApprovalsData
	Observatory     *OpsObservatoryData
	Overview        *OpsOverviewData
	Observation     *OpsObservationData
	Control         *OpsControlData
	Factory         *OpsFactoryData
	Civilization    *OpsCivilizationAssemblyData
	GitHubCanonical *OpsGitHubCanonicalData
	LegacyURL       string
}

type OpsOverviewData struct {
	GeneratedAt string
	Summary     string
	Boundary    string
	Primary     []OpsSurface
	Drilldowns  []OpsSurface
}

type OpsObservationData struct {
	GeneratedAt   string
	Source        string
	Freshness     string
	Boundary      string
	Metrics       []OpsObservationMetric
	Canary        OpsObservationCanary
	AgentRows     []OpsObservationAgentRow
	FactoryStages []OpsObservationStage
	SpendRows     []OpsObservationSpendRow
	Exceptions    []OpsObservationException
	EvidenceRefs  []string
}

type OpsObservationMetric struct {
	Label     string
	Value     string
	Context   string
	State     string
	Source    string
	UpdatedAt string
}

type OpsObservationCanary struct {
	Status               string
	Summary              string
	Source               string
	UpdatedAt            string
	Boundary             string
	DiscoveredIssueCount int
	PRReadyIssueCount    int
	ParkedIssueCount     int
	HumanActionCount     int
	ProtectedActionCount int
	Rows                 []OpsObservationCanaryIssueRow
}

type OpsObservationCanaryIssueRow struct {
	Issue       string
	Title       string
	State       string
	Blocker     string
	Action      string
	EvidenceRef string
}

type OpsObservationAgentRow struct {
	Role     string
	State    string
	Model    string
	Activity string
	LastSeen string
	Source   string
	Boundary string
}

type OpsObservationStage struct {
	Label    string
	Count    int
	State    string
	Source   string
	Boundary string
}

type OpsObservationSpendRow struct {
	Label  string
	Value  string
	Limit  string
	State  string
	Source string
}

type OpsObservationException struct {
	Label       string
	State       string
	Reason      string
	EvidenceRef string
}

type OpsControlData struct {
	GeneratedAt   string
	StorageStatus string
	Boundary      string
	ReadOnly      bool
	Confirmation  string
	Error         string
	Actions       []OpsControlAction
	ModelTargets  []OpsModelTargetOption
	Intents       []OpsControlIntent
	EvidenceRefs  []string
}

type OpsControlAction struct {
	Kind          string
	Label         string
	ActionLabel   string
	Description   string
	Placeholder   string
	Status        string
	TargetCatalog bool
}

type OpsControlIntent struct {
	ID          string
	Kind        string
	Title       string
	Target      string
	RequestedBy string
	Status      string
	CreatedAt   string
	Detail      string
}

type OpsModelTargetOption struct {
	Value  string
	Label  string
	Status string
	Source string
}

type opsControlIntentMeta struct {
	IntentKind  string `json:"intent_kind"`
	RequestedBy string `json:"requested_by"`
	Target      string `json:"target"`
}

type OpsFactoryData struct {
	GeneratedAt    string
	StorageStatus  string
	Boundary       string
	ReadOnly       bool
	SubmitterQuery string
	Confirmation   string
	Error          string
	Submissions    []OpsFactorySubmission
	Stages         []OpsFactoryStage
	EvidenceRefs   []string
}

type OpsFactorySubmission struct {
	ID        string
	Title     string
	Submitter string
	Email     string
	Org       string
	Filename  string
	SHA256    string
	Status    string
	CreatedAt string
	Notes     string
	Preview   string
}

type OpsFactoryStage struct {
	Label    string
	Count    int
	State    string
	Boundary string
}

type opsFactorySubmissionMeta struct {
	SubmitterName  string `json:"submitter_name"`
	SubmitterEmail string `json:"submitter_email"`
	SubmitterOrg   string `json:"submitter_org"`
	Filename       string `json:"filename"`
	SHA256         string `json:"sha256"`
	Notes          string `json:"notes"`
}

type OpsHiveShellData struct {
	Active    string
	Profile   string
	Intake    OpsHiveIntakeView
	Runs      []OpsHiveRunView
	Launches  []OpsHiveRunLaunchView
	Agents    []OpsHiveAgentView
	Resources []OpsHiveResourceView
}

type OpsHiveShellCard struct {
	ID          string
	Label       string
	Href        string
	Description string
	Status      string
}

type OpsHiveSourceView struct {
	ID        string
	Kind      string
	Title     string
	Detail    string
	Content   string
	Status    string
	CreatedAt string
}

type OpsHiveMissingFieldView struct {
	Label  string
	Detail string
	Status string
}

// OpsHiveBriefPreviewView is render-only. Operators can edit the fields locally
// while assembling a draft; persistence and launch remain separate governed work.
type OpsHiveBriefPreviewView struct {
	Title      string
	Objective  string
	Scope      string
	Acceptance string
	Risks      string
	Readiness  string
	Missing    []OpsHiveMissingFieldView
}

type OpsHiveIntakeView struct {
	Status          string
	Confidence      string
	SuggestedMode   string
	AuthorityLevel  string
	EstimatedBudget string
	StorageStatus   string
	ReadOnly        bool
	ReadOnlyReason  string
	Error           string
	LaunchError     string
	Sources         []OpsHiveSourceView
	MissingFields   []OpsHiveMissingFieldView
	Brief           OpsHiveBriefPreviewView
}

type OpsHiveRunView struct {
	ID        string
	Title     string
	Status    string
	Guardian  string
	Budget    string
	Phase     string
	Approvals int
	Artifacts int
	UpdatedAt string
}

type OpsHiveRunLaunchView struct {
	RunID        string
	Status       string
	FirstEventID string
	OperatorID   string
	Title        string
	TargetRepos  string
	Budget       string
	CreatedAt    string
	Selected     bool
}

type opsHiveRunLaunchPayload struct {
	OperatorID  string                    `json:"operator_id"`
	IntakeID    string                    `json:"intake_id"`
	Title       string                    `json:"title"`
	Brief       opsHiveRunLaunchBrief     `json:"brief"`
	Sources     []opsHiveRunLaunchSource  `json:"sources"`
	Authority   opsHiveRunLaunchAuthority `json:"authority"`
	Budget      opsHiveRunLaunchBudget    `json:"budget"`
	TargetRepos []string                  `json:"target_repos"`
}

type opsHiveRunLaunchBrief struct {
	Title      string   `json:"title"`
	Objective  string   `json:"objective"`
	Scope      string   `json:"scope"`
	Acceptance string   `json:"acceptance"`
	Risks      string   `json:"risks"`
	Readiness  string   `json:"readiness"`
	Missing    []string `json:"missing"`
}

type opsHiveRunLaunchSource struct {
	ID    string `json:"id,omitempty"`
	Type  string `json:"type"`
	Ref   string `json:"ref"`
	Title string `json:"title,omitempty"`
}

type opsHiveRunLaunchAuthority struct {
	InitialLevel string `json:"initial_level"`
	Scope        string `json:"scope"`
	PolicyRef    string `json:"policy_ref"`
	Rationale    string `json:"rationale"`
}

type opsHiveRunLaunchBudget struct {
	MaxIterations int     `json:"max_iterations"`
	MaxCostUSD    float64 `json:"max_cost_usd"`
}

type opsHiveRunLaunchResponse struct {
	RunID        string `json:"run_id"`
	Status       string `json:"status"`
	FirstEventID string `json:"first_event_id"`
}

type OpsHiveAgentView struct {
	Name      string
	Role      string
	State     string
	Budget    string
	LastEvent string
}

type OpsHiveResourceView struct {
	Label  string
	Used   string
	Limit  string
	Status string
	Detail string
}

type OpsTelemetryData struct {
	WorkURL              string
	PipelineURL          string
	GeneratedAt          string
	ActorCount           int
	AgentCount           int
	ActiveAgentCount     int
	ProcessingAgentCount int
	PhaseLabel           string
	PhaseStatus          string
	Pipeline             *OpsPipelineReport
	PipelineError        string
	RecentAgents         []OpsTelemetryAgent
	RecentEvents         []OpsTelemetryEvent
	Error                string
}

type OpsTelemetryAgent struct {
	Role        string `json:"role"`
	State       string `json:"state"`
	Model       string `json:"model"`
	LastEventAt string `json:"last_event_at"`
	LastMessage string `json:"last_message"`
	Errors      int    `json:"errors"`
}

type OpsTelemetryEvent struct {
	EventType string `json:"event_type"`
	ActorRole string `json:"actor_role"`
	Summary   string `json:"summary"`
	At        string `json:"at"`
}

type OpsPipelineReport struct {
	CycleID           string             `json:"cycle_id"`
	Status            string             `json:"status"`
	CurrentStage      string             `json:"current_stage"`
	CurrentPhase      string             `json:"current_phase"`
	LastOutcome       string             `json:"last_outcome"`
	LastSummary       string             `json:"last_summary"`
	StartedAt         string             `json:"started_at"`
	UpdatedAt         string             `json:"updated_at"`
	DurationSecs      float64            `json:"duration_secs"`
	TotalCostUSD      float64            `json:"total_cost_usd"`
	TotalInputTokens  int                `json:"total_input_tokens"`
	TotalOutputTokens int                `json:"total_output_tokens"`
	TotalTokens       int                `json:"total_tokens"`
	OpenBoardItems    int                `json:"open_board_items"`
	ReviseCount       int                `json:"revise_count"`
	IntakeComplete    bool               `json:"intake_complete"`
	DesignComplete    bool               `json:"design_complete"`
	EmissionComplete  bool               `json:"emission_complete"`
	Phases            []OpsPipelinePhase `json:"phases"`
	HumanStatus       string             `json:"human_status"`
}

type OpsPipelinePhase struct {
	CycleID       string  `json:"cycle_id"`
	Phase         string  `json:"phase"`
	WorkflowStage string  `json:"workflow_stage"`
	Outcome       string  `json:"outcome"`
	Repo          string  `json:"repo"`
	TaskID        string  `json:"task_id"`
	TaskTitle     string  `json:"task_title"`
	DurationSecs  float64 `json:"duration_secs"`
	CostUSD       float64 `json:"cost_usd"`
	InputTokens   int     `json:"input_tokens"`
	OutputTokens  int     `json:"output_tokens"`
	BoardOpen     int     `json:"board_open"`
	ReviseCount   int     `json:"revise_count"`
	Summary       string  `json:"summary"`
	Error         string  `json:"error"`
	InputRef      string  `json:"input_ref"`
	OutputRef     string  `json:"output_ref"`
	RecordedAt    string  `json:"recorded_at"`
}

type OpsWorkData struct {
	WorkURL       string
	GeneratedAt   string
	Total         int
	Open          int
	Active        int
	Blocked       int
	Completed     int
	Ready         int
	HighPriority  int
	Unassigned    int
	EvidenceCount int
	WaivedCount   int
	RecentTasks   []OpsWorkTask
	BlockedTasks  []OpsWorkTask
	PhaseGates    []OpsPhaseGate
	Error         string
}

type OpsWorkTask struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	Priority       string   `json:"priority"`
	Workspace      string   `json:"workspace"`
	Status         string   `json:"status"`
	Assignee       string   `json:"assignee"`
	Blocked        bool     `json:"blocked"`
	ArtifactCount  int      `json:"artifact_count"`
	Waived         bool     `json:"waived"`
	Ready          bool     `json:"ready"`
	MissingGates   []string `json:"missing_gates"`
	CreatedBy      string   `json:"created_by,omitempty"`
	RiskClass      string   `json:"risk_class,omitempty"`
	Cell           string   `json:"cell,omitempty"`
	FactoryOrderID string   `json:"factory_order_id,omitempty"`
	CreatedAt      string   `json:"created_at,omitempty"`
}

type OpsPhaseGate struct {
	ID         string   `json:"id"`
	Phase      string   `json:"phase"`
	Title      string   `json:"title"`
	Criteria   []string `json:"criteria"`
	Status     string   `json:"status"`
	Summary    string   `json:"summary"`
	Reason     string   `json:"reason"`
	DeclaredAt string   `json:"declared_at"`
	UpdatedAt  string   `json:"updated_at"`
}

type OpsHiveData struct {
	GeneratedAt        string
	Iteration          int
	Phase              string
	BuildTitle         string
	BuildCost          float64
	TotalOps           int
	LastActive         string
	VerifiedBuilds     int
	TotalCost          float64
	AverageCost        float64
	DiagnosticCount    int
	ProjectionSource   string
	ProjectionError    string
	PendingApprovals   []OpsHiveApproval
	AuthorityDecisions []OpsHiveDecision
	Lifecycle          []OpsHiveLifecycle
	KeyAuditTraces     []OpsHiveKeyAuditTrace
	RuntimeEvidence    OpsHiveRuntimeEvidence
	ModelSelection     OpsHiveModelSelection
	RecentCommits      []RecentCommit
	RecentEvents       []DiagEntry
	Error              string
}

type OpsHiveProjection struct {
	GeneratedAt        string                 `json:"generated_at"`
	Source             string                 `json:"source"`
	PendingApprovals   []OpsHiveApproval      `json:"pending_approvals"`
	AuthorityDecisions []OpsHiveDecision      `json:"authority_decisions"`
	Lifecycle          []OpsHiveLifecycle     `json:"lifecycle"`
	KeyAuditTraces     []OpsHiveKeyAuditTrace `json:"key_audit_traces"`
	RuntimeEvidence    OpsHiveRuntimeEvidence `json:"runtime_evidence"`
	ModelSelection     OpsHiveModelSelection  `json:"model_selection"`
	Errors             []string               `json:"errors"`
}

type OpsHiveRuntimeEvidence struct {
	Source               string                    `json:"source"`
	Status               string                    `json:"status"`
	LastRun              *OpsHiveRuntimeRun        `json:"last_run"`
	AgentEvents          OpsHiveRuntimeAgentEvents `json:"agent_events"`
	LastQueuedRunRequest *OpsHiveQueuedRunRequest  `json:"last_queued_run_request"`
	Artifacts            []OpsHiveRuntimeArtifact  `json:"artifacts"`
	RunEvents            []OpsHiveRuntimeEvent     `json:"run_events"`
	CausalGraph          OpsHiveCausalGraph        `json:"causal_graph"`
	Limitations          []string                  `json:"limitations"`
}

type OpsHiveRuntimeRun struct {
	StartedEventID   string   `json:"started_event_id"`
	ConversationID   string   `json:"conversation_id"`
	StartedAt        string   `json:"started_at"`
	SeedIdea         string   `json:"seed_idea"`
	RepoPath         string   `json:"repo_path"`
	CompletedEventID string   `json:"completed_event_id"`
	CompletedAt      string   `json:"completed_at"`
	AgentCount       *int     `json:"agent_count"`
	DurationMs       *int64   `json:"duration_ms"`
	TotalCost        *float64 `json:"total_cost"`
}

type OpsHiveRuntimeAgentEvents struct {
	Scope            string                `json:"scope"`
	Spawned          int                   `json:"spawned"`
	Stopped          int                   `json:"stopped"`
	ObservedActive   int                   `json:"observed_active"`
	ActiveAgents     []OpsHiveRuntimeAgent `json:"active_agents"`
	LastAgentEventID string                `json:"last_agent_event_id"`
	LastAgentEventAt string                `json:"last_agent_event_at"`
}

type OpsHiveRuntimeAgent struct {
	Name           string `json:"name"`
	Role           string `json:"role"`
	Model          string `json:"model"`
	ActorID        string `json:"actor_id"`
	SpawnedEventID string `json:"spawned_event_id"`
	SpawnedAt      string `json:"spawned_at"`
}

type OpsHiveQueuedRunRequest struct {
	EventID               string                           `json:"event_id"`
	ConversationID        string                           `json:"conversation_id"`
	RunID                 string                           `json:"run_id"`
	Title                 string                           `json:"title"`
	OperatorID            string                           `json:"operator_id"`
	Status                string                           `json:"status"`
	TargetRepos           []string                         `json:"target_repos"`
	AuthorityInitialLevel string                           `json:"authority_initial_level"`
	AuthorityScope        string                           `json:"authority_scope"`
	BudgetMaxIterations   *int                             `json:"budget_max_iterations"`
	BudgetMaxCostUSD      *float64                         `json:"budget_max_cost_usd"`
	SourceEventID         string                           `json:"source_event_id"`
	BriefEventID          string                           `json:"brief_event_id"`
	BriefKind             string                           `json:"brief_kind"`
	LifecycleVersion      string                           `json:"lifecycle_version"`
	LifecycleEvidenceKind string                           `json:"lifecycle_evidence_kind"`
	SelectionPolicy       *OpsHiveQueuedRunSelectionPolicy `json:"selection_policy,omitempty"`
	DevelopmentLifecycle  []OpsHiveQueuedRunLifecycleStage `json:"development_lifecycle"`
	AgentExecutionPlan    []OpsHiveQueuedRunAgentPlanStep  `json:"agent_execution_plan"`
	EvidenceKind          string                           `json:"evidence_kind"`
	CreatedAt             string                           `json:"created_at"`
}

type OpsHiveQueuedRunSelectionPolicy struct {
	PolicyID       string   `json:"policy_id"`
	SelectedRank   int      `json:"selected_rank"`
	CandidateCount int      `json:"candidate_count"`
	RankingInputs  []string `json:"ranking_inputs,omitempty"`
	Rationale      string   `json:"rationale,omitempty"`
}

type OpsHiveQueuedRunLifecycleStage struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	RequiredRoles     []string `json:"required_roles"`
	RequiredEvidence  []string `json:"required_evidence"`
	AuthorityBoundary string   `json:"authority_boundary"`
	CompletionGate    string   `json:"completion_gate"`
	EvidenceStatus    string   `json:"evidence_status"`
}

type OpsHiveQueuedRunAgentPlanStep struct {
	ID                string   `json:"id"`
	StageID           string   `json:"stage_id"`
	Role              string   `json:"role"`
	CanOperate        bool     `json:"can_operate"`
	Objective         string   `json:"objective"`
	RequiredInputs    []string `json:"required_inputs"`
	RequiredOutputs   []string `json:"required_outputs"`
	AuthorityBoundary string   `json:"authority_boundary"`
	CompletionGate    string   `json:"completion_gate"`
	EvidenceStatus    string   `json:"evidence_status"`
}

type OpsHiveRuntimeArtifact struct {
	EventID         string                `json:"event_id"`
	RunID           string                `json:"run_id"`
	ArtifactID      string                `json:"artifact_id"`
	Label           string                `json:"label"`
	Title           string                `json:"title"`
	MediaType       string                `json:"media_type"`
	URI             string                `json:"uri"`
	Summary         string                `json:"summary"`
	ProducerActorID string                `json:"producer_actor_id"`
	Causes          []OpsHiveRuntimeCause `json:"causes"`
	CauseStatus     string                `json:"cause_status"`
	CreatedAt       string                `json:"created_at"`
}

type OpsHiveRuntimeCause struct {
	EventID   string `json:"event_id"`
	EventType string `json:"event_type"`
	Scope     string `json:"scope"`
}

type OpsHiveRuntimeEvent struct {
	EventID        string          `json:"event_id"`
	EventType      string          `json:"event_type"`
	ConversationID string          `json:"conversation_id"`
	CreatedAt      string          `json:"created_at"`
	Causes         []string        `json:"causes"`
	InspectorKind  string          `json:"inspector_kind"`
	Content        json.RawMessage `json:"content"`
	ContentError   string          `json:"content_error"`
}

type OpsHiveCausalGraph struct {
	Scope          string              `json:"scope"`
	ConversationID string              `json:"conversation_id"`
	Limit          int                 `json:"limit"`
	Truncated      bool                `json:"truncated"`
	Nodes          []OpsHiveCausalNode `json:"nodes"`
	Edges          []OpsHiveCausalEdge `json:"edges"`
}

type OpsHiveCausalNode struct {
	EventID    string `json:"event_id"`
	EventType  string `json:"event_type"`
	Label      string `json:"label"`
	ArtifactID string `json:"artifact_id"`
	Scope      string `json:"scope"`
	CreatedAt  string `json:"created_at"`
}

type OpsHiveCausalEdge struct {
	FromEventID string `json:"from_event_id"`
	ToEventID   string `json:"to_event_id"`
	Scope       string `json:"scope"`
}

type OpsHiveModelSelection struct {
	Source        string                       `json:"source"`
	CatalogSource string                       `json:"catalog_source"`
	TierDefaults  map[string]string            `json:"tier_defaults"`
	GlobalMode    string                       `json:"global_mode"`
	SelectionMode string                       `json:"selection_mode"`
	LoadedAt      string                       `json:"loaded_at"`
	ReloadMode    string                       `json:"reload_mode"`
	HotReload     bool                         `json:"hot_reload"`
	LastReloadAt  string                       `json:"last_reload_at"`
	Models        []OpsHiveModelCatalogEntry   `json:"models"`
	Assignments   []OpsHiveModelRoleAssignment `json:"assignments"`
	Errors        []string                     `json:"errors"`
}

type OpsHiveModelCatalogEntry struct {
	Metadata        map[string]string `json:"metadata"`
	ID              string            `json:"id"`
	Aliases         []string          `json:"aliases"`
	Provider        string            `json:"provider"`
	AuthMode        string            `json:"auth_mode"`
	Tier            string            `json:"tier"`
	Capabilities    []string          `json:"capabilities"`
	ContextWindow   int               `json:"context_window"`
	MaxOutputTokens int               `json:"max_output_tokens"`
	Deprecated      bool              `json:"deprecated"`
}

type OpsHiveModelRoleAssignment struct {
	Role                 string   `json:"role"`
	Tier                 string   `json:"tier"`
	CanOperate           bool     `json:"can_operate"`
	Model                string   `json:"model"`
	Provider             string   `json:"provider"`
	AuthMode             string   `json:"auth_mode"`
	Profile              string   `json:"profile"`
	PolicyModel          string   `json:"policy_model"`
	PolicyProvider       string   `json:"policy_provider"`
	PreferredTier        string   `json:"preferred_tier"`
	RequiredCapabilities []string `json:"required_capabilities"`
	MaxCostPerCallUSD    *float64 `json:"max_cost_per_call_usd"`
	SelectionStrategy    string   `json:"selection_strategy"`
	SelectionMode        string   `json:"selection_mode"`
	OverrideMode         string   `json:"override_mode"`
	EffectiveMode        string   `json:"effective_mode"`
	Source               string   `json:"source"`
	PolicyEventID        string   `json:"policy_event_id"`
	Error                string   `json:"error"`
}

type OpsHiveApproval struct {
	EventID           string `json:"event_id"`
	RequestID         string `json:"request_id"`
	RequestingActor   string `json:"requesting_actor"`
	ActionName        string `json:"action_name"`
	Target            string `json:"target"`
	Environment       string `json:"environment"`
	RequestedOutcome  string `json:"requested_outcome"`
	Justification     string `json:"justification"`
	RiskSummary       string `json:"risk_summary"`
	ProposedOperation string `json:"proposed_operation"`
	CreatedAt         string `json:"created_at"`
}

type OpsApprovalsData struct {
	GeneratedAt        string
	ProjectionSource   string
	ProjectionError    string
	ProjectionWarning  string
	PendingApprovals   []OpsApprovalQueueItem
	AuthorityDecisions []OpsHiveDecision
	KeyAuditTraces     []OpsHiveKeyAuditTrace
}

type OpsApprovalQueueItem struct {
	OpsHiveApproval
	DecisionHref   string
	ResolveStatus  string
	ResolveEnabled bool
}

type OpsHiveDecision struct {
	EventID         string `json:"event_id"`
	DecisionID      string `json:"decision_id"`
	RequestID       string `json:"request_id"`
	ApproverActor   string `json:"approver_actor"`
	Outcome         string `json:"outcome"`
	ApprovedAction  string `json:"approved_action"`
	ApprovedTarget  string `json:"approved_target"`
	Rationale       string `json:"rationale"`
	RequestedAction string `json:"requested_action"`
	RequestedTarget string `json:"requested_target"`
	CreatedAt       string `json:"created_at"`
}

type OpsHiveLifecycle struct {
	ActorID         string `json:"actor_id"`
	DisplayName     string `json:"display_name"`
	Role            string `json:"role"`
	LifecycleStatus string `json:"lifecycle_status"`
	AuthorityScope  string `json:"authority_scope"`
	KeyProvenance   string `json:"key_provenance"`
	Environment     string `json:"environment"`
	IdentityMode    string `json:"identity_mode"`
	LastEventType   string `json:"last_event_type"`
	UpdatedAt       string `json:"updated_at"`
}

type OpsHiveKeyAuditTrace struct {
	EventID          string `json:"event_id"`
	EventType        string `json:"event_type"`
	ActorID          string `json:"actor_id"`
	SubjectActorID   string `json:"subject_actor_id"`
	KeyProvenance    string `json:"key_provenance"`
	Environment      string `json:"environment"`
	IdentityMode     string `json:"identity_mode"`
	PublicKey        string `json:"public_key"`
	OldPublicKey     string `json:"old_public_key"`
	NewPublicKey     string `json:"new_public_key"`
	ExternalKeyRef   string `json:"external_key_ref"`
	Reason           string `json:"reason"`
	RecordKind       string `json:"record_kind"`
	Rationale        string `json:"rationale"`
	AuthorityRequest string `json:"authority_request"`
	DecisionEvent    string `json:"decision_event"`
	CreatedAt        string `json:"created_at"`
}

// OpsRoleTimelineRow is one row in the society-view role timeline.
// Roles are displayed in the canonical civic order:
//
//	strategist → planner → implementer → reviewer → guardian → human (Site approval) → draft PR
type OpsRoleTimelineRow struct {
	Role   string // canonical role label (e.g. "strategist", "human (Site approval)", "draft PR")
	Actors string // actor display names observed for this role, comma-separated; empty if none seen
	Status string // lifecycle_status of the actor(s), or derived status for synthetic rows
	Notes  string // additional context (e.g. approved action for the human row)
}

type OpsEvidenceData struct {
	GeneratedAt        string
	Source             string
	ProjectionURL      string
	ProjectionError    string
	FactoryOrderID     string
	ReleaseCandidateID string
	FactoryOrder       *OpsEvidenceFactoryOrder
	ReleaseCandidate   *OpsEvidenceReleaseCandidate
	Decision           *OpsEvidenceDecision
	AuditReport        *OpsEvidenceAuditReport
	Timeline           []OpsEvidenceTimelineEvent
	GateEvidence       []OpsEvidenceGate
	ReleaseEvidence    []OpsEvidenceReleaseEvidence
	FailuresRepairs    []OpsEvidenceFailureRepair
	MissingProvenance  []OpsEvidenceMissingProvenance
	ProofOfWorkPacket  *OpsProofOfWorkPacket
	Errors             []string
	// RoleTimeline is the society view: order built by the civic roles.
	// Built from the hive operator projection Lifecycle + AuthorityDecisions.
	// Empty when HIVE_OPS_API_BASE_URL is not configured.
	RoleTimeline []OpsRoleTimelineRow
}

type OpsPublicProofData struct {
	GeneratedAt          string
	ReferenceGeneratedAt string
	ReferenceFreshUntil  string
	Source               string
	Summary              string
	Boundary             string
	RequiredLabels       []string
	Records              []OpsPublicProofRecord
}

type OpsPublicProofRecord struct {
	Category     string
	State        string
	Source       string
	LastUpdate   string
	Boundary     string
	EvidenceRef  string
	EvidenceHref string
	Notes        string
	Labels       []string
}

type OpsDecisionData struct {
	AuthorizationSource string
	CorrelationID       string
	TraceID             string
	RequestID           string
	RequestedAction     string
	DecisionReason      string
	TargetType          string
	Repo                string
	TargetRef           string
	Status              string
	Effect              string
	OperatorSummary     string
	BlockedReasons      []string
	RequiredEvidence    []string
	Actions             []OpsDecisionAction
	GovernancePosture   []OpsDecisionPosture
	BoundaryChecks      []OpsDecisionPosture
}

// opsDecisionApprove, opsDecisionDeny, opsDecisionMoreEvidence are the canonical
// wire values emitted by the decision form buttons and tested by the submit handler.
// Using shared constants ensures the UI and handler can never drift independently.
//
//   - opsDecisionApprove / opsDecisionDeny are forwarded to hive normalised to the
//     hive-canonical past-tense form ("approved" / "denied").
//   - opsDecisionMoreEvidence is handled locally; hive is never called.
const (
	opsDecisionApprove      = "approve"
	opsDecisionDeny         = "deny"
	opsDecisionMoreEvidence = "request-more-evidence"
)

type OpsDecisionAction struct {
	Label       string
	WireValue   string
	Description string
}

type OpsDecisionPosture struct {
	Label  string
	Field  string
	Value  string
	Status string
}

type OpsEvidenceProjection struct {
	GeneratedAt       string                         `json:"generated_at"`
	Source            string                         `json:"source"`
	FactoryOrder      *OpsEvidenceFactoryOrder       `json:"factory_order"`
	ReleaseCandidate  *OpsEvidenceReleaseCandidate   `json:"release_candidate"`
	Decision          *OpsEvidenceDecision           `json:"decision"`
	AuditReport       *OpsEvidenceAuditReport        `json:"audit_report"`
	Timeline          []OpsEvidenceTimelineEvent     `json:"timeline"`
	GateEvidence      []OpsEvidenceGate              `json:"gate_evidence"`
	ReleaseEvidence   []OpsEvidenceReleaseEvidence   `json:"release_evidence"`
	FailuresRepairs   []OpsEvidenceFailureRepair     `json:"failures_repairs"`
	MissingProvenance []OpsEvidenceMissingProvenance `json:"missing_provenance"`
	ProofOfWorkPacket *OpsProofOfWorkPacket          `json:"proof_of_work_packet"`
	Errors            []string                       `json:"errors"`
}

type OpsEvidenceFactoryOrder struct {
	ID               string `json:"id"`
	Version          int    `json:"version"`
	Status           string `json:"status"`
	SourceIntentHash string `json:"source_intent_hash"`
	SourceIntentRef  string `json:"source_intent_ref"`
	RiskClass        string `json:"risk_class"`
	ReleasePolicy    string `json:"release_policy"`
}

type OpsEvidenceReleaseCandidate struct {
	ID                      string   `json:"id"`
	Status                  string   `json:"status"`
	FactoryOrderID          string   `json:"factory_order_id"`
	FactoryRuntimeVersionID string   `json:"factory_runtime_version_id"`
	ArtifactRefs            []string `json:"artifact_refs"`
}

type OpsEvidenceDecision struct {
	Kind         string   `json:"kind"`
	ID           string   `json:"id"`
	ActorID      string   `json:"actor_id"`
	Reason       string   `json:"reason"`
	EvidenceRefs []string `json:"evidence_refs"`
	Status       string   `json:"status"`
	CreatedAt    string   `json:"created_at"`
}

type OpsEvidenceAuditReport struct {
	ID           string   `json:"id"`
	TargetType   string   `json:"target_type"`
	TargetID     string   `json:"target_id"`
	Status       string   `json:"status"`
	TraceScore   float64  `json:"trace_score"`
	MissingLinks []string `json:"missing_links"`
}

type OpsEvidenceTimelineEvent struct {
	Label     string `json:"label"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	NodeID    string `json:"node_id"`
	CreatedAt string `json:"created_at"`
	Summary   string `json:"summary"`
}

type OpsEvidenceGate struct {
	GateName     string   `json:"gate_name"`
	Status       string   `json:"status"`
	GateResultID string   `json:"gate_result_id"`
	EvidenceRefs []string `json:"evidence_refs"`
	WaiverRef    string   `json:"waiver_ref"`
	MissingRefs  []string `json:"missing_refs"`
}

type OpsEvidenceReleaseEvidence struct {
	Label            string   `json:"label"`
	Status           string   `json:"status"`
	ArtifactRefs     []string `json:"artifact_refs"`
	RuntimeRefs      []string `json:"runtime_refs"`
	BOMRefs          []string `json:"bom_refs"`
	RequiredPathRefs []string `json:"required_path_refs"`
	MissingRefs      []string `json:"missing_refs"`
}

type OpsEvidenceFailureRepair struct {
	FailureID         string `json:"failure_id"`
	FailureClass      string `json:"failure_class"`
	Severity          string `json:"severity"`
	Summary           string `json:"summary"`
	TaskID            string `json:"task_id"`
	GateResultID      string `json:"gate_result_id"`
	TestRunID         string `json:"test_run_id"`
	RepairID          string `json:"repair_id"`
	RepairStatus      string `json:"repair_status"`
	ActorInvocationID string `json:"actor_invocation_id"`
}

type OpsEvidenceMissingProvenance struct {
	PathName  string   `json:"path_name"`
	NodeIDs   []string `json:"node_ids"`
	EdgeIDs   []string `json:"edge_ids"`
	Missing   []string `json:"missing"`
	Completed bool     `json:"completed"`
}

type OpsProofOfWorkPacket struct {
	ID                     string               `json:"id"`
	Status                 string               `json:"status"`
	Summary                string               `json:"summary"`
	WorkItem               *OpsProofOfWorkItem  `json:"work_item"`
	RuntimeInvocation      *OpsProofOfWorkItem  `json:"runtime_invocation"`
	ChangedFiles           []OpsProofOfWorkItem `json:"changed_files"`
	TestsRun               []OpsProofOfWorkItem `json:"tests_run"`
	CIStatus               *OpsProofOfWorkItem  `json:"ci_status"`
	ReviewFeedback         []OpsProofOfWorkItem `json:"review_feedback"`
	SecurityScanResults    []OpsProofOfWorkItem `json:"security_scan_results"`
	ScreenshotsWalkthrough []OpsProofOfWorkItem `json:"screenshots_walkthrough_artifacts"`
	KnownFailures          []OpsProofOfWorkItem `json:"known_failures"`
	OperatorDecision       *OpsProofOfWorkItem  `json:"operator_decision"`
	EventGraphRefs         []string             `json:"event_graph_refs"`
}

type OpsProofOfWorkItem struct {
	Label          string   `json:"label"`
	Status         string   `json:"status"`
	Summary        string   `json:"summary"`
	ArtifactRef    string   `json:"artifact_ref"`
	EventGraphRefs []string `json:"event_graph_refs"`
}

type opsWorkTasksResponse struct {
	Tasks []OpsWorkTask `json:"tasks"`
}

type opsPhaseGatesResponse struct {
	Gates []OpsPhaseGate `json:"gates"`
}

type opsTelemetryOverview struct {
	Timestamp string `json:"timestamp"`
	Actors    []struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
		ActorType   string `json:"actor_type"`
		Status      string `json:"status"`
	} `json:"actors"`
	Agents       []OpsTelemetryAgent `json:"agents"`
	RecentEvents []OpsTelemetryEvent `json:"recent_events"`
	Phases       []struct {
		Phase  int    `json:"phase"`
		Label  string `json:"label"`
		Status string `json:"status"`
	} `json:"phases"`
}

type opsPipelineReportResponse struct {
	ComputedAt string             `json:"computed_at"`
	Report     *OpsPipelineReport `json:"report"`
	Error      string             `json:"error"`
	Detail     string             `json:"detail"`
}

var hiveOpsProjectionClient = &http.Client{Timeout: 3 * time.Second}
var civilizationOpsProjectionClient = &http.Client{Timeout: 9 * time.Second}
var evidenceOpsProjectionClient = &http.Client{Timeout: 3 * time.Second}

func (h *Handlers) handleOps(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	h.renderOps(w, r, OpsPageData{
		Title:       "Operations",
		Description: "Three entry points for Civilization work: observe state, queue bounded control intent, or submit human Factory artifacts.",
		Active:      "overview",
		Overview: &OpsOverviewData{
			GeneratedAt: formatOpsTime(now.Format(time.RFC3339)),
			Summary:     "Observation, Control, and Factory replace the old flat route index as the primary workflow surfaces. Detailed evidence pages remain available as drilldowns.",
			Boundary:    "Site is the display/operator shell. EventGraph remains truth; Hive owns runtime/governance orchestration; Work owns work-item semantics; Docs owns canonical governance records.",
			Primary:     opsSurfaces(h.store == nil),
			Drilldowns:  opsDrilldownSurfaces(h.store == nil),
		},
	})
}

func (h *Handlers) handleOpsObservation(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	telemetry := fetchOpsTelemetry(r)
	hive := h.fetchOpsHive(r)
	civilization := buildOpsCivilizationAssemblyDataFromProjection(fetchOpsCivilizationProjection(r), now)
	githubCanonical := buildOpsGitHubCanonicalDataWithScannerArtifact(now, os.Getenv(githubCanonicalScannerArtifactEnv))
	var storeSources []OpsHiveIntakeSource
	if h.store != nil {
		storeSources, _ = h.store.ListOpsHiveIntakeSources(r.Context(), opsHiveProfileSlugFromRequest(r), 50)
	}
	h.renderOps(w, r, OpsPageData{
		Title:       "Observation",
		Description: "High-level monitoring for Civilization health, Factory movement, live agents, spend, tokens, blockers, source freshness, and evidence refs.",
		Active:      "observation",
		Observation: buildOpsObservationData(now, telemetry, hive, civilization, githubCanonical, storeSources),
	})
}

func (h *Handlers) handleOpsControl(w http.ResponseWriter, r *http.Request) {
	h.renderOpsControl(w, r, "", "")
}

func (h *Handlers) handleOpsControlIntentCreate(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		http.Error(w, "graph store is not configured", http.StatusServiceUnavailable)
		return
	}
	if err := requireOpsHiveSameOrigin(r); err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	hive := h.fetchOpsHive(r)
	params, err := opsControlIntentParamsFromForm(r, h.userName(r), opsModelTargetValueSet(opsControlModelTargetOptions(hive)))
	if err != nil {
		h.renderOpsControl(w, r, "", err.Error())
		return
	}
	if _, err := h.store.CreateOpsHiveIntakeSource(r.Context(), params); err != nil {
		h.renderOpsControl(w, r, "", "could not queue control intent")
		return
	}
	h.renderOpsControl(w, r, "Queued intent recorded. It remains pending human approval and does not execute protected actions.", "")
}

func (h *Handlers) renderOpsControl(w http.ResponseWriter, r *http.Request, confirmation, errMsg string) {
	now := time.Now().UTC()
	var sources []OpsHiveIntakeSource
	if h.store != nil {
		sources, _ = h.store.ListOpsHiveIntakeSources(r.Context(), opsHiveProfileSlugFromRequest(r), 50)
	}
	hive := h.fetchOpsHive(r)
	h.renderOps(w, r, OpsPageData{
		Title:       "Control",
		Description: "Bounded admin control room for queued model, budget, Council, and Civilization-evolution intent. Controls request or draft intent only.",
		Active:      "control",
		Control:     buildOpsControlData(now, h.store == nil, sources, hive, confirmation, errMsg),
	})
}

func (h *Handlers) handleFactory(w http.ResponseWriter, r *http.Request) {
	h.renderFactory(w, r, "", "")
}

func (h *Handlers) handleFactoryArtifactCreate(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		http.Error(w, "graph store is not configured", http.StatusServiceUnavailable)
		return
	}
	if err := requireOpsHiveSameOrigin(r); err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	params, err := opsFactorySubmissionParamsFromRequest(r)
	if err != nil {
		h.renderFactory(w, r, "", err.Error())
		return
	}
	if _, err := h.store.CreateOpsHiveIntakeSource(r.Context(), params); err != nil {
		h.renderFactory(w, r, "", "could not submit artifact for governed FactoryOrder review")
		return
	}
	h.renderFactory(w, r, "Artifact submitted. FactoryOrder conversion requires separate governed review.", "")
}

func (h *Handlers) renderFactory(w http.ResponseWriter, r *http.Request, confirmation, errMsg string) {
	now := time.Now().UTC()
	var sources []OpsHiveIntakeSource
	if h.store != nil {
		sources, _ = h.store.ListOpsHiveIntakeSources(r.Context(), opsHiveProfileSlugFromRequest(r), 100)
	}
	h.renderOps(w, r, OpsPageData{
		Title:       "Human Factory",
		Description: "Non-admin workspace for Markdown artifact submission, Factory-in-motion review, and filtering by human submitter.",
		Active:      "factory",
		Factory:     buildOpsFactoryData(now, h.store == nil, sources, strings.TrimSpace(r.URL.Query().Get("submitter")), confirmation, errMsg),
	})
}

func (h *Handlers) handleOpsCivilization(w http.ResponseWriter, r *http.Request) {
	h.renderOps(w, r, OpsPageData{
		Title:        "Civilization Assembly",
		Description:  "Read-only v4.0 assembly projection for roles, tiers, model-selection posture, and authority boundaries. Site displays unavailable truth sources as unavailable; it does not execute work.",
		Active:       "civilization",
		Civilization: buildOpsCivilizationAssemblyDataFromProjection(fetchOpsCivilizationProjection(r), time.Now().UTC()),
	})
}

func (h *Handlers) handleOpsGitHubCanonical(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Transpara-Archive-Boundary", "civilization-history/v1")
	h.renderOps(w, r, OpsPageData{
		Title:           "GitHub Canonical",
		Description:     "Read-only migration progress for replacing markdown development arcs with GitHub issue-canonical work records.",
		Active:          "github-canonical",
		GitHubCanonical: buildOpsGitHubCanonicalDataWithScannerArtifact(time.Now().UTC(), os.Getenv(githubCanonicalScannerArtifactEnv)),
	})
}

func (h *Handlers) handleOpsPublicProof(w http.ResponseWriter, r *http.Request) {
	h.renderOps(w, r, OpsPageData{
		Title:       "Public Proof",
		Description: "Display-only public-reader and public-correction evidence ledger. Site shows configured public refs and explicit unavailable states; it does not fetch private data or execute work.",
		Active:      "public-proof",
		PublicProof: buildOpsPublicProofData(time.Now().UTC()),
	})
}

func (h *Handlers) handleOpsWork(w http.ResponseWriter, r *http.Request) {
	h.renderOps(w, r, OpsPageData{
		Title:       "Work",
		Description: "Native Site task summary sourced from the Work API.",
		Active:      "work",
		Work:        fetchOpsWork(r),
	})
}

func (h *Handlers) handleOpsTelemetry(w http.ResponseWriter, r *http.Request) {
	h.renderOps(w, r, OpsPageData{
		Title:       "Telemetry",
		Description: "Native Site telemetry summary sourced from Work APIs.",
		Active:      "telemetry",
		Telemetry:   fetchOpsTelemetry(r),
	})
}

func (h *Handlers) handleOpsHive(w http.ResponseWriter, r *http.Request) {
	h.renderOps(w, r, OpsPageData{
		Title:       "Hive",
		Description: "Native Site operator summary for Hive runtime status, diagnostics, and build signal.",
		Active:      "hive",
		Hive:        h.fetchOpsHive(r),
		LegacyURL:   "/hive",
	})
}

func (h *Handlers) handleOpsHiveModelPolicySubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	payload, err := opsHiveModelPolicyPayloadFromForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	base := strings.TrimSpace(os.Getenv("HIVE_OPS_API_BASE_URL"))
	if base == "" {
		http.Error(w, "HIVE_OPS_API_BASE_URL is not configured", http.StatusBadRequest)
		return
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, "internal error marshalling request: "+err.Error(), http.StatusInternalServerError)
		return
	}
	endpoint := strings.TrimRight(base, "/") + "/api/hive/model-selection/role-policy"
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		http.Error(w, "could not build hive request: "+err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey := strings.TrimSpace(os.Getenv("HIVE_OPS_API_KEY")); apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := hiveOpsProjectionClient.Do(req)
	if err != nil {
		http.Error(w, "hive unreachable: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		http.Error(w, "hive model policy rejected: "+opsHiveResponseError(resp), http.StatusBadGateway)
		return
	}
	target := "/ops/hive"
	if profile := strings.TrimSpace(r.FormValue("profile")); profile != "" {
		target += "?profile=" + url.QueryEscape(profile)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func opsHiveModelPolicyPayloadFromForm(r *http.Request) (map[string]any, error) {
	role := strings.TrimSpace(r.FormValue("role"))
	if role == "" {
		return nil, errors.New("role is required")
	}
	payload := map[string]any{
		"role": role,
	}
	for key, value := range map[string]string{
		"operator_id":    strings.TrimSpace(r.FormValue("operator_id")),
		"reason":         strings.TrimSpace(r.FormValue("reason")),
		"model":          strings.TrimSpace(r.FormValue("model")),
		"profile":        strings.TrimSpace(r.FormValue("profile")),
		"auth_mode":      strings.TrimSpace(r.FormValue("auth_mode")),
		"preferred_tier": strings.TrimSpace(r.FormValue("preferred_tier")),
	} {
		if value != "" {
			payload[key] = value
		}
	}
	capabilities := trimOpsHiveFormValues(r.Form["required_capability"])
	if len(capabilities) > 0 {
		payload["required_capabilities"] = capabilities
	}
	if rawCost := strings.TrimSpace(r.FormValue("max_cost_per_call_usd")); rawCost != "" {
		maxCost, err := strconv.ParseFloat(rawCost, 64)
		if err != nil {
			return nil, fmt.Errorf("max_cost_per_call_usd must be a number: %w", err)
		}
		payload["max_cost_per_call_usd"] = maxCost
	}
	return payload, nil
}

func trimOpsHiveFormValues(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func opsHiveMaxCostValue(value *float64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatFloat(*value, 'f', -1, 64)
}

func opsHiveOptionalInt(value *int, fallback string) string {
	if value == nil {
		return fallback
	}
	return strconv.Itoa(*value)
}

func opsHiveIntValue(value int, fallback string) string {
	if value <= 0 {
		return fallback
	}
	return strconv.Itoa(value)
}

func opsHiveOptionalFloatUSD(value *float64, fallback string) string {
	if value == nil {
		return fallback
	}
	return fmt.Sprintf("$%.2f", *value)
}

func opsHiveOptionalDurationMs(value *int64, fallback string) string {
	if value == nil {
		return fallback
	}
	if *value < 1000 {
		return fmt.Sprintf("%dms", *value)
	}
	return formatOpsDuration(float64(*value) / 1000)
}

func opsHiveRuntimeEvidenceEmpty(e OpsHiveRuntimeEvidence) bool {
	return e.Source == "" &&
		e.Status == "" &&
		e.LastRun == nil &&
		e.LastQueuedRunRequest == nil &&
		e.AgentEvents.Spawned == 0 &&
		e.AgentEvents.Stopped == 0 &&
		e.AgentEvents.ObservedActive == 0 &&
		e.AgentEvents.Scope == "" &&
		e.AgentEvents.LastAgentEventID == "" &&
		e.AgentEvents.LastAgentEventAt == "" &&
		len(e.AgentEvents.ActiveAgents) == 0 &&
		len(e.Artifacts) == 0 &&
		len(e.RunEvents) == 0 &&
		e.CausalGraph.Scope == "" &&
		e.CausalGraph.ConversationID == "" &&
		e.CausalGraph.Limit == 0 &&
		!e.CausalGraph.Truncated &&
		len(e.CausalGraph.Nodes) == 0 &&
		len(e.CausalGraph.Edges) == 0 &&
		len(e.Limitations) == 0
}

func opsHiveArtifactLabel(a OpsHiveRuntimeArtifact) string {
	if strings.TrimSpace(a.Title) != "" {
		return a.Title
	}
	if strings.TrimSpace(a.Label) != "" {
		return a.Label
	}
	if strings.TrimSpace(a.ArtifactID) != "" {
		return a.ArtifactID
	}
	return "artifact"
}

func opsHiveEventContentText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, content, "", "  "); err == nil {
		return buf.String()
	}
	return string(content)
}

func opsHiveResponseError(resp *http.Response) string {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if msg := strings.TrimSpace(string(body)); msg != "" {
		return resp.Status + ": " + msg
	}
	return resp.Status
}

func (h *Handlers) handleOpsHiveIntake(w http.ResponseWriter, r *http.Request) {
	shell := buildOpsHiveShellData("intake")
	shell.Profile = opsHiveProfileSlugFromRequest(r)
	if h.store != nil {
		shell.Intake = h.buildOpsHiveIntakeView(r)
	} else {
		shell.Intake = buildOpsHiveReadOnlyIntakeView()
	}
	description := "Site-owned intake shell for persisted source capture, interpretation, and launch readiness."
	if shell.Intake.ReadOnly {
		description = "Read-only degraded ingestion surface for source capture, interpretation, and launch-boundary status."
	}
	h.renderOps(w, r, OpsPageData{
		Title:       "Hive intake",
		Description: description,
		Active:      "hive",
		HiveShell:   shell,
	})
}

func buildOpsHiveReadOnlyIntakeView() OpsHiveIntakeView {
	return OpsHiveIntakeView{
		Status:          "unavailable",
		Confidence:      "not derived",
		SuggestedMode:   "not derived",
		AuthorityLevel:  "read-only degraded shell",
		EstimatedBudget: "unavailable",
		StorageStatus:   "graph store unavailable",
		ReadOnly:        true,
		ReadOnlyReason:  "Ingestion source capture and Hive launch queueing require the graph store and governed Hive write path. This no-DB shell renders the ingestion boundary only.",
		Sources: []OpsHiveSourceView{
			{
				Kind:   "system",
				Title:  "No persisted intake sources",
				Detail: "DATABASE_URL is not configured, so Site cannot persist source records for this render.",
				Status: "unavailable",
			},
		},
		MissingFields: []OpsHiveMissingFieldView{
			{Label: "Graph store", Detail: "Configure DATABASE_URL before source cards can be saved.", Status: "unavailable"},
			{Label: "Hive launch write path", Detail: "Queueing a run request is disabled in the read-only shell.", Status: "unavailable"},
			{Label: "Runtime evidence", Detail: "No runtime start or Hive wake is implied by this page.", Status: "boundary"},
		},
		Brief: OpsHiveBriefPreviewView{
			Title:      "Ingestion unavailable",
			Objective:  "Display the MFOF ingestion boundary without accepting source writes.",
			Scope:      "Read-only Site operator shell; no persistence, queueing, runtime execution, or protected side effects.",
			Acceptance: "Operators can see that ingestion is unavailable and where the boundary sits.",
			Risks:      "Treating missing graph storage as successful ingestion would create a fake green light.",
			Readiness:  "not ready until graph store and governed Hive launch path are configured",
			Missing: []OpsHiveMissingFieldView{
				{Label: "source persistence", Detail: "No graph store is configured.", Status: "unavailable"},
				{Label: "launch authority", Detail: "Runtime start remains separately governed.", Status: "blocked"},
			},
		},
	}
}

func (h *Handlers) handleOpsHiveIntakeSourceCreate(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		http.Error(w, "graph store is not configured", http.StatusInternalServerError)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	params, err := opsHiveIntakeSourceParamsFromForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := h.store.CreateOpsHiveIntakeSource(r.Context(), params); err != nil {
		http.Error(w, "could not save intake source", http.StatusInternalServerError)
		return
	}
	target := "/ops/hive/intake"
	if profile := strings.TrimSpace(r.FormValue("profile")); profile != "" {
		target += "?profile=" + url.QueryEscape(params.ProfileSlug)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (h *Handlers) handleOpsHiveIntakeLaunch(w http.ResponseWriter, r *http.Request) {
	if err := requireOpsHiveSameOrigin(r); err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	profileSlug := opsHiveProfileSlugFromRequest(r)
	if h.store == nil {
		h.renderOpsHiveIntakeLaunchError(w, r, profileSlug, "graph store is not configured")
		return
	}
	payload, storeParams, err := h.buildOpsHiveRunLaunchPayload(r, profileSlug)
	if err != nil {
		h.renderOpsHiveIntakeLaunchError(w, r, profileSlug, err.Error())
		return
	}
	response, err := postOpsHiveRunLaunch(r, payload)
	if err != nil {
		h.renderOpsHiveIntakeLaunchError(w, r, profileSlug, err.Error())
		return
	}
	storeParams.RunID = response.RunID
	storeParams.Status = response.Status
	storeParams.FirstEventID = response.FirstEventID
	if _, err := h.store.CreateOpsHiveRunLaunch(r.Context(), storeParams); err != nil {
		h.renderOpsHiveIntakeLaunchError(w, r, profileSlug, "Hive accepted queued run "+response.RunID+" but Site could not store queued run proof: "+err.Error())
		return
	}
	target := "/ops/hive/runs?profile=" + url.QueryEscape(profileSlug) + "&run_id=" + url.QueryEscape(response.RunID)
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func requireOpsHiveSameOrigin(r *http.Request) error {
	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
		return requireOpsHiveRequestHost(r, origin)
	}
	if referer := strings.TrimSpace(r.Header.Get("Referer")); referer != "" {
		return requireOpsHiveRequestHost(r, referer)
	}
	return nil
}

func requireOpsHiveRequestHost(r *http.Request, rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return errors.New("invalid request origin")
	}
	if !strings.EqualFold(parsed.Host, r.Host) {
		return errors.New("cross-origin launch request")
	}
	return nil
}

func (h *Handlers) renderOpsHiveIntakeLaunchError(w http.ResponseWriter, r *http.Request, profileSlug, msg string) {
	shell := buildOpsHiveShellData("intake")
	shell.Profile = profileSlug
	if h.store != nil {
		shell.Intake = h.buildOpsHiveIntakeView(r)
	}
	shell.Intake.LaunchError = msg
	h.renderOps(w, r, OpsPageData{
		Title:       "Hive intake",
		Description: "Site-owned intake shell for persisted source capture, interpretation, and launch readiness.",
		Active:      "hive",
		HiveShell:   shell,
	})
}

func (h *Handlers) handleOpsHiveRuns(w http.ResponseWriter, r *http.Request) {
	shell := buildOpsHiveShellData("runs")
	shell.Profile = opsHiveProfileSlugFromRequest(r)
	if h.store != nil {
		launches, err := h.store.ListOpsHiveRunLaunches(r.Context(), shell.Profile, 10)
		if err == nil {
			shell.Launches = opsHiveRunLaunchViews(launches, strings.TrimSpace(r.URL.Query().Get("run_id")))
		}
	}
	h.renderOps(w, r, OpsPageData{
		Title:       "Hive runs",
		Description: "Site-owned run tower with sample pipeline state and stored queued-launch proof.",
		Active:      "hive",
		HiveShell:   shell,
	})
}

func (h *Handlers) handleOpsHiveAgents(w http.ResponseWriter, r *http.Request) {
	h.renderOps(w, r, OpsPageData{
		Title:       "Hive agents",
		Description: "Static Site-owned agent topology with sample roles, lifecycle state, and budget posture.",
		Active:      "hive",
		HiveShell:   buildOpsHiveShellData("agents"),
	})
}

func (h *Handlers) handleOpsHiveResources(w http.ResponseWriter, r *http.Request) {
	h.renderOps(w, r, OpsPageData{
		Title:       "Hive resources",
		Description: "Static Site-owned resource dashboard with sample budget, queue, and capacity signals.",
		Active:      "hive",
		HiveShell:   buildOpsHiveShellData("resources"),
	})
}

func (h *Handlers) handleOpsEvidence(w http.ResponseWriter, r *http.Request) {
	evidence := fetchOpsEvidence(r)
	// Build the society-view role timeline from the hive operator projection.
	// Failure is non-fatal: if the projection is unavailable, RoleTimeline holds
	// all canonical rows in empty state (no actors/statuses).
	if proj, err := fetchHiveOperatorProjection(r); err == nil {
		evidence.RoleTimeline = buildOpsRoleTimeline(proj)
		if proj.GeneratedAt != "" {
			evidence.GeneratedAt = formatOpsTime(proj.GeneratedAt)
		}
	} else {
		evidence.RoleTimeline = buildOpsRoleTimeline(nil)
	}
	h.renderOps(w, r, OpsPageData{
		Title:       "Evidence",
		Description: "Read-only operator projection for FactoryOrder timeline, gate, release, audit, failure, repair, and provenance evidence.",
		Active:      "evidence",
		View:        strings.ToLower(strings.TrimSpace(r.URL.Query().Get("view"))),
		Evidence:    evidence,
	})
}

func (h *Handlers) handleOpsApprovals(w http.ResponseWriter, r *http.Request) {
	h.renderOps(w, r, OpsPageData{
		Title:       "Approval queue",
		Description: "Site-owned authority queue sourced from Hive's operator projection. Site forwards supported decisions to Hive and does not mutate EventGraph directly.",
		Active:      "approvals",
		Approvals:   fetchOpsApprovals(r),
	})
}

func (h *Handlers) handleOpsDecision(w http.ResponseWriter, r *http.Request) {
	h.renderOps(w, r, OpsPageData{
		Title:       "Decision boundary",
		Description: "Non-executing Gate E decision surface for bounded approve, deny, and request-more-evidence envelopes.",
		Active:      "decision",
		Decision:    buildOpsDecisionData(r),
	})
}

// handleOpsDecisionSubmit forwards the operator's approve/deny to hive and
// re-renders the decision surface with a confirmation or error.
// Site emits a GOVERNANCE POST only — it never calls GitHub or writes the graph.
//
// Only "approved" and "denied" are forwarded to hive; hive's operator-decision
// endpoint accepts only those two values and returns 400 on anything else.
// "request-more-evidence" (and any unrecognised value) is handled LOCALLY:
// Site re-renders the decision page with a note that no decision was recorded.
// Requesting more evidence is not a recordable governance decision.
func (h *Handlers) handleOpsDecisionSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	requestID := strings.TrimSpace(r.FormValue("request_id"))
	decision := strings.TrimSpace(r.FormValue("decision"))
	reason := strings.TrimSpace(r.FormValue("reason"))

	// Normalise the UI wire value to the hive-canonical past-tense form.
	// opsDecisionApprove ("approve") → hive receives "approved"
	// opsDecisionDeny ("deny")       → hive receives "denied"
	// Anything else (including opsDecisionMoreEvidence) is handled locally;
	// hive only accepts "approved" / "denied" and would 400 on anything else.
	var hiveDecision string
	switch decision {
	case opsDecisionApprove:
		hiveDecision = "approved"
	case opsDecisionDeny:
		hiveDecision = "denied"
	default:
		h.renderOpsDecisionWithConfirmation(w, r, requestID, decision, "more-evidence-local")
		return
	}
	if reason == "" {
		h.renderOpsDecisionWithConfirmation(w, r, requestID, "", "decision_reason is required for approve/deny")
		return
	}

	base := strings.TrimSpace(os.Getenv("HIVE_OPS_API_BASE_URL"))
	if base == "" {
		h.renderOpsDecisionWithConfirmation(w, r, requestID, "", "HIVE_OPS_API_BASE_URL is not configured")
		return
	}
	apiKey := strings.TrimSpace(os.Getenv("HIVE_OPS_API_KEY"))

	// Build the governance POST body (JSON — matches hive contract).
	// Use hiveDecision (past-tense canonical) rather than the UI wire value.
	payload := map[string]string{
		"request_id": requestID,
		"decision":   hiveDecision,
		"reason":     reason,
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		h.renderOpsDecisionWithConfirmation(w, r, requestID, "", "internal error marshalling request: "+err.Error())
		return
	}

	endpoint := strings.TrimRight(base, "/") + "/api/hive/operator-decision"
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		h.renderOpsDecisionWithConfirmation(w, r, requestID, "", "could not build hive request: "+err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := hiveOpsProjectionClient.Do(req)
	if err != nil {
		h.renderOpsDecisionWithConfirmation(w, r, requestID, "", "hive unreachable: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		h.renderOpsDecisionWithConfirmation(w, r, requestID, "", fmt.Sprintf("hive returned %s", resp.Status))
		return
	}

	h.renderOpsDecisionWithConfirmation(w, r, requestID, hiveDecision, "")
}

// renderOpsDecisionWithConfirmation re-renders the decision surface with either
// a "decision recorded" confirmation, a local evidence-request note, or an error
// message. effect = none is preserved in all cases.
//
// errMsg sentinel values:
//
//	""                  → success: governance POST forwarded for approved/denied.
//	"more-evidence-local" → request-more-evidence handled locally; hive NOT called.
//	anything else       → governance POST failed; show error.
func (h *Handlers) renderOpsDecisionWithConfirmation(w http.ResponseWriter, r *http.Request, requestID, decision, errMsg string) {
	// Rebuild the display data from the real pending request (or fallback).
	var data *OpsDecisionData
	if requestID != "" {
		data = buildOpsDecisionDataFromProjection(r, requestID)
	} else {
		data = buildOpsDecisionData(r)
	}
	switch errMsg {
	case "":
		data.OperatorSummary = "Decision recorded on the graph by hive. Governance POST forwarded: decision=" + decision + ". Site remains display console; effect = none."
	case "more-evidence-local":
		// request-more-evidence is not a recordable governance decision; hive was NOT called.
		data.OperatorSummary = "More evidence requested — no decision recorded. Site handled this locally; hive was not called. effect = none."
	default:
		data.OperatorSummary = "Governance POST failed: " + errMsg + " (effect = none; no downstream action taken)"
		data.BlockedReasons = append(data.BlockedReasons, errMsg)
	}
	h.renderOps(w, r, OpsPageData{
		Title:       "Decision boundary",
		Description: "Non-executing Gate E decision surface for bounded approve, deny, and request-more-evidence envelopes.",
		Active:      "decision",
		Decision:    data,
	})
}

func (h *Handlers) handleOpsRefinery(w http.ResponseWriter, r *http.Request) {
	target := "/app/journey-test/refinery?profile=transpara"
	if slug := strings.TrimSpace(r.URL.Query().Get("space")); slug != "" {
		q := url.Values{}
		q.Set("profile", "transpara")
		target = "/app/" + url.PathEscape(slug) + "/refinery?" + q.Encode()
	}
	h.renderOps(w, r, OpsPageData{
		Title:       "Refinery",
		Description: "Intake and design review surface backed by the site refinery projection and simplified FSM.",
		Active:      "refinery",
		EmbedURL:    target,
		EmbedLabel:  "Refinery",
	})
}

func (h *Handlers) renderOps(w http.ResponseWriter, r *http.Request, data OpsPageData) {
	data.Surfaces = opsSurfaces(h.store == nil)
	OpsPage(data, h.viewUser(r), profile.FromContext(r.Context())).Render(r.Context(), w)
}

func opsSurfaces(readOnly bool) []OpsSurface {
	return []OpsSurface{
		{
			ID:          "observation",
			Label:       "Observation",
			Description: "Civilization health, Factory statistics, live agents, spend, tokens, blockers, source freshness, and evidence refs.",
			Href:        "/ops/observation",
			Target:      "work telemetry, hive projection, Site evidence projections",
			Owner:       "site display shell",
			Status:      "safe hybrid",
		},
		{
			ID:          "control",
			Label:       "Control",
			Description: "Queue model, budget, Council, and Civilization evolution intent without executing protected actions.",
			Href:        "/ops/control",
			Target:      "site-local queued intent records",
			Owner:       "site intent shell",
			Status:      "queue only",
		},
		{
			ID:          "factory",
			Label:       "Human Factory",
			Description: "Submit Markdown artifacts for governed FactoryOrder review and view Factory-in-motion state by human submitter.",
			Href:        "/factory",
			Target:      "site-local artifact intake records",
			Owner:       "site intake shell",
			Status:      opsFactorySurfaceStatus(readOnly),
		},
	}
}

func opsFactorySurfaceStatus(readOnly bool) string {
	if readOnly {
		return "unavailable"
	}
	return "artifact intake"
}

func opsDrilldownSurfaces(readOnly bool) []OpsSurface {
	surfaces := []OpsSurface{
		{
			ID:          "telemetry",
			Label:       "Telemetry detail",
			Description: "Agent status, phase activity, pipeline report, and event stream health.",
			Href:        "/ops/telemetry",
			Target:      "work API /telemetry/*",
			Owner:       "site native UI, work telemetry",
			Status:      "native summary",
		},
		{
			ID:          "civilization",
			Label:       "Civilization",
			Description: "Assembly view for bootstrap roles, emergence, tiers, model-selection posture, and non-authority boundaries.",
			Href:        "/ops/civilization",
			Target:      "EventGraph Civilization Assembly projection",
			Owner:       "site read-only projection",
			Status:      "v4.0 bounded",
		},
		{
			ID:          "public-proof",
			Label:       "Public Proof",
			Description: "Display-only public-reader and public-correction proof ledger with explicit unavailable, stale, fixture, and projection-only states.",
			Href:        "/ops/public-proof",
			Target:      "static Site public-proof evidence records",
			Owner:       "site operator evidence surface",
			Status:      "display only",
		},
		{
			ID:          "hive",
			Label:       "Hive",
			Description: "Runtime iteration, phase timeline, diagnostics, and recent build signal.",
			Href:        "/ops/hive",
			Target:      "/hive",
			Owner:       "site shell, hive diagnostics",
			Status:      "native summary",
		},
		{
			ID:          "ingestion",
			Label:       "Ingestion",
			Description: "Source capture, brief readiness, missing input, and governed Hive launch boundary.",
			Href:        "/ops/hive/intake",
			Target:      "site graph store + Hive queued-run projection",
			Owner:       "site shell, hive intake",
			Status:      "store-aware",
		},
		{
			ID:          "evidence",
			Label:       "Evidence",
			Description: "Read-only FactoryOrder evidence projection: timeline, gates, release, audit, failures, repairs, and provenance gaps.",
			Href:        "/ops/evidence",
			Target:      "configured projection URL",
			Owner:       "site shell, eventgraph/work projection",
			Status:      "read only",
		},
		{
			ID:          "decision",
			Label:       "Decision",
			Description: "Gate E governed decision boundary with non-executing approve, deny, and request-more-evidence envelopes.",
			Href:        "/ops/decision",
			Target:      "local Site decision surface",
			Owner:       "site shell only",
			Status:      "effect none",
		},
		{
			ID:          "approvals",
			Label:       "Approvals",
			Description: "Pending authority requests, recent decisions, and causal audit refs from Hive.",
			Href:        "/ops/approvals",
			Target:      "hive operator projection + governed decision POST",
			Owner:       "site forwarding UI, hive records decisions",
			Status:      "governed POST",
		},
		{
			ID:          "refinery",
			Label:       "Refinery",
			Description: "Intake/design review backed by the simplified FSM projection.",
			Href:        "/ops/refinery",
			Target:      "/app/journey-test/refinery?profile=transpara",
			Owner:       "site shell, eventgraph/work projection",
			Status:      "native FSM framed",
		},
	}
	if !readOnly {
		return surfaces
	}
	registeredReadOnly := map[string]bool{
		"telemetry":    true,
		"observatory":  true,
		"civilization": true,
		"public-proof": true,
		"ingestion":    true,
		"evidence":     true,
	}
	filtered := make([]OpsSurface, 0, len(registeredReadOnly))
	for _, surface := range surfaces {
		if registeredReadOnly[surface.ID] {
			filtered = append(filtered, surface)
		}
	}
	return filtered
}

func buildOpsObservationData(now time.Time, telemetry *OpsTelemetryData, hive *OpsHiveData, civilization *OpsCivilizationAssemblyData, canonical *OpsGitHubCanonicalData, sources []OpsHiveIntakeSource) *OpsObservationData {
	if civilization == nil {
		civilization = &OpsCivilizationAssemblyData{
			GeneratedAt:      "unavailable",
			ProjectionStatus: "unavailable",
		}
	}
	data := &OpsObservationData{
		GeneratedAt: formatOpsTime(now.Format(time.RFC3339)),
		Source:      "Safe hybrid: Work telemetry, Hive projection, Site typed projections, and Site-local intake records when configured.",
		Freshness:   "current render; individual rows may be unavailable, stale, or projection-only",
		Boundary:    "Observation is display only. It does not wake Hive, mutate Work, write EventGraph production state, deploy, close Test 001, or increase autonomy.",
		EvidenceRefs: []string{
			"site#195",
			"docs/designs/civilization-minimum-functioning-operational-front-end-v0.1.0.md",
			"docs/dark-factory/evidence/civilization-mfof-20260627/README.md",
		},
	}
	data.Metrics = []OpsObservationMetric{
		opsObservationMetric("Civilization health", opsObservationHealthValue(telemetry, hive, civilization), opsObservationHealthContext(telemetry, hive, civilization), opsObservationHealthState(telemetry, hive, civilization), "Site observation builder", data.GeneratedAt),
		opsObservationMetric("Factory throughput", fmt.Sprintf("%d", len(civilization.FactoryOrders)), "projected FactoryOrder records", opsProjectionState(civilization.ProjectionStatus), "Civilization assembly projection", civilization.GeneratedAt),
		opsObservationMetric("Live agents", fmt.Sprintf("%d", opsObservationLiveAgentCount(telemetry, hive)), "active or processing actors", opsObservationAgentState(telemetry, hive), "Work telemetry + Hive runtime evidence", opsObservationLatest(opsObservationTelemetryGeneratedAt(telemetry), opsObservationHiveGeneratedAt(hive))),
		opsObservationMetric("Cycle cost", opsObservationCostValue(telemetry), "latest pipeline cycle", opsObservationPipelineState(telemetry), "Work pipeline report", opsObservationPipelineUpdated(telemetry)),
		opsObservationMetric("Tokens", opsObservationTokenValue(telemetry), "input plus output when reported", opsObservationPipelineState(telemetry), "Work pipeline report", opsObservationPipelineUpdated(telemetry)),
		opsObservationMetric("Open blockers", opsObservationBlockerValue(canonical), "parked or human-scope issue posture", opsProjectionState(opsObservationCanonicalProjectionState(canonical)), "GitHub canonical projection", opsObservationCanonicalGeneratedAt(canonical)),
	}
	data.Canary = opsObservationCanary(civilization)
	data.AgentRows = opsObservationAgentRows(telemetry, hive)
	data.FactoryStages = opsObservationFactoryStages(sources, civilization)
	data.SpendRows = opsObservationSpendRows(telemetry, hive)
	data.Exceptions = opsObservationExceptions(telemetry, hive, canonical)
	return data
}

func opsObservationMetric(label, value, context, state, source, updatedAt string) OpsObservationMetric {
	return OpsObservationMetric{Label: label, Value: opsValueOr(value, "unavailable"), Context: context, State: opsValueOr(state, "unavailable"), Source: source, UpdatedAt: opsValueOr(updatedAt, "unavailable")}
}

func opsObservationHealthValue(t *OpsTelemetryData, h *OpsHiveData, c *OpsCivilizationAssemblyData) string {
	if opsObservationTelemetryCurrent(t) && opsObservationHiveCurrent(h) && opsObservationCivilizationCurrent(c) {
		return "current"
	}
	if opsObservationTelemetryCurrent(t) || opsObservationHiveCurrent(h) || opsObservationCivilizationDegraded(c) {
		return "degraded"
	}
	return "unavailable"
}

func opsObservationHealthContext(t *OpsTelemetryData, h *OpsHiveData, c *OpsCivilizationAssemblyData) string {
	if t != nil && t.Error != "" {
		return "telemetry unavailable"
	}
	if h != nil && h.ProjectionError != "" {
		return "Hive projection unavailable"
	}
	if !opsObservationCivilizationCurrent(c) {
		state := opsProjectionState("")
		if c != nil {
			state = opsProjectionState(c.ProjectionStatus)
		}
		if state == "unavailable" {
			return "Civilization projection unavailable"
		}
		return "Civilization projection " + state
	}
	return "sources checked"
}

func opsObservationHealthState(t *OpsTelemetryData, h *OpsHiveData, c *OpsCivilizationAssemblyData) string {
	if opsObservationTelemetryCurrent(t) && opsObservationHiveCurrent(h) && opsObservationCivilizationCurrent(c) {
		return "current"
	}
	if opsObservationTelemetryCurrent(t) || opsObservationHiveCurrent(h) || opsObservationCivilizationDegraded(c) {
		return "degraded"
	}
	return "unavailable"
}

func opsObservationTelemetryCurrent(t *OpsTelemetryData) bool {
	return t != nil && t.Error == ""
}

func opsObservationTelemetryGeneratedAt(t *OpsTelemetryData) string {
	if t == nil {
		return ""
	}
	return t.GeneratedAt
}

func opsObservationHiveCurrent(h *OpsHiveData) bool {
	return h != nil && h.ProjectionError == ""
}

func opsObservationHiveGeneratedAt(h *OpsHiveData) string {
	if h == nil {
		return ""
	}
	return h.GeneratedAt
}

func opsObservationCivilizationCurrent(c *OpsCivilizationAssemblyData) bool {
	if c == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(c.ProjectionStatus)) {
	case "current", "available", "live", opsCivilizationProjectionStatusComplete:
		return true
	default:
		return false
	}
}

func opsObservationCivilizationDegraded(c *OpsCivilizationAssemblyData) bool {
	if c == nil {
		return false
	}
	state := opsProjectionState(c.ProjectionStatus)
	return state != "" && state != "unavailable"
}

func opsProjectionState(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "missing", "unavailable":
		return "unavailable"
	case "current", "available", "live":
		return "current"
	default:
		if strings.Contains(strings.ToLower(value), "projection") {
			return "projection-only"
		}
		return strings.ToLower(value)
	}
}

func opsObservationLiveAgentCount(t *OpsTelemetryData, h *OpsHiveData) int {
	count := 0
	if t != nil {
		count = t.ActiveAgentCount
		if t.ProcessingAgentCount > count {
			count = t.ProcessingAgentCount
		}
	}
	if h != nil && h.RuntimeEvidence.AgentEvents.ObservedActive > count {
		count = h.RuntimeEvidence.AgentEvents.ObservedActive
	}
	return count
}

func opsObservationAgentState(t *OpsTelemetryData, h *OpsHiveData) string {
	if opsObservationLiveAgentCount(t, h) > 0 {
		return "current"
	}
	if t != nil && t.Error == "" {
		return "current zero"
	}
	if h != nil && h.ProjectionError == "" {
		return "projection-only"
	}
	return "unavailable"
}

func opsObservationCostValue(t *OpsTelemetryData) string {
	if t == nil || t.Pipeline == nil {
		return "unavailable"
	}
	return fmt.Sprintf("$%.4f", t.Pipeline.TotalCostUSD)
}

func opsObservationTokenValue(t *OpsTelemetryData) string {
	if t == nil || t.Pipeline == nil {
		return "unavailable"
	}
	return fmt.Sprintf("%d", t.Pipeline.TotalTokens)
}

func opsObservationPipelineState(t *OpsTelemetryData) string {
	if t == nil || t.Pipeline == nil {
		return "unavailable"
	}
	if t.Pipeline.Status == "" {
		return "projection-only"
	}
	return strings.ToLower(t.Pipeline.Status)
}

func opsObservationPipelineUpdated(t *OpsTelemetryData) string {
	if t == nil || t.Pipeline == nil {
		return "unavailable"
	}
	return formatOpsTime(t.Pipeline.UpdatedAt)
}

func opsObservationLatest(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return "unavailable"
}

func opsObservationBlockerValue(c *OpsGitHubCanonicalData) string {
	if c == nil {
		return "unavailable"
	}
	return fmt.Sprintf("%d", c.Progress.ParkedOpenIssueCount+c.AutonomyFrontier.NeedsHumanScopeIssueCount+c.AutonomyFrontier.ProtectedActionIssueCount)
}

func opsObservationCanonicalProjectionState(c *OpsGitHubCanonicalData) string {
	if c == nil {
		return ""
	}
	return c.ProjectionState
}

func opsObservationCanonicalGeneratedAt(c *OpsGitHubCanonicalData) string {
	if c == nil {
		return "unavailable"
	}
	return c.GeneratedAt
}

func opsObservationCanary(c *OpsCivilizationAssemblyData) OpsObservationCanary {
	canary := OpsObservationCanary{
		Status:    "unavailable",
		Summary:   "No Level 1 canary issue-discovery state is projected.",
		Source:    "Civilization assembly projection",
		UpdatedAt: "unavailable",
		Boundary:  "Display-only canary summary. This does not authorize Hive wake, issue closure, PR merge, deploy, Test 001 GREEN, value allocation, or autonomy increase.",
	}
	if c == nil {
		return canary
	}
	canary.UpdatedAt = opsValueOr(c.GeneratedAt, "unavailable")
	canary.DiscoveredIssueCount = len(c.IssueIntake.Issues)
	cards := opsObservationIssueScanCards(c.IssueScanKanban)
	if canary.DiscoveredIssueCount == 0 {
		canary.DiscoveredIssueCount = opsObservationDistinctIssueCount(cards)
	}
	if canary.DiscoveredIssueCount == 0 && len(cards) == 0 {
		return canary
	}

	canary.Status = opsProjectionState(c.ProjectionStatus)
	if canary.Status == "unavailable" && strings.TrimSpace(c.IssueScanKanban.Status) != "" {
		canary.Status = opsCivilizationStatusValue(c.IssueScanKanban.Status)
	}
	aggregates := opsObservationCanaryAggregates(cards)
	aggregateKeys := make([]string, 0, len(aggregates))
	for key := range aggregates {
		aggregateKeys = append(aggregateKeys, key)
	}
	sort.Strings(aggregateKeys)
	for _, key := range aggregateKeys {
		aggregate := aggregates[key]
		switch aggregate.State {
		case "pr_ready":
			canary.PRReadyIssueCount++
		case "human_action":
			canary.ParkedIssueCount++
			canary.HumanActionCount++
		case "parked", "blocked":
			canary.ParkedIssueCount++
		}
		if aggregate.HasProtectedAction {
			canary.ProtectedActionCount++
		}
		canary.Rows = append(canary.Rows, aggregate.Row)
	}
	if len(canary.Rows) == 0 {
		for _, issue := range c.IssueIntake.Issues {
			row := OpsObservationCanaryIssueRow{
				Issue:       opsObservationIssueLabel(issue.Repo, issue.Number),
				Title:       opsValueOr(issue.Title, "title not projected"),
				State:       opsValueOr(issue.Readiness, opsValueOr(issue.StateReason, "projected intake")),
				Blocker:     opsValueOr(issue.RiskClass, "blocker not projected"),
				Action:      opsValueOr(issue.PRReadyWhen, "Human must mark the issue PR-ready before execution."),
				EvidenceRef: opsFirstNonEmpty(append(issue.SourceRefs, issue.URL)...),
			}
			canary.Rows = append(canary.Rows, row)
			switch opsObservationCanaryStateClass(row.State, row.Blocker) {
			case "pr_ready":
				canary.PRReadyIssueCount++
			case "human_action":
				canary.ParkedIssueCount++
				canary.HumanActionCount++
			case "parked", "blocked":
				canary.ParkedIssueCount++
			}
			if opsObservationCanaryProtectedBlocker(row.Blocker) {
				canary.ProtectedActionCount++
			}
		}
	}
	sort.Slice(canary.Rows, func(i, j int) bool {
		if canary.Rows[i].State != canary.Rows[j].State {
			return canary.Rows[i].State < canary.Rows[j].State
		}
		return canary.Rows[i].Issue < canary.Rows[j].Issue
	})
	canary.Summary = fmt.Sprintf("%d issue(s) discovered: %d PR-ready, %d parked, %d awaiting human action.",
		canary.DiscoveredIssueCount,
		canary.PRReadyIssueCount,
		canary.ParkedIssueCount,
		canary.HumanActionCount,
	)
	return canary
}

type opsObservationCanaryAggregate struct {
	Row                OpsObservationCanaryIssueRow
	State              string
	Priority           int
	HasProtectedAction bool
}

func opsObservationCanaryAggregates(cards []OpsCivilizationIssueScanKanbanCard) map[string]opsObservationCanaryAggregate {
	aggregates := map[string]opsObservationCanaryAggregate{}
	for _, card := range cards {
		key := opsObservationCanaryCardIssueKey(card)
		if key == "" {
			continue
		}
		row := opsObservationCanaryRow(card)
		state := opsObservationCanaryStateClass(row.State, row.Blocker)
		row.State = state
		aggregate := opsObservationCanaryAggregate{
			Row:                row,
			State:              state,
			Priority:           opsObservationCanaryStatePriority(state),
			HasProtectedAction: opsObservationCanaryProtectedCard(card),
		}
		existing, ok := aggregates[key]
		if ok {
			aggregate.HasProtectedAction = aggregate.HasProtectedAction || existing.HasProtectedAction
			if existing.Priority > aggregate.Priority {
				existing.HasProtectedAction = aggregate.HasProtectedAction
				aggregates[key] = existing
				continue
			}
		}
		aggregates[key] = aggregate
	}
	return aggregates
}

func opsObservationIssueScanCards(kanban OpsCivilizationIssueScanKanban) []OpsCivilizationIssueScanKanbanCard {
	cards := []OpsCivilizationIssueScanKanbanCard{}
	for _, column := range kanban.Columns {
		for _, card := range column.Cards {
			if strings.TrimSpace(card.CurrentState) == "" {
				card.CurrentState = column.State
			}
			cards = append(cards, card)
		}
	}
	return cards
}

func opsObservationDistinctIssueCount(cards []OpsCivilizationIssueScanKanbanCard) int {
	seen := map[string]bool{}
	for _, card := range cards {
		ref := card.SelectedIssue
		if strings.TrimSpace(ref.Repo) == "" || ref.Number == 0 {
			ref = card.TargetIssue
		}
		key := opsObservationIssueLabel(ref.Repo, ref.Number)
		if key != "issue not projected" {
			seen[key] = true
		}
	}
	return len(seen)
}

func opsObservationCanaryCardIssueKey(card OpsCivilizationIssueScanKanbanCard) string {
	ref := card.SelectedIssue
	if strings.TrimSpace(ref.Repo) == "" || ref.Number == 0 {
		ref = card.TargetIssue
	}
	label := opsObservationIssueLabel(ref.Repo, ref.Number)
	if label == "issue not projected" {
		return ""
	}
	return label
}

func opsObservationCanaryRow(card OpsCivilizationIssueScanKanbanCard) OpsObservationCanaryIssueRow {
	ref := card.SelectedIssue
	if strings.TrimSpace(ref.Repo) == "" || ref.Number == 0 {
		ref = card.TargetIssue
	}
	blocker := "none projected"
	action := opsValueOr(card.CompletionGate, "no required action projected")
	evidence := opsFirstNonEmpty(append(card.EvidenceRefs, card.SourceRefs...)...)
	if len(card.Blockers) > 0 {
		blocker = opsValueOr(card.Blockers[0].BlockerType, "blocker projected")
		action = opsValueOr(card.Blockers[0].RequiredAction, action)
		evidence = opsFirstNonEmpty(append(card.Blockers[0].EvidenceRefs, append(card.Blockers[0].SourceRefs, evidence)...)...)
	}
	return OpsObservationCanaryIssueRow{
		Issue:       opsObservationIssueLabel(ref.Repo, ref.Number),
		Title:       opsValueOr(ref.Title, "title not projected"),
		State:       opsValueOr(card.CurrentState, "projected"),
		Blocker:     blocker,
		Action:      action,
		EvidenceRef: opsValueOr(evidence, "evidence not projected"),
	}
}

func opsObservationIssueLabel(repo string, number int) string {
	repo = strings.TrimSpace(repo)
	if repo == "" || number == 0 {
		return "issue not projected"
	}
	return fmt.Sprintf("%s#%d", repo, number)
}

func opsObservationCanaryStateClass(state string, blocker string) string {
	state = strings.ToLower(strings.TrimSpace(state))
	blocker = strings.ToLower(strings.TrimSpace(blocker))
	switch {
	case opsObservationCanaryHumanActionBlocker(blocker), opsObservationCanaryProtectedBlocker(blocker), state == "human_action":
		return "human_action"
	case state == "blocked":
		return "blocked"
	case state == "parked":
		return "parked"
	case state == "pr_ready", state == "ready", state == "ready_for_pr":
		return "pr_ready"
	default:
		return opsValueOr(state, "projected")
	}
}

func opsObservationCanaryStatePriority(state string) int {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "human_action":
		return 50
	case "blocked":
		return 40
	case "parked":
		return 30
	case "pr_ready":
		return 20
	default:
		return 10
	}
}

func opsObservationCanaryProtectedCard(card OpsCivilizationIssueScanKanbanCard) bool {
	for _, blocker := range card.Blockers {
		if opsObservationCanaryProtectedBlocker(blocker.BlockerType) {
			return true
		}
	}
	return false
}

func opsObservationCanaryProtectedBlocker(blocker string) bool {
	blocker = strings.ToLower(strings.TrimSpace(blocker))
	return blocker == "protected_action" || strings.Contains(blocker, "protected")
}

func opsObservationCanaryHumanActionBlocker(blocker string) bool {
	blocker = strings.ToLower(strings.TrimSpace(blocker))
	return blocker == "needs_human_scope" || strings.Contains(blocker, "human")
}

func opsFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func opsObservationAgentRows(t *OpsTelemetryData, h *OpsHiveData) []OpsObservationAgentRow {
	rows := []OpsObservationAgentRow{}
	if t != nil {
		for _, agent := range t.RecentAgents {
			rows = append(rows, OpsObservationAgentRow{
				Role:     opsValueOr(agent.Role, "agent"),
				State:    opsValueOr(agent.State, "unavailable"),
				Model:    opsValueOr(agent.Model, "model unavailable"),
				Activity: opsValueOr(agent.LastMessage, "no recent message"),
				LastSeen: opsValueOr(t.GeneratedAt, "unavailable"),
				Source:   "Work telemetry",
				Boundary: "display only",
			})
		}
	}
	if len(rows) < 6 && h != nil {
		for _, agent := range h.RuntimeEvidence.AgentEvents.ActiveAgents {
			rows = append(rows, OpsObservationAgentRow{
				Role:     opsValueOr(agent.Role, agent.ActorID),
				State:    "projection-only",
				Model:    opsValueOr(agent.Model, "model unavailable"),
				Activity: opsValueOr(agent.Name, "runtime evidence projected"),
				LastSeen: formatOpsTime(agent.SpawnedAt),
				Source:   "Hive runtime evidence",
				Boundary: "projection-only",
			})
			if len(rows) >= 6 {
				break
			}
		}
	}
	if len(rows) == 0 {
		rows = append(rows, OpsObservationAgentRow{
			Role:     "No live agents projected",
			State:    "unavailable",
			Model:    "unavailable",
			Activity: "No telemetry or runtime source returned active agents for this render.",
			LastSeen: "unavailable",
			Source:   "Site observation fallback",
			Boundary: "no fake green lights",
		})
	}
	return rows
}

func opsObservationFactoryStages(sources []OpsHiveIntakeSource, c *OpsCivilizationAssemblyData) []OpsObservationStage {
	submitted := 0
	for _, source := range sources {
		if source.Kind == opsMarkdownArtifactSourceKind {
			submitted++
		}
	}
	return []OpsObservationStage{
		{Label: "Submitted artifact", Count: submitted, State: opsCountState(submitted, "current zero"), Source: "Site-local artifact intake", Boundary: "artifact intake only"},
		{Label: "FactoryOrder candidate", Count: 0, State: "unavailable", Source: "governed review process", Boundary: "conversion requires separate review"},
		{Label: "Confirmed FactoryOrder", Count: len(c.FactoryOrders), State: opsProjectionState(c.ProjectionStatus), Source: "Civilization assembly projection", Boundary: "display only"},
	}
}

func opsCountState(count int, zeroState string) string {
	if count > 0 {
		return "current"
	}
	return zeroState
}

func opsObservationSpendRows(t *OpsTelemetryData, h *OpsHiveData) []OpsObservationSpendRow {
	rows := []OpsObservationSpendRow{
		{Label: "Pipeline cost", Value: opsObservationCostValue(t), Limit: "cycle cap not projected", State: opsObservationPipelineState(t), Source: "Work pipeline report"},
		{Label: "Pipeline tokens", Value: opsObservationTokenValue(t), Limit: "token cap not projected", State: opsObservationPipelineState(t), Source: "Work pipeline report"},
	}
	if h != nil && h.RuntimeEvidence.LastQueuedRunRequest != nil {
		maxCost := "not projected"
		if h.RuntimeEvidence.LastQueuedRunRequest.BudgetMaxCostUSD != nil {
			maxCost = fmt.Sprintf("$%.2f", *h.RuntimeEvidence.LastQueuedRunRequest.BudgetMaxCostUSD)
		}
		maxIterations := "iterations not projected"
		if h.RuntimeEvidence.LastQueuedRunRequest.BudgetMaxIterations != nil {
			maxIterations = fmt.Sprintf("%d iterations", *h.RuntimeEvidence.LastQueuedRunRequest.BudgetMaxIterations)
		}
		rows = append(rows, OpsObservationSpendRow{
			Label:  "Queued run budget",
			Value:  maxCost,
			Limit:  maxIterations,
			State:  "projection-only",
			Source: "Hive queued-run projection",
		})
	}
	return rows
}

func opsObservationExceptions(t *OpsTelemetryData, h *OpsHiveData, c *OpsGitHubCanonicalData) []OpsObservationException {
	exceptions := []OpsObservationException{}
	if t != nil && t.Error != "" {
		exceptions = append(exceptions, OpsObservationException{Label: "Telemetry", State: "unavailable", Reason: t.Error, EvidenceRef: t.WorkURL})
	}
	if t != nil && t.PipelineError != "" {
		exceptions = append(exceptions, OpsObservationException{Label: "Pipeline report", State: "unavailable", Reason: t.PipelineError, EvidenceRef: t.PipelineURL})
	}
	if h != nil && h.ProjectionError != "" {
		exceptions = append(exceptions, OpsObservationException{Label: "Hive projection", State: "unavailable", Reason: h.ProjectionError, EvidenceRef: h.ProjectionSource})
	}
	if c != nil && c.ScannerArtifact.Error != "" {
		exceptions = append(exceptions, OpsObservationException{Label: "Issue scanner", State: "degraded", Reason: c.ScannerArtifact.Error, EvidenceRef: c.ScannerArtifact.Path})
	}
	if len(exceptions) == 0 {
		exceptions = append(exceptions, OpsObservationException{Label: "Exception queue", State: "current zero", Reason: "No source errors projected for this render.", EvidenceRef: "Site render"})
	}
	return exceptions
}

func buildOpsControlData(now time.Time, readOnly bool, sources []OpsHiveIntakeSource, hive *OpsHiveData, confirmation, errMsg string) *OpsControlData {
	status := "persisted"
	if readOnly {
		status = "graph store unavailable"
	}
	modelTargets := opsControlModelTargetOptions(hive)
	return &OpsControlData{
		GeneratedAt:   formatOpsTime(now.Format(time.RFC3339)),
		StorageStatus: status,
		Boundary:      "Queue-intent-only Site surface. No model policy, budget, Council, Hive, EventGraph, Work, deploy, approval, merge, or autonomy side effect is performed here.",
		ReadOnly:      readOnly,
		Confirmation:  confirmation,
		Error:         errMsg,
		Actions:       opsControlActions(),
		ModelTargets:  modelTargets,
		Intents:       opsControlIntentsFromSources(sources),
		EvidenceRefs:  []string{"site#195", "CFADA packet 20260629T120717Z-site195-cfada"},
	}
}

func opsControlActions() []OpsControlAction {
	return []OpsControlAction{
		{Kind: "model_policy", Label: "Role or agent model policy", ActionLabel: "Request model change", Description: "Queue a proposed role or agent model change for human review.", Placeholder: "Model: gpt-5; reason: reduce review latency.", Status: "Pending human approval", TargetCatalog: true},
		{Kind: "budget_policy", Label: "Budget policy", ActionLabel: "Propose budget change", Description: "Queue a proposed spend, iteration, or token cap change for a role or agent.", Placeholder: "Max cycle cost: $10; token cap: 180k; reason: bounded exploration.", Status: "Pending human approval", TargetCatalog: true},
		{Kind: "council_agenda", Label: "Council agenda", ActionLabel: "Draft agenda update", Description: "Draft the next Council agenda for review without convening agents.", Placeholder: "Agenda: review issue-canonical cutover blockers and MFOF evidence.", Status: "Queued"},
		{Kind: "council_meeting", Label: "Ad-hoc Council meeting", ActionLabel: "Queue Council Meeting request", Description: "Queue a Council Meeting request for a primary role or agent. No agents are contacted by this form.", Placeholder: "Topic: evaluate FactoryOrder candidate acceptance criteria.", Status: "Pending human approval", TargetCatalog: true},
		{Kind: "evolution_action", Label: "Civilization evolution action", ActionLabel: "Propose evolution action", Description: "Queue a proposed next action for governed Civilization development.", Placeholder: "Action: split runtime authority wiring into Site/Hive/EventGraph child issues.", Status: "Blocked: protected action required"},
	}
}

func opsControlModelTargetOptions(hive *OpsHiveData) []OpsModelTargetOption {
	return opsModelTargetOptionsFromHive(hive)
}

func opsHiveModelTargetOptions(selection OpsHiveModelSelection) []OpsModelTargetOption {
	return opsModelTargetOptionsFromHive(&OpsHiveData{ModelSelection: selection})
}

func opsModelTargetOptionsFromHive(hive *OpsHiveData) []OpsModelTargetOption {
	options := []OpsModelTargetOption{}
	index := map[string]int{}
	add := func(value, status, source string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		key := strings.ToLower(value)
		if i, ok := index[key]; ok {
			if options[i].Status == "" && status != "" {
				options[i].Status = status
			}
			if source != "" && !strings.Contains(options[i].Source, source) {
				if options[i].Source == "" {
					options[i].Source = source
				} else {
					options[i].Source += "; " + source
				}
			}
			return
		}
		index[key] = len(options)
		options = append(options, OpsModelTargetOption{
			Value:  value,
			Label:  opsModelTargetLabel(value),
			Status: status,
			Source: source,
		})
	}

	for _, role := range opsCanonicalRoles {
		if strings.Contains(role, "human") || strings.Contains(role, "draft PR") {
			continue
		}
		add(role, "canonical", "civic role catalog")
	}
	personaNames := make([]string, 0, len(sitepersonas.HiveStatus))
	for name, status := range sitepersonas.HiveStatus {
		if status == "absorbed" || status == "retired" {
			continue
		}
		personaNames = append(personaNames, name)
	}
	sort.Strings(personaNames)
	for _, name := range personaNames {
		add(name, sitepersonas.HiveStatus[name], "Hive persona catalog")
	}
	if hive != nil {
		for _, item := range hive.ModelSelection.Assignments {
			status := "projected assignment"
			if item.CanOperate {
				status = "projected assignment; can operate"
			}
			add(item.Role, status, "Hive model-selection projection")
		}
		for _, item := range hive.Lifecycle {
			add(item.Role, opsValueOr(item.LifecycleStatus, "projected lifecycle"), "Hive lifecycle projection")
			add(item.ActorID, opsValueOr(item.LifecycleStatus, "projected agent"), "Hive lifecycle agent")
		}
	}
	sort.SliceStable(options, func(i, j int) bool {
		return strings.ToLower(options[i].Value) < strings.ToLower(options[j].Value)
	})
	return options
}

func opsModelTargetLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	return strings.ReplaceAll(value, "-", " ")
}

func opsModelTargetValueSet(options []OpsModelTargetOption) map[string]bool {
	set := make(map[string]bool, len(options))
	for _, option := range options {
		value := strings.ToLower(strings.TrimSpace(option.Value))
		if value != "" {
			set[value] = true
		}
	}
	return set
}

func opsControlIntentsFromSources(sources []OpsHiveIntakeSource) []OpsControlIntent {
	intents := []OpsControlIntent{}
	for _, source := range sources {
		if source.Kind != opsControlIntentSourceKind {
			continue
		}
		meta := opsParseControlIntentDetail(source.Detail)
		intents = append(intents, OpsControlIntent{
			ID:          source.ID,
			Kind:        meta.IntentKind,
			Title:       source.Title,
			Target:      meta.Target,
			RequestedBy: meta.RequestedBy,
			Status:      opsValueOr(source.Status, "Queued"),
			CreatedAt:   source.CreatedAt.Format("2006-01-02 15:04"),
			Detail:      source.Content,
		})
	}
	if len(intents) == 0 {
		intents = append(intents, OpsControlIntent{
			Kind:        "none",
			Title:       "No queued control intents",
			Target:      "none",
			RequestedBy: "none",
			Status:      "Queued",
			CreatedAt:   "unavailable",
			Detail:      "Create a request to record queue-only control intent.",
		})
	}
	return intents
}

func opsParseControlIntentDetail(detail string) opsControlIntentMeta {
	var meta opsControlIntentMeta
	if err := json.Unmarshal([]byte(detail), &meta); err == nil {
		return meta
	}
	values := map[string]string{}
	for _, part := range strings.Split(detail, ";") {
		k, v, ok := strings.Cut(part, "=")
		if ok {
			values[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return opsControlIntentMeta{
		IntentKind:  values["intent_kind"],
		RequestedBy: values["requested_by"],
		Target:      values["target"],
	}
}

func opsControlIntentParamsFromForm(r *http.Request, fallbackRequester string, allowedModelTargets map[string]bool) (CreateOpsHiveIntakeSourceParams, error) {
	kind := strings.TrimSpace(r.FormValue("intent_kind"))
	title := strings.TrimSpace(r.FormValue("title"))
	target := strings.TrimSpace(r.FormValue("target"))
	requestedBy := strings.TrimSpace(r.FormValue("requested_by"))
	body := strings.TrimSpace(r.FormValue("content"))
	if requestedBy == "" {
		requestedBy = fallbackRequester
	}
	if !opsControlIntentKindAllowed(kind) {
		return CreateOpsHiveIntakeSourceParams{}, errors.New("unsupported control intent kind")
	}
	if title == "" {
		return CreateOpsHiveIntakeSourceParams{}, errors.New("intent title is required")
	}
	if body == "" {
		return CreateOpsHiveIntakeSourceParams{}, errors.New("intent detail is required")
	}
	if opsControlIntentKindUsesModelTarget(kind) {
		if target == "" {
			return CreateOpsHiveIntakeSourceParams{}, errors.New("target must be selected from available agents/roles")
		}
		if !allowedModelTargets[strings.ToLower(target)] {
			return CreateOpsHiveIntakeSourceParams{}, errors.New("target must be selected from available agents/roles")
		}
	}
	if len(body) > opsHiveIntakeMaxContentBytes {
		return CreateOpsHiveIntakeSourceParams{}, fmt.Errorf("intent detail must be %d bytes or less", opsHiveIntakeMaxContentBytes)
	}
	if opsControlContainsForbiddenVerb(title) || opsControlContainsForbiddenVerb(target) || opsControlContainsForbiddenVerb(requestedBy) || opsControlContainsForbiddenVerb(body) {
		return CreateOpsHiveIntakeSourceParams{}, errors.New("control intent uses forbidden execution vocabulary")
	}
	detail, err := json.Marshal(opsControlIntentMeta{IntentKind: kind, RequestedBy: requestedBy, Target: target})
	if err != nil {
		return CreateOpsHiveIntakeSourceParams{}, fmt.Errorf("could not encode control intent metadata: %w", err)
	}
	return CreateOpsHiveIntakeSourceParams{
		ProfileSlug: opsHiveProfileSlugFromRequest(r),
		Kind:        opsControlIntentSourceKind,
		Title:       truncateOpsHiveIntakeTitle(title),
		Detail:      string(detail),
		Content:     body,
		Status:      "Queued",
	}, nil
}

func opsControlIntentKindAllowed(kind string) bool {
	switch kind {
	case "model_policy", "budget_policy", "council_agenda", "council_meeting", "evolution_action":
		return true
	default:
		return false
	}
}

func opsControlIntentKindUsesModelTarget(kind string) bool {
	switch kind {
	case "model_policy", "budget_policy", "council_meeting":
		return true
	default:
		return false
	}
}

func opsControlContainsForbiddenVerb(value string) bool {
	lower := strings.ToLower(value)
	forbidden := map[string]bool{
		"invoke":  true,
		"set":     true,
		"execute": true,
		"start":   true,
		"launch":  true,
		"deploy":  true,
		"write":   true,
		"trigger": true,
		"approve": true,
		"merge":   true,
	}
	for _, token := range strings.FieldsFunc(lower, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if forbidden[token] {
			return true
		}
	}
	return false
}

func buildOpsFactoryData(now time.Time, readOnly bool, sources []OpsHiveIntakeSource, submitterQuery, confirmation, errMsg string) *OpsFactoryData {
	submissions := opsFactorySubmissionsFromSources(sources, submitterQuery)
	return &OpsFactoryData{
		GeneratedAt:    formatOpsTime(now.Format(time.RFC3339)),
		StorageStatus:  opsFactoryStorageStatus(readOnly),
		Boundary:       "Artifact-intake-only Site surface. Submitted Markdown is not a FactoryOrder. Conversion requires a separate governed review process.",
		ReadOnly:       readOnly,
		SubmitterQuery: submitterQuery,
		Confirmation:   confirmation,
		Error:          errMsg,
		Submissions:    submissions,
		Stages:         opsFactoryStages(submissions),
		EvidenceRefs:   []string{"site#195", "MFOF design v0.1.1"},
	}
}

func opsFactoryStorageStatus(readOnly bool) string {
	if readOnly {
		return "graph store unavailable"
	}
	return "persisted"
}

func opsFactorySubmissionsFromSources(sources []OpsHiveIntakeSource, submitterQuery string) []OpsFactorySubmission {
	query := strings.ToLower(strings.TrimSpace(submitterQuery))
	out := []OpsFactorySubmission{}
	for _, source := range sources {
		if source.Kind != opsMarkdownArtifactSourceKind {
			continue
		}
		meta := opsParseFactorySubmissionMeta(source.Detail)
		submitter := opsValueOr(meta.SubmitterName, "unknown submitter")
		if query != "" {
			haystack := strings.ToLower(submitter + " " + meta.SubmitterEmail + " " + meta.SubmitterOrg)
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		out = append(out, OpsFactorySubmission{
			ID:        source.ID,
			Title:     source.Title,
			Submitter: submitter,
			Email:     meta.SubmitterEmail,
			Org:       meta.SubmitterOrg,
			Filename:  meta.Filename,
			SHA256:    meta.SHA256,
			Status:    opsValueOr(source.Status, "submitted"),
			CreatedAt: source.CreatedAt.Format("2006-01-02 15:04"),
			Notes:     meta.Notes,
			Preview:   opsHiveBriefExcerpt(source.Content, 220),
		})
	}
	if len(out) == 0 {
		out = append(out, OpsFactorySubmission{
			Title:     "No submitted artifacts",
			Submitter: opsValueOr(submitterQuery, "all submitters"),
			Status:    "unavailable",
			CreatedAt: "unavailable",
			Preview:   "No Markdown artifacts match the current filter.",
		})
	}
	return out
}

func opsParseFactorySubmissionMeta(detail string) opsFactorySubmissionMeta {
	var meta opsFactorySubmissionMeta
	_ = json.Unmarshal([]byte(detail), &meta)
	return meta
}

func opsFactoryStages(submissions []OpsFactorySubmission) []OpsFactoryStage {
	submitted := 0
	for _, submission := range submissions {
		if submission.ID != "" && strings.EqualFold(submission.Status, "submitted") {
			submitted++
		}
	}
	return []OpsFactoryStage{
		{Label: "Submitted artifact", Count: submitted, State: opsCountState(submitted, "current zero"), Boundary: "Site artifact intake only"},
		{Label: "FactoryOrder candidate", Count: 0, State: "unavailable", Boundary: "requires governed review"},
		{Label: "Confirmed FactoryOrder", Count: 0, State: "unavailable", Boundary: "not created by upload"},
	}
}

func opsFactorySubmissionParamsFromRequest(r *http.Request) (CreateOpsHiveIntakeSourceParams, error) {
	if err := r.ParseMultipartForm(int64(opsHiveIntakeMaxContentBytes + 8192)); err != nil {
		return CreateOpsHiveIntakeSourceParams{}, errors.New("multipart Markdown upload is required")
	}
	submitterName := strings.TrimSpace(r.FormValue("submitter_name"))
	submitterEmail := strings.TrimSpace(r.FormValue("submitter_email"))
	submitterOrg := strings.TrimSpace(r.FormValue("submitter_org"))
	title := strings.TrimSpace(r.FormValue("title"))
	notes := strings.TrimSpace(r.FormValue("notes"))
	if submitterName == "" {
		return CreateOpsHiveIntakeSourceParams{}, errors.New("submitter name is required")
	}
	file, header, err := r.FormFile("artifact")
	if err != nil {
		return CreateOpsHiveIntakeSourceParams{}, errors.New("Markdown artifact file is required")
	}
	defer file.Close()
	content, filename, hash, err := opsReadMarkdownArtifact(file, header)
	if err != nil {
		return CreateOpsHiveIntakeSourceParams{}, err
	}
	if title == "" {
		title = strings.TrimSuffix(filename, filepath.Ext(filename))
	}
	meta := opsFactorySubmissionMeta{
		SubmitterName:  submitterName,
		SubmitterEmail: submitterEmail,
		SubmitterOrg:   submitterOrg,
		Filename:       filename,
		SHA256:         hash,
		Notes:          notes,
	}
	detail, err := json.Marshal(meta)
	if err != nil {
		return CreateOpsHiveIntakeSourceParams{}, fmt.Errorf("could not encode artifact metadata: %w", err)
	}
	return CreateOpsHiveIntakeSourceParams{
		ProfileSlug: opsHiveProfileSlugFromRequest(r),
		Kind:        opsMarkdownArtifactSourceKind,
		Title:       truncateOpsHiveIntakeTitle(title),
		Detail:      string(detail),
		Content:     content,
		Status:      "submitted",
	}, nil
}

func opsReadMarkdownArtifact(file multipart.File, header *multipart.FileHeader) (string, string, string, error) {
	filename := "artifact.md"
	if header != nil && strings.TrimSpace(header.Filename) != "" {
		filename = filepath.Base(header.Filename)
	}
	if !strings.EqualFold(filepath.Ext(filename), ".md") {
		return "", "", "", errors.New("artifact filename must end in .md")
	}
	limited := io.LimitReader(file, int64(opsHiveIntakeMaxContentBytes+1))
	body, err := io.ReadAll(limited)
	if err != nil {
		return "", "", "", errors.New("could not read Markdown artifact")
	}
	if len(body) == 0 {
		return "", "", "", errors.New("Markdown artifact is empty")
	}
	if len(body) > opsHiveIntakeMaxContentBytes {
		return "", "", "", fmt.Errorf("Markdown artifact must be %d bytes or less", opsHiveIntakeMaxContentBytes)
	}
	sum := sha256.Sum256(body)
	return string(body), filename, hex.EncodeToString(sum[:]), nil
}

func buildOpsPublicProofData(now time.Time) *OpsPublicProofData {
	operationPacketState := opsPublicProofOperationPacketState(now)
	operationPacketLabels := []string{"operation-reference", "projection-only"}
	if operationPacketState == "stale" {
		operationPacketLabels = append(operationPacketLabels, "stale")
	}

	return &OpsPublicProofData{
		GeneratedAt:          formatOpsTime(now.Format(time.RFC3339)),
		ReferenceGeneratedAt: formatOpsTime(opsPublicProofOperationPacketGeneratedAt.Format(time.RFC3339)),
		ReferenceFreshUntil:  formatOpsTime(opsPublicProofOperationPacketFreshUntil.Format(time.RFC3339)),
		Source:               "Operation-approved public-proof evidence reference displayed from static Site records",
		Summary:              "Site displays the Operation-owned public-proof reference selected for /ops/public-proof. It is not live deployed public-reader proof or public-correction proof; those rows stay unavailable until a later Operation packet cites explicit proof refs.",
		Boundary:             "Display-only Site operator evidence/status surface. Site is not the source of truth. No deploy, private fetch, runtime execution, EventGraph write, Hive wake, Test 001 GREEN or closure, operation#26 closure, operation#45 closure, value allocation, residual-risk closure, autonomy increase, or production go-live.",
		RequiredLabels: []string{
			"unavailable",
			"stale",
			"fixture/local",
			"projection-only",
			"operation-reference",
			"deployed-reference",
			"live-reader-proof",
			"public-correction-proof",
		},
		Records: []OpsPublicProofRecord{
			{
				Category:     "Operation public-proof packet",
				State:        operationPacketState,
				Source:       "Operation-approved public-proof evidence reference",
				LastUpdate:   formatOpsTime(opsPublicProofOperationPacketGeneratedAt.Format(time.RFC3339)),
				Boundary:     "operation-reference; Site displays this packet but Operation remains source of truth.",
				EvidenceRef:  opsPublicProofOperationPacketPath,
				EvidenceHref: opsPublicProofOperationPacketURL(),
				Notes:        "Packet " + opsPublicProofOperationPacketID + " has proof class operation-approved-public-proof-reference; fresh_until " + formatOpsTime(opsPublicProofOperationPacketFreshUntil.Format(time.RFC3339)) + "; not live deployed proof.",
				Labels:       operationPacketLabels,
			},
			{
				Category:     "Site scope decision",
				State:        "projection-only",
				Source:       "GitHub issue authorization",
				LastUpdate:   "2026-06-28 09:31:13",
				Boundary:     "Authorizes this display route only; no proof, deploy, runtime, write, closure, or autonomy authority.",
				EvidenceRef:  "transpara-ai/site#191 scope comment",
				EvidenceHref: "https://github.com/transpara-ai/site/issues/191#issuecomment-4826247687",
				Notes:        "Human scope decision permits /ops/public-proof with static/manual evidence records and required unavailable labels.",
				Labels:       []string{"fixture/local", "projection-only"},
			},
			{
				Category:    "Deployed public URL evidence",
				State:       "unavailable",
				Source:      "manual Site evidence record",
				LastUpdate:  "not configured",
				Boundary:    "May show only a labeled public URL evidence reference. Site performs no private fetch and no deploy.",
				EvidenceRef: "no deployed public URL evidence ref configured",
				Notes:       "When a public URL is approved, this row can show the URL as evidence only; it must not become a runtime health claim by implication.",
				Labels:      []string{"unavailable", "deployed-reference"},
			},
			{
				Category:     "Live-reader proof",
				State:        "unavailable",
				Source:       "Operation-approved public-proof reference required",
				LastUpdate:   "not configured",
				Boundary:     "Display permitted only when an explicit evidence ref exists; otherwise unavailable.",
				EvidenceRef:  "transpara-ai/operation#45 pending",
				EvidenceHref: "https://github.com/transpara-ai/operation/issues/45",
				Notes:        "operation#45 remains the pending evidence path; this page does not close or satisfy it.",
				Labels:       []string{"unavailable", "live-reader-proof"},
			},
			{
				Category:     "Public-correction proof",
				State:        "unavailable",
				Source:       "Operation-approved public-proof reference required",
				LastUpdate:   "not configured",
				Boundary:     "Display permitted only when an explicit evidence ref exists; otherwise unavailable.",
				EvidenceRef:  "transpara-ai/operation#45 pending",
				EvidenceHref: "https://github.com/transpara-ai/operation/issues/45",
				Notes:        "No correction proof is claimed and no residual-risk closure is implied.",
				Labels:       []string{"unavailable", "public-correction-proof"},
			},
			{
				Category:    "Telemetry precedent",
				State:       "stale",
				Source:      "historical design carry-forward",
				LastUpdate:  "0.4.1 historical",
				Boundary:    "Design precedent only; not current live proof and not a Civilization arc baseline.",
				EvidenceRef: "docs/designs/telemetry-mission-control-design-v0.4.1.md",
				Notes:       "Carries forward honest staleness and no-fake-green-light display posture only.",
				Labels:      []string{"stale", "fixture/local"},
			},
		},
	}
}

func opsPublicProofOperationPacketState(now time.Time) string {
	if !now.UTC().Before(opsPublicProofOperationPacketFreshUntil) {
		return "stale"
	}
	return "projection-only"
}

func opsPublicProofOperationPacketURL() string {
	return "https://github.com/" + opsPublicProofOperationRepo + "/blob/" + opsPublicProofOperationPacketCommit + "/" + opsPublicProofOperationPacketRepoPath
}

func opsHiveShellCards() []OpsHiveShellCard {
	return []OpsHiveShellCard{
		{
			ID:          "overview",
			Label:       "Overview",
			Href:        "/ops/hive",
			Description: "Runtime summary and public build link.",
			Status:      "native summary",
		},
		{
			ID:          "intake",
			Label:       "Intake",
			Href:        "/ops/hive/intake",
			Description: "Source capture, interpretation, and brief readiness.",
			Status:      "persisted drafts",
		},
		{
			ID:          "runs",
			Label:       "Runs",
			Href:        "/ops/hive/runs",
			Description: "Run tower, pipeline phase, approvals, and artifacts.",
			Status:      "static sample",
		},
		{
			ID:          "agents",
			Label:       "Agents",
			Href:        "/ops/hive/agents",
			Description: "Agent topology, lifecycle state, and budget posture.",
			Status:      "static sample",
		},
		{
			ID:          "resources",
			Label:       "Resources",
			Href:        "/ops/hive/resources",
			Description: "Budget, queue, token, and capacity signals.",
			Status:      "static sample",
		},
	}
}

func buildOpsHiveShellData(active string) *OpsHiveShellData {
	intake := OpsHiveIntakeView{
		Status:          "draft ready",
		Confidence:      "0.91",
		SuggestedMode:   "full product pipeline",
		AuthorityLevel:  "human launch required",
		EstimatedBudget: "$18.00",
		StorageStatus:   "sample only",
		Sources: []OpsHiveSourceView{
			{Kind: "PRD", Title: "checkout-redesign.md", Detail: "Product intent and acceptance criteria", Status: "parsed"},
			{Kind: "URL", Title: "customer-notes", Detail: "Reference source queued for review", Status: "classified"},
			{Kind: "Repo", Title: "transpara-ai/site", Detail: "UI boundary context selected", Status: "scoped"},
		},
		MissingFields: []OpsHiveMissingFieldView{
			{Label: "Rollback owner", Detail: "Name the human owner for launch rollback.", Status: "missing"},
			{Label: "Budget cap", Detail: "Confirm max spend before run launch.", Status: "warning"},
			{Label: "Target branch", Detail: "Choose the exact repo branch or draft-PR target.", Status: "ready"},
		},
	}
	intake.Brief = opsHiveBriefPreview(intake.Sources, intake.MissingFields, intake.Status, intake.SuggestedMode)
	return &OpsHiveShellData{
		Active: active,
		Intake: intake,
		Runs: []OpsHiveRunView{
			{ID: "run_static_001", Title: "Build onboarding control surface", Status: "active", Guardian: "clear", Budget: "18% used", Phase: "Design", Approvals: 2, Artifacts: 7, UpdatedAt: "sample now"},
			{ID: "run_static_002", Title: "Refine evidence inspection flow", Status: "waiting", Guardian: "watch", Budget: "4% used", Phase: "Research", Approvals: 0, Artifacts: 3, UpdatedAt: "sample 12m ago"},
			{ID: "run_static_003", Title: "Prepare integration checklist", Status: "paused", Guardian: "blocked", Budget: "31% used", Phase: "Review", Approvals: 1, Artifacts: 11, UpdatedAt: "sample 1h ago"},
		},
		Agents: []OpsHiveAgentView{
			{Name: "guardian", Role: "Risk and authority", State: "watching", Budget: "12%", LastEvent: "approval request classified"},
			{Name: "architect", Role: "System design", State: "active", Budget: "24%", LastEvent: "brief split into run graph"},
			{Name: "builder", Role: "Implementation", State: "waiting", Budget: "0%", LastEvent: "blocked until launch approval"},
			{Name: "tester", Role: "Verification", State: "idle", Budget: "0%", LastEvent: "waiting for artifact stream"},
		},
		Resources: []OpsHiveResourceView{
			{Label: "Run budget", Used: "$3.24", Limit: "$18.00", Status: "clear", Detail: "Human cap visible before launch."},
			{Label: "Approval queue", Used: "2", Limit: "unresolved", Status: "attention", Detail: "Operator decisions required before protected actions."},
			{Label: "Token budget", Used: "84k", Limit: "460k", Status: "clear", Detail: "Sample aggregate across active agents."},
			{Label: "Artifact store", Used: "7", Limit: "collected", Status: "clear", Detail: "Outputs stay inspectable and causally linked."},
		},
	}
}

func (h *Handlers) buildOpsHiveIntakeView(r *http.Request) OpsHiveIntakeView {
	profileSlug := opsHiveProfileSlugFromRequest(r)
	view := OpsHiveIntakeView{
		Status:          "collecting sources",
		Confidence:      "0.00",
		SuggestedMode:   "intake pending",
		AuthorityLevel:  "human launch required",
		EstimatedBudget: "not estimated",
		StorageStatus:   "persisted",
	}
	sources, err := h.store.ListLaunchableOpsHiveIntakeSources(r.Context(), profileSlug, opsHiveLaunchableIntakeListLimit)
	if err != nil {
		view.Error = "Could not load persisted intake sources."
		view.MissingFields = opsHiveIntakeMissingFields(nil)
		view.Brief = opsHiveBriefPreview(nil, view.MissingFields, view.Status, view.SuggestedMode)
		return view
	}
	view.Sources = opsHiveIntakeSourceViews(sources)
	view.MissingFields = opsHiveIntakeMissingFields(view.Sources)
	if len(view.Sources) > 0 {
		view.Confidence = fmt.Sprintf("%.2f", opsHiveIntakeConfidence(view.Sources))
		view.SuggestedMode = opsHiveIntakeSuggestedMode(view.Sources)
		view.EstimatedBudget = "cap pending"
		view.Status = "draft ready"
	}
	view.Brief = opsHiveBriefPreview(view.Sources, view.MissingFields, view.Status, view.SuggestedMode)
	return view
}

func (h *Handlers) buildOpsHiveRunLaunchPayload(r *http.Request, profileSlug string) (opsHiveRunLaunchPayload, CreateOpsHiveRunLaunchParams, error) {
	sources, err := h.store.ListLaunchableOpsHiveIntakeSources(r.Context(), profileSlug, opsHiveLaunchableIntakeListLimit)
	if err != nil {
		return opsHiveRunLaunchPayload{}, CreateOpsHiveRunLaunchParams{}, fmt.Errorf("could not load persisted intake sources: %w", err)
	}
	sourceViews := opsHiveIntakeSourceViews(sources)
	if len(sourceViews) == 0 {
		return opsHiveRunLaunchPayload{}, CreateOpsHiveRunLaunchParams{}, errors.New("add at least one intake source before queueing a Hive run")
	}
	missing := opsHiveIntakeMissingFields(sourceViews)
	brief := opsHiveBriefPreview(sourceViews, missing, "draft ready", opsHiveIntakeSuggestedMode(sourceViews))
	targetRepos := opsHiveLaunchTargetRepos(r.FormValue("target_repos"))
	if len(targetRepos) == 0 {
		return opsHiveRunLaunchPayload{}, CreateOpsHiveRunLaunchParams{}, errors.New("target_repos is required")
	}
	maxIterations, err := opsHiveLaunchPositiveInt(r.FormValue("max_iterations"), "budget.max_iterations")
	if err != nil {
		return opsHiveRunLaunchPayload{}, CreateOpsHiveRunLaunchParams{}, err
	}
	maxCost, err := opsHiveLaunchNonNegativeFloat(r.FormValue("max_cost_usd"), "budget.max_cost_usd")
	if err != nil {
		return opsHiveRunLaunchPayload{}, CreateOpsHiveRunLaunchParams{}, err
	}
	operatorID := "site_operator_" + opsHiveSafeLaunchID(h.userID(r))
	intakeID := "site_" + opsHiveSafeLaunchID(profileSlug) + "_" + newID()
	title := brief.Title
	payload := opsHiveRunLaunchPayload{
		OperatorID: operatorID,
		IntakeID:   intakeID,
		Title:      title,
		Brief: opsHiveRunLaunchBrief{
			Title:      brief.Title,
			Objective:  brief.Objective,
			Scope:      brief.Scope,
			Acceptance: brief.Acceptance,
			Risks:      brief.Risks,
			Readiness:  brief.Readiness,
			Missing:    opsHiveLaunchMissingLabels(brief.Missing),
		},
		Sources: opsHiveLaunchSources(sourceViews),
		Authority: opsHiveRunLaunchAuthority{
			InitialLevel: "Required",
			Scope:        "operator-launch",
			PolicyRef:    "dark-factory/operator-ui-contract-v0.1.2",
			Rationale:    "operator queued launch from Site-derived Factory Brief; Hive records queued intent before any runtime start",
		},
		Budget: opsHiveRunLaunchBudget{
			MaxIterations: maxIterations,
			MaxCostUSD:    maxCost,
		},
		TargetRepos: targetRepos,
	}
	storeParams := CreateOpsHiveRunLaunchParams{
		ProfileSlug:         profileSlug,
		OperatorID:          operatorID,
		IntakeID:            intakeID,
		Title:               title,
		TargetRepos:         targetRepos,
		BudgetMaxIterations: maxIterations,
		BudgetMaxCostUSD:    maxCost,
	}
	return payload, storeParams, nil
}

func opsHiveLaunchableIntakeSources(sources []OpsHiveIntakeSource) []OpsHiveIntakeSource {
	out := make([]OpsHiveIntakeSource, 0, len(sources))
	for _, source := range sources {
		if opsHiveLaunchableIntakeKind(source.Kind) {
			out = append(out, source)
		}
	}
	return out
}

func opsHiveLaunchableIntakeKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "url", "repo", "prd", "spec", "plan", "text":
		return true
	default:
		return false
	}
}

func opsHiveIntakeSourceViews(sources []OpsHiveIntakeSource) []OpsHiveSourceView {
	out := make([]OpsHiveSourceView, 0, len(sources))
	for _, source := range sources {
		out = append(out, OpsHiveSourceView{
			ID:        source.ID,
			Kind:      source.Kind,
			Title:     source.Title,
			Detail:    source.Detail,
			Content:   source.Content,
			Status:    source.Status,
			CreatedAt: source.CreatedAt.Format("2006-01-02 15:04"),
		})
	}
	return out
}

func opsHiveRunLaunchViews(launches []OpsHiveRunLaunch, selectedRunID string) []OpsHiveRunLaunchView {
	out := make([]OpsHiveRunLaunchView, 0, len(launches))
	for _, launch := range launches {
		out = append(out, OpsHiveRunLaunchView{
			RunID:        launch.RunID,
			Status:       launch.Status,
			FirstEventID: launch.FirstEventID,
			OperatorID:   launch.OperatorID,
			Title:        launch.Title,
			TargetRepos:  strings.Join(launch.TargetRepos, ", "),
			Budget:       fmt.Sprintf("%d iter / $%.2f", launch.BudgetMaxIterations, launch.BudgetMaxCostUSD),
			CreatedAt:    launch.CreatedAt.Format("2006-01-02 15:04"),
			Selected:     launch.RunID == selectedRunID,
		})
	}
	return out
}

func opsHiveBriefPreview(sources []OpsHiveSourceView, missing []OpsHiveMissingFieldView, status, mode string) OpsHiveBriefPreviewView {
	primary := opsHivePrimaryBriefSource(sources)
	title := "Factory brief draft"
	objective := "Awaiting source material."
	if primary.Title != "" {
		title = primary.Title
	}
	if primary.Content != "" {
		objective = opsHiveBriefExcerpt(primary.Content, 260)
	}
	return OpsHiveBriefPreviewView{
		Title:      title,
		Objective:  objective,
		Scope:      opsHiveBriefScope(sources),
		Acceptance: opsHiveBriefAcceptance(missing),
		Risks:      opsHiveBriefRisks(missing),
		Readiness:  opsHiveBriefReadiness(sources, status, mode),
		Missing:    missing,
	}
}

func opsHivePrimaryBriefSource(sources []OpsHiveSourceView) OpsHiveSourceView {
	for _, source := range sources {
		switch strings.ToLower(source.Kind) {
		case "prd", "spec", "plan", "text":
			return source
		}
	}
	if len(sources) > 0 {
		return sources[0]
	}
	return OpsHiveSourceView{}
}

func opsHiveBriefScope(sources []OpsHiveSourceView) string {
	if len(sources) == 0 {
		return "No scoped sources yet."
	}
	var b strings.Builder
	for i, source := range sources {
		if i >= 5 {
			fmt.Fprintf(&b, "Additional sources: %d\n", len(sources)-i)
			break
		}
		fmt.Fprintf(&b, "%s: %s\n", source.Kind, source.Title)
	}
	return strings.TrimSpace(b.String())
}

func opsHiveBriefAcceptance(missing []OpsHiveMissingFieldView) string {
	lines := []string{}
	for _, field := range missing {
		if field.Status != "ready" {
			lines = append(lines, "Resolve "+field.Label+": "+field.Detail)
		}
	}
	if len(lines) == 0 {
		return "Current source prerequisites are present."
	}
	return strings.Join(lines, "\n")
}

func opsHiveBriefRisks(missing []OpsHiveMissingFieldView) string {
	lines := []string{"Queued launch requests are not runtime-start evidence."}
	for _, field := range missing {
		if field.Status != "ready" {
			lines = append(lines, field.Label+": "+field.Detail)
		}
	}
	return strings.Join(lines, "\n")
}

func opsHiveBriefReadiness(sources []OpsHiveSourceView, status, mode string) string {
	if len(sources) == 0 {
		return "intake pending"
	}
	return status + " / " + mode
}

func opsHiveBriefExcerpt(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return "Awaiting source material."
	}
	if limit < 4 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return strings.TrimSpace(string(runes[:limit-3])) + "..."
}

func opsHiveProfileSlugFromRequest(r *http.Request) string {
	if slug := strings.TrimSpace(r.FormValue("profile")); slug != "" {
		if p := profile.Lookup(slug); p != nil {
			return p.GetSlug()
		}
	}
	if p := profile.FromContext(r.Context()); p != nil {
		return p.GetSlug()
	}
	return opsHiveIntakeDefaultProfileSlug
}

func opsHiveIntakeMissingFields(sources []OpsHiveSourceView) []OpsHiveMissingFieldView {
	hasSource, hasURL, hasRepo := false, false, false
	for _, source := range sources {
		hasSource = true
		switch strings.ToLower(source.Kind) {
		case "url":
			hasURL = true
		case "repo":
			hasRepo = true
		}
	}
	fields := []OpsHiveMissingFieldView{
		{Label: "Source material", Detail: "Paste text, add a URL, or add repository context.", Status: "missing"},
		{Label: "URL reference", Detail: "Add at least one external reference URL when available.", Status: "warning"},
		{Label: "Repo context", Detail: "Add owner/repo context before launch planning.", Status: "warning"},
		{Label: "Budget cap", Detail: "Confirm max spend before run launch.", Status: "warning"},
	}
	if hasSource {
		fields[0].Status = "ready"
	}
	if hasURL {
		fields[1].Status = "ready"
	}
	if hasRepo {
		fields[2].Status = "ready"
	}
	return fields
}

func opsHiveIntakeConfidence(sources []OpsHiveSourceView) float64 {
	score := 0.35
	seen := map[string]bool{}
	for _, source := range sources {
		seen[strings.ToLower(source.Kind)] = true
	}
	if seen["prd"] || seen["spec"] || seen["plan"] || seen["text"] {
		score += 0.2
	}
	if seen["url"] {
		score += 0.15
	}
	if seen["repo"] {
		score += 0.2
	}
	if len(sources) >= 3 {
		score += 0.1
	}
	if score > 0.99 {
		return 0.99
	}
	return score
}

func opsHiveIntakeSuggestedMode(sources []OpsHiveSourceView) string {
	hasRepo, hasText := false, false
	for _, source := range sources {
		switch strings.ToLower(source.Kind) {
		case "repo":
			hasRepo = true
		case "prd", "spec", "plan", "text":
			hasText = true
		}
	}
	if hasRepo && hasText {
		return "full product pipeline"
	}
	if hasRepo {
		return "repo-scoped planning"
	}
	return "brief drafting"
}

func opsHiveIntakeSourceParamsFromForm(r *http.Request) (CreateOpsHiveIntakeSourceParams, error) {
	rawKind := strings.TrimSpace(r.FormValue("source_kind"))
	title := strings.TrimSpace(r.FormValue("title"))
	content := strings.TrimSpace(r.FormValue("content"))
	if content == "" {
		return CreateOpsHiveIntakeSourceParams{}, errors.New("source content is required")
	}
	if len(content) > opsHiveIntakeMaxContentBytes {
		return CreateOpsHiveIntakeSourceParams{}, fmt.Errorf("source content must be %d bytes or less", opsHiveIntakeMaxContentBytes)
	}
	kind, status := opsHiveClassifyIntakeSource(rawKind, title, content)
	if title == "" {
		title = opsHiveIntakeSourceTitle(kind, content)
	} else {
		title = truncateOpsHiveIntakeTitle(title)
	}
	return CreateOpsHiveIntakeSourceParams{
		ProfileSlug: opsHiveProfileSlugFromRequest(r),
		Kind:        kind,
		Title:       title,
		Detail:      opsHiveIntakeSourceDetail(kind, content),
		Content:     content,
		Status:      status,
	}, nil
}

func postOpsHiveRunLaunch(r *http.Request, payload opsHiveRunLaunchPayload) (opsHiveRunLaunchResponse, error) {
	base := strings.TrimSpace(os.Getenv("HIVE_OPS_API_BASE_URL"))
	if base == "" {
		return opsHiveRunLaunchResponse{}, errors.New("HIVE_OPS_API_BASE_URL is not configured")
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return opsHiveRunLaunchResponse{}, fmt.Errorf("internal error marshalling run launch: %w", err)
	}
	endpoint := strings.TrimRight(base, "/") + "/api/hive/runs"
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return opsHiveRunLaunchResponse{}, fmt.Errorf("could not build hive run request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey := strings.TrimSpace(os.Getenv("HIVE_OPS_API_KEY")); apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := hiveOpsProjectionClient.Do(req)
	if err != nil {
		return opsHiveRunLaunchResponse{}, fmt.Errorf("hive unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return opsHiveRunLaunchResponse{}, fmt.Errorf("hive returned %s", opsHiveResponseError(resp))
	}
	var out opsHiveRunLaunchResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return opsHiveRunLaunchResponse{}, fmt.Errorf("decode hive run response: %w", err)
	}
	if out.RunID == "" {
		return opsHiveRunLaunchResponse{}, errors.New("hive response did not include run_id")
	}
	if out.Status == "" {
		out.Status = "queued"
	}
	return out, nil
}

func opsHiveLaunchTargetRepos(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\t' || r == ' '
	})
	out := []string{}
	seen := map[string]struct{}{}
	for _, part := range parts {
		repo := strings.TrimSpace(part)
		if repo == "" {
			continue
		}
		if _, exists := seen[repo]; exists {
			continue
		}
		seen[repo] = struct{}{}
		out = append(out, repo)
	}
	return out
}

func opsHiveLaunchPositiveInt(value, label string) (int, error) {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", label)
	}
	return parsed, nil
}

func opsHiveLaunchNonNegativeFloat(value, label string) (float64, error) {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("%s must be zero or greater", label)
	}
	return parsed, nil
}

func opsHiveSafeLaunchID(value string) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "transpara"
	}
	return b.String()
}

func opsHiveLaunchMissingLabels(fields []OpsHiveMissingFieldView) []string {
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if field.Status == "ready" {
			continue
		}
		out = append(out, field.Label+": "+field.Detail)
	}
	return out
}

func opsHiveLaunchSources(sources []OpsHiveSourceView) []opsHiveRunLaunchSource {
	out := make([]opsHiveRunLaunchSource, 0, len(sources))
	for _, source := range sources {
		ref := source.Content
		switch strings.ToLower(source.Kind) {
		case "text", "prd", "spec", "plan":
			ref = "site-intake-source:" + source.ID
		}
		if strings.TrimSpace(ref) == "" {
			ref = "site-intake-source:" + source.ID
		}
		out = append(out, opsHiveRunLaunchSource{
			ID:    source.ID,
			Type:  strings.ToLower(source.Kind),
			Ref:   ref,
			Title: source.Title,
		})
	}
	return out
}

func opsHiveClassifyIntakeSource(rawKind, title, content string) (string, string) {
	kind := strings.ToLower(strings.TrimSpace(rawKind))
	haystack := strings.ToLower(title + " " + content)
	switch kind {
	case "url":
		return "URL", "classified"
	case "repo":
		return "Repo", "scoped"
	case "text", "":
		switch {
		case strings.Contains(haystack, "acceptance") || strings.Contains(haystack, "prd") || strings.Contains(haystack, "requirements"):
			return "PRD", "parsed"
		case strings.Contains(haystack, "api") || strings.Contains(haystack, "contract") || strings.Contains(haystack, "schema"):
			return "Spec", "parsed"
		case strings.Contains(haystack, "milestone") || strings.Contains(haystack, "plan") || strings.Contains(haystack, "roadmap"):
			return "Plan", "parsed"
		default:
			return "Text", "parsed"
		}
	default:
		return "Text", "parsed"
	}
}

func opsHiveIntakeSourceTitle(kind, content string) string {
	firstLine := strings.TrimSpace(strings.Split(content, "\n")[0])
	if kind == "URL" {
		if parsed, err := url.Parse(firstLine); err == nil && parsed.Host != "" {
			path := strings.Trim(parsed.Path, "/")
			if path != "" {
				return truncateOpsHiveIntakeTitle(parsed.Host + "/" + path)
			}
			return parsed.Host
		}
	}
	return truncateOpsHiveIntakeTitle(firstLine)
}

func truncateOpsHiveIntakeTitle(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "Untitled source"
	}
	runes := []rune(value)
	if len(runes) <= 72 {
		return value
	}
	return strings.TrimSpace(string(runes[:69])) + "..."
}

func opsHiveIntakeSourceDetail(kind, content string) string {
	switch kind {
	case "URL":
		return "Reference URL captured for review"
	case "Repo":
		return "Repository context captured for scoping"
	case "PRD":
		return "Product intent and acceptance criteria captured"
	case "Spec":
		return "Technical contract source captured"
	case "Plan":
		return "Implementation plan source captured"
	default:
		return fmt.Sprintf("Text source captured (%d bytes)", len(content))
	}
}

// buildOpsDecisionDataFromProjection fetches the hive operator projection,
// finds the matching pending approval by request_id, and returns an
// OpsDecisionData populated from the real request. Effect stays "none".
func buildOpsDecisionDataFromProjection(r *http.Request, requestID string) *OpsDecisionData {
	projection, err := fetchHiveOperatorProjection(r)
	if err != nil {
		return &OpsDecisionData{
			AuthorizationSource: "transpara-ai/docs#75 / a50190ce470e8686a561e0e0b6e62ef0c5f5bb13",
			RequestID:           requestID,
			Status:              "blocked",
			Effect:              "none",
			OperatorSummary:     "Could not fetch hive operator projection: " + err.Error(),
			BlockedReasons:      []string{"hive projection unavailable: " + err.Error()},
		}
	}

	var found *OpsHiveApproval
	for i := range projection.PendingApprovals {
		if projection.PendingApprovals[i].RequestID == requestID {
			found = &projection.PendingApprovals[i]
			break
		}
	}
	if found == nil {
		return &OpsDecisionData{
			AuthorizationSource: "transpara-ai/docs#75 / a50190ce470e8686a561e0e0b6e62ef0c5f5bb13",
			RequestID:           requestID,
			Status:              "blocked",
			Effect:              "none",
			OperatorSummary:     "No pending approval found for request_id " + requestID,
			BlockedReasons:      []string{"request_id " + requestID + " not found in pending approvals"},
		}
	}

	// Slice 1's decision surface governs ONLY the draft-PR create action. A
	// pending request for any other protected action must NOT render as an
	// approvable pull_request — refuse it (blocked, effect none) rather than
	// mislabel its target_type as pull_request. Mirrors the hive decision
	// endpoint's draft-PR-only gate (hive#129 P1-a).
	if found.ActionName != "pull_request.create" {
		return &OpsDecisionData{
			AuthorizationSource: "transpara-ai/docs#75 / a50190ce470e8686a561e0e0b6e62ef0c5f5bb13",
			RequestID:           found.RequestID,
			RequestedAction:     found.ActionName,
			Status:              "blocked",
			Effect:              "none",
			OperatorSummary:     "Request " + requestID + " is not a draft-PR creation; this decision surface only governs pull_request.create.",
			BlockedReasons:      []string{"action " + found.ActionName + " is not pull_request.create: out of scope for the Gate-E draft-PR decision surface"},
		}
	}

	// Target field encodes "repo ref" (space-separated); split into Repo and TargetRef.
	repo := ""
	targetRef := ""
	if parts := strings.SplitN(found.Target, " ", 2); len(parts) == 2 {
		repo = parts[0]
		targetRef = parts[1]
	} else {
		targetRef = found.Target
	}

	seed := strings.Join([]string{found.ActionName, found.Justification, repo, targetRef, requestID}, "|")
	return &OpsDecisionData{
		AuthorizationSource: "transpara-ai/docs#75 / a50190ce470e8686a561e0e0b6e62ef0c5f5bb13",
		CorrelationID:       "gate-e-correlation-" + opsDecisionHash(seed),
		TraceID:             "gate-e-trace-" + opsDecisionHash("trace|"+seed),
		RequestID:           found.RequestID,
		RequestedAction:     found.ActionName,
		TargetType:          "pull_request", // guaranteed: this path runs only for pull_request.create (gated above); OpsHiveApproval carries no target_type.
		Repo:                repo,
		TargetRef:           targetRef,
		Status:              "accepted_for_review",
		Effect:              "none",
		OperatorSummary:     found.Justification,
		BlockedReasons:      []string{},
		RequiredEvidence:    []string{},
		Actions:             opsDecisionActions(r),
		GovernancePosture:   []OpsDecisionPosture{},
		BoundaryChecks: []OpsDecisionPosture{
			{Label: "R-001", Field: "runner/worktree evidence gap", Value: "unresolved and excluded", Status: "blocked outside scope"},
			{Label: "R-002", Field: "real protected side effects", Value: "unresolved and excluded", Status: "blocked outside scope"},
			{Label: "R-003", Field: "policy adapter / policy bundle", Value: "unresolved and excluded", Status: "blocked outside scope"},
		},
	}
}

func buildOpsDecisionData(r *http.Request) *OpsDecisionData {
	q := r.URL.Query()

	// When request_id is present, load the real pending approval from the hive
	// operator projection and populate the decision surface from it.
	// Site remains a pure display console: effect = none is preserved.
	if requestID := strings.TrimSpace(q.Get("request_id")); requestID != "" {
		return buildOpsDecisionDataFromProjection(r, requestID)
	}

	actionRaw := strings.TrimSpace(q.Get("action"))
	action, actionOK := normalizeOpsDecisionAction(actionRaw)
	reason := strings.TrimSpace(q.Get("reason"))
	targetType := opsValueOr(strings.TrimSpace(q.Get("target_type")), "pull_request")
	repo := opsValueOr(strings.TrimSpace(q.Get("repo")), "transpara-ai/site")
	targetRef := strings.TrimSpace(q.Get("target_ref"))

	blocked := make([]string, 0)
	required := make([]string, 0)
	if actionRaw == "" {
		blocked = append(blocked, "missing requested_action")
		required = append(required, "requested_action must be approve, deny, or request-more-evidence")
	} else if !actionOK {
		blocked = append(blocked, "requested_action is outside the Gate E decision boundary")
		required = append(required, "requested_action must be approve, deny, or request-more-evidence")
	}
	if targetRef == "" {
		blocked = append(blocked, "missing target reference")
		required = append(required, "target_ref must point to an existing PR, issue, artifact, FactoryOrder, or release candidate")
	}
	if reason == "" {
		blocked = append(blocked, "missing decision_reason")
		required = append(required, "decision_reason must explain the operator-visible rationale")
	}
	if !opsDecisionTargetTypeAllowed(targetType) {
		blocked = append(blocked, "target_type is outside the governed decision boundary")
		required = append(required, "target_type must be pull_request, issue, artifact, factory_order, or release_candidate")
	}
	for _, guard := range []struct {
		keys   []string
		reason string
	}{
		{[]string{"direct_execution", "direct_execution_requested"}, "direct execution is forbidden for this Site slice"},
		{[]string{"protected_side_effect", "protected_side_effect_requested"}, "protected side effects are forbidden for this Site slice"},
		{[]string{"policy_adapter_reliance", "policy_adapter_reliance_requested"}, "policy-adapter reliance is forbidden for this Site slice"},
		{[]string{"runner_worktree_execution", "runner_worktree_execution_requested"}, "runner/worktree protected execution is forbidden for this Site slice"},
		{[]string{"production_autonomy", "production_autonomy_requested"}, "production autonomy is forbidden for this Site slice"},
		{[]string{"execution_receipt", "execution_receipt_requested"}, "ExecutionReceipt production behavior is forbidden for this Site slice"},
		{[]string{"policy_bundle", "policy_bundle_requested"}, "policy-bundle reliance is forbidden for this Site slice"},
	} {
		if opsDecisionQueryTrue(q, guard.keys...) {
			blocked = append(blocked, guard.reason)
		}
	}

	if action == "" {
		action = "none"
	}
	status := "accepted_for_review"
	summary := "Decision request stays inside the governed Gate E boundary. Site displays the envelope and trace only."
	if len(blocked) > 0 {
		status = "blocked"
		summary = "Request failed closed inside Site. No downstream action was executed."
	}

	seed := strings.Join([]string{action, reason, targetType, repo, targetRef, strings.Join(blocked, "|")}, "|")
	return &OpsDecisionData{
		AuthorizationSource: "transpara-ai/docs#75 / a50190ce470e8686a561e0e0b6e62ef0c5f5bb13",
		CorrelationID:       "gate-e-correlation-" + opsDecisionHash(seed),
		TraceID:             "gate-e-trace-" + opsDecisionHash("trace|"+seed),
		RequestedAction:     action,
		DecisionReason:      reason,
		TargetType:          targetType,
		Repo:                repo,
		TargetRef:           targetRef,
		Status:              status,
		Effect:              "none",
		OperatorSummary:     summary,
		BlockedReasons:      blocked,
		RequiredEvidence:    required,
		Actions:             opsDecisionActions(r),
		GovernancePosture: []OpsDecisionPosture{
			{Label: "direct execution", Field: "direct_execution_requested", Value: fmt.Sprintf("%t", opsDecisionQueryTrue(q, "direct_execution", "direct_execution_requested")), Status: opsDecisionGuardStatus(q, "direct_execution", "direct_execution_requested")},
			{Label: "protected side effects", Field: "protected_side_effect_requested", Value: fmt.Sprintf("%t", opsDecisionQueryTrue(q, "protected_side_effect", "protected_side_effect_requested")), Status: opsDecisionGuardStatus(q, "protected_side_effect", "protected_side_effect_requested")},
			{Label: "policy-adapter reliance", Field: "policy_adapter_reliance_requested", Value: fmt.Sprintf("%t", opsDecisionQueryTrue(q, "policy_adapter_reliance", "policy_adapter_reliance_requested")), Status: opsDecisionGuardStatus(q, "policy_adapter_reliance", "policy_adapter_reliance_requested")},
			{Label: "runner/worktree protected execution", Field: "runner_worktree_execution_requested", Value: fmt.Sprintf("%t", opsDecisionQueryTrue(q, "runner_worktree_execution", "runner_worktree_execution_requested")), Status: opsDecisionGuardStatus(q, "runner_worktree_execution", "runner_worktree_execution_requested")},
			{Label: "production autonomy", Field: "production_autonomy_requested", Value: fmt.Sprintf("%t", opsDecisionQueryTrue(q, "production_autonomy", "production_autonomy_requested")), Status: opsDecisionGuardStatus(q, "production_autonomy", "production_autonomy_requested")},
			{Label: "ExecutionReceipt production path", Field: "execution_receipt_requested", Value: fmt.Sprintf("%t", opsDecisionQueryTrue(q, "execution_receipt", "execution_receipt_requested")), Status: opsDecisionGuardStatus(q, "execution_receipt", "execution_receipt_requested")},
			{Label: "policy-bundle reliance", Field: "policy_bundle_requested", Value: fmt.Sprintf("%t", opsDecisionQueryTrue(q, "policy_bundle", "policy_bundle_requested")), Status: opsDecisionGuardStatus(q, "policy_bundle", "policy_bundle_requested")},
		},
		BoundaryChecks: []OpsDecisionPosture{
			{Label: "R-001", Field: "runner/worktree evidence gap", Value: "unresolved and excluded", Status: "blocked outside scope"},
			{Label: "R-002", Field: "real protected side effects", Value: "unresolved and excluded", Status: "blocked outside scope"},
			{Label: "R-003", Field: "policy adapter / policy bundle", Value: "unresolved and excluded", Status: "blocked outside scope"},
		},
	}
}

func fetchOpsApprovals(r *http.Request) *OpsApprovalsData {
	data := &OpsApprovalsData{}
	projection, err := fetchHiveOperatorProjection(r)
	if err != nil {
		data.ProjectionError = err.Error()
		return data
	}
	data.ProjectionSource = projection.Source
	if projection.GeneratedAt != "" {
		data.GeneratedAt = formatOpsTime(projection.GeneratedAt)
	}
	if len(projection.Errors) > 0 {
		data.ProjectionWarning = strings.Join(projection.Errors, "; ")
	}
	data.PendingApprovals = make([]OpsApprovalQueueItem, 0, len(projection.PendingApprovals))
	for _, approval := range projection.PendingApprovals {
		item := OpsApprovalQueueItem{OpsHiveApproval: approval}
		if approval.ActionName == "pull_request.create" {
			item.ResolveEnabled = true
			item.ResolveStatus = "resolve"
			item.DecisionHref = opsApprovalDecisionHref(r, approval.RequestID)
		} else {
			item.ResolveStatus = "projected only"
		}
		data.PendingApprovals = append(data.PendingApprovals, item)
	}
	data.AuthorityDecisions = projection.AuthorityDecisions
	data.KeyAuditTraces = projection.KeyAuditTraces
	return data
}

func opsApprovalDecisionHref(r *http.Request, requestID string) string {
	q := url.Values{}
	q.Set("request_id", requestID)
	if profile := strings.TrimSpace(r.URL.Query().Get("profile")); profile != "" {
		q.Set("profile", profile)
	}
	return "/ops/decision?" + q.Encode()
}

func normalizeOpsDecisionAction(action string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "approve":
		return "approve", true
	case "deny":
		return "deny", true
	case "request-more-evidence", "request_more_evidence":
		return "request-more-evidence", true
	default:
		return "", false
	}
}

func opsDecisionTargetTypeAllowed(targetType string) bool {
	switch strings.ToLower(strings.TrimSpace(targetType)) {
	case "pull_request", "issue", "artifact", "factory_order", "release_candidate":
		return true
	default:
		return false
	}
}

func opsDecisionQueryTrue(q url.Values, keys ...string) bool {
	for _, key := range keys {
		switch strings.ToLower(strings.TrimSpace(q.Get(key))) {
		case "1", "true", "yes", "on", "requested":
			return true
		}
	}
	return false
}

func opsDecisionGuardStatus(q url.Values, keys ...string) string {
	if opsDecisionQueryTrue(q, keys...) {
		return "blocked"
	}
	return "absent"
}

func opsDecisionActions(r *http.Request) []OpsDecisionAction {
	return []OpsDecisionAction{
		{
			Label:       "Approve",
			WireValue:   opsDecisionApprove,
			Description: "Displays an approval envelope for review with effect = none.",
		},
		{
			Label:       "Deny",
			WireValue:   opsDecisionDeny,
			Description: "Displays a denial envelope for review with effect = none.",
		},
		{
			Label:       "Request more evidence",
			WireValue:   opsDecisionMoreEvidence,
			Description: "Displays an evidence request envelope for review with effect = none.",
		},
	}
}

func opsDecisionHash(seed string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(seed))
	return fmt.Sprintf("%012x", h.Sum64())[:12]
}

func fetchOpsEvidence(r *http.Request) *OpsEvidenceData {
	data := &OpsEvidenceData{
		GeneratedAt:        time.Now().UTC().Format("2006-01-02 15:04:05"),
		FactoryOrderID:     strings.TrimSpace(r.URL.Query().Get("factory_order_id")),
		ReleaseCandidateID: strings.TrimSpace(r.URL.Query().Get("release_candidate_id")),
	}
	rawURL := strings.TrimSpace(os.Getenv("DARK_FACTORY_EVIDENCE_PROJECTION_URL"))
	if rawURL == "" {
		data.ProjectionError = "Dark Factory evidence projection URL is not configured."
		return data
	}
	endpoint, err := evidenceProjectionURL(rawURL, data.FactoryOrderID, data.ReleaseCandidateID)
	if err != nil {
		data.ProjectionError = err.Error()
		return data
	}
	data.ProjectionURL = endpoint
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, endpoint, nil)
	if err != nil {
		data.ProjectionError = err.Error()
		return data
	}
	resp, err := evidenceOpsProjectionClient.Do(req)
	if err != nil {
		data.ProjectionError = err.Error()
		return data
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data.ProjectionError = fmt.Sprintf("evidence projection returned %s", resp.Status)
		return data
	}
	var projection OpsEvidenceProjection
	if err := json.NewDecoder(resp.Body).Decode(&projection); err != nil {
		data.ProjectionError = err.Error()
		return data
	}
	data.Source = projection.Source
	if projection.GeneratedAt != "" {
		data.GeneratedAt = formatOpsTime(projection.GeneratedAt)
	}
	data.FactoryOrder = projection.FactoryOrder
	data.ReleaseCandidate = projection.ReleaseCandidate
	data.Decision = projection.Decision
	data.AuditReport = projection.AuditReport
	data.Timeline = projection.Timeline
	data.GateEvidence = projection.GateEvidence
	data.ReleaseEvidence = projection.ReleaseEvidence
	data.FailuresRepairs = projection.FailuresRepairs
	data.MissingProvenance = projection.MissingProvenance
	data.ProofOfWorkPacket = projection.ProofOfWorkPacket
	data.Errors = projection.Errors
	if data.FactoryOrderID == "" && data.FactoryOrder != nil {
		data.FactoryOrderID = data.FactoryOrder.ID
	}
	if data.ReleaseCandidateID == "" && data.ReleaseCandidate != nil {
		data.ReleaseCandidateID = data.ReleaseCandidate.ID
	}
	if len(data.Errors) > 0 {
		data.ProjectionError = strings.Join(data.Errors, "; ")
	}
	return data
}

func evidenceProjectionURL(rawURL, factoryOrderID, releaseCandidateID string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	if factoryOrderID != "" {
		q.Set("factory_order_id", factoryOrderID)
	}
	if releaseCandidateID != "" {
		q.Set("release_candidate_id", releaseCandidateID)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (h *Handlers) fetchOpsHive(r *http.Request) *OpsHiveData {
	ctx := r.Context()
	data := &OpsHiveData{
		GeneratedAt: time.Now().UTC().Format("2006-01-02 15:04:05"),
	}
	applyHiveOperatorProjection(r, data)
	if h == nil || h.store == nil {
		data.Error = "Hive local store is unavailable in read-only no-database mode."
		return data
	}

	entries, _ := h.store.ListHiveDiagnostics(ctx, maxHiveDiagEntries)
	if len(entries) == 0 {
		entries = readDiagnostics(h.loopDir, maxHiveDiagEntries)
	}
	data.RecentEvents = entries
	data.DiagnosticCount = len(entries)

	ls := readLoopState(h.loopDir)
	data.Iteration = ls.Iteration
	data.Phase = ls.Phase
	data.BuildTitle = ls.BuildTitle
	data.BuildCost = ls.BuildCost

	repoDir := ""
	if h.loopDir != "" {
		repoDir = filepath.Dir(h.loopDir)
	}
	data.RecentCommits = readRecentCommits(repoDir, 6)

	agentID := h.store.GetHiveAgentID(ctx)
	if agentID == "" {
		return data
	}

	totalOps, lastActive, err := h.store.GetHiveTotals(ctx, agentID)
	if err != nil {
		data.Error = err.Error()
		return data
	}
	data.TotalOps = totalOps
	if !lastActive.IsZero() {
		data.LastActive = lastActive.Format("2006-01-02 15:04")
		if data.BuildTitle == "" {
			data.BuildTitle = "Last active: " + data.LastActive
		}
	}

	posts, err := h.store.ListHiveActivity(ctx, agentID, maxHivePosts)
	if err != nil {
		data.Error = err.Error()
		return data
	}
	stats := computeHiveStats(posts)
	data.VerifiedBuilds = stats.Features
	data.TotalCost = stats.TotalCost
	data.AverageCost = stats.AvgCost

	if ic := parseIterFromPosts(posts); ic > data.Iteration {
		data.Iteration = ic
	}
	if data.Iteration == 0 {
		tasks, err := h.store.ListHiveAgentTasks(ctx, agentID, 1000)
		if err != nil {
			data.Error = err.Error()
			return data
		}
		doneCount := 0
		for _, task := range tasks {
			if task.State == "done" {
				doneCount++
			}
		}
		data.Iteration = doneCount
	}
	if data.TotalOps > 0 && data.Phase == "" {
		data.Phase = "idle"
	}
	return data
}

func applyHiveOperatorProjection(r *http.Request, data *OpsHiveData) {
	base := strings.TrimSpace(os.Getenv("HIVE_OPS_API_BASE_URL"))
	if base == "" {
		data.ProjectionError = "Hive operator projection source is not configured."
		return
	}
	endpoint := strings.TrimRight(base, "/") + "/api/hive/operator-projection"
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, endpoint, nil)
	if err != nil {
		data.ProjectionError = err.Error()
		return
	}
	if key := strings.TrimSpace(os.Getenv("HIVE_OPS_API_KEY")); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := hiveOpsProjectionClient.Do(req)
	if err != nil {
		data.ProjectionError = err.Error()
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data.ProjectionError = fmt.Sprintf("hive operator projection returned %s", resp.Status)
		return
	}
	var projection OpsHiveProjection
	if err := json.NewDecoder(resp.Body).Decode(&projection); err != nil {
		data.ProjectionError = err.Error()
		return
	}
	data.ProjectionSource = projection.Source
	if projection.GeneratedAt != "" {
		data.GeneratedAt = formatOpsTime(projection.GeneratedAt)
	}
	if len(projection.Errors) > 0 {
		data.ProjectionError = strings.Join(projection.Errors, "; ")
	}
	data.PendingApprovals = projection.PendingApprovals
	data.AuthorityDecisions = projection.AuthorityDecisions
	data.Lifecycle = projection.Lifecycle
	data.KeyAuditTraces = projection.KeyAuditTraces
	data.RuntimeEvidence = projection.RuntimeEvidence
	data.ModelSelection = projection.ModelSelection
}

func fetchOpsCivilizationProjection(r *http.Request) *OpsCivilizationAssemblyProjection {
	base := strings.TrimSpace(os.Getenv("HIVE_OPS_API_BASE_URL"))
	if base == "" {
		return nil
	}
	endpoint := strings.TrimRight(base, "/") + "/api/hive/civilization/assembly-projection"
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, endpoint, nil)
	if err != nil {
		return failedOpsCivilizationProjection(err.Error())
	}
	if key := strings.TrimSpace(os.Getenv("HIVE_OPS_API_KEY")); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := civilizationOpsProjectionClient.Do(req)
	if err != nil {
		return failedOpsCivilizationProjection(err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return failedOpsCivilizationProjection(fmt.Sprintf("hive civilization projection returned %s", resp.Status))
	}
	var projection OpsCivilizationAssemblyProjection
	if err := json.NewDecoder(resp.Body).Decode(&projection); err != nil {
		return failedOpsCivilizationProjection(err.Error())
	}
	if err := validateOpsCivilizationProjection(projection); err != nil {
		return failedOpsCivilizationProjection(err.Error())
	}
	return &projection
}

func validateOpsCivilizationProjection(projection OpsCivilizationAssemblyProjection) error {
	if !opsCivilizationProjectionSchemaSupported(projection.ProjectionSchemaVersion) {
		return fmt.Errorf("unsupported projection schema version %q", projection.ProjectionSchemaVersion)
	}
	if subject := strings.TrimSpace(projection.ProjectionSubject); subject != "" && subject != "civilization_assembly" {
		return fmt.Errorf("unsupported projection subject %q", projection.ProjectionSubject)
	}
	if strings.TrimSpace(projection.DerivationStatus) == "" {
		return errors.New("missing projection derivation status")
	}
	return nil
}

func opsCivilizationProjectionSchemaSupported(version string) bool {
	version = strings.TrimSpace(version)
	if version == "" {
		return false
	}
	major, _, _ := strings.Cut(version, ".")
	return major == "1"
}

func failedOpsCivilizationProjection(reason string) *OpsCivilizationAssemblyProjection {
	return &OpsCivilizationAssemblyProjection{
		ProjectionSchemaVersion:     "1.0.0",
		ProjectionSubject:           "civilization_assembly",
		GeneratedAt:                 time.Now().UTC(),
		DerivationStatus:            opsCivilizationProjectionStatusFailed,
		FailureReasons:              []string{reason},
		WithheldOrUnavailableFields: []OpsCivilizationAssemblyUnavailableField{{Field: "hive_civilization_projection", Status: opsCivilizationFieldUnavailable, Reason: reason}},
		BoundaryFlags:               []string{"read_only_site_consumer", "failed_closed"},
	}
}

// fetchHiveOperatorProjection fetches and decodes the hive operator projection.
// Returns the projection or an error; it does not touch any OpsHiveData.
func fetchHiveOperatorProjection(r *http.Request) (*OpsHiveProjection, error) {
	base := strings.TrimSpace(os.Getenv("HIVE_OPS_API_BASE_URL"))
	if base == "" {
		return nil, errors.New("HIVE_OPS_API_BASE_URL is not configured")
	}
	endpoint := strings.TrimRight(base, "/") + "/api/hive/operator-projection"
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if key := strings.TrimSpace(os.Getenv("HIVE_OPS_API_KEY")); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := hiveOpsProjectionClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("hive operator projection returned %s", resp.Status)
	}
	var projection OpsHiveProjection
	if err := json.NewDecoder(resp.Body).Decode(&projection); err != nil {
		return nil, err
	}
	return &projection, nil
}

// opsCanonicalRoles is the fixed civic-role order shown in the society view.
// The last two rows ("human (Site approval)" and "draft PR") are synthetic: they
// are not lifecycle actors but represent the Gate E approval and the resulting
// draft PR action that flows from an approved pull_request.create decision.
var opsCanonicalRoles = []string{
	"strategist",
	"planner",
	"implementer",
	"reviewer",
	"guardian",
	"human (Site approval)",
	"draft PR",
}

// buildOpsRoleTimeline maps the hive operator projection's Lifecycle entries and
// AuthorityDecisions into a role-grouped timeline in canonical civic order.
//
// Sources (read-only, no new external calls):
//   - projection.Lifecycle — one entry per actor; actor.Role normalised to lower-case.
//   - projection.AuthorityDecisions — for the "human (Site approval)" and "draft PR" rows.
//
// The "human (Site approval)" row is populated when any decision has
// outcome == "approved" and approved_action == "pull_request.create".
// The "draft PR" row immediately follows: its status is "pending" unless an
// approved pull_request.create decision exists, in which case it is "approved".
func buildOpsRoleTimeline(projection *OpsHiveProjection) []OpsRoleTimelineRow {
	if projection == nil {
		// Return all canonical rows in empty state.
		rows := make([]OpsRoleTimelineRow, len(opsCanonicalRoles))
		for i, role := range opsCanonicalRoles {
			rows[i] = OpsRoleTimelineRow{Role: role}
		}
		return rows
	}

	// Index lifecycle entries by normalised role name (lower-case).
	// Multiple actors can share a role; collect their display names.
	type actorSummary struct {
		names    []string
		statuses []string
	}
	byRole := make(map[string]*actorSummary)
	for _, lc := range projection.Lifecycle {
		role := strings.ToLower(strings.TrimSpace(lc.Role))
		if role == "" {
			continue
		}
		if _, ok := byRole[role]; !ok {
			byRole[role] = &actorSummary{}
		}
		name := lc.DisplayName
		if name == "" {
			name = lc.ActorID
		}
		byRole[role].names = append(byRole[role].names, name)
		byRole[role].statuses = append(byRole[role].statuses, lc.LifecycleStatus)
	}

	// Look for an approved pull_request.create decision.
	var prDecision *OpsHiveDecision
	for i := range projection.AuthorityDecisions {
		d := &projection.AuthorityDecisions[i]
		if strings.ToLower(d.Outcome) == "approved" &&
			strings.ToLower(d.ApprovedAction) == "pull_request.create" {
			prDecision = d
			break
		}
	}

	rows := make([]OpsRoleTimelineRow, 0, len(opsCanonicalRoles))
	for _, canonicalRole := range opsCanonicalRoles {
		row := OpsRoleTimelineRow{Role: canonicalRole}

		switch canonicalRole {
		case "human (Site approval)":
			if prDecision != nil {
				row.Status = prDecision.Outcome
				row.Actors = prDecision.ApproverActor
				row.Notes = "approved_action: " + prDecision.ApprovedAction
				if prDecision.ApprovedTarget != "" {
					row.Notes += " → " + prDecision.ApprovedTarget
				}
			}

		case "draft PR":
			if prDecision != nil {
				row.Status = "approved"
				row.Notes = "pull_request.create approved — draft PR queued"
			}

		default:
			// Match against lifecycle entries using the canonical role label.
			// The canonical label IS the normalised role key (all lower-case).
			if summary, ok := byRole[canonicalRole]; ok && len(summary.names) > 0 {
				row.Actors = strings.Join(summary.names, ", ")
				// Use the first (or only) lifecycle_status; if multiple,
				// prefer "active" > anything else.
				row.Status = summary.statuses[0]
				for _, s := range summary.statuses {
					if strings.ToLower(s) == "active" {
						row.Status = s
						break
					}
				}
			}
		}

		rows = append(rows, row)
	}
	return rows
}

func fetchOpsTelemetry(r *http.Request) *OpsTelemetryData {
	workBase := serverWorkAPIBaseURL()
	data := &OpsTelemetryData{
		WorkURL:     legacyWorkURL(workBase, "/telemetry/overview"),
		PipelineURL: legacyWorkURL(workBase, "/telemetry/pipeline/report"),
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, data.WorkURL, nil)
	if err != nil {
		data.Error = err.Error()
		return data
	}
	setWorkAuth(req)
	resp, err := obsWorkClient.Do(req)
	if err != nil {
		data.Error = err.Error()
		return data
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data.Error = fmt.Sprintf("work telemetry returned %s", resp.Status)
		return data
	}
	var overview opsTelemetryOverview
	if err := json.NewDecoder(resp.Body).Decode(&overview); err != nil {
		data.Error = err.Error()
		return data
	}
	data.ActorCount = len(overview.Actors)
	data.AgentCount = len(overview.Agents)
	for _, agent := range overview.Agents {
		switch strings.ToLower(agent.State) {
		case "processing":
			data.ProcessingAgentCount++
			data.ActiveAgentCount++
		case "idle", "":
		default:
			data.ActiveAgentCount++
		}
	}
	for _, phase := range overview.Phases {
		if phase.Status == "in_progress" || phase.Status == "blocked" {
			data.PhaseLabel = phase.Label
			data.PhaseStatus = phase.Status
			break
		}
	}
	if data.PhaseLabel == "" && len(overview.Phases) > 0 {
		last := overview.Phases[len(overview.Phases)-1]
		data.PhaseLabel = last.Label
		data.PhaseStatus = last.Status
	}
	data.GeneratedAt = formatOpsTime(overview.Timestamp)
	data.RecentAgents = takeTelemetryAgents(overview.Agents, 8)
	data.RecentEvents = takeTelemetryEvents(overview.RecentEvents, 8)
	if report, err := fetchOpsPipelineReport(r, data.PipelineURL); err != nil {
		data.PipelineError = err.Error()
	} else {
		data.Pipeline = report
	}
	return data
}

func fetchOpsPipelineReport(r *http.Request, reportURL string) (*OpsPipelineReport, error) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, reportURL, nil)
	if err != nil {
		return nil, err
	}
	setWorkAuth(req)
	resp, err := obsWorkClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("work pipeline report returned %s", resp.Status)
	}
	var payload opsPipelineReportResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	if payload.Report == nil {
		if payload.Detail != "" {
			return nil, errors.New(payload.Detail)
		}
		if payload.Error != "" {
			return nil, errors.New(payload.Error)
		}
		return nil, nil
	}
	return payload.Report, nil
}

func fetchOpsWork(r *http.Request) *OpsWorkData {
	workBase := serverWorkAPIBaseURL()
	data := &OpsWorkData{
		WorkURL:     legacyWorkURL(workBase, "/tasks"),
		GeneratedAt: time.Now().UTC().Format("2006-01-02 15:04:05"),
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, data.WorkURL, nil)
	if err != nil {
		data.Error = err.Error()
		return data
	}
	setWorkAuth(req)
	resp, err := obsWorkClient.Do(req)
	if err != nil {
		data.Error = err.Error()
		return data
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data.Error = fmt.Sprintf("work tasks returned %s", resp.Status)
		return data
	}
	var tasks opsWorkTasksResponse
	if err := json.NewDecoder(resp.Body).Decode(&tasks); err != nil {
		data.Error = err.Error()
		return data
	}
	data.Total = len(tasks.Tasks)
	for _, task := range tasks.Tasks {
		switch strings.ToLower(task.Status) {
		case "completed", "done", "closed":
			data.Completed++
		case "in_progress", "active", "assigned":
			data.Active++
			data.Open++
		default:
			data.Open++
		}
		if task.Blocked {
			data.Blocked++
			data.BlockedTasks = append(data.BlockedTasks, task)
		}
		if strings.EqualFold(task.Priority, "high") {
			data.HighPriority++
		}
		if strings.TrimSpace(task.Assignee) == "" && !isCompletedWorkTask(task) {
			data.Unassigned++
		}
		data.EvidenceCount += task.ArtifactCount
		if task.Waived {
			data.WaivedCount++
		}
		if task.Ready {
			data.Ready++
		}
	}
	data.RecentTasks = takeWorkTasks(tasks.Tasks, 10)
	data.BlockedTasks = takeWorkTasks(data.BlockedTasks, 6)
	data.PhaseGates = fetchOpsPhaseGates(r, workBase)
	return data
}

func fetchOpsPhaseGates(r *http.Request, workBase string) []OpsPhaseGate {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, legacyWorkURL(workBase, "/phase-gates"), nil)
	if err != nil {
		return nil
	}
	setWorkAuth(req)
	resp, err := obsWorkClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil
	}
	var gates opsPhaseGatesResponse
	if err := json.NewDecoder(resp.Body).Decode(&gates); err != nil {
		return nil
	}
	if len(gates.Gates) > 6 {
		return gates.Gates[:6]
	}
	return gates.Gates
}

func setWorkAuth(req *http.Request) {
	if key := strings.TrimSpace(os.Getenv("WORK_API_KEY")); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
		return
	}
	req.Header.Set("Authorization", "Bearer dev")
}

func serverWorkAPIBaseURL() string {
	if base := strings.TrimSpace(os.Getenv("WORK_API_BASE_URL")); base != "" {
		return strings.TrimRight(base, "/")
	}
	if base := strings.TrimSpace(os.Getenv("WORK_UI_BASE_URL")); base != "" {
		return strings.TrimRight(base, "/")
	}
	return "http://localhost:8080"
}

// Explicit legacy configuration takes precedence on deployments that run Work.
func consoleUsesCivilizationWork() bool {
	return strings.TrimSpace(os.Getenv("CIVILIZATION_API_BASE_URL")) != "" &&
		strings.TrimSpace(os.Getenv("WORK_API_BASE_URL")) == "" &&
		strings.TrimSpace(os.Getenv("WORK_UI_BASE_URL")) == ""
}

func takeWorkTasks(tasks []OpsWorkTask, limit int) []OpsWorkTask {
	if len(tasks) <= limit {
		return tasks
	}
	return tasks[:limit]
}

func isCompletedWorkTask(task OpsWorkTask) bool {
	switch strings.ToLower(task.Status) {
	case "completed", "done", "closed":
		return true
	default:
		return false
	}
}

func takeTelemetryAgents(agents []OpsTelemetryAgent, limit int) []OpsTelemetryAgent {
	if len(agents) <= limit {
		return agents
	}
	return agents[:limit]
}

func takeTelemetryEvents(events []OpsTelemetryEvent, limit int) []OpsTelemetryEvent {
	if len(events) <= limit {
		return events
	}
	return events[:limit]
}

func formatOpsTime(raw string) string {
	if raw == "" {
		return ""
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t.Format("2006-01-02 15:04:05")
	}
	return raw
}

func formatOpsDuration(seconds float64) string {
	if seconds <= 0 {
		return "0s"
	}
	if seconds < 60 {
		return fmt.Sprintf("%.0fs", seconds)
	}
	if seconds < 3600 {
		return fmt.Sprintf("%.1fm", seconds/60)
	}
	return fmt.Sprintf("%.1fh", seconds/3600)
}

func opsPipelineOutcomeClass(outcome string) string {
	switch {
	case strings.Contains(strings.ToLower(outcome), "pass"), strings.Contains(strings.ToLower(outcome), "done"):
		return "border-emerald-400/30 text-emerald-300 bg-emerald-400/10"
	case strings.Contains(strings.ToLower(outcome), "revise"), strings.Contains(strings.ToLower(outcome), "fail"), strings.Contains(strings.ToLower(outcome), "error"):
		return "border-red-400/30 text-red-300 bg-red-400/10"
	case strings.Contains(strings.ToLower(outcome), "progress"):
		return "border-sky-400/30 text-sky-300 bg-sky-400/10"
	default:
		return "border-brand/30 text-brand bg-brand/10"
	}
}

func opsPhaseGateStatusClass(status string) string {
	switch strings.ToLower(status) {
	case "approved":
		return "border-emerald-400/30 text-emerald-300 bg-emerald-400/10"
	case "rejected":
		return "border-red-400/30 text-red-300 bg-red-400/10"
	default:
		return "border-amber-400/30 text-amber-300 bg-amber-400/10"
	}
}

func opsDecisionStatusClass(status string) string {
	switch strings.ToLower(status) {
	case "accepted_for_review", "absent":
		return "border-emerald-400/30 text-emerald-300 bg-emerald-400/10"
	case "blocked", "blocked outside scope":
		return "border-amber-400/30 text-amber-300 bg-amber-400/10"
	default:
		return "border-brand/30 text-brand bg-brand/10"
	}
}

func opsHiveShellStatusClass(status string) string {
	switch strings.ToLower(status) {
	case "clear", "ready", "draft ready", "active", "watching":
		return "border-emerald-400/30 text-emerald-300 bg-emerald-400/10"
	case "attention", "warning", "watch", "waiting":
		return "border-amber-400/30 text-amber-300 bg-amber-400/10"
	case "blocked", "paused", "missing":
		return "border-red-400/30 text-red-300 bg-red-400/10"
	case "idle":
		return "border-edge text-warm-faint bg-void/30"
	default:
		return "border-brand/30 text-brand bg-brand/10"
	}
}

func opsPublicProofStateClass(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "unavailable":
		return "border-red-300/40 text-red-300 bg-red-300/10"
	case "stale":
		return "border-amber-300/40 text-amber-300 bg-amber-300/10"
	case "fixture/local", "projection-only":
		return "border-edge text-warm-faint bg-void/30"
	case "operation-reference":
		return "border-sky-300/40 text-sky-200 bg-sky-300/10"
	case "deployed-reference", "live-reader-proof", "public-correction-proof":
		return "border-brand/40 text-brand bg-brand/10"
	default:
		return "border-edge text-warm-muted bg-void/30"
	}
}

func opsAgentStateClass(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "processing":
		return "border-sky-400/30 text-sky-300 bg-sky-400/10"
	case "idle", "projection-only":
		return "border-edge text-warm-faint bg-void/30"
	case "degraded", "stale":
		return "border-amber-400/30 text-amber-300 bg-amber-400/10"
	case "error", "failed", "unavailable", "missing":
		return "border-red-400/30 text-red-300 bg-red-400/10"
	default:
		return "border-brand/30 text-brand bg-brand/10"
	}
}

func opsPercentForCount(count, maxCount int) int {
	if count <= 0 {
		return 0
	}
	if maxCount <= 0 {
		return 100
	}
	percent := count * 100 / maxCount
	if percent < 8 {
		return 8
	}
	if percent > 100 {
		return 100
	}
	return percent
}

func legacyWorkURL(base, path string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	u.Path = path
	u.RawQuery = ""
	return u.String()
}
