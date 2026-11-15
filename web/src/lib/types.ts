// ARTEX domain model — types used across the UI.
// Derived from the functional spec (section 7: key data shapes).

export type TaskStatus = "created" | "queued" | "running" | "paused" | "done" | "failed" | "timeout";
export type EngineMode = "exploring" | "paused" | "stalled" | "idle";

// Keep these limits aligned with db.MaxTaskRemarkRunes and db.MaxTaskWorkerCount.
export const MAX_TASK_REMARK_RUNES = 2000;
export const MAX_TASK_WORKER_COUNT = 50;

// Parse the optional worker-count input without silently changing invalid values.
// An empty field means 0 (use the global default); every other value must be an
// integer in the backend's accepted range.
export function parseWorkerCountInput(value: string): number | null {
  const trimmed = value.trim();
  if (trimmed === "") return 0;
  const parsed = Number(trimmed);
  if (!Number.isFinite(parsed) || !Number.isInteger(parsed)) return null;
  if (parsed < 0 || parsed > MAX_TASK_WORKER_COUNT) return null;
  return parsed;
}

export interface Task {
  distinct_findings?: FindingDistinctStats;
  id: string;
  remark?: string;
  worker_count?: number; // Number of Workers for this task; 0/omitted = use the global default
  name?: string; // Optional task name; empty/omitted = unnamed, falls back to the description when displayed
  category_id?: number;
  category_name?: string;
  pinned?: boolean;
  pinned_at?: string | null;
  description: string;
  goal: string;
  status: TaskStatus;
  created_at: string;
  created_unix?: number;
  completed_at?: string; // RFC3339 finish time (done/failed); "" if unfinished
  completed_unix?: number; // completed_at as unix seconds (0/undef if unfinished)
  last_activity_unix?: number; // unix seconds of the last activity (0/undef if none)
  run_seconds?: number; // backend-computed active runtime seconds
  active_run_seconds?: number; // completed running segments in seconds
  run_segment_started_unix?: number; // current running segment start; 0 when inactive
  paused?: boolean;
  pause_reason?: string;
  reconnect?: { attempt: number; max: number; next_at?: number; exhausted: boolean; last_error?: string };
  queued?: boolean;
  active?: boolean;
  in_flight?: number;
  findings?: { critical: number; high: number; medium: number; low: number }; // Number of registered vulnerabilities (grouped by severity)
  last_activity?: string;
  stalled?: boolean;
  goals_total?: number;
  goals_met?: number;
  engine_mode?: EngineMode;
  tokens?: TokenTotal; // whole-task token consumption
  llm_profile_id?: number; // LLM profile used; absent = default profile
  llm_profile_ids?: number[]; // ordered task-level failover chain
  active_llm_profile_id?: number; // profile used by the next LLM call
  llm_failover_state?: "default" | "ready" | "chain_exhausted" | string;
  llm_failover_reason?: string;
  source_task_ids?: string[]; // directly related tasks inherited as read-only context
  archive_blocked_by_task_id?: string; // live direct dependent that must be archived first
  company_ids?: number[]; // associated company scopes; current company assets join the task at creation
  share_global_assets?: boolean; // opt-in global asset visibility; default false
  coverage_enabled?: boolean; // Asset coverage feature toggle (set at creation, enabled by default); false = do not calculate/display coverage
}

export interface TaskCategory {
  id: number;
  name: string;
  task_count: number;
  created_at: string;
  updated_at: string;
}

export interface TaskTemplate {
  id: number;
  name: string;
  description: string;
  goal: string;
  remark?: string; // Preset notes
  category_id?: number | null; // Preset category; null/omitted = none
  intercept_rules?: AssetInterceptRuleInput[]; // Preset task-level block/allow rules
  worker_count?: number; // Preset number of Workers; 0/omitted = use the global default
  created_at: string;
  updated_at: string;
}

export interface DeleteTaskOptions {
  delete_assets: boolean;
  delete_traffic: boolean;
  delete_files: boolean;
  delete_findings: boolean;
  delete_llm_records: boolean;
}

export interface DeleteTaskResult {
  deleted: string;
  assets_deleted: number;
  assets_detached: number;
  traffic_deleted: number;
  files_deleted: boolean;
  findings_deleted: number;
  llm_records_deleted: number;
  cleanup_warning?: string;
}

export type TaskArchiveState =
  | "archive_queued"
  | "archiving"
  | "archive_failed"
  | "ready"
  | "restore_queued"
  | "restoring"
  | "restore_failed"
  | "delete_queued"
  | "deleting"
  | "delete_failed";

export interface TaskArchiveTokenStats {
  calls?: number;
  input_tokens?: number;
  output_tokens?: number;
  cache_read_tokens?: number;
  cache_write_tokens?: number;
}

export interface TaskArchive {
  id: number;
  task_id: number;
  state: TaskArchiveState;
  phase: string;
  progress: number;
  error?: string;
  warnings?: string[];
  format_version: number;
  sha256?: string;
  original_size: number;
  compressed_size: number;
  task_name: string;
  task_description: string;
  task_goal: string;
  original_status: TaskStatus;
  category_id?: number;
  category_name?: string;
  source_task_ids: number[];
  remaining_timeout_seconds: number;
  data_counts: Record<string, number>;
  aggregate_stats: {
    tokens?: TaskArchiveTokenStats;
    skills?: Record<string, number>;
    tools?: Record<string, number>;
    findings?: Record<string, number>;
  };
  archived_at?: string;
  requested_at: string;
  created_at: string;
  updated_at: string;
}

export interface TaskArchivePage {
  items: TaskArchive[];
  total: number;
  page: number;
  size: number;
}

export interface ArchiveBatchItem {
  id: string;
  archive_id?: number;
  ok: boolean;
  queued: boolean;
  error?: string;
}

// ---- Asset graph (global, shared across tasks) ----
export type AssetType =
  | "company"
  | "domain"
  | "ip"
  | "port"
  | "service"
  | "site"
  | "endpoint"
  | "parameter"
  | "tech"
  | "credential"
  | "data";

export type NodeState = "observed" | "confirmed" | "tombstoned";

export interface AssetNode {
  id: string;
  type: AssetType;
  name: string;
  key: string; // nkey
  value?: string;
  company_id?: string; //Company asset ID; empty = unassigned
  state: NodeState;
  confidence: number; // 0..1
  attrs?: Record<string, unknown>;
  first_seen: string;
  last_seen: string;
}

export type AssetRel =
  | "owns"
  | "resolves"
  | "exposes"
  | "runs"
  | "serves"
  | "has_endpoint"
  | "has_param"
  | "fingerprinted"
  | "authenticates_as"
  | "reachable"
  | "has_subdomain";

export interface Edge {
  src: string;
  dst: string;
  rel: AssetRel | ExploreRel;
}

// Task asset view — server-side enriched, paginated.
export interface TaskAssetRef {
  id: string;
  name?: string;
  key: string;
  attrs?: Record<string, unknown>;
}

export interface TaskAssetItem extends AssetNode {
  techs?: TaskAssetRef[];
  auth?: TaskAssetRef[];
  params?: TaskAssetRef[];
}

export interface TaskAssetView {
  counts: Record<string, number>;
  total: number;
  items: TaskAssetItem[];
}

// ---- New unified asset model (new backend) ----
export type NewAssetType = "root_domain" | "ip" | "subdomain" | "app" | "service" | "endpoint";

