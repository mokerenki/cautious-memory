package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
)

// jsonResult marshals v to a JSON tool result.
func jsonResult(v any) (actool.Result, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return actool.Errorf(err.Error()), nil
	}
	return actool.Text(string(b)), nil
}

// This file implements P2 “cross-task orchestration tools” (see the orchestration design document, section 2). These are host tools—they need
// access to Manager (any task's Store), Engine (for pausing), and the task creation flow, so they live in the server layer.
// Read tools redirect existing per-task tools to run against the target task's store (create a temporary ToolSet
// and Call its corresponding tool), thereby reusing exactly the same logic; control tools (spawn/pause) call Manager/Engine directly.
// They are seeded into the tools table like traffic tools and bound to agents (visible only when bound to an orchestration agent).

// hostTools is the runtime host-tool provider fed to ToolAugment: traffic tools
// (gated by capture) + cross-task orchestration tools + user-defined custom tools.
// The second return is the names of custom tools flagged `deferred` (schema
// withheld, routed via SearchExtraTools/ExecuteExtraTool). Per-agent binding still
// decides who actually sees any of them.
//
//nolint:unused // used as the hostTools provider in wireAgentAugment
func (s *Server) hostTools() ([]actool.CoreTool, map[string][]string) {
	tools := append(s.m.HostTools(), s.orchestrationTools()...)
	tools = append(tools, s.findingRetestTools()...)
	tools = append(tools, s.platformTools()...) // Platform operation tools (create/edit skills/tools/MCP, for Auto)
	custom, err := s.customTools()
	if err != nil {
		log.Printf("[custom-tool] failed to load: %v", err)
		return tools, nil
	}
	tools = append(tools, custom...)
	// deferred custom tools → name -> its bound agent keys. ToolAugment turns a
	// name into a deferred entry only for agents it's actually bound to (so we don't
	// advertise a tool the per-agent binding will drop from the callable set).
	deferred := map[string][]string{}
	rows, _ := s.m.pg.ListCustomTools()
	for _, t := range rows {
		if t.Deferred && t.Enabled {
			deferred[t.Key] = t.Agents
		}
	}
	return tools, deferred
}

// orchestrationTools returns the cross-task tool set. Bound per-agent via the
// tools table (default: no binding — opt-in for orchestration agents).
func (s *Server) orchestrationTools() []actool.CoreTool {
	return append([]actool.CoreTool{
		s.toolListTasks(),
		s.toolListLLMProfiles(),
		s.toolSpawnTask(),
		s.toolPauseTask(),
		s.toolGetTaskGraph(),
		s.toolListTaskFindings(),
		s.toolAddHint(),
		s.toolGetWorkerTrace(),
		s.toolListWorkerTraces(),
		s.toolSearchWorkerTraces(),
		s.toolGetTaskNodeDetail(),
		s.toolUpdateFindingReport(),
		s.toolGetFindingTraffic(),
		s.toolBindFindingTraffic(),
	}, s.findingCaseTools()...)
}

// --- schema helpers ---

func strParam(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

// parseProfileID reads an LLM profile id from a tool arg that may arrive as a JSON
// number (5) or a numeric string ("5"); returns 0 when absent/unparseable.
func parseProfileID(raw json.RawMessage) int64 {
	if len(raw) == 0 {
		return 0
	}
	var n int64
	if json.Unmarshal(raw, &n) == nil {
		return n
	}
	var str string
	if json.Unmarshal(raw, &str) == nil {
		v, _ := strconv.ParseInt(strings.TrimSpace(str), 10, 64)
		return v
	}
	return 0
}

func objSchema(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		req := make([]any, len(required))
		for i, r := range required {
			req[i] = r
		}
		m["required"] = req
	}
	return m
}

func roTool(name, desc string, schema map[string]any, run func(context.Context, json.RawMessage) (actool.Result, error)) actool.CoreTool {
	return actool.Build(actool.Spec{
		Name: name, Description: desc, Schema: schema,
		ReadOnly:   func(json.RawMessage) bool { return true },
		Concurrent: func(json.RawMessage) bool { return true },
		Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
			return permission.Allowed()
		},
		Run: func(ctx context.Context, in json.RawMessage, _ *actool.ToolContext) (actool.Result, error) {
			return run(ctx, in)
		},
	})
}

func wrTool(name, desc string, schema map[string]any, run func(context.Context, json.RawMessage) (actool.Result, error)) actool.CoreTool {
	return actool.Build(actool.Spec{
		Name: name, Description: desc, Schema: schema,
		Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
			return permission.Allowed()
		},
		Run: func(ctx context.Context, in json.RawMessage, _ *actool.ToolContext) (actool.Result, error) {
			return run(ctx, in)
		},
	})
}

// delegateToTask resolves the `task_id` in the input, builds a ToolSet bound to
// that task's store, strips task_id, and calls the chosen per-task tool — so the
// cross-task read reuses the exact in-task logic against another task.
func (s *Server) delegateToTask(ctx context.Context, in json.RawMessage, pick func(*agent.ToolSet) actool.CoreTool) (actool.Result, error) {
	var head struct {
		TaskID string `json:"task_id"`
	}
	_ = json.Unmarshal(in, &head)
	if strings.TrimSpace(head.TaskID) == "" {
		return actool.Errorf("task_id is required"), nil
	}
	t, ok := s.m.Task(head.TaskID)
	if !ok {
		return actool.Errorf("Task not found: " + head.TaskID), nil
	}
	var m map[string]json.RawMessage
	_ = json.Unmarshal(in, &m)
	delete(m, "task_id")
	inner, _ := json.Marshal(m)
	tsx := agent.NewToolSet(t.Store, "orchestrator")
	if s.m.Assets() != nil {
		tsx.SetAssetStore(s.m.Assets(), s.m.Assets().Companies())
	}
	tsx.SetNotify(t.Notify)         // Generic wake-up (write operations without a dedicated callback use this; read tools are no-ops)
	tsx.SetNotifyHint(t.NotifyHint) // add_hint → record “A person added N strategic hints:…” and trigger/wake the planner
	return pick(tsx).Call(ctx, inner, nil)
}

// --- tools ---

