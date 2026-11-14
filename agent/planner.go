package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Autumn-27/norma/transcript"
)

// Planner is the event-driven LLM planner (docs §4.3): each time the asset or
// exploration graph changes (debounced), it reads the exploration route, queries
// assets, judges whether the task goal is met, and emits 0..N exploration intents
// into the frontier. It is the sole intent generator.
type Planner struct {
	findingRecorder   FindingRecorder
	prov              llm.Provider
	model             string
	tx                *transcript.Store                      // raw LLM conversation persistence (nil = off)
	window            int                                    // context window in tokens (for compaction)
	windowFn          func() int                             // optional dynamic task-chain minimum
	maxTurns          int                                    // max agent turns per run (0 = unlimited)
	killWork          func(intentID int64) error             // engine callback to terminate a running work (nil = off)
	steerWork         func(intentID int64, msg string) error // engine callback to steer a running work mid-run (nil = off)
	proxyAddr         string                                 // recording proxy for WebFetch (empty = direct)
	proxyAddrFn       func() string
	proxyCACert       string        // recording proxy's CA cert path (HTTPS verify)
	webSearch         WebSearchOpts // web_search tool backend selection (off by default)
	workDir           string        // shared work dir (surfaced in prompt as artifact-output target)
	injectConstraints func() bool   // resolver: inject task operation constraints into system prompt? (nil = yes)
	nonStreamingFn    func() bool   // resolver: use non-streaming (Complete) path? (nil = streaming)
	noaEnabledFn      func() bool   // resolver: use experimental noa compaction? (nil = off)
	maxTokensFn       func() int    // resolver: per-reply output cap (nil/0 = send no cap)
	compactor         *Compactor    // cold-node compaction (§7); nil = disabled

	// todos keeps ONE plan-scratchpad per task (keyed by exploration id) so the
	// planner's multi-step plan survives across wake-ups — each Plan() is a fresh
	// session, but the shared store lets it record a serial exploit chain once and
	// dispatch it step-by-step over rounds instead of front-loading it in parallel.
	todoMu sync.Mutex
	todos  map[int64]*actool.TodoStore
}

func NewPlanner(prov llm.Provider, model, workDir string, tx *transcript.Store, window, maxTurns int) *Planner {
	return &Planner{prov: prov, model: model, workDir: workDir, tx: tx, window: window, maxTurns: maxTurns, todos: map[int64]*actool.TodoStore{}}
}

func (p *Planner) SetCompactionWindowResolver(fn func() int) { p.windowFn = fn }

// SetCompactor wires the cold-node compactor (cold-digest §7). Called each
// planner wake-up to advance the round counter, maintain cold stamps, and
// (off the hot path) fold cold nodes into digests. nil = feature disabled.
func (p *Planner) SetCompactor(c *Compactor) { p.compactor = c }

// SetNonStreaming wires a resolver deciding whether runs use the non-streaming
// model path (true = non-streaming). nil/unset = streaming (default).
func (p *Planner) SetNonStreaming(fn func() bool) { p.nonStreamingFn = fn }

func (p *Planner) nonStreaming() bool { return p.nonStreamingFn != nil && p.nonStreamingFn() }

// SetNoaEnabled wires a resolver deciding whether runs use the experimental noa
// context-compression mechanism. nil/unset = off (built-in compaction). Read per
// run so the settings toggle takes effect without rebuilding the agent.
func (p *Planner) SetNoaEnabled(fn func() bool) { p.noaEnabledFn = fn }

// SetMaxTokens wires a resolver for the per-reply output cap. nil/unset or 0 =
// send no cap and let the endpoint decide. Read per run, like nonStreaming.
func (p *Planner) SetMaxTokens(fn func() int) { p.maxTokensFn = fn }

func (p *Planner) maxTokens() int {
	if p.maxTokensFn == nil {
		return 0
	}
	return p.maxTokensFn()
}

func (p *Planner) compactionWindow() int {
	if p.windowFn != nil {
		return p.windowFn()
	}
	return p.window
}

// SetProxy points the planner's WebFetch at the recording proxy plus the CA cert
// it trusts to verify HTTPS through it (empty addr = direct).
func (p *Planner) SetProxy(addr, caCert string)      { p.proxyAddr, p.proxyCACert = addr, caCert }
func (p *Planner) SetProxyProvider(fn func() string) { p.proxyAddrFn = fn }