export interface Asset {
  id: number;
  type: NewAssetType;
  company_id?: number;
  task_ids: number[];
  domain?: string;
  root_domain?: string;
  ip?: string;
  c_segment?: string;
  port?: number;
  icp?: string;
  bound_domains?: string[];
  open_ports?: { port: number; service?: string }[];
  record_type?: string;
  record_value?: string[] | string;
  bundle_id?: string;
  app_name?: string;
  category?: string;
  app_description?: string;
  app_icp?: string;
  url?: string;
  service_type?: string;
  service_name?: string;
  favicon_mmh3?: string;
  status_code?: number;
  content_length?: number;
  page_title?: string;
  technologies?: string[];
  auth?: Record<string, unknown>[];
  method?: string;
  params?: Record<string, unknown>[];
  extra?: Record<string, unknown>;
  last_seen: string;
  task_source?: string;
  task_source_summary?: string;
  task_source_node_id?: number;
}

export interface IntentAsset {
  intent_id: number | string;
  asset_id: number;
  type: NewAssetType;
  label: string;
  source: string;
  source_summary: string;
  source_node_id?: number;
  source_task_id: number;
  inherited: boolean;
}

export interface TaskAssetMutation {
  requested: number;
  attached: number;
  existing: number;
}

export interface TaskAssetScopeMutation {
  requested: number;
  assets_linked: number;
  assets_existing: number;
  scopes_added: number;
  scopes_existing: number;
}

// ---- Asset coverage graph (per task) ----
// A node in the force-directed "asset coverage graph." Keys are unique: assets = "a:<id>", companies = "c:<id>",
// root domains without asset rows = "r:<domain>". Nodes with in_scope=false are gray context nodes used only for links.
export interface CoverageGraphNode {
  key: string;
  kind: "company" | "root_domain" | "subdomain" | "ip" | "service" | "app" | "endpoint";
  label: string;
  tested: boolean;
  in_scope: boolean;
  asset_id?: number;
  company_id?: number;
  domain?: string;
  root_domain?: string;
  ip?: string;
  url?: string;
  port?: number;
  service_type?: string;
  app_name?: string;
  page_title?: string;
  status_code?: number;
  zone?: "internet" | "boundary" | "internal";
  zone_reason?: string;
}

export interface CoverageGraphEdge {
  src: string;
  dst: string;
}

export interface CoverageGraphData {
  nodes: CoverageGraphNode[];
  edges: CoverageGraphEdge[];
}

// Intent/fact/finding associated with an asset in this task's exploration graph (used by the coverage graph node drawer).
export interface CoverageAssetRef {
  id: number;
  kind: string;
  state: string;
  summary: string;
  source_task_id?: string;
  inherited?: boolean;
}
export interface CoverageAssetRefs {
  intents: CoverageAssetRef[];
  facts: CoverageAssetRef[];
  findings: CoverageAssetRef[];
}

// ---- Workspace file manager (workDir) ----
export interface WorkspaceEntry {
  name: string;
  path: string; // workspace-relative, forward slashes
  dir: boolean;
  size: number;
  mtime: number; // unix millis
}
export interface WorkspaceListing {
  path: string;
  entries: WorkspaceEntry[];
}
export interface WorkspaceFile {
  path: string;
  size: number;
  binary: boolean;
  too_large?: boolean;
  content?: string;
}

// An item in the task's test scope (coverage denominator + authorization boundary).
export interface TaskScopeRow {
  id: number;
  task_id: number;
  kind: "company" | "root_domain" | "subdomain" | "ip" | "cidr" | "icp" | "keyword";
  company_id?: number;
  company_name?: string; //Resolved by the backend JOIN on companies; populated only when kind=company
  domain?: string;
  net?: string;
  value?: string;
  source: "auto" | "agent" | "manual";
  reason?: string;
}

export type CompanyScopeKind = "domain" | "ip" | "cidr" | "icp" | "keyword";

// Structured asset scope rules submitted when adding a company.
export interface CompanyScopeRule {
  kind: CompanyScopeKind;
  value: string;
}

// Result of writing asset scope. errors are invalid rows in this submission; warnings are existing data issues unrelated to this submission,
// but that may cause ownership results to differ from expectations (e.g. assets with hostnames stored in the ip field).
export interface CompanyScopeMutation {
  added: number;
  skipped: number;
  invalid: number;
  errors?: string[];
  warnings?: string[];
}

// A company asset scope rule (the single source of truth for ownership).
export interface ScopeRow {
  id: number;
  company_id: number;
  kind: CompanyScopeKind;
  domain?: string; //Populated when kind=domain
  net?: string; //Populated when kind=ip|cidr
  value?: string; //May be returned directly by the backend when kind=icp|keyword
  raw: string; //Raw user input, used for display and prefill
  reason?: string;
}

// Company: asset nodes with type=company + icon + asset count + asset scope rules.
export interface Company {
  id: number;
  name: string;
  logo?: string; //Remote icon URL; if empty, the frontend uses the initial of the name
  asset_count: number;
  scope?: ScopeRow[];
}

// ---- Exploration graph (per task) ----
export type ExploreKind = "task" | "begin" | "goal" | "intent" | "fact" | "finding" | "hint" | "digest";
export type GoalState = "open" | "met" | "abandoned";
export type IntentState = "open" | "running" | "paused" | "done" | "blocked" | "exhausted" | "stopped";
export type FindingState = "confirmed" | "dismissed";
export type HintState = "active" | "consumed";
export type ExploreRel = "spawns" | "derived_from" | "yields" | "proves" | "covers";

export interface TaskNode {
  id: string;
  type: ExploreKind;
  payload?: string;
  priority: number; // 0..10
  state: string; // GoalState | IntentState | FindingState | HintState
  origin: string;
  ts: string;
  source_task_id?: string;
  inherited?: boolean;
  delete_reason?: string; //Reason for deleting an intent (state='deleted')
}

// One page of the activity feed: nodes paginated by creation order + edges involving this page + nodes at the other ends of those edges (refs, indexed by id),
// so each activity entry can explain "where it came from and what it produced" without loading the entire graph.
export interface ExplorationNodePage {
  items: TaskNode[];
  total: number;
  page: number;
  size: number;
  edges: Edge[];
  refs: Record<string, TaskNode>;
  // Node ID → the asset anchored to that node (also shown when expanding the activity feed, including nodes on this page and their neighbors).
  assets: Record<string, FindingAsset[]>;
}

export interface ExplorationNodeQuery {
  page?: number;
  size?: number;
  kinds?: ExploreKind[];
  states?: string[];
  q?: string;
  order?: "asc" | "desc";
}

// A goal for the goal management card (the backend has split payload into text/vulnclass).
export interface TaskGoal {
  id: string;
  text: string;
  vulnclass?: string;
  state: string; // GoalState
  origin?: string;
  ts: string;
}

// An operational constraint for the constraint management card (allow = permitted / deny = prohibited).
export type ConstraintKind = "allow" | "deny";
export interface TaskConstraint {
  id: string;
  kind: ConstraintKind;
  text: string;
  origin?: string;
  ts?: string;
}

// ---- Findings ----
export type Severity = "critical" | "high" | "medium" | "low";

// Vulnerability handling status: pending / in progress / confirmed / processed / fixed / false positive / ignored / duplicate / risk accepted.
export type FindingStatus =
  | "pending"
  | "in_progress"
  | "confirmed"
  | "resolved"
  | "fixed"
  | "false_positive"
  | "ignored"
  | "duplicate"
  | "risk_accepted";