func (s *Server) toolListTasks() actool.CoreTool {
	return roTool("list_tasks",
		"List all tasks (id/description/goal/status/runtime/parent task/LLM config). Orchestration agents use it for global awareness, to see which tasks are stuck too long, and which LLM each uses. Runtime counts only actual execution time; paused, queued, and terminal time is excluded. llm_profile: config name used by task planner/worker; (active config)=follow global active config.",
		objSchema(map[string]any{}),
		func(context.Context, json.RawMessage) (actool.Result, error) {
			// id -> name to resolve each task's pinned LLM profile.
			profName := map[int64]string{}
			if profs, err := s.m.pg.ListProfiles(); err == nil {
				for _, p := range profs {
					profName[p.ID] = p.Name
				}
			}
			out := make([]map[string]any, 0)
			for _, t := range s.m.List() {
				status := s.deriveTaskStatus(t)
				lifecycle := t.lifecycleSnapshot()
				dur := taskRunSeconds(lifecycle.ActiveRunSeconds, lifecycle.RunSegmentStartedAt, time.Now().Unix(), status, lifecycle.Paused, lifecycle.Queued)
				row := map[string]any{"id": t.ID, "description": t.Description, "goal": t.Goal, "status": status, "run_seconds": dur}
				if t.ParentRef != "" {
					row["parent_ref"] = t.ParentRef
				}
				llmState := t.llmStateSnapshot()
				if llmState.ProfileID == nil {
					row["llm_profile"] = "(active config)"
				} else if n, ok := profName[*llmState.ProfileID]; ok {
					row["llm_profile"] = n
				} else {
					row["llm_profile"] = fmt.Sprintf("#%d(deleted)", *llmState.ProfileID)
				}
				out = append(out, row)
			}
			return jsonResult(out)
		})
}

// toolListLLMProfiles lists the available LLM profiles (name/model/active) so an
// orchestration agent can pick one for spawn_task's llm_profile. Never leaks keys.
func (s *Server) toolListLLMProfiles() actool.CoreTool {
	return roTool("list_llm_profiles",
		"List available LLM profiles: id, name, model, format, and whether it is the current active config. Use id with spawn_task llm_profile_id to assign a task-specific LLM (e.g. cheaper model for recon, stronger for exploitation). API keys are not included.",
		objSchema(map[string]any{}),
		func(context.Context, json.RawMessage) (actool.Result, error) {
			profs, err := s.m.pg.ListProfiles()
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			out := make([]map[string]any, 0, len(profs))
			for _, p := range profs {
				out = append(out, map[string]any{
					"id": p.ID, "name": p.Name, "model": p.Model, "format": p.Format, "is_active": p.IsDefault,
				})
			}
			return jsonResult(map[string]any{"profiles": out})
		})
}

func (s *Server) toolSpawnTask() actool.CoreTool {
	return wrTool("spawn_task",
		"Create a new child task and start the exploration engine, returning task_id. Use it to dispatch work (e.g. a challenge/target) as an independent task. parent_ref is optional: provide the parent task id from current orchestration to establish a parent-child relationship.",
		objSchema(map[string]any{
			"description":            strParam("Task description (short title)"),
			"goal":                   strParam("Task goal (what to achieve)"),
			"parent_ref":             strParam("Optional: parent task id (for parent-child relationship)"),
			"source_task_ids":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": fmt.Sprintf("Optional: read-only inherited source task ID list (max %d). Subtasks can read-only reference assets/conclusions already explored by these tasks as a starting point; unlike parent_ref's pure parent-child pointer, this is content inheritance.", db.MaxTaskSourceCount)},
			"llm_profile_id":         map[string]any{"type": "integer", "description": "Optional: LLM profile id for this child task planner/worker (see list_llm_profiles); leave empty to inherit parent task, then fall back to global active config"},
			"timeout_seconds":        map[string]any{"type": "integer", "description": "Optional: task-level timeout (seconds). On expiry triggers graceful shutdown and enters timeout terminal state; leave empty or 0 = no limit"},
			"plan_heartbeat_seconds": map[string]any{"type": "integer", "description": "Optional: planner heartbeat trigger interval (seconds). If this duration passes since last planning end/task start with no trigger, run a planning round (deadlock fallback + wake to supervise in-flight workers). Leave empty or 0 = default 600 (10min)."},
			"seed_first_intent":      map[string]any{"type": "boolean", "description": "Optional: for simple tasks, enable to dispatch a seed intent at creation (content = description + goal) so workers can start without waiting for first planner round; default false (standard plan-then-execute)."},
		}, "description", "goal"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Description          string          `json:"description"`
				Goal                 string          `json:"goal"`
				ParentRef            string          `json:"parent_ref"`
				SourceTaskIDs        []string        `json:"source_task_ids"`
				LLMProfileID         json.RawMessage `json:"llm_profile_id"`
				TimeoutSeconds       int             `json:"timeout_seconds"`
				PlanHeartbeatSeconds int             `json:"plan_heartbeat_seconds"`
				SeedFirstIntent      bool            `json:"seed_first_intent"`
			}
			_ = json.Unmarshal(in, &a)
			if strings.TrimSpace(a.Description) == "" {
				a.Description = "Unnamed task"
			}
			if strings.TrimSpace(a.Goal) == "" {
				return actool.Errorf("goal is required"), nil
			}
			if a.TimeoutSeconds < 0 {
				a.TimeoutSeconds = 0
			}
			// Read-only inherited source tasks: enforce a count limit and validate each id for validity, uniqueness, and existence, using the same rules as HTTP task creation.
			if len(a.SourceTaskIDs) > db.MaxTaskSourceCount {
				return actool.Errorf(fmt.Sprintf("Associated tasks max %d", db.MaxTaskSourceCount)), nil
			}
			sourceIDs := make([]int64, 0, len(a.SourceTaskIDs))
			seenSources := map[int64]bool{}
			for _, raw := range a.SourceTaskIDs {
				id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
				if err != nil || id <= 0 || seenSources[id] {
					return actool.Errorf("Related task ID is invalid or duplicated"), nil
				}
				if _, ok := s.m.Task(strconv.FormatInt(id, 10)); !ok {
					return actool.Errorf(fmt.Sprintf("Related task #%d does not exist", id)), nil
				}
				seenSources[id] = true
				sourceIDs = append(sourceIDs, id)
			}
			// LLM profile resolution: explicit id > inherit parent's pin > active(nil).
			var pin *int64
			if id := parseProfileID(a.LLMProfileID); id > 0 {
				if _, ok := s.loadProfileConfig(id); !ok {
					return actool.Errorf(fmt.Sprintf("LLM config #%d does not exist or API Key not set", id)), nil
				}
				pin = &id
			} else if a.ParentRef != "" {
				if pt, ok := s.m.Task(a.ParentRef); ok {
					pin = pt.LLMProfileID
				}
			}
			var llmIDs []int64
			if pin != nil {
				llmIDs = []int64{*pin}
			}
			name := ""
			autoTitleEnabled := s.m.pg.GetBool(db.SettingTaskAutoTitle, false)
			if autoTitleEnabled {
				if err := ctx.Err(); err != nil {
					return actool.Errorf(err.Error()), nil
				}
				active, activeErr := s.m.pg.ActiveProfile()
				if activeErr != nil {
					log.Printf("[auto-title] active profile lookup failed: %v", activeErr)
				} else if active == nil {
					log.Printf("[auto-title] no active profile")
				} else if prov, _, providerOK := s.providerForProfile(active.ID); providerOK {
					titleCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
					defer cancel()
					generated, titleErr := agent.GenerateAutoTitle(titleCtx, prov, a.Description+"\n"+a.Goal)
					if err := ctx.Err(); err != nil {
						return actool.Errorf(err.Error()), nil
					}
					if titleErr == nil {
						name = generated
					} else {
						log.Printf("[auto-title] task title generation failed: %v", titleErr)
					}
				} else {
					log.Printf("[auto-title] task provider unavailable for active profile %d", active.ID)
				}
			}
			if autoTitleEnabled {
				if err := ctx.Err(); err != nil {
					return actool.Errorf(err.Error()), nil
				}
			}
			t, err := s.m.CreateTaskWithOptions(a.Description, a.Goal, db.TaskCreateOptions{
				Name:                 name,
				SourceTaskIDs:        sourceIDs,
				LLMProfileIDs:        llmIDs,
				TimeoutSeconds:       a.TimeoutSeconds,
				PlanHeartbeatSeconds: a.PlanHeartbeatSeconds,
			})
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if a.ParentRef != "" {
				t.ParentRef = a.ParentRef
				if id, e := strconv.ParseInt(t.ID, 10, 64); e == nil {
					_ = s.m.PG().SetParentRef(id, a.ParentRef)
				}
			}
			// Shared post-creation flow, reusing the same launchTask section as HTTP task creation (server.go createTask):
			// seed + decompose targets in the background (round 0/LLM step/each goal) + engine.Run.
			// seed_first_intent defaults to false (standard flow plans before execution); simple tasks can enable it to dispatch one work item directly for testing.
			s.launchTask(t, a.Description+" "+a.Goal, a.SeedFirstIntent)
			return actool.Text(fmt.Sprintf("task created: %s", t.ID)), nil
		})
}