// SetWebSearch selects the web_search backend for the planner (off by default).
func (p *Planner) SetWebSearch(o WebSearchOpts) { p.webSearch = o }

// SetConstraintInject wires a resolver deciding whether this task's operation
// constraints get injected into the planner system prompt. Read per round so the
// settings toggle takes effect without rebuilding the agent. nil = inject (default).
func (p *Planner) SetConstraintInject(fn func() bool) { p.injectConstraints = fn }

// wantConstraints reports whether constraint injection is enabled (default yes).
func (p *Planner) wantConstraints() bool { return p.injectConstraints == nil || p.injectConstraints() }

// todoFor returns the task's persistent planning todo store, creating it on first
// use. Shared across all of this task's planner wake-ups.
func (p *Planner) todoFor(expID int64) *actool.TodoStore {
	p.todoMu.Lock()
	defer p.todoMu.Unlock()
	s := p.todos[expID]
	if s == nil {
		s = actool.NewTodoStore()
		p.todos[expID] = s
	}
	return s
}

// SetKillWork wires the engine's per-work terminate callback so the planner's
// kill_work tool can stop a single running worker.
func (p *Planner) SetKillWork(fn func(intentID int64) error) { p.killWork = fn }

// SetSteerWork wires the engine's per-work steering callback so the planner's
// steer_work tool can inject a mid-run course-correction into a running worker.
func (p *Planner) SetSteerWork(fn func(intentID int64, msg string) error) { p.steerWork = fn }

// renderPlannerTodos formats the persistent planning todo for injection into the
// wake-up prompt (empty when there are no todos yet — first wake-up).
func renderPlannerTodos(items []actool.Todo) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n[Your planning todos (retained across wake-ups, written by you in the last round)]:\n")
	for _, it := range items {
		mark := map[actool.TodoStatus]string{actool.TodoPending: "☐", actool.TodoInProgress: "▶", actool.TodoCompleted: "✔"}[it.Status]
		if mark == "" {
			mark = "☐"
		}
		b.WriteString(fmt.Sprintf("  %s %s\n", mark, it.Content))
	}
	b.WriteString("Proceed accordingly: only dispatch intents for the next step whose [prerequisite steps completed / depended-on facts already exist]; use TodoWrite to update the list (mark steps satisfied by facts as completed). Do not re-dispatch steps already in the list as pending/in_progress.")
	return b.String()
}

// TriggerEvent describes what concretely caused this planning round to fire, so
// the planner looks first at the actual change instead of re-scanning the whole
// overview. Kind:
//
//	"done"    — a worker finished intent IntentID (its output conclusion is fetched).
//	"finding" — a worker reported a finding on intent IntentID (Detail = summary).
//	"goal"    — the human (via main agent's set_goals) added one OR MORE goals in a
//	            single call (Goals = newly added goal text for this call, 1+ items; set_goals supports batch).
//	"goal_deleted" — the human deleted a goal from the overview goal management (Detail = deleted goal text).
//	"goal_edited"  — the human edited a goal from the overview goal management (OldGoal→NewGoal text).
//	"cancelled" — the human deleted intent IntentID (Detail = deletion reason). The intent is
//	            stopped (not deleted) and the reason is attached to it as a fact.
type TriggerEvent struct {
	Kind     string
	IntentID int64
	Detail   string
	Summary  string   // Kind=="cancelled" only: intent summary captured before deletion (node no longer exists after true deletion, cannot query)
	Goals    []string // Kind=="goal" only: newly added goal text for this set_goals call (1 or more items)
	OldGoal  string   // Kind=="goal_edited" only: goal text before edit
	NewGoal  string   // Kind=="goal_edited" only: goal text after edit
	Hints    []string // Kind=="hint" only: newly added hint text for this add_hint call (1 or more items)
}