// FindingAsset is an asset associated with a vulnerability (label pre-rendered by the backend).
export interface FindingAsset {
  id: string;
  type: string;
  label: string;
}

export interface Finding {
  case_id?: string;
  traffic_count?: number;
  evidence_version?: number;
  report_evidence_version?: number;
  report_stale?: boolean;
  id: string;
  finding_id?: string; //Row ID in the standalone findings table; handle for status updates (may be missing for legacy nodes in a task)
  vulnclass: string;
  name?: string; //Vulnerability name; falls back to vulnclass when empty
  severity: Severity;
  status: FindingStatus;
  summary: string;
  evidence: string;
  report?: string; //Detailed report (Markdown); returned only by the detail endpoint, empty in the list
  report_conversation_id?: number;
  intent_id?: string;
  param_id?: string;
  task_id?: string;
  task_description?: string;
  source_task_id?: string;
  inherited?: boolean;
  assets?: FindingAsset[];
  ts: string;
}

// FindingsPage is the server-paginated response for the findings list.
export interface FindingsPage {
  items: Finding[];
  total: number;
  page: number;
  page_size: number;
}

export interface FindingGroup {
  task_id: string | number | null;
  task_name?: string; //Optional task name; empty/omitted = unnamed
  task_description: string;
  task_status: string;
  count: number;
  critical: number;
  high: number;
  medium: number;
  low: number;
  last_found_at: string;
}

export interface FindingGroupsPage {
  items: FindingGroup[];
  total: number;
  finding_total: number;
  page: number;
  page_size: number;
}

export interface FindingDeepenResponse {
  task_id: string;
  intent_id: string;
  state: IntentState;
  queued: boolean;
}

// FindingStats is an aggregate over all findings (stats cards + vulnerability type dropdown), computed server-side and unaffected by pagination.
export interface FindingStats {
  distinct?: FindingDistinctStats;
  total: number;
  pending: number;
  critical: number;
  high: number;
  medium: number;
  low: number;
  vulnclasses: string[];
  tasks: FindingTaskOption[];
}

// FindingTaskOption is an item in the findings page's "by task" filter dropdown: a task with vulnerabilities (empty description means the task has been deleted;
// the frontend falls back to displaying the ID) and its vulnerability count.
export interface FindingTaskOption {
  id: string | number;
  name?: string; //Optional task name; empty/omitted = unnamed
  description: string;
  count: number;
}

// FindingQuery contains pagination/filter/sort parameters for the findings list.
export interface FindingQuery {
  page: number;
  pageSize: number;
  severity?: "all" | Severity;
  status?: "all" | FindingStatus;
  vulnclass?: string;
  task?: string; //Task ID; "all"/empty = do not filter by task
  query?: string;
  sort?: "severity" | "time";
  timeDirection?: "asc" | "desc"; // Missing = newest first; secondary to severity when sort="severity".
  from?: string; // ISO timestamp, inclusive
  to?: string; // ISO timestamp, inclusive
  // Asset tree node key; selecting a node selects its entire subtree. Empty = no asset filtering.
  assetScope?: string;
}

// ---- Findings by asset (asset view) ----
export type FindingAssetKind = "company" | "root_domain" | "subdomain" | "ip" | "service" | "app" | "endpoint" | "none";

// FindingAssetNode is a node in the asset tree. Keys have the form a:<id>(asset), c:<id>(company),
// r:<domain>(root domain with no asset row in the database), __none__(unassociated asset).
export interface FindingAssetNode {
  key: string;
  parent?: string;
  kind: FindingAssetKind;
  label: string;
  asset_id?: number;
  company_id?: number;
  self: number; // Findings directly attached to this asset
  total: number; // Includes descendants, deduplicated by finding
  critical: number;
  high: number;
  medium: number;
  low: number;
  last_found_at: string;
}

export interface FindingAssetTree {
  nodes: FindingAssetNode[];
  finding_total: number;
  truncated: boolean;
  dropped_kinds?: string[];
}

// FINDING_UNASSIGNED_ASSET corresponds to backend db.FindingUnassignedAsset.
export const FINDING_UNASSIGNED_ASSET = "__none__";

// ---- Activity / sessions ----
export type ActivityKind =
  | "tool_use"
  | "tool_result"
  | "text"
  | "thinking"
  | "result"
  | "user"
  | "intent" // LLM-generated exploration objective leading a worker session (UI-synthesized)
  | "round" // planner round boundary marker (engine-emitted)
  | "usage" // live cumulative token usage (per model turn); not rendered
  | "llm_switch" // automatic/manual task-level LLM switch
  | "llm_failover" // task-level provider switch / chain exhaustion audit event
  | "intercept_request"; // user-approval request from the intercept layer

// ChatAttachment is a file uploaded once: path is relative to the working directory of the session/task (i.e., the agent's CWD).
export interface ChatAttachment {
  name: string;
  path: string;
  size: number;
  abs?: string; // Absolute path (returned when uploading to staging with scope=staging; write it into the description before creating the task)
}

export interface Activity {
  seq: number;
  intent_id?: string;
  worker: string; // session owner: planner | mainagent | work#1 ...
  ts: string;
  kind: ActivityKind;
  tool?: string;
  tool_use_id?: string;
  is_error?: boolean;
  summary: string;
  detail?: string;
  metadata?: {
    llm_transition?: LLMTransition;
  };
  source_task_id?: string;
  inherited?: boolean;
  main_seg?: number; // main-agent conversation segment (present only on worker="mainagent" rows)
  // token usage (present only on kind='result')
  input_tokens?: number;
  output_tokens?: number;
  cache_read_tokens?: number;
  cache_write_tokens?: number;
}

export interface LLMAuditProfile {
  id: number;
  name: string;
  format: string;
  model: string;
}

export interface LLMTransition {
  mode: "automatic" | "manual" | "exhausted";
  reason: string;
  previous?: LLMAuditProfile;
  next?: LLMAuditProfile;
}

export interface TaskLLMResolution {
  profile_id?: number;
  name: string;
  format: string;
  model: string;
  source: "task_chain" | "agent_binding" | "global_profile" | "environment" | "global";
  available: boolean;
  reason?: string;
}

export interface TaskLLMResolutions {
  mainagent: TaskLLMResolution;
  planner: TaskLLMResolution;
  worker: TaskLLMResolution;
}

// ---- Agent triggers (P3 scheduling, custom agents only) ----
export interface AgentTrigger {
  id: number;
  agent_key: string;
  enabled: boolean;
  interval_sec: number; // Scheduled: every N seconds (0 = not scheduled)
  on_finding: boolean; // Triggered when any task finds a finding
  on_goal_met: boolean; // Triggered when any task achieves its goal
  on_task_timeout: boolean; // Triggered when any task times out
  on_tool_call: boolean; // Triggered when a selected tool is called (execution completes)
  on_task_create: boolean; // Triggered when any task is created
  interval_message: string; // Separate user message for each trigger condition
  finding_message: string;
  goal_message: string;
  task_timeout_message: string;
  tool_call_message: string;
  task_create_message: string;
  tool_names: string[]; //Tool keys selected for on_tool_call (at least one)
  last_fire?: string;
}

export interface TaskSchedule {
  id: number;
  name: string;
  remark?: string;
  enabled: boolean;
  timezone_mode: "beijing" | "system";
  timezone?: string;
  schedule_type: "once" | "weekly";
  run_date?: string;
  end_date?: string;
  weekdays: number[];
  start_time: string;
  end_time?: string;
  start_immediately: boolean;
  status: "waiting" | "running" | "outside" | "completed" | "error" | "paused";
  last_window_key?: string;
  last_transition_at?: string;
  last_error?: string;
  next_start?: string;
  next_end?: string;
  run_count?: number;
  last_run_at?: string;
  task_ids: number[];
  history?: Array<{
    id: number;
    task_id?: number;
    action: string;
    success: boolean;
    message: string;
    created_at: string;
  }>;
}