func (s *Server) toolPauseTask() actool.CoreTool {
	return wrTool("pause_task", "Pause the specified task (stop its planner/worker loops).",
		objSchema(map[string]any{"task_id": strParam("Task id to pause")}, "task_id"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				TaskID string `json:"task_id"`
			}
			_ = json.Unmarshal(in, &a)
			t, ok := s.m.Task(a.TaskID)
			if !ok {
				return actool.Errorf("Task not found: " + a.TaskID), nil
			}
			if _, err := s.applyTaskControlWithCause(t, "pause", agent.AbortPausedByOrchestrator); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return actool.Text("task paused: " + a.TaskID), nil
		})
}

func (s *Server) toolGetTaskGraph() actool.CoreTool {
	return roTool("get_task_graph", "Read exploration graph overview for a task (same as graph_overview: asset counts/frontier/findings/coverage, etc.), specified by task_id.",
		objSchema(map[string]any{"task_id": strParam("Task id")}, "task_id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			return s.delegateToTask(ctx, in, (*agent.ToolSet).GraphOverviewTool)
		})
}

func (s *Server) toolListTaskFindings() actool.CoreTool {
	return roTool("list_task_findings", "Read confirmed findings for a task (including flag/PoC; each with id/task_id/intent_id/vulnclass/severity/summary/status), specified by task_id.",
		objSchema(map[string]any{"task_id": strParam("Task id")}, "task_id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			return s.delegateToTask(ctx, in, (*agent.ToolSet).ListFindingsTool)
		})
}

func (s *Server) toolAddHint() actool.CoreTool {
	return wrTool("add_task_hint", "Inject strategic hints into a task (read by that task planner on next intent generation).\n"+
		"* Prefer batch: submit multiple hints in the hints array at once (returns ids array same length/order as hints, failed items id=0); for a single hint omit hints and provide top-level text.",
		objSchema(map[string]any{
			"task_id":      strParam("Task id"),
			"hints":        map[string]any{"type": "array", "description": "[Preferred] Hint array; each element has the same fields as top-level (text/asset_ids/traffic_refs).", "items": objSchema(map[string]any{"text": strParam("Hint content"), "asset_ids": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}}, "traffic_refs": agent.HintTrafficSchema()})},
			"text":         strParam("[Single] Hint content"),
			"traffic_refs": agent.HintTrafficSchema(),
			"asset_ids":    map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "Anchored asset ids (optional, 0/1/many; asset ids within this task)"},
		}, "task_id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			return s.delegateToTask(ctx, in, (*agent.ToolSet).AddHintTool)
		})
}

func (s *Server) toolGetWorkerTrace() actool.CoreTool {
	return roTool("get_task_worker_trace",
		"Inspect execution of a work item (intent) in a task: get_task_worker_trace(task_id, intent_id) for step summaries; then pass step_ids=[...] to fetch full content for those steps (max 5 at once; extra ids are omitted).",
		objSchema(map[string]any{
			"task_id":   strParam("Task id"),
			"intent_id": map[string]any{"type": "integer", "description": "Intent id (work item in this task)"},
			"step_ids":  map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "Optional: step ids to fetch full content for (max 5 at once; extras listed in omitted_step_ids)"},
		}, "task_id", "intent_id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			return s.delegateToTask(ctx, in, (*agent.ToolSet).GetWorkerTraceTool)
		})
}

func (s *Server) toolListWorkerTraces() actool.CoreTool {
	return roTool("list_task_worker_traces", "List executed work items (intents) in a task and their step counts, to find which work is worth inspecting (then use get_task_worker_trace).",
		objSchema(map[string]any{"task_id": strParam("Task id")}, "task_id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			return s.delegateToTask(ctx, in, (*agent.ToolSet).ListWorkerTracesTool)
		})
}