// renderTriggers spells out the change(s) that fired this round: for a finished
// worker — which intent + its output conclusion; for a finding — which intent +
// what was found. Empty for time/heartbeat wakes. Reads the store (best-effort;
// a blank field never blocks the round).
func renderTriggers(ts *db.ExplorationStore, evs []TriggerEvent) string {
	if len(evs) == 0 || ts == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n[Actual changes that triggered this round (look here first, then decide whether to add directions)] :")
	for _, ev := range evs {
		switch ev.Kind {
		case "goal":
			if len(ev.Goals) == 1 {
				b.WriteString(fmt.Sprintf("\n- Human (main agent) added one goal: %s —— new goal to achieve, please add exploration directions accordingly (if no corresponding intent yet).", ev.Goals[0]))
			} else {
				b.WriteString(fmt.Sprintf("\n- Human (main agent) added %d goals: %s —— all are new goals to achieve, please add exploration directions one by one for goals without corresponding intents yet.", len(ev.Goals), strings.Join(ev.Goals, "; ")))
			}
		case "hint":
			if len(ev.Hints) == 1 {
				b.WriteString(fmt.Sprintf("\n- Human (main agent) added one strategic hint: %s —— already attached to the exploration graph, please adjust/add exploration directions accordingly (if no corresponding intent yet).", ev.Hints[0]))
			} else {
				b.WriteString(fmt.Sprintf("\n- Human (main agent) added %d strategic hints: %s —— all already attached to the exploration graph, please adjust/add exploration directions one by one accordingly.", len(ev.Hints), strings.Join(ev.Hints, "; ")))
			}
		case "goal_deleted":
			b.WriteString(fmt.Sprintf("\n- Human deleted this goal: %s —— this goal has been removed, please re-judge remaining goals/directions accordingly (no need to dispatch intents for it).", ev.Detail))
		case "goal_edited":
			b.WriteString(fmt.Sprintf("\n- Human edited the goal, from「%s」to「%s」—— please adjust exploration directions according to the new goal (if the original direction no longer applies, stop dispatching).", ev.OldGoal, ev.NewGoal))
		case "finding":
			b.WriteString(fmt.Sprintf("\n- Worker of intent #%d (%s) reported a finding: %s", ev.IntentID, intentSummary(ts, ev.IntentID), ev.Detail))
		case "cancelled":
			// Intent content preferred from Summary captured at deletion (after true deletion the node no longer exists, intentSummary can't find it).
			sm := ev.Summary
			if sm == "" {
				sm = intentSummary(ts, ev.IntentID)
			}
			b.WriteString(fmt.Sprintf("\n- Intent #%d deleted by user, intent content is: %s, deletion reason is: %s. This intent has been deleted (no longer executed); please re-plan accordingly.", ev.IntentID, sm, ev.Detail))
		default: // "done"
			b.WriteString(fmt.Sprintf("\n- Worker of intent #%d (%s) finished, output conclusion: %s", ev.IntentID, intentSummary(ts, ev.IntentID), workerOutput(ts, ev.IntentID)))
			if fids := factIDsYielded(ts, ev.IntentID); fids != "" {
				b.WriteString(fmt.Sprintf("; this intent produced new fact ids: %s ", fids))
			}
		}
	}
	b.WriteString("\n(Full details can be checked via node_detail / get_worker_output / list_findings.)")
	return b.String()
}

// factIDsYielded lists the fact ids an intent produced this run as "#12、#15", so the
// planner can jump straight to the round's incremental facts. Empty (best-effort) when
// the intent yielded no facts or the lookup fails.
func factIDsYielded(ts *db.ExplorationStore, id int64) string {
	ids, err := ts.FactsYielded(id)
	if err != nil || len(ids) == 0 {
		return ""
	}
	parts := make([]string, len(ids))
	for i, fid := range ids {
		parts[i] = fmt.Sprintf("#%d", fid)
	}
	return strings.Join(parts, "、")
}

// intentSummary reads an intent node's one-line summary (best-effort, "?" on miss).
func intentSummary(ts *db.ExplorationStore, id int64) string {
	n, err := ts.GetNode(id)
	if err != nil || n == nil {
		return "?"
	}
	var p map[string]any
	if json.Unmarshal(n.Payload, &p) == nil {
		if s, ok := p["summary"].(string); ok && s != "" {
			return s
		}
	}
	return "?"
}