// ---- Conversations (chat page) ----
export interface ActiveFindingRetest {
  id: number;
  finding_id: string;
  conversation_id: number;
  status: "pending" | "running";
}

export interface FindingRetest {
  id: number;
  finding_id: number;
  conversation_id: number | null;
  status: "pending" | "running" | "completed" | "failed" | "stopped";
  verdict: "" | "reproduced" | "fixed" | "inconclusive";
  notes: string;
  summary: string;
  evidence: string;
  error: string;
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
}

export interface Conversation {
  id: number;
  running?: boolean; // live server state, returned with the conversation list
  agent_key: string;
  title: string;
  llm_profile_id?: number;
  pinned?: boolean;
  pinned_at?: string | null;
  created_at: string;
  updated_at: string;
}

// ---- Backend logs (/logs page) ----
export interface LogLine {
  seq: number;
  db_id?: number; // server_logs.id; present for DB-persisted lines
  ts: string;
  level: "info" | "warn" | "error";
  tag: string;
  text: string;
}

export type SessionRole = "mainagent" | "planner" | "worker" | "system";
export type SessionStatus = "running" | "paused" | "done" | "blocked" | "exhausted" | "pending" | "stopped" | "deleted";

// Daily token aggregate bucket (GET /api/tokens/daily).
export interface DailyTokenBucket {
  date: string; // "YYYY-MM-DD"
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
}

// Per-worker token usage (GET /api/exploration/tokens).
export interface TokenUsage {
  worker: string;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
}

export interface SessionTokenUsage {
  session: string;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
}

export interface BatchControlItem {
  id: string;
  ok: boolean;
  status?: string;
  queued?: boolean;
  error?: string;
}

// Per-task results of bulk category changes. Failures can only occur if a task has been deleted; category writes themselves are atomic.
export interface BatchCategoryItem {
  id: string;
  ok: boolean;
  error?: string;
}

// Whole-task (all agents) token aggregate.
export interface TokenTotal {
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
}

// Global per-profile token spend from the llm_usage ledger (GET /api/tokens/usage).
export interface ProfileUsage {
  profile_name: string;
  calls: number;
  tasks: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
}

// One (profile, UTC day) token bucket for the dashboard's daily chart (new source).
export interface ProfileDayUsage {
  profile_name: string;
  date: string; // YYYY-MM-DD
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
}

// Response of GET /api/tokens/usage — the dashboard's "new" (llm_usage) token view.
export interface UsageStats {
  by_profile: ProfileUsage[];
  daily: ProfileDayUsage[];
}

// Per-model token usage for one task (GET /api/llm/records/by-model), from the
// always-on llm_usage metering ledger. calls = number of LLM calls on this model.
export interface ModelTokenStat {
  model: string;
  calls: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
}

export interface Session {
  id: string;
  role: SessionRole;
  title: string;
  status: SessionStatus;
  live: boolean;
  last_activity: string;
  intent_id?: string;
  source_task_id?: string;
  inherited?: boolean;
  seg?: number; // main-agent session: which conversation segment (0 = original)
}

// ---- Security ----
export interface AuditEntry {
  ts: string;
  tool: string;
  action: "allow" | "block";
  reason?: string;
  command?: string;
}

export interface Audit {
  entries?: AuditEntry[];
  attributions?: Record<string, number>;
}

// ---- Traffic ----
export interface TrafficExchange {
  id: string;
  ts: string;
  host: string;
  method: string;
  url: string;
  status: number;
  content_type: string;
  resp_len: number;
}

export interface TrafficResp {
  enabled: boolean;
  proxy?: string;
  count?: number; // global total (unfiltered)
  total?: number; // rows matching the current filter (for pagination)
  page?: number;
  size?: number;
  exchanges?: TrafficExchange[];
}

// Full raw request/response of one exchange (lazy-loaded on row select).
export interface TrafficDetail {
  req: string;
  resp: string;
}

// One distinct recorded host with its exchange count (target picker).
export interface TrafficHost {
  host: string;
  count: number;
}

// ---- App settings (runtime toggles) ----
export interface Settings {
  traffic_capture: boolean;
  /** Whether to expose the browser MCP screenshot tool to visible Agents. Disabled by default. */
  browser_screenshot_enabled?: boolean;
  /** Whether to show the screenshot save time in the top-right corner of report screenshots. Disabled by default. */
  browser_screenshot_timestamp_enabled?: boolean;
  /** Whether to inject unified OPSEC guidelines into the Agent. Disabled by default. */
  opsec_enabled?: boolean;
  credential_agent_enabled: boolean;
  credential_traffic_binding: boolean;
  task_intelligence_enabled: boolean; // Disabled by default; when disabled, no intelligence tools or prompts are injected
  task_auto_title_enabled: boolean; // Automatically generate a task name when the Agent creates a task
  chat_auto_title_enabled: boolean; // Automatically generate a session title after the first message in a conversation
  agent_traffic_binding: boolean; // Automatically bind traffic evidence to the Agent; disabled by default. Does not affect manual binding
  llm_record: boolean; //LLM recording toggle (off by default); when off, no LLM calls are recorded
  // Web search. brave_key_set / tavily_key_set reflect whether a key is stored
  // (the values are never returned). On PUT, send the corresponding field to set/clear.
  web_search_enabled: boolean;
  web_search_backend: string; // "ddgs" | "brave-free" | "tavily" | "deepseek"
  brave_key_set: boolean;
  tavily_key_set: boolean;
  deepseek_search_key_set: boolean;
  // write-only: only sent on PUT to store/clear the key.
  brave_search_api_key?: string;
  tavily_search_api_key?: string;
  deepseek_search_api_key?: string;
  // Independent egress proxy (http/https/socks5) for accessing search endpoints; unrelated to the MITM proxy used to record traffic. Empty = direct connection.
  web_search_proxy?: string;
  // Global egress proxy (http/https/socks5, can include user:pass); all target traffic goes through it. When traffic capture is enabled, it serves as the
  // MITM upstream; when capture is disabled, it is injected directly into the agent's bash/WebFetch. Empty = direct connection.
  global_proxy?: string;
  proxy_pool?: ProxyPoolEntry[];
  proxy_pool_active_id?: string;
  proxy_pool_switch_log?: ProxySwitchLog[];
  proxy_rotate_timed_enabled?: boolean;
  proxy_rotate_interval_seconds?: number;
  proxy_rotate_on_failure?: boolean;
  proxy_rotate_strategy?: "sequential" | "random";
  proxy_health_fail_threshold?: number;
  proxy_health_cooldown_seconds?: number;
  proxy_health_check_interval_seconds?: number;
  proxy_reconnect_max_attempts?: number;
  proxy_reconnect_interval_seconds?: number;
  show_egress_ip?: boolean; // Show the direct / global proxy egress IP in the top bar; disabled by default
  egress_probe_url?: string; // Custom IP detection service; empty = default public echo service
  python_interpreter?: string; // Python interpreter path for custom script tools (empty = detect at runtime)
  workers?: number; // Number of concurrent worker agents (default 3); applies to tasks started afterward
  worker_pools: WorkerPool[];
  // Task concurrency limit: maximum number of tasks running simultaneously. Disabled = unlimited; when enabled, new tasks exceeding the limit are queued and start automatically when a slot becomes available.
  task_concurrency_enabled?: boolean; // Default false
  task_concurrency_limit?: number; // Default 5 when enabled
  // LLM polling (failover). Off by default; when enabled, agents with no specified model automatically switch to the next configuration when the current configuration is unavailable
  // (insufficient balance/invalid key/rate limit/service error).
  llm_pool_enabled?: boolean; // Default false
  // Whether agents/tasks bound to a specific configuration also fall back to the polling chain on failure. Default false = binding is exclusive.
  llm_pool_bind_fallback?: boolean;
  // Scope of operation-constraint injection (all enabled by default): append the task's allow/deny constraints to the corresponding agent's system prompt.
  constraints_inject_planner?: boolean;
  constraints_inject_worker?: boolean;
  // Experimental feature: noa model-driven context compaction (off by default). When enabled, noa takes over context compaction for the four types of agents integrated by the platform (planner/
  // worker/main agent/conversation), replacing built-in compaction; read once per run, effective for
  // runs started afterward.
  noa_compaction?: boolean;
  // ---- Vulnerability IM notifications (channels are independent resources; see /api/notify/*; only these three global settings are here) ----
  notify_enabled?: boolean; // Global notification toggle, on by default; for quickly stopping notifications during maintenance
  notify_public_base_url?: string; // External access URL for vulnerability details; empty = messages omit the link
  notify_digest_interval_min?: number; // Summary mode interval (minutes), default 30
}