func (s *Server) toolSearchWorkerTraces() actool.CoreTool {
	return roTool("search_task_worker_traces", "Search all work execution traces in a task by keyword (returns matching step summaries + intent_id).",
		objSchema(map[string]any{"task_id": strParam("Task id"), "q": strParam("Search keyword")}, "task_id", "q"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			return s.delegateToTask(ctx, in, (*agent.ToolSet).SearchWorkerTracesTool)
		})
}

func (s *Server) toolGetTaskNodeDetail() actool.CoreTool {
	return roTool("get_task_node_detail",
		"Read full content of an exploration graph node in a task (finding/fact/intent/goal: summary + details/evidence/PoC). id is the exploration node id (e.g. returned by report_finding, or id in list_task_findings). Use it to fetch complete evidence before writing a vulnerability report.",
		objSchema(map[string]any{
			"task_id": strParam("Task id"),
			"id":      map[string]any{"type": "integer", "description": "Exploration graph node id (not an asset id)"},
		}, "task_id", "id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			return s.delegateToTask(ctx, in, (*agent.ToolSet).NodeDetailTool)
		})
}

// toolUpdateFindingReport writes/overwrites a finding's detailed Markdown report.
// finding_id is the id report_finding returned ("finding recorded: <id>", the
// finding node id). The write (SetFindingReportByNodeID) is keyed by node_id and
// task-agnostic, so this host tool needs no task_id / exploration store.
func (s *Server) toolUpdateFindingReport() actool.CoreTool {
	return wrTool("update_finding_report",
		"Write/update the [detailed report] for a recorded finding (full Markdown, replaces old content). finding_id is the id returned by report_finding (the number in \"finding recorded: <id>\"). Suggested sections: overview, impact, reproduction steps, evidence/PoC, remediation.",
		objSchema(map[string]any{
			"finding_id":       map[string]any{"type": "integer", "description": "Target finding id (id returned by report_finding)"},
			"report":           strParam("Full detailed report in Markdown"),
			"evidence_version": map[string]any{"type": "integer", "description": "Evidence version from get_finding_traffic; prevents report from overwriting newer evidence changes"},
		}, "finding_id", "report"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				EvidenceVersion *int64          `json:"evidence_version"`
				FindingID       json.RawMessage `json:"finding_id"`
				Report          string          `json:"report"`
			}
			_ = json.Unmarshal(in, &a)
			nodeID := parseProfileID(a.FindingID) // Reuse parsing for “numbers or numeric strings”
			if nodeID <= 0 {
				return actool.Errorf("finding_id is invalid"), nil
			}
			fid, err := s.m.pg.FindingIDByNodeID(nodeID)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if fid > 0 {
				if err := s.checkCaseReviewScope(ctx, "update_finding_report", findingCaseRequest{FindingID: json.RawMessage(i64s(fid))}); err != nil {
					return actool.Errorf(err.Error()), nil
				}
				if f, err := s.m.pg.GetFinding(fid); err == nil && f != nil && f.TaskID != nil {
					if err := s.archiveFindingScreenshots(*f.TaskID, a.Report); err != nil {
						return actool.Errorf("Failed to archive report screenshot: " + err.Error()), nil
					}
				}
				if conversationID := intercept.ConvIDFromContext(ctx); conversationID > 0 {
					if err := s.m.pg.SetFindingReportConversationID(fid, conversationID); err != nil {
						return actool.Errorf("Failed to link report conversation: " + err.Error()), nil
					}
				}
			}
			n, err := s.m.pg.SetFindingReportVersionByNodeID(ctx, nodeID, a.Report, a.EvidenceVersion)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if n == 0 {
				return actool.Errorf(fmt.Sprintf("finding_id=%d vulnerability record not found (register with report_finding first)", nodeID)), nil
			}
			return actool.Text(fmt.Sprintf("finding %d report updated (%d chars)", nodeID, len(a.Report))), nil
		})
}

// deriveTaskStatus mirrors listTasks' status derivation for the list_tasks tool.
func (s *Server) deriveTaskStatus(t *Task) string {
	lifecycle := t.lifecycleSnapshot()
	switch {
	case isTerminalStatus(lifecycle.Status):
		return lifecycle.Status
	case lifecycle.Paused || s.engine.IsPaused(t.ID):
		return "paused"
	case s.engine.ReadyFor(t) && s.engine.Started(t.ID):
		return "running"
	}
	return "created"
}

// orchestrationToolSeeds seeds the cross-task tools into the tools table so they
// are bindable per-agent (default: bound to nobody — opt-in for orchestration
// agents). First-insert only, like the traffic seeds.
func (s *Server) seedOrchestrationTools() {
	// task-op + platform tools default-bind to the built-in Auto agent (it is inherently intended for
	// operating the platform). SeedTool takes effect on first insertion; seedAutoDefaultBindings adds bindings for rows already seeded in existing databases.
	autoAgents, _ := json.Marshal([]string{"auto"})
	for _, t := range s.orchestrationTools() {
		schema, _ := json.Marshal(t.InputSchema())
		bindings := autoAgents
		if t.Name() == "bind_finding_traffic" || strings.Contains(t.Name(), "finding_case") || t.Name() == "search_finding_duplicates" || t.Name() == "get_finding_record" || t.Name() == "merge_finding_records" || t.Name() == "suggest_finding_merge" {
			bindings = json.RawMessage(`["reporter"]`)
		}
		_ = s.m.PG().SeedTool(t.Name(), t.Description(), schema, bindings)
	}
	for _, t := range s.platformTools() {
		schema, _ := json.Marshal(t.InputSchema())
		_ = s.m.PG().SeedTool(t.Name(), t.Description(), schema, autoAgents)
	}
	s.refreshBuiltinToolSchemas()
	s.seedAutoDefaultBindings()
	s.seedPlannerDefaultBindings()
	s.seedPlannerListAssetsBinding()
	s.seedCompanyScopeRebind()
	s.seedWorkerReadToolsUnbind() // Unbind list_facts/list_companies/list_worker_traces from worker by default (one-time)
	s.seedWorkerReadbackRebind()  // Fix accidental deletion in an old migration: bind search_all_worker_traces/get_worker_trace/node_detail back to worker (one-time)
	s.seedAutoReportFindingBinding()
	s.unbindGoalMetDefault()
	s.reseedGoalsPrompt()             // Add an “extract operational constraints” step to the goals prompt → append a new default to old databases (one-time)
	s.reseedMainAgentPrompt()         // Add “after a goal is achieved, use add_intent to ask whether to create a goal” to the mainagent prompt (one-time)
	s.reseedPlannerPrompt()           // planner prompt: rewrite the valid reasons for “0 intents” + add a quantitative acceptance check (one-time)
	s.reseedWorkerPrompt()            // worker prompt: add an evidence threshold for negative conclusions (one-time)
	s.seedReporterAgent()             // Preinstall the report-writing agent + tool bindings + finding trigger (one-time)
	s.upgradeReporterTriggerMessage() // Migrate older databases: have reporter return evidence_version (one-time)
	s.migrateReporterWorkflow()       // Fix stale reproduction tool/prompt bindings in older databases
	if s.m.pg.GetBool(settingBrowserScreenshot, false) {
		// Re-apply the opt-in after startup seeding so an existing database gets
		// both reporter visibility and the repaired Playwright launch arguments.
		if err := s.setReporterBrowserScreenshotVisibility(true); err != nil {
			log.Printf("[reporter] failed to sync screenshot MCP startup configuration: %v", err)
		}
	}
	s.seedFindingTrafficTools() // Add optional evidence parameters and a read-only evidence tool; preserve user configuration
	s.seedFindingWorkflowTools()
	s.seedFindingCaseTools()
	// Note: pentest's default tool bindings require no migration—BuiltinToolSeeds already ensures that on fresh initialization
	// list_assets/insert_assets/report_finding/list_findings/list_companies along with
	// pentest have been seeded (the project has no legacy database, so no migration is needed).
}