// workerOutput returns the finished worker's conclusion for an intent — the last
// 'result' (else 'text') activity's full detail, truncated. Same source get_worker_output uses.
func workerOutput(ts *db.ExplorationStore, id int64) string {
	acts, _, err := ts.ActivityList(&id, 0, 1000)
	if err != nil {
		return "(failed to get output)"
	}
	var pick *db.Activity
	for i := range acts {
		if acts[i].Kind == "result" {
			pick = &acts[i]
		} else if acts[i].Kind == "text" && pick == nil {
			pick = &acts[i]
		}
	}
	if pick == nil {
		return "(this work has no output records yet)"
	}
	out, _ := ts.ActivityDetail(pick.ID)
	if out == "" {
		out = pick.Summary
	}
	return truncOutput(out, 800)
}

// truncOutput caps a worker-output blob so the trigger context doesn't bloat the
// system prompt every round; full text is one get_worker_output call away.
func truncOutput(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + " … (truncated, full text see get_worker_output)"
}

// renderGraphOverview folds the pre-computed graph_overview snapshot into the
// wake-up prompt so the planner starts each round with the full situation in
// hand — saving the round-trip it would otherwise spend calling the tool. It is
// the exact same JSON graph_overview would return; deeper detail is still one
// tool call away (node_detail / list_facts / …).
func renderGraphOverview(data map[string]any) string {
	b, err := json.Marshal(data)
	if err != nil {
		return "" // fall back to the model calling graph_overview itself
	}
	return "\n\n[This round's situation (graph_overview pre-fetch, equivalent to you calling that tool's return; get details on demand via node_detail/list_facts etc.)]:\n" + string(b)
}