export interface ProxyPoolEntry {
  id: string;
  url: string;
  name?: string;
  remark?: string;
  enabled: boolean;
  egress_ip?: string;
  latency_ms?: number;
  last_error?: string;
  last_checked_at?: string;
  consecutive_failures: number;
  health: "healthy" | "unhealthy-cooling" | "disabled" | string;
}

export interface ProxySwitchLog { at: string; from: string; to: string; reason: string }

export interface WorkerPool {
  profile_id: string;
  workers: number;
}

// ---- Vulnerability IM notifications ----

// NotificationFilter contains a channel's filter conditions. All fields are optional; omitted fields mean no filtering.
// The backend performs no validation on any fields: malformed configuration is treated as a "match" (better to send too many than miss any).
export interface NotificationFilter {
  min_severity?: string; // "" | low | medium | high | critical
  task_ids?: number[]; // Empty = unrestricted; if non-empty, must intersect with the tasks associated with the vulnerability
  asset_ids?: number[]; // Empty = unrestricted; if non-empty, must intersect with the assets anchored to the vulnerability
  vulnclass_include?: string[]; // Empty = accept all; if non-empty, the vulnerability type must contain any keyword (case-insensitive substring match)
  vulnclass_exclude?: string[]; // Exclude if any keyword matches (exclusion takes precedence over inclusion)
  on_status_change?: boolean; // Whether to also receive vulnerability remediation status change events
}

// NotificationChannel is a channel instance. The fields in config vary by kind,
// and credential fields are replaced on read with masked values prefixed by "__masked__"—returning them unchanged means "do not modify."
export interface NotificationChannel {
  id: number;
  name: string;
  kind: string;
  enabled: boolean;
  mode: "realtime" | "digest";
  config: Record<string, unknown>;
  filter: NotificationFilter;
  rate_per_min: number;
  created_at: string;
  updated_at: string;
  // secret_keys are provided by the backend according to channel type; the frontend uses them to render password fields and the "leave blank to keep unchanged" hint,
  // without hardcoding knowledge of any channel.
  secret_keys: string[];
}

// NotificationKind is channel type metadata returned by /api/notify/meta.
export interface NotificationKind {
  kind: string;
  default_rate_per_min: number;
  secret_keys: string[];
}

export interface NotificationMeta {
  kinds: NotificationKind[];
  enabled: boolean;
  public_base_url: string;
  digest_interval_min: string;
  defaults: { digest_interval_min: number };
  stats: {
    channels: number;
    channels_on: number;
    pending: number;
    failed: number;
    sent_today: number;
    backlog_age_ms: number;
  };
}

// NotificationDelivery is a delivery record used for delivery history and retrying failures.
export interface NotificationDelivery {
  id: number;
  finding_id: string;
  event_kind: string; // finding_created | finding_status_changed
  channel_id: number;
  channel_name: string;
  channel_kind: string;
  state: "pending" | "sending" | "sent" | "failed" | "skipped";
  attempts: number;
  last_error: string;
  batch_id?: number;
  created_at: string;
  sent_at?: string;
  next_attempt_at: string;
  title: string;
  severity: string;
}

// ---- LLM config ----
export interface LLMProfile {
  id: string;
  name: string;
  format: "openai" | "anthropic" | "openai-responses" | "jev";
  base_url?: string;
  proxy?: string;
  model: string;
  api_key_hint?: string;
  rate_per_second: number;
  rate_per_minute: number;
  context_window_k?: number;
  // Thinking toggle (thinking.type): "" = not sent (default) | "disabled" = off | "enabled" = on
  thinking_type?: string;
  // Thinking intensity: "" = not sent (default) | "low"/"medium"/"high"/"xhigh"/"max"
  reasoning_effort?: string;
  is_default: boolean;
  // Polling priority: higher values are selected first. The active configuration is always first in the chain, regardless of this value.
  priority?: number;
  // true = not a failover target (can still be explicitly bound to an agent/task).
  pool_exclude?: boolean;
  // true (default) = streaming (SSE) | false = true non-streaming (stream:false, returned all at once).
  streaming?: boolean;
  // Output limit (tokens) for a single reply. 0 = do not send this field; the server default applies.
  // Note the distinction from context_window_k: the latter is the model's total capacity and is used locally only for the compaction threshold.
  max_tokens?: number;
  // Which request field name to use for the token limit; only meaningful when format="openai":
  // ""=max_tokens (default) | "max_completion_tokens" (OpenAI reasoning models only recognize this)
  max_tokens_field?: string;
  // Custom session header name: when non-empty, include this HTTP header with every request; header value = the current session/intent session ID.
  // ""=do not send. For gateways that use the session-id header for prompt caching/sticky routing.
  session_header_key?: string;
  // Overrides for retries in this configuration (connect/empty response/same-provider safety window). Blank/all 0 = follow the global policy.
  retry?: LLMRetryOverride;
}

// ---- LLM Retry Policy ----
// Two knobs for each retry layer. For both, "0 = not configured":
//   attempts    0=use default count | -1=disable this retry layer | >0=number of retries
//   interval_ms 0=use default exponential backoff | >0=use this fixed interval in milliseconds
export interface LLMRetryRule {
  attempts: number;
  interval_ms: number;
}

// Three layers that a single LLM configuration can override (all retries "follow the endpoint").
export interface LLMRetryOverride {
  connect: LLMRetryRule; // Connect retries: connection reset/timeout/429/5xx, before the stream starts
  empty: LLMRetryRule; // Empty-response retries: completes but returns no content (openai format only)
  stream: LLMRetryRule; // Same-provider safety-window retries: replay after a dropped connection before output is delivered
}

// Global policy = defaults for the three layers above + two global-only layers:
//   breaker polling circuit breaker (attempts=consecutive transient failures before tripping, interval_ms=fixed cooldown duration)
//   intent  intent rerun (rerun the entire intent after the worker exits with model_error)
export interface LLMRetryPolicy extends LLMRetryOverride {
  breaker: LLMRetryRule;
  intent: LLMRetryRule;
}