// migrateReporterWorkflow repairs databases created by the short-lived
// structured-reproduction reporter. That workflow referenced
// update_finding_reproduction, which is no longer a runtime tool; leaving the
// old prompt/trigger in place makes a newly reported vulnerability fail after
// all evidence has already been collected. User-authored reporter prompts are
// preserved unless they contain the removed tool contract.
func (s *Server) migrateReporterWorkflow() {
	const flag = "reporter_workflow_markdown_v2"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	defer func() { _ = s.m.pg.SetSetting(flag, "true") }()

	reporter, err := s.m.pg.GetAgentByKey("reporter")
	if err != nil || reporter == nil {
		return
	}
	if current, err := s.m.pg.CurrentPrompt(reporter.ID); err == nil &&
		(strings.Contains(current, "update_finding_reproduction") || strings.Contains(current, "vulnerability step authoring agent")) {
		if _, err := s.m.pg.ResetPromptToDefault(reporter.ID, agent.ReporterDefaultPrompt); err != nil {
			log.Printf("[reporter] failed to migrate legacy prompt: %v", err)
		} else {
			log.Printf("[reporter] Migrated legacy reproduction-steps prompt to the Markdown report flow")
		}
	}

	// Only add the bindings required by the current report workflow; don't overwrite other existing admin configurations.
	_ = s.m.pg.AddAgentToToolBinding("reporter", []string{
		"update_finding_report", "get_finding_traffic", "bind_finding_traffic",
		"get_task_node_detail", "list_task_findings", "get_task_worker_trace",
		"list_task_worker_traces", "search_task_worker_traces", "get_task_graph",
	})
	// Keep old rows in the tool directory for historical audits, but don't advertise or call them at runtime.
	if _, err := s.m.pg.Exec(`UPDATE tools SET enabled=false, updated_at=now() WHERE key='update_finding_reproduction' AND system`); err != nil {
		log.Printf("[reporter] failed to disable legacy reproduction tool: %v", err)
	}

	triggers, err := s.m.pg.ListTriggersFor("reporter")
	if err != nil {
		log.Printf("[reporter] failed to read trigger: %v", err)
		return
	}
	for _, t := range triggers {
		if !t.OnToolCall || !strings.Contains(t.ToolCallMessage, "update_finding_reproduction") {
			continue
		}
		t.ToolCallMessage = reporterToolCallMessage
		if err := s.m.pg.UpdateTrigger(t); err != nil {
			log.Printf("[reporter] failed to migrate legacy trigger: %v", err)
			continue
		}
		log.Printf("[reporter] Migrated legacy reproduction trigger to the report-saving flow")
	}
}

// refreshBuiltinToolSchemas propagates code schema/description changes on the
// orchestration + platform tools into already-seeded rows ONCE per version flag —
// SeedTool is first-insert-only, so a new parameter (e.g. spawn_task’s llm_profile) never
// reaches an old DB otherwise. Preserves each tool's agent binding + enabled flag.
// Bump the flag whenever these tools' schemas/descriptions change in code.
func (s *Server) refreshBuiltinToolSchemas() {
	const flag = "tool_schema_refresh_v8_finding_case_tools"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	tools := append(s.orchestrationTools(), s.platformTools()...)
	for _, t := range tools {
		schema, _ := json.Marshal(t.InputSchema())
		if err := s.m.pg.RefreshToolDefaults(t.Name(), t.Description(), schema); err != nil {
			log.Printf("[tools] refresh %s schema failed: %v", t.Name(), err)
		}
	}
	// Also reset some built-in agent tools to their code defaults:
	//   - goal_met: the seeded description in old databases misleadingly said “end this planning round,” which could make the planner treat it as a way to
	//     “end an empty round” and mistakenly mark the entire task complete right after it starts.
	//   - insert_assets: add the related argument (marks whether an asset is related to the current task and determines whether it counts toward coverage),
	//     SeedTool inserts only on first seed; otherwise, schemas already seeded in existing databases won't receive this new parameter.
	//   - list_facts: switch to pagination and add limit/before/q arguments; otherwise, empty schemas already seeded in existing databases
	//     will show "No parameters" on the tool management page, and the model won't get descriptions of these parameters.
	refreshBuiltin := map[string]bool{"goal_met": true, "insert_assets": true, "list_facts": true}
	for _, sd := range agent.BuiltinToolSeeds() {
		if !refreshBuiltin[sd.Key] {
			continue
		}
		schema, _ := json.Marshal(sd.Schema)
		if err := s.m.pg.RefreshToolDefaults(sd.Key, sd.Desc, schema); err != nil {
			log.Printf("[tools] refresh %s desc failed: %v", sd.Key, err)
		}
	}
	_ = s.m.pg.SetSetting(flag, "true")
	log.Printf("[tools] Refreshed orchestration/platform tool schema to code defaults (one-time)")
}