// plannerDefaultTmpl is the built-in EDITABLE body (section [A]) of the planner prompt,
// seeded into agent_prompts. Goal is a {{.Goal}} template var; the intermediate artifact
// output spec tail is code-owned (artifactSpec) and appended by plannerSystem after rendering.
const plannerDefaultTmpl = `You are the "planner" of an authorized penetration testing system on a security platform, frequently woken up (woken whenever the graph changes). Responsibility: read situation → judge goals → **only add exploration intents when confirmed new uncovered directions exist**. You are the planner, not the executor: this round's outputs can only be [generate/explain intents] or [judge goals], absolutely do not do the work in the plan.

Task goal: {{.Goal}}

**How many intents should this round produce (think this through first)**:
- **Hard bottom line (highest priority)**: as long as [goal not yet achieved] AND [currently no open or running intents] (frontier_open=0 and running_intents empty), this round [must] produce at least one intent advancing toward the goal—when there's no work running to wait for and no queued direction, producing 0 intents = task stalled; even if known directions are only in recent_done, still open a new one or re-dispatch one based on the done/exhausted/blocked judgment below.
- Outside the hard bottom line, **producing 0 intents is a normal result, but must have a valid reason** (not a default of "fewer is safer"): ① **Already covered**—directions you think of are all being handled by existing open/running intents (rewording to regenerate already-existing intents is a serious error); ② **Waiting on dependency**—the next step depends on output from work currently running, but it hasn't come out yet (hard dispatching at this point would leave downstream with nothing to work on, idle; wait until the next wake-up after the graph updates before dispatching).
- Conversely: when there's definitely a [new uncovered direction not depending on running work], or the goal is not yet achieved and there's still untested surface within scope, you should dispatch—don't treat 0 intents as a default of laziness.

**Decision process for each wake-up**:

1. **The full situation is already attached below this prompt** (it's the graph_overview return, no need to call it): task (original title + goal/root node), asset counts, goals + status, open/running/recent_done intents, sites_without_endpoints (sites without endpoints, hinting at possibly unexplored directions), facts (count of exploration facts, distinct from findings), recent_facts ({id, summary, confidence?}).
   - **Scope**: exploration nodes (goals/intents/facts/findings) contain only this task; **assets are isolated by task by default** (only this task and explicitly associated tasks; after the user enables "share global assets", can query the whole library)—ignore assets not related to this task when they appear.
   - **Lineage**: each intent carries parents (upstream: which facts/intents it derives from) and yields (downstream: which facts/findings it produced), each recent_fact carries from_intent; use this to understand "which facts come from which direction, can new directions be synthesized".
   - **Negative/doubtful observations** ("port closed / not injectable" etc. in recent_facts) are worker observations, not conclusions: before accepting, first check node_detail(id) for evidence—only when evidence is solid, confidence=observed, and methods exhausted, is that direction considered temporarily closed; missing evidence, just "looks like / only probed once", or confidence=inferred, treat as [not yet explored]; if within scope and no other intent covers it, default to dispatching a verification intent to confirm or refute (**at most one re-check per negative direction**); after re-check still negative and evidence reasonable, respect that conclusion, don't dispatch again.
   - **Call for deeper details only when needed**: list_facts (paginated, newest first, default 20, q filterable, before pagination, with total/has_more), list_findings (all findings), node_detail(id) (full evidence/details; list/recent_facts only give summaries), list_assets (pull: q search, type/company_id/task_id filter, pagination, or id/ids direct lookup), asset_neighbors. Whether assets are shared is decided by user settings, cannot be enabled on your own; sharing does not mean expanding testing authorization.

2. **Judge goals (core responsibility)**: the goals field already contains goals and status; for unachieved goals already proven by a discovery/fact, call prove_goal(goal_id, evidence_id, reason) to mark met.**When you mark the last uncompleted goal, the system automatically determines the entire task is complete**—finalization is driven only by prove_goal one by one, there is no other "one-click complete" method.
   - ⚠️ **Quantitative acceptance check (strictly prohibit premature sealing)**: when the goal contains quantifiable conditions (coverage reaches X%, reaches a specified count, obtains a certain permission), [must] verify the actual measured values in graph_overview above (coverage.pct, findings_total count, etc.) before prove_goal: if not met, [prohibit] prove_goal, instead dispatch an intent to make up the difference; do not mark met prematurely on grounds of "basically achieved / core already obtained". Example: requires 100% coverage but actual measured coverage.pct=40% → not achieved, continue dispatching supplementary testing intents.

3. **(Optional, opening only, very light) Probe understanding**: only when there are almost no facts in the graph (recent_facts basically empty, task just started), and the situation alone cannot make the initial intent specific, use Bash etc. to do very few read-only probes against the target (e.g. 1–2 curl to view homepage/fingerprint). **The only legal output is one more precise intent description**—absolutely not discovery/verification/exploitation of vulnerabilities, nor enumeration results of endpoints/directories/parameters (those are worker's work, write them as intents and dispatch). Three hard boundaries:
   - If the graph already has worker-produced facts (facts>0 / recent_facts non-empty) → [prohibit] probing yourself anymore, all judgments based on existing facts, this round's output can only be "dispatch new intent" or "end"; want to dig deeper into a clue → dispatch an intent for the worker to check, don't curl yourself.
   - Even at opening, probe at most ≤3 times then stop, just to clarify the initial intent; once you find yourself "deeply verifying" rather than "quickly determining direction" (enumerating endpoints/directories one by one, trying ids one by one, decoding chains, repeatedly probing the same interface, any injection/escalation/vulnerability testing/verification—all heavy worker work), stop immediately and write it as an intent.
   - Anything that can be judged from existing facts/situation, no need to probe at all.

4. **Decide which new directions to add**: **"restraint" here means only [not repeating existing intents], not "dispatch as few as possible"**—when the goal is not yet achieved, the default question is "what deeper, fiercer, still uncovered techniques are there to approach the goal", not "can we finish". Intent is [an open exploration direction] (not a fixed type/menu), combine known facts, assets, goals to judge direction yourself, compare one by one with open + running + recent_done:
   - Already covered by existing open/running → no longer generate (being handled).
   - Appeared in recent_done → **first check that intent's state (each carries one) to distinguish how it stopped, then decide**:
     · **done (finished normally)**: covered → don't re-dispatch as-is; whether it's a dead end depends on the fact conclusions it yields, not state; only re-dispatch when there's [materially new mechanism] (new fact/asset/parameter/obviously different technique), and write clearly in summary how it differs from last time; rewording, "try once more maybe it works" doesn't count, prohibited retry.
     · **exhausted (budget exhausted, probed halfway then cut, only partially written back) / blocked (model or network failure, basically not probed)**: both ended mid-way without good conclusion, incomplete information—first use get_worker_trace / get_worker_output to see where it actually got to, where it's stuck, then choose from the following: close to breakthrough but cut by budget → dispatch "continue from previous progress"; pure external failure didn't run (blocked is often this) → directly re-dispatch same direction; gets stuck at the same place every time → change technique/direction. Basis is always real progress in trace, not state itself.
   - Brand new direction with no intent coverage at all → generate.
   - All known directions covered by existing open/running intents → no generation, end directly (there's work running/queued, wait for them to progress); but if only recent_done coverage remains, no open/running and goal not yet achieved → per the hard bottom line above, must open a new one or re-dispatch.
   - **Depth over coverage**: coverage is a floor/acceptance item, not the exploration goal itself; after discovering a high-value entry (possibly leading to RCE/privilege escalation/data exfiltration), prioritize dispatching an intent to [drive deep down that path], rather than spreading thin for flat coverage, shallow-probing each asset one by one.
   - **Maintain route diversity, don't converge too early**: when goal not achieved, if existing intents are all crowded on the same route/entry, and there's an [essentially different] uncovered direction (different entry surface/asset type/exploit chain), prioritize filling that divergent direction, rather than adding synonymous intents on the same line (look at substantive difference, not wording); if that divergent direction is already covered by existing intents, still don't generate. Ideal is 2–3 routes with different mechanisms coexisting (e.g. "hit via upload chain" and "hit via authentication bypass"), concentrate resources only after one produces [goal-approaching] evidence.**But diversity always obeys the top [operation constraints]**: entry surfaces/ports/hosts/operations excluded by constraints, even if essentially different, absolutely do not generate intents.

   **Serial exploit chains: dispatch step by step, don't split into parallel.** Strongly dependent serial chains (①→②→③, later step depends on actual output of earlier step): don't issue all in parallel at once (downstream can't get upstream output that doesn't exist, only repeats/idles); use TodoWrite to record the whole chain as todos (one per step), only dispatch the step whose [prerequisite already satisfied] this round (usually the first step), wait for it to produce a fact, then on next wake-up (the prompt will carry the todo list) dispatch the next step and mark the satisfied one completed. Don't split "the same thing" into two ("confirm trigger point" and "trigger trigger point" are the same step); only [parallel, independent] dimensions (e.g. enumerating multiple unrelated endpoints) use multiple intents in parallel.

5. **Submit**: use [one] add_intent to batch submit the screened new directions (intents array, at most 4 highest-value ones, don't call multiple times one by one):
   - **summary**: one-sentence natural language description of the direction (complete target address of test + what to do + why), no fixed categories; dedup mainly by comparing with existing intents.
   - **asset_ids**: target asset ids to test/attack for this direction (pass as many as possible, 0/1/multiple, from list_assets)—as long as the direction revolves around specific assets (sites/interfaces/parameters/hosts), be sure to pass, used for coverage dedup and connecting into asset chains; pass all for multi-asset; leave empty only for pure global reconnaissance with no specific asset.
   - **parent_ids**: which upstream nodes this direction is synthesized from (optional, 0/1/multiple)—pass all when multiple facts combine to produce one intent, also pass the id when derived from an upstream intent/finding, leave empty for top-level brand new directions.

No repetition, no forced padding; but when goal not achieved and there's uncovered deeper technique, dispatch when you should. Concise, focused, efficient.`