// ---- LLM Polling (Failover) ----
// A configuration's position in the polling chain and its health state. state:
//   ok       healthy
//   degraded consecutive failures, but below the circuit-breaker threshold
//   tripped  circuit breaker tripped; skipped during cooldown (cooldown_secs is the remaining seconds)
export interface LLMPoolMember {
  profile_id: string;
  name: string;
  model: string;
  format: string;
  priority: number;
  active: boolean; // Whether this is the currently active configuration (always the head of the chain)
  excluded: boolean; // pool_exclude: excluded from polling
  state: "ok" | "degraded" | "tripped";
  fails: number;
  trips: number;
  cooldown_secs: number;
  last_error?: string;
  last_at?: string;
}

export interface LLMPoolStatus {
  enabled: boolean;
  bind_fallback: boolean;
  chain: LLMPoolMember[];
}

// ---- Agents ----
export interface Agent {
  id: string;
  key: string; // Built-in: goals/planner/mainagent/worker; custom: user-defined key
  name: string;
  description?: string;
  role: string;
  builtin: boolean;
  enabled: boolean;
  llm_profile_id?: number | null; // Bound LLM configuration; null/absent = follow task/session/global
  max_turns?: number; // 0 = unlimited
  run_seconds?: number; // Worker single-run wall-clock limit (seconds); 0 = unlimited
  web_search?: boolean; // Whether network search is enabled (gated by the system-wide setting)
  interactive_shell?: boolean; // Whether the interactive shell is enabled (persistent PTY session tool family)
  // Post-trigger handling policy for P3 (only relevant to custom agents)
  trigger_run_mode?: "serial" | "parallel"; // Serial queueing / one concurrent session per trigger
  trigger_merge_mode?: "by_task" | "all" | "none"; // Serial only: merge within the same task / merge all / do not merge
  trigger_max_parallel?: number; // Parallel only: per-agent concurrency limit; 0=unlimited
  // Binding counts (returned only by list endpoints): visible MCP / visible Skills / bound tools
  mcp_count?: number;
  skill_count?: number;
  tool_count?: number;
}

export interface PromptVar {
  name: string;
  description: string;
  example: string;
  source: "exploration" | "runtime" | "distilled";
}

export interface PromptVersion {
  version: number;
  ts: string;
  note: string;
  template_text: string;
}

export interface AgentDetail {
  agent: Agent;
  prompt: string;
  variables: PromptVar[];
  versions: PromptVersion[];
  visibility: { mcp: number[]; skill: string[] };
  // Bindable LLM configuration candidates (for the "Default model" dropdown); see agent.llm_profile_id for the current binding
  llm_profiles?: { id: number; name: string; model: string; is_default: boolean }[];
  wrapup_prompt?: string; // Saved wrap-up prompt (empty = use built-in default)
  wrapup_default?: string; // Built-in default wrap-up prompt (placeholder/restore default)
  wrapup_max_turns?: number; // Saved wrap-up rounds (0 = use built-in default)
  wrapup_max_turns_default?: number; // Built-in default wrap-up rounds (for "0=default N" hint)
  // Task-level timeout wrap-up prompt (worker/planner only; this section is shown only when task_timeout_wrapup_supported=true)
  task_timeout_wrapup_supported?: boolean;
  task_timeout_wrapup_prompt?: string;
  task_timeout_wrapup_default?: string;
  task_timeout_wrapup_max_turns?: number;
  task_timeout_wrapup_max_turns_default?: number;
}

// ---- MCP ----
export interface MCPServer {
  id: number;
  name: string;
  transport: "stdio" | "http" | "sse";
  command?: string;
  args: string[];
  env: Record<string, string>;
  url?: string;
  enabled: boolean;
  insecure?: boolean; // http: skip TLS cert verification (self-signed servers)
  tools?: string[]; // mcp_tools_cache (names only, for the count)
}

export interface MCPTool {
  name: string;
  description: string;
}

// ---- Skills ----
// Fields align with the agentskills.io open specification.
// description covers both "what the skill does" and "when to use it".
export interface SkillItem {
  name: string; // unique key = directory name
  description?: string; // required per spec; covers what + when to use
  license?: string; // optional: SPDX identifier or free text
  compatibility?: string; // optional: environment requirements
  mcps?: string[]; // MCP server names this skill unlocks on load
  files: string[]; // files in the skill directory
  // Usage statistics (skill_usage ledger). Skills never called have calls=0 and last_used unset.
  calls: number;
  tasks: number; // Number of tasks that loaded it (chat sessions are not counted)
  usage_agents: string[]; // Agent keys that loaded it
  last_used?: string;
}

// SkillCall is one Skill() call (recent calls for a single skill).
export interface SkillCall {
  ts: string;
  agent_key: string;
  task_id: number; // 0 = non-task context (chat session)
  session_id: string;
  args_len: number;
}

// MissingSkill is a skill that was mentioned by name but does not exist—the gap between "wanted" and "available."
export interface MissingSkill {
  skill: string;
  calls: number;
  agents: string[];
  last_used?: string;
}

// ---- Tools (built-in tool catalog) ----
// key + handler live in Go; only these fields are page-editable. system tools lock
// the key and the parameter *structure* (name/type/required) — the per-param
// description/default and the agent binding are what move.
export interface Tool {
  key: string;
  system: boolean;
  description: string;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  schema: Record<string, any>; // full JSON-Schema (object with properties)
  agents: string[]; // bound agent keys
  enabled: boolean;
  kind?: "builtin" | "shell" | "command" | "script" | "http"; // Custom tool type
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  exec?: Record<string, any>; // Custom tool execution specification (kind!=builtin)
  deferred?: boolean; // Deferred schema (SearchExtraTools/ExecuteExtraTool)
  calls?: number; // persistent runtime invocation count (older APIs may omit it)
}

// ---- Stats ----
export interface Stats {
  assets: number;
  engine_mode: EngineMode;
  llm_configured: boolean;
  roe_enabled: boolean;
  findings_confirmed: number;
  active_task?: Partial<Task>;
}

export interface SystemMetricValue {
  used_bytes: number;
  total_bytes: number;
  percent: number;
}

export interface SystemMetrics {
  scope: "container" | "unsupported" | string;
  supported: boolean;
  timestamp: string;
  container_metrics: boolean;
  cpu: SystemMetricValue;
  memory: SystemMetricValue;
  disk: SystemMetricValue;
  data_disk: SystemMetricValue;
}

// ---- Intercept Rules ----
export type InterceptAction = "allow" | "deny" | "ask";
export type InterceptMatchTarget = "tool_name" | "tool_input";
export type InterceptMatchType = "string" | "regex";

export interface InterceptRule {
  id: number;
  name: string;
  enabled: boolean;
  priority: number;
  match_target: InterceptMatchTarget;
  match_type: InterceptMatchType;
  pattern: string;
  action: InterceptAction;
  message: string;
  timeout_enabled: boolean;
  timeout_seconds: number;
  timeout_action: "deny" | "allow";
  created_at: string;
  updated_at: string;
}

// ---- Asset Intercept Rules (global blocklist) ----
export type AssetInterceptKind =
  | "exact_domain"
  | "exact_ip"
  | "exact_url"
  | "fuzzy_domain"
  | "fuzzy_ip"
  | "fuzzy_url"
  | "cidr";

// action is only used for task-level rules: block = intercept (prohibit testing), allow = permit (allowlist).
export type AssetInterceptAction = "block" | "allow";