// unbindGoalMetDefault removes goal_met's default "planner" binding ONCE (guarded by
// a settings flag), so existing DBs match the new default of NO agent. goal_met bypasses
// per-goal prove_goal to declare the whole task done — powerful/risky and redundant with
// the prove_goal→auto-complete path — so it ships unbound; users can re-bind it per agent
// in the UI. A user's own binding to another agent is untouched (we only strip planner).
func (s *Server) unbindGoalMetDefault() {
	const flag = "goal_met_unbind_default_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	if err := s.m.pg.RemoveAgentFromTool("planner", "goal_met"); err != nil {
		log.Printf("[tools] failed to unbind goal_met from planner: %v", err)
		return
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

// reseedGoalsPrompt refreshes the goals decomposer prompt to 【current code default】—because the default text now adds
// the step 「extract operational constraints (set_constraints) first, then decompose goals」, and SeedPromptIfEmpty only inserts on first seed, existing
// version 1 won't get this step. Here, use version management to 【append a new version】 and switch to it (ResetPromptToDefault),
// old versions remain in history, so users who customized the prompt can restore it from the version history. settings flag guard → only once;
// If the default changes again later, bump this flag. New databases need no handling (SeedPromptIfEmpty has seeded the latest default).
func (s *Server) reseedGoalsPrompt() {
	const flag = "goals_prompt_constraint_step_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	defer func() { _ = s.m.pg.SetSetting(flag, "true") }() // Attempt only once, whether it succeeds or fails
	a, err := s.m.pg.GetAgentByKey("goals")
	if err != nil || a == nil {
		return // For a fresh database with no agent rows yet, seedPrompts seeds the latest defaults directly, so this migration is unnecessary
	}
	tmpl := agent.BuiltinPromptSeeds()["goals"]
	if tmpl == "" {
		return
	}
	// New databases: seedPrompts has seeded the latest default → the current version already matches the code default, so there is no need to append a duplicate version.
	if cur, err := s.m.pg.CurrentPrompt(a.ID); err == nil && cur == tmpl {
		return
	}
	if _, err := s.m.pg.ResetPromptToDefault(a.ID, tmpl); err != nil {
		log.Printf("[prompts] failed to refresh goals prompt to new default: %v", err)
		return
	}
	log.Printf("[prompts] Appended new default version of goals prompt (added an operation-constraint extraction step, one-time)")
}

// reseedMainAgentPrompt refreshes the mainagent prompt to 【the current code default】—the default body now includes the guidance 「after all
// goals are achieved, when directly submitting an intent with add_intent, ask the person whether to register it as an official goal」. SeedPromptIfEmpty inserts
// only on first insert, so existing versions in old databases won't receive it. Use version management to 【append a new version】 and switch to it (ResetPromptToDefault); old versions are still
// kept in history, so users who customized one can recover it from the version history. settings flag guard → run only once. New databases need no handling
// (SeedPromptIfEmpty has seeded the latest default). Exactly parallel to reseedGoalsPrompt.
func (s *Server) reseedMainAgentPrompt() {
	const flag = "mainagent_prompt_goalless_intent_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	defer func() { _ = s.m.pg.SetSetting(flag, "true") }() // Attempt only once, whether it succeeds or fails
	a, err := s.m.pg.GetAgentByKey("mainagent")
	if err != nil || a == nil {
		return // For a brand-new database with no agent row yet, seedPrompts will seed the latest defaults directly; no migration is needed
	}
	tmpl := agent.BuiltinPromptSeeds()["mainagent"]
	if tmpl == "" {
		return
	}
	// New databases: seedPrompts has seeded the latest default → the current version already matches the code default, so there is no need to append a duplicate version.
	if cur, err := s.m.pg.CurrentPrompt(a.ID); err == nil && cur == tmpl {
		return
	}
	if _, err := s.m.pg.ResetPromptToDefault(a.ID, tmpl); err != nil {
		log.Printf("[prompts] failed to refresh mainagent prompt to new default: %v", err)
		return
	}
	log.Printf("[prompts] Appended new default version of mainagent prompt (ask about creating goals after goals are achieved, one-time)")
}

// reseedPlannerPrompt updates the planner prompt to the 【current code default】—the default body has been streamlined and refactored, and 「restraint」 has been downgraded to
// deduplication only; adds 「depth over coverage」 and 「hard rule: if the goal is not achieved and no intent is running, one must be produced」; adds an upper bound to reviews of negative conclusions.
// Whenever the default changes materially, bump the flag below (currently v2) to reseed existing databases once. SeedPromptIfEmpty only inserts initially; existing databases won't receive updates, so use version management
// 【append a new version】and switch to it (ResetPromptToDefault); old versions remain in history, so users who customized theirs can retrieve them from the version history
// settings flag guard → run only once. No action is needed for a fresh database (SeedPromptIfEmpty has already seeded the latest default). Same structure as
// reseedGoalsPrompt.
func (s *Server) reseedPlannerPrompt() {
	const flag = "planner_prompt_compact_realistic_v2"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	defer func() { _ = s.m.pg.SetSetting(flag, "true") }() // Try only once, whether it succeeds or not
	a, err := s.m.pg.GetAgentByKey("planner")
	if err != nil || a == nil {
		return // For a brand-new database with no agent row yet, seedPrompts will seed the latest defaults directly; no migration is needed
	}
	tmpl := agent.BuiltinPromptSeeds()["planner"]
	if tmpl == "" {
		return
	}
	// A fresh database has already seeded the latest default via seedPrompts → the current version already matches the code default, so there is no need to append a duplicate version.
	if cur, err := s.m.pg.CurrentPrompt(a.ID); err == nil && cur == tmpl {
		return
	}
	if _, err := s.m.pg.ResetPromptToDefault(a.ID, tmpl); err != nil {
		log.Printf("[prompts] failed to refresh planner prompt to new default: %v", err)
		return
	}
	log.Printf("[prompts] Appended new default version of planner prompt (streamlined refactor + restrained fallback deduplication + depth-first + upper bound for negative review, one-time)")
}

// reseedWorkerPrompt updates the worker prompt to the 【current code default】—the default body’s record_fact section has removed 「negative conclusions
// Write the entire 「observations + exploratory reading」 sentence, and decouple confidence(observed/inferred) from 「whether the means for this intent have been exhausted」 (these can mislead planners),
// At the same time, tighten the facts array into a very small number of exceptions that are 「completely independent of each other and cannot be consolidated」. Bump flag to v3 to refresh existing old databases once more.
// SeedPromptIfEmpty only inserts on first use; existing databases won't receive it, so use version management to 【append a new version】 and switch to it; the old version remains in history and can be retrieved.
// settings flag guard → run only once. No action is needed for a brand-new database. Exactly parallel to reseedGoalsPrompt.
func (s *Server) reseedWorkerPrompt() {
	const flag = "worker_prompt_compact_v4"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	defer func() { _ = s.m.pg.SetSetting(flag, "true") }() // Try only once, whether it succeeds or not
	a, err := s.m.pg.GetAgentByKey("worker")
	if err != nil || a == nil {
		return // For a brand-new database with no agent row yet, seedPrompts will seed the latest defaults directly; no migration is needed
	}
	tmpl := agent.BuiltinPromptSeeds()["worker"]
	if tmpl == "" {
		return
	}
	// For a brand-new database, seedPrompts has already seeded the latest default → the current version already matches the code default, so there's no need to append a duplicate version.
	if cur, err := s.m.pg.CurrentPrompt(a.ID); err == nil && cur == tmpl {
		return
	}
	if _, err := s.m.pg.ResetPromptToDefault(a.ID, tmpl); err != nil {
		log.Printf("[prompts] failed to refresh worker prompt to new default: %v", err)
		return
	}
	log.Printf("[prompts] Appended new default version of worker prompt (converged context lookup to list_assets/list_findings, removed list_facts/node_detail/asset_neighbors, one-time)")
}

// reporterToolCallMessage must unconditionally require calling get_finding_traffic once before writing the report.
// This tool is read-only and 「does not depend on the capture switch」; it can read manually bound evidence regardless of whether automatic binding is on or off. If this is
// written as 「read only when automatic binding is enabled」, reporter won't pass evidence_version with the default-off configuration,
// SetFindingReportVersionByNodeID then writes -1 with legacy semantics, leaving the vulnerability details and Markdown export
// permanently stuck at “evidence changed, report needs updating,” with no UI entry point to clear it.
const reporterToolCallMessage = "A vulnerability was just recorded by report_finding. Read finding_id (independent finding record ID) and finding_node_id (exploration node ID) from the returned JSON, " +
	"first call get_finding_traffic(finding_id) to read the current evidence list and its version (an empty list is normal; still write the report); " +
	"if run guidance enables auto-binding, verify and associate traffic for this finding before reading. Use finding_node_id for node details. " +
	"Finally call update_finding_report(finding_id=finding_node_id, report, evidence_version=actual read version) to save. " +
	"evidence_version is required, otherwise the report will be permanently marked for update. Do not mix the two IDs."

// Legacy trigger message (0.3.8 and earlier). Only records still exactly matching it are overwritten during migration; user-edited ones remain unchanged.
const reporterToolCallMessageV1 = "A vulnerability was just recorded by report_finding. Extract finding_id from the trigger context " +
	"(the number in tool result \"finding recorded: <id>\") and the task id, write the detailed report for this finding per your role, " +
	"then call update_finding_report(finding_id, report) to save."

// upgradeReporterTriggerMessage updates reporter trigger messages in old databases that still contain the default text to the new version.
// seedReporterAgent is guarded by reporter_agent_seed_v1 and writes the trigger only when creating a new agent, so
// upgraded databases don't get the new text — the tool schema was supplemented with
// evidence_version by seedFindingTrafficTools, but nothing tells the reporter to use it. One-time, and only overwrites unchanged text.
func (s *Server) upgradeReporterTriggerMessage() {
	const flag = "reporter_trigger_evidence_version_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	defer func() { _ = s.m.pg.SetSetting(flag, "true") }() // Try only once
	triggers, err := s.m.pg.ListTriggersFor("reporter")
	if err != nil {
		log.Printf("[reporter] failed to read trigger: %v", err)
		return
	}
	for _, t := range triggers {
		if !t.OnToolCall || t.ToolCallMessage != reporterToolCallMessageV1 {
			continue // The user changed it or it isn't a finding trigger; leave it alone.
		}
		t.ToolCallMessage = reporterToolCallMessage
		if err := s.m.pg.UpdateTrigger(t); err != nil {
			log.Printf("[reporter] failed to upgrade trigger message: %v", err)
			return
		}
		log.Printf("[reporter] Upgraded trigger message to read and return evidence_version")
	}
}

// seedReporterAgent pre-seeds a custom “report writer” agent (builtin=false, editable/deletable in the UI):
// Bind update_finding_report + task query tools, and attach a “report_finding call triggers”
// trigger — wake it to write a detailed report for each finding recorded. One-time (settings flag guard): do not recreate if the user deletes it.
// Dependency: the orchestration tool was seeded into the database with SeedTool above this function, so it can be bound.
func (s *Server) seedReporterAgent() {
	const flag = "reporter_agent_seed_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	defer func() { _ = s.m.pg.SetSetting(flag, "true") }() // Try only once, whether it succeeds or not

	if exist, _ := s.m.pg.GetAgentByKey("reporter"); exist != nil {
		return // key is already occupied (created manually by the user)—do not overwrite
	}
	a, err := s.m.pg.CreateAgent("reporter", "Report Writing",
		"Detailed vulnerability report writing: auto-triggered when a finding is discovered; fetch evidence and execution traces, then write and persist a Markdown report.")
	if err != nil {
		log.Printf("[reporter] failed to create agent: %v", err)
		return
	}
	if err := s.m.pg.SeedPromptIfEmpty(a.ID, agent.ReporterDefaultPrompt); err != nil {
		log.Printf("[reporter] failed to seed prompt: %v", err)
	}
	// Trigger execution strategy: parallel + none — one report per vulnerability, with multiple findings concurrently writing their own.
	// merge must be none: otherwise (default all) a batch of findings will be merged into one run, making parallelism pointless.
	// maxParallel=5: at most 5 report sessions at once, to avoid too many LLM calls at once.
	if err := s.m.pg.SetAgentTriggerBehavior("reporter", "parallel", "none", 5); err != nil {
		log.Printf("[reporter] failed to set trigger run policy: %v", err)
	}
	// Bind the tools it needs: write reports + read evidence/execution process/status.
	if err := s.m.pg.AddAgentToToolBinding("reporter", []string{
		"update_finding_report", "get_task_node_detail", "list_task_findings",
		"get_task_worker_trace", "list_task_worker_traces", "search_task_worker_traces",
		"get_task_graph",
	}); err != nil {
		log.Printf("[reporter] failed to bind tools: %v", err)
	}
	// Trigger: a report_finding call triggers it (the tool returns "finding recorded: <id>" with finding_id,
	// The task id is also in the trigger message).
	if _, err := s.m.pg.CreateTrigger(&db.AgentTrigger{
		AgentKey:        "reporter",
		Enabled:         true,
		OnToolCall:      true,
		ToolNames:       []string{"report_finding"},
		ToolCallMessage: reporterToolCallMessage,
	}); err != nil {
		log.Printf("[reporter] failed to create trigger: %v", err)
	}
	log.Printf("[reporter] Preconfigured 「report writing」 agent + finding trigger")
}

// seedAutoReportFindingBinding adds "auto" to report_finding's binding ONCE so
// conversation-context agents can call it without requiring an intent_id.
func (s *Server) seedAutoReportFindingBinding() {
	const flag = "auto_report_finding_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	if err := s.m.pg.AddAgentToToolBinding("auto", []string{"report_finding"}); err != nil {
		log.Printf("[auto] failed to bind report_finding by default: %v", err)
		return
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

// seedPlannerDefaultBindings adds "planner" to report_finding's binding ONCE
// (guarded by a settings flag), so existing DBs — whose report_finding row was
// seeded as worker-only — also let the planner record findings. Fresh DBs already
// get it via PlannerTools(); this only backfills without overriding a user unbind.
func (s *Server) seedPlannerDefaultBindings() {
	const flag = "planner_report_finding_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	if err := s.m.pg.AddAgentToToolBinding("planner", []string{"report_finding"}); err != nil {
		log.Printf("[planner] failed to bind report_finding by default: %v", err)
		return
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

// seedPlannerListAssetsBinding adds "planner" to list_assets's binding ONCE
// (guarded by a settings flag), so existing DBs — whose list_assets row was seeded
// as auto/pentest-only — also let the planner query the asset store by DSL. Fresh
// DBs already get it via PlannerTools(); this only backfills without overriding a
// user unbind.
func (s *Server) seedPlannerListAssetsBinding() {
	const flag = "planner_list_assets_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	if err := s.m.pg.AddAgentToToolBinding("planner", []string{"list_assets"}); err != nil {
		log.Printf("[planner] failed to bind list_assets by default: %v", err)
		return
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

// seedCompanyScopeRebind changes add_company_scope's default binding ONCE on
// existing DBs (guarded by a settings flag): the tool moves off worker and onto
// planner — defining a company's asset scope is a planning/main/auto concern, not
// something a worker does mid-exploration. Fresh DBs already get planner via
// PlannerTools() and lack worker via WorkerTools(); this only backfills old rows.
// One-shot + flag-guarded so a user who later re-binds worker isn't overridden.
func (s *Server) seedCompanyScopeRebind() {
	const flag = "company_scope_rebind_v1" // Switch the default binding from worker to planner
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	if err := s.m.pg.AddAgentToToolBinding("planner", []string{"add_company_scope"}); err != nil {
		log.Printf("[planner] failed to bind add_company_scope by default: %v", err)
		return
	}
	if err := s.m.pg.RemoveAgentFromTool("worker", "add_company_scope"); err != nil {
		log.Printf("[worker] failed to unbind add_company_scope: %v", err)
		return
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

// seedWorkerReadToolsUnbind strips the read-context tools off worker's default
// binding ONCE on existing DBs (guarded by a settings flag): a worker executes one
// intent and writes back — reading facts/companies and listing all workers' traces is
// a planning/main concern, not the executor's. Fresh DBs already lack these via
// WorkerTools(); this only backfills old rows without overriding a user who
// deliberately re-binds worker. Each RemoveAgentFromTool is per-tool +
// membership-guarded, so planner/mainagent bindings of the same tool are untouched.
//
// NOTE: search_all_worker_traces / get_worker_trace / node_detail are intentionally NOT
// unbound — worker owns them for cross-work look-back + node drill-down (see WorkerTools).
// They used to be in this list back when worker lacked them; seedWorkerReadbackRebind
// repairs DBs whose old run stripped them.
func (s *Server) seedWorkerReadToolsUnbind() {
	const flag = "worker_readtools_unbind_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	for _, k := range []string{
		"list_facts", "list_companies", "list_worker_traces",
	} {
		if err := s.m.pg.RemoveAgentFromTool("worker", k); err != nil {
			log.Printf("[worker] failed to unbind %s from worker: %v", k, err)
			return // On error, do not set the flag; retry on the next startup
		}
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

// seedWorkerReadbackRebind re-binds the cross-work look-back / drill-down tools onto
// worker ONCE (guarded by a settings flag): an earlier seedWorkerReadToolsUnbind wrongly
// stripped search_all_worker_traces / get_worker_trace / node_detail from worker after
// they had been added to WorkerTools(), so any DB that ran that migration lost them.
// Fresh DBs already have them via WorkerTools() and this is a harmless no-op there.
// One-shot + flag-guarded so a user who later deliberately unbinds them isn't overridden.
func (s *Server) seedWorkerReadbackRebind() {
	const flag = "worker_readback_rebind_v2" // v2: append node_detail
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	if err := s.m.pg.AddAgentToToolBinding("worker", []string{
		"search_all_worker_traces", "get_worker_trace", "node_detail",
	}); err != nil {
		log.Printf("[worker] failed to rebind trace/detail tools: %v", err)
		return // On error, do not set the flag; retry on the next startup
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

// seedAutoDefaultBindings adds "auto" to the task-op + platform tools' bindings
// ONCE (guarded by a settings flag), so existing DBs whose tool rows were seeded
// before Auto existed still give Auto its default toolset — without re-adding it
// after a user deliberately unbinds.
func (s *Server) seedAutoDefaultBindings() {
	const flag = "auto_default_bindings_v3" // v3: replace the old asset tool names and add insert_assets/add_company_scope
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	keys := make([]string, 0, len(platformToolKeys)+12)
	for _, t := range s.orchestrationTools() {
		keys = append(keys, t.Name())
	}
	keys = append(keys, platformToolKeys...)
	// Asset tools: the Auto operations platform often needs to view/register assets and manage company-wide scope.
	keys = append(keys, "insert_assets", "add_company_scope", "list_assets")
	if err := s.m.pg.AddAgentToToolBinding("auto", keys); err != nil {
		log.Printf("[auto] failed to apply default bindings: %v", err)
		return
	}
	_ = s.m.pg.SetSetting(flag, "true")
}