func plannerSystem(goal, dataDir, workDir string) string {
	body := renderSystem("planner", plannerDefaultTmpl, PlannerVars{Goal: goal, DataDir: dataDir, Now: nowStr()})
	return body + artifactSpec(workDir)
}

// Plan runs one planning round. emit, if non-nil, receives the planner's execution
// steps (so users can see how it reads the situation and judges goals — the
// planner is the intent generator and was previously a black box). Returns whether
// the planner judged the goal met.
// triggers carries the concrete change(s) that fired this round — worker(s) done
// and/or finding(s) reported (may be several — the engine debounces a burst; empty
// for time/heartbeat wakes). They are spelled out at the top of the prompt so the
// planner looks first at the actual change (which intent, its output/finding).
func (p *Planner) Plan(ctx context.Context, taskID int64, as *db.AssetStore, ts *db.ExplorationStore, goal string, triggers []TriggerEvent, emit func(db.Activity)) (met bool, reason string, err error) {
	// cold-digest §2.3/§7: advance this task's planner-round counter, maintain the
	// cold_since_round stamps, and (if a threshold is hit) kick off background
	// compaction. Synchronous part is cheap (a few queries); the LLM compaction
	// runs in a detached goroutine so it never adds latency to this round.
	p.compactor.OnPlannerRound(ctx, ts)
	tsx := NewToolSet(ts, "planner")
	tsx.SetFindingRecorder(p.findingRecorder)
	if as != nil {
		tsx.SetAssetStore(as, as.Companies())
	}
	tsx.SetTaskID(taskID)
	tsx.SetCoverageEnabled(as == nil || as.CoverageEnabled(taskID))
	tsx.killWork = p.killWork   // enable kill_work tool (nil = unavailable)
	tsx.steerWork = p.steerWork // enable steer_work tool (nil = unavailable)
	if origin, _ := ts.OriginFactID(); origin > 0 {
		tsx.SetOwnerNode(origin) // planner-side anchors default to the task root (origin fact)
	}
	// Domain tools + basic default tool set (Read/Write/Edit/MultiEdit/LS/Glob/Grep/Bash)
	// When asset coverage is disabled, drop add_task_scope/list_untested_assets (not in prompt).
	base := append(tsx.DropCoverageTools(tsx.PlannerTools()), actool.DefaultTools()...)
	ctx = WithRunInfo(ctx, RunInfo{TaskID: taskID, ExplorationID: explorationID(ts)})
	tools, def, cleanup := AugmentTools(ctx, "planner", base)
	defer cleanup()
	// Key situation (just-completed intent + pre-fetched full graph) moved to [current round user input] (see input below), system
	// keeps only static planning body. Move-out makes system stable each round, better for caching; cost is if single round grows long,
	// situation may be compressed by compaction (planner single round usually short, low risk). situational gets appended to input below.
	situational := renderTriggers(ts, triggers) + renderGraphOverview(tsx.graphOverviewData())
	// Task-level deadline / endgame mode (injected via ctx, see taskclock.go). The endgame round puts the task timeout
	// planner wrap-up words as [current round operation instruction] into current round user input (with situational), making it only do final
	// goal judgment, not produce new intents.
	tc := taskClockFrom(ctx)
	if tc.Final {
		situational += "\n\n[Task final wrap-up (special instruction this round, overrides regular planning flow)]:" + resolveTaskTimeoutWrapup("planner")
	}
	// This task's working directory <workDir>/tasks/<taskID>, create it first.
	taskDir := ensureRunDir(p.workDir, taskID, 0)
	ctx = intercept.WithReviewContext(ctx, taskDir, intercept.ReviewBackground{})
	sysBody := plannerSystem(goal, p.workDir, taskDir)
	if p.wantConstraints() {
		sysBody += constraintBlock(ts) // inject operation constraints (if any) into system prompt to bound exploration scope
	}
	system, boundary := deferredSystem(sysBody, def)
	// planner has no own wall-clock budget; when deadline exists, clamp MaxDuration to remaining time so running planning rounds
	// enter wrap-up when task deadline hits (timeout → task timeout wrap-up, turns → per-run wrap-up).
	maxDur, clamped := clampMaxDuration(tc.DeadlineUnix, 0)
	settle := wrapupSettlement("planner", nil)
	if tc.DeadlineUnix > 0 {
		settle = wrapupSettlementForTask("planner", nil, clamped)
	}
	proxyAddr := p.proxyAddr
	if p.proxyAddrFn != nil {
		proxyAddr = p.proxyAddrFn()
	}
	opts := agentcore.Options{
		Provider:        p.prov,
		SystemPrompt:    system,
		DynamicBoundary: boundary,
		Tools:           append(tools, deepSeekTools(p.webSearch)...),
		DeferredTools:   def.Deferred,
		UnlockSet:       def.Unlock,
		PermissionMode:  permission.ModeBypass,
		EnableWebFetch:  true, // route via recording proxy for traces; load proxy CA to verify MITM-re-signed HTTPS certs
		WebFetchProxy:   proxyAddr,
		WebFetchCACert:  p.proxyCACert,
		// Web search (optional). ddgs needs no key; brave-free needs BraveKey; tavily needs TavilyKey.
		// WebSearchProxy is a separate egress proxy (http/https/socks5), unrelated to the traffic-recording MITM proxy; empty = direct.
		EnableWebSearch:    p.webSearch.Enabled && p.webSearch.Backend != "deepseek",
		WebSearchBackend:   p.webSearch.Backend,
		BraveSearchAPIKey:  p.webSearch.BraveKey,
		TavilySearchAPIKey: p.webSearch.TavilyKey,
		WebSearchProxy:     p.webSearch.Proxy,
		BashEnv:            proxyEnv(proxyAddr, p.proxyCACert), // Bash subprocesses default to proxy + trust CA
		WorkingDir:         taskDir,                            // this task's working directory <workDir>/tasks/<taskID>
		ToolOutputDir:      cmdOutDir(taskDir),
		MaxTurns:           p.maxTurns, // 0 = unlimited (configurable in agent management)
		MaxDuration:        maxDur,     // 0=unlimited; with deadline = time until deadline
		Compaction:         compactionConfig(p.compactionWindow()),
		// Cross-wakeup shared planning todos: let serial chains persist across rounds (session is new, store is not).
		Todos: p.todoFor(ts.ID()),
		// Hit [current round] turn budget → SDK runs wrap-up: land the conclusions already clear this round (add_intent to dispatch,
		// prove_goal to mark, serial chain TodoWrite), NOT stop planning — planner will be woken repeatedly.
		// clamped (task deadline clamped) → switch to PromptByReason (see wrapupSettlementForTask).
		Settlement:   settle,
		NonStreaming: p.nonStreaming(), // when this profile selects non-streaming, use Provider.Complete
		MaxTokens:    p.maxTokens(),    // 0 = send no limit, let server default decide
	}
	if p.tx != nil { // persist raw LLM conversation; one accumulating file per task's planner
		opts.Transcript = p.tx
		opts.SessionID = fmt.Sprintf("exp%d-planner", ts.ID())
	}
	// Experimental: when enabled, noa takes over context compression (archives centralized in <workDir>/noa/<SessionID>, persistent).
	noaSession := fmt.Sprintf("exp%d-planner", ts.ID())
	enableNoa(&opts, p.noaEnabledFn, p.workDir, noaSession, noaWarn(noaSession))
	// Situation (just-completed intent + full graph) now goes into current round user input (see input below). user also has
	// instructions + cross-wakeup todos (todo is model's own planning scratchpad, regenerable, fine in user).
	// Opener splits by "whether this round has concrete changes": has changes → point to [Actual Changes] block below; no changes
	// (scheduled heartbeat / hint / recovery etc.) → don't falsely claim "graph changed", instead prompt to incidentally review running intents.
	lead := "There were specific changes this round (see the [Actual changes this round] block below); plan the next step based on them:"
	if len(triggers) == 0 {
		lead = "This round is a **scheduled heartbeat check / no specific change signal** wakeup—the graph may not have new changes. Incidentally review running intents: use steer_work to correct long-stalled or off-track intents, use kill_work to abort entirely wrong directions; then judge goals and decide whether to add new directions:"
		// On heartbeat/no-change wakeup, if the entire graph has no open or running intents → exploration has stalled (no worker running,
		// no queued directions). Explicitly tell planner and force it to produce new directions this round, not just review running intents and spin empty.
		if active, err := ts.HasActiveIntent(); err == nil && !active {
			lead = "This round is a **scheduled heartbeat check** wakeup, and currently **there are no open or running intents**—no worker is running, no queued directions, exploration has stalled. You **must** produce one or more new intents this round that advance the goal and do not duplicate existing intents in the graph (must not produce 0 intents); first judge whether the goal is met based on the situation below, if not met immediately add directions:"
		}
	}
	input := lead + situational + "\n\nJudge the goal based on the situation above. When the goal is **truly met** (goal results obtained / target vulnerabilities confirmed), mark them one by one using prove_goal.**Hard bottom line: as long as the goal is not yet met and there are no open or running intents (frontier_open=0 and running_intents is empty), this round must produce at least one intent advancing the goal—there is no running work to wait for and no queued direction; producing 0 intents = task stalled. Only when there are open/running intents advancing or the goal is already met may this round skip producing new intents.**" +
		renderPlannerTodos(opts.Todos.List())
	// MaxDuration now interrupts running tools at wall-clock deadline and enters wrap-up in-place (on live ctx), single-round hangs no longer
	// bypass wrap-up, no external hard ctx fallback needed. ctx only carries pause / kill / shutdown.
	_, _, err = captureRun(ctx, opts, input,
		func(r db.Activity) {
			if emit != nil {
				r.Worker = "planner" // planner activity has no intent_id (it generates them)
				emit(r)
			}
		})
	return tsx.GoalMet, tsx.Reason, err
}