// Entry for task-level asset block/allow rules (used when creating a task and editing task details).
export interface AssetInterceptRuleInput {
  action: AssetInterceptAction;
  kind: AssetInterceptKind;
  pattern: string;
  note: string;
  enabled: boolean;
}

export interface AssetInterceptRule {
  id: number;
  enabled: boolean;
  action?: AssetInterceptAction; // Global rules omit this field (always block); task-level rules distinguish block/allow
  kind: AssetInterceptKind;
  pattern: string;
  note: string;
  builtin: boolean;
  created_at: string;
  updated_at: string;
}

export interface InterceptPending {
  decision_source?: "rule" | "model" | "manual" | "unknown" | "";
  id: number;
  rule_id?: number;
  conversation_id?: number;
  task_id?: string;
  agent_name: string;
  tool_name: string;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  tool_input: Record<string, any>;
  status: "pending" | "allowed" | "denied" | "timeout";
  reason: string; //Rule message or model judgment rationale (model judgments have the [model] prefix
  decided_at?: string;
  created_at: string;
}

// JudgeConfig: Global configuration for model fallback approval (judged by the model only when no block rules match).
export interface JudgeConfig {
  enabled: boolean;
  profile_id: number; // 0 = follow active/default configuration
  prompt: string; // Judgment prompt; if not set, GET fills in the full built-in template
  timeout_seconds: number; // Timeout for each model approval call
  retry_count: number; // Additional retries after approval failure; 0 = no retries
  retry_interval_ms: number; // Approval retry interval (milliseconds); 0 = retry immediately
  fail_action: "allow" | "ask" | "deny"; // Fallback when the model errors, times out, or cannot be parsed
  ask_timeout_seconds: number; // Approval wait timeout after the model's judgment of ask routes to human review
  ask_timeout_action: "allow" | "deny"; // Default action after approval timeout
  incomplete_script_action: "ask" | "model"; // When the script snapshot is incomplete: human review or continue model review
  confidence_threshold: number; // Route to human review when Jev confidence is below this value
}

// JudgeUsage: Cumulative token usage for model fallback approval (judge channel) + daily series for the most recent N days.
export interface JudgeDayUsage {
  date: string; // YYYY-MM-DD (UTC)
  calls: number;
  input_tokens: number;
  output_tokens: number;
}
export interface JudgeUsage {
  calls: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  daily: JudgeDayUsage[];
}

export interface InterceptApprovalFilter {
  status?: InterceptPending["status"];
  decision_source?: "rule" | "model" | "manual" | "unknown";
  q?: string;
  from?: string;
  to?: string;
}

// InterceptApprovalRow enriches InterceptPending with conversation/task and rule context.
export interface InterceptApprovalRow extends InterceptPending {
  conv_title: string; // "" if no linked conversation
  conv_agent_key: string; // "" if no linked conversation
  rule_name: string; // "" if rule was deleted
}

// ── Asset synchronization (ScopeSentry data source) ──────────────────────────────────────────────
export interface SSProject {
  id: string; // MongoDB ObjectID — used as filter.project
  name: string;
  logo?: string;
  AssetCount?: number;
  tag?: string;
}

export interface SSTask {
  id: string;
  name: string; // used as filter.task
  status?: number;
  progress?: number;
  creatTime?: string;
  endTime?: string;
}

// ConvTokenSummary — one conversation's token total (+ profile/date) for merging
// chat usage into the dashboard token stats. GET /api/tokens/conversations.
export interface ConvTokenSummary {
  llm_profile_id: number | null;
  created_at: string;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
}

// ---- Command recording (Bash execution history) ----
export interface CommandRecord {
  id: number;
  exploration_id: number;
  worker: string;
  tool: string;
  command: string; // raw tool input (JSON)
  output: string;
  is_error: boolean;
  created_at: string;
}

// Call statistics for a single tool (/commands/stats); errors is the number of failed calls.
export interface ToolStat {
  tool: string;
  total: number;
  errors: number;
}

// ---- LLM recording ----
export interface LLMRecordItem {
  id: number;
  ts: string;
  model: string;
  profile_name: string;
  session_id: string;
  task_id: string;
  worker: string;
  latency_ms: number;
  input_tokens: number;
  output_tokens: number;
  cache_read: number;
  cache_write: number;
  status: string;
  error?: string;
}

export interface LLMRecordDetail extends LLMRecordItem {
  request_body: string;
  response_body: string;
  // Raw HTTP data actually sent and received by the provider: the complete body sent by buildBody() for requests (including tools
  // schema), and the response is raw SSE frames. The request_body/response_body above are normalized views,
  // with tool schemas and tool_use blocks discarded. Old records are empty.
  raw_request?: string;
  raw_response?: string;
}

// One distinct task with its LLM-record count (task picker on the records page).
export interface LLMTask {
  task_id: string;
  count: number;
}

// The exact JSON sent to the review model, retained for all model verdicts.
export interface InterceptReviewInput {
  version: number;
  script_capture_status?: "not_applicable" | "complete" | "incomplete";
  script_files?: {
    path: string;
    source?: "file" | "command";
    sha256?: string;
    bytes?: number;
    content?: string;
    complete?: boolean;
    truncated?: boolean;
    read_error?: string;
  }[];
  background?: {
    // worker_summary is retained only for immutable v2/v3 snapshots.
    source: "user_message" | "worker_summary";
    text: string;
    truncated?: boolean;
  };
  // Version 1 snapshots are immutable and remain readable in historical audits.
  task?: {
    task_id: number;
    description: string;
    goal: string;
    constraints: { id: number; kind: string; text: string; origin: string; created_at: number }[];
    truncated?: boolean;
  };
  working_directory?: string;
  worker_intent?: string;
  turn_input?: string;
  background_truncated?: boolean;
  // Legacy v1/v2 snapshots only; v3 never sends execution history.
  history?: {
    tool_use_id: string;
    tool: string;
    arguments_preview: string;
    result: string;
    status: "succeeded" | "failed";
    truncated?: boolean;
  }[];
  history_truncated?: boolean;
  correlation?: "exact" | "ambiguous" | "unavailable";
  tool_name: string;
  arguments: Record<string, unknown>;
}

// Immutable review snapshot plus separately recorded execution outcome.
export interface InterceptAudit {
  model_input?: InterceptReviewInput;
  model_input_digest?: string;
  run_id?: string;
  tool_use_id?: string;
  correlation: "exact" | "ambiguous" | "unavailable";
  input_digest: string;
  user_message: string;
  user_truncated?: boolean;
  context:
    | { kind: string; tool?: string; tool_use_id?: string; text: string; is_error?: boolean; truncated?: boolean }[]
    | null;
  context_truncated?: boolean;
  captured_at: string;
  model_fallback?: boolean;
  initial_action: "allow" | "ask" | "deny";
  initial_reason: string;
  effective_action?: "allow" | "deny";
  decision_reason?: string;
  rule_name?: string;
  config_digest?: string;
  profile_id?: number;
  execution_status: "not_started" | "not_executed" | "awaiting_result" | "succeeded" | "failed" | "unknown";
  output?: string;
  output_truncated?: boolean;
  execution_ended_at?: string;
}
export interface InterceptDetail extends InterceptApprovalRow {
  audit: InterceptAudit | null;
}

export type TrafficEvidenceRole = "baseline" | "proof" | "verification" | "supporting";
export interface TrafficEvidenceRef {
  traffic_id: string;
  role?: TrafficEvidenceRole;
  note?: string;
}
export interface TrafficEvidenceSnapshot {
  id: string;
  source_traffic_id: string;
  captured_at: number;
  url: string;
  method: string;
  status: number;
  content_type: string;
  req_head?: string;
  resp_head?: string;
  req_hash: string;
  resp_hash: string;
  req_len: number;
  resp_len: number;
}
export interface FindingTrafficBinding {
  id: string;
  finding_id: string;
  snapshot_id: string;
  role: TrafficEvidenceRole;
  note: string;
  position: number;
  created_at: string;
  snapshot: TrafficEvidenceSnapshot;
}
export interface FindingTraffic {
  finding_id: string;
  version: number;
  report_version: number;
  bindings: FindingTrafficBinding[];
}
export interface EvidenceBodyPreview {
  content: string;
  offset: number;
  total: number;
  next_offset: number;
  truncated: boolean;
  binary: boolean;
}
export interface FindingTrafficDetail {
  binding: FindingTrafficBinding;
  request: EvidenceBodyPreview;
  response: EvidenceBodyPreview;
}

export interface RepeaterSendRequest {
  method: string;
  http_version?: string;
  url: string;
  headers?: Record<string, string>;
  body?: string;
  timeout_ms?: number;
  insecure_skip_verify?: boolean;
  use_recording_proxy?: boolean;
}

export interface RepeaterSendResponse {
  status: number;
  status_text: string;
  proto: string;
  headers: Record<string, string[]>;
  body: string;
  body_truncated: boolean;
  body_bytes: number;
  elapsed_ms: number;
  url: string;
  error?: string;
}

export interface UpdateSettings {
  proxy: string;
}

/** Comparison between the current version and the latest stable GitHub release. */
export interface UpdateCheck {
  /** The currently running version; development builds use "dev" or a suffixed form from git describe. */
  current: string;
  /** Runtime mode. In Docker, replacement only affects the container's writable layer; rebuilding the container reverts to the image version. */
  mode: "docker" | "binary";
  os: string;
  arch: string;
  repo: string;
  /** Whether a previous version (artex.old) is available for rollback. */
  has_backup: boolean;
  /** Outcome of the self-update bootstrap at startup (e.g., replacement failed / rolled back); empty if nothing happened. */
  boot_notice?: string;
  rolled_back?: boolean;
  /** Reason for a GitHub query failure; the fields below will be absent in this case. */
  error?: string;
  latest?: string;
  notes?: string;
  html_url?: string;
  published_at?: string;
  /** Release package name for the current platform, and whether the Release actually includes it. */
  asset?: string;
  asset_available?: boolean;
  size?: number;
  has_update?: boolean;
  /** Whether the two version numbers are comparable; false for development builds, which disables one-click updates. */
  comparable?: boolean;
  /** Explanation when comparable is false. */
  reason?: string;
}

/** An update progress event pushed by /api/update/stream. */
export interface UpdateProgress {
  phase: "idle" | "downloading" | "verifying" | "extracting" | "staged" | "failed";
  /** Meaningful only during the download phase (0-100); -1 for other stages. */
  percent: number;
  message: string;
  version?: string;
  error?: string;
}

// Original execution selected from an approval, never submitted to the reviewer.
export interface InterceptExecution {
  conversation_id: number | null;
  task_id: string | null;
  session: string;
  seq: number;
  items: Activity[];
}

export interface FindingDistinctStats {
  total: number;
  reports: number;
  critical: number;
  high: number;
  medium: number;
  low: number;
  unassessed: number;
}
export interface FindingCase {
  id: string;
  task_id: string | null;
  title: string;
  reason: string;
  severity: Severity | "";
  severity_reason: string;
  report?: string;
  version: number;
  report_version: number;
  active: boolean;
  count: number;
  critical: number;
  high: number;
  medium: number;
  low: number;
  created_at: string;
}
export interface FindingCaseListRow {
  task_name?: string;
  task_description?: string;
  assets?: FindingAsset[];
  asset_count?: number;
  last_found_at?: string;
  case?: FindingCase | null;
  finding?: Finding;
  matched_ids: number[];
}
export interface FindingCasePage {
  matching_reports?: number;
  items: FindingCaseListRow[];
  total: number;
  page: number;
  page_size: number;
  stats: FindingDistinctStats;
}
export interface FindingCaseEvent {
  id: string;
  action: string;
  actor: string;
  reason: string;
  finding_ids: number[];
  created_at: string;
}
export interface FindingCaseDetail {
  case: FindingCase;
  events: FindingCaseEvent[];
}
export interface FindingCaseSuggestion {
  id: string;
  task_id: string;
  left_id: string;
  right_id: string;
  finding_ids: string[];
  title: string;
  reason: string;
  state: string;
}
export interface FindingCaseReviewRun {
  conversation_id: number;
  task_id: string;
  state: "queued" | "running" | "done" | "failed";
  error: string;
}

export type IntelligenceKind = "credential" | "lead" | "sensitive_data";
export interface CredentialAccount {
  label?: string;
  username?: string;
  password?: string;
  token?: string;
  cookie?: string;
  endpoint?: string;
  protocol?: string;
}
export interface CredentialTraffic {
  id: string;
  url: string;
  method: string;
  status: number;
  side: string;
  excerpt: string;
  accounts: number[];
}
export interface CredentialAnalysis {
  state: "queued" | "processing" | "ready" | "error";
  method?: "rules" | "agent";
  accounts: CredentialAccount[] | null;
  traffic: CredentialTraffic[] | null;
  traffic_state: string;
  traffic_note?: string;
  note?: string;
  error?: string;
  model?: string;
  input_tokens: number;
  output_tokens: number;
  processed_at?: string;
}
export interface CredentialTrafficDetail {
  reference: CredentialTraffic;
  req?: string;
  resp?: string;
  unavailable: boolean;
}
export interface TaskIntelligence {
  credentials?: CredentialAnalysis;
  source_session?: string;
  id: number;
  task_id: number;
  node_id?: number;
  kind: IntelligenceKind;
  title: string;
  content: string;
  evidence: string;
  confidence: "observed" | "inferred";
  status: string;
  asset_ids: number[];
  worker: string;
  created_at: string;
  updated_at: string;
  inherited: boolean;
}
export interface IntelligencePage {
  credential_agent_enabled: boolean;
  credential_traffic_binding: boolean;
  items: TaskIntelligence[];
  total: number;
  counts: Record<IntelligenceKind, number>;
  enabled: boolean;
}

// ---- Egress proxy connectivity test (POST /api/settings/proxy/test) ----
export interface EgressProbeResult {
  ok: boolean;
  exit_ip?: string;
  via?: "direct" | "http" | "https" | "socks5";
  via_label?: string;
  latency_ms: number;
  error?: string;
  // Details for each address: distinguishes "cannot connect to proxy" from "proxy is reachable but cannot access the internet."
  probes: {
    url: string;
    ok: boolean;
    ip?: string;
    latency_ms: number;
    error?: string;
  }[];
}

// One line in the report: ① direct connection / ② via global proxy.
export interface EgressCheck {
  key: "direct" | "global";
  label: string;
  path?: string;
  skipped?: boolean;
  skip_reason?: string;
  result: EgressProbeResult;
}

export interface EgressReport {
  ok: boolean;
  capture: boolean;
  configured: string;
  // agent_proxy is the proxy address actually used by the Agent: when traffic capture is enabled, this is the captured traffic's address, not the value in the input field.
  agent_proxy: string;
  checks: EgressCheck[];
  warnings?: string[];
}
