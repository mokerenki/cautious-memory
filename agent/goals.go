package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/llm"
	acperm "github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Autumn-27/norma/transcript"
)

// goalsDefaultTmpl is the built-in EDITABLE body (段 [A]) of the goals-decomposer
// prompt, seeded into agent_prompts. No template vars are used today.
const goalsDefaultTmpl = `You are a penetration testing goal decomposer. Your responsibility is to identify the **ultimate result to be achieved** from user input, not plan attack steps.

**Step 1 (do before splitting goals): extract operation constraints**
From "task goal / task description" identify the operator's explicit rules on [what operations can/cannot be done], call set_constraints to register one by one (if description/goals do not involve operation constraints, extraction can be skipped):
- type=deny: prohibited operations (e.g. "do not scan ports", "do not write/delete operations on production environment", "prohibit brute force", "do not touch certain subdomain").
- type=allow: explicitly allowed/limited operation scope (e.g. "only passive reconnaissance allowed", "only target certain domain").
- Constraints ≠ goals, also ≠ attack steps: they are regulations on operational behavior boundaries.
- **Constraints must be [self-contained, write concrete targets]**: replace "current target/current port/current IP/current current domain/this site" such **referential pronouns** with **specific values** from task goal/description. Constraints will be injected separately into execution phase prompts, after leaving context referential pronouns cannot determine who they refer to.
  Example: goal is https://abc.example.net → write "only allow testing abc.example.net" instead of "only allow testing current target"; "only test target port 443, do not scan other ports" instead of "only test current port". If original text only says "current target" but target address is clear, fill in the address.
- **Only register constraints [clearly written or emphasized] in goal/description, strictly prohibit fabrication**; when unsure of type use deny (more conservative).
- If there are indeed no operation constraints in goal/description, then **do not** call set_constraints.
After registering constraints (if any), proceed to the goal splitting below.

**Goal = final deliverable/verifiable result**

**Content that is NOT a goal (prohibited from being listed as sub-goals)**:
- Information collection, reconnaissance, endpoint scanning
- Vulnerability analysis and verification process
- Attack steps, exploitation methods
- Result verification steps

**Splitting principles**:
- User-described final goal is only one → output one
- Multiple **mutually independent** final deliverables → list separately
- Can mark vulnclass corresponding to clear vulnerability class; information collection/business logic class goals leave blank
- Strictly prohibit fabricating goals not mentioned by user

Call set_goals to submit results.`

// goalsScopeTail is the code-owned tail appended after the editable goals body
// WHEN an asset store + task context are available. It teaches the decomposer to
// also lift the explicit asset scope out of the goal/description and register it
// via add_task_scope. Kept in code (not the DB-editable body) so it always applies
// on released DBs and can't be edited away — same pattern as the trafficTool tail.
const goalsScopeTail = `

**Extra responsibility: register test asset scope**
Besides splitting goals, you also need to identify **explicitly given test asset scope** from "task goal / task description", call add_task_scope to register (this task's authorization boundary, also denominator for asset test coverage). **Minimum scope principle: only register the one target user explicitly points to, absolutely do not expand on your own.**
- Goal is URL or hostname address (e.g. https://xxx.example.com/path, app.example.com) → take its **full hostname**, kind=subdomain, value=full hostname.
  Example: goal https://a1b2c3.lab.example.net/path → kind=subdomain, value=a1b2c3.lab.example.net (**not** example.net).
  **Strictly prohibit** shrinking hostname with subdomain into root domain—seeing xxx.example.com and registering entire example.com would expand scope beyond user's goal, violating minimum scope principle.
- Only when user gives **bare root domain with no subdomain** (e.g. directly writes example.com), or explicitly says "entire site / all subdomains / full domain" → use kind=root_domain, value=example.com.
- Pure IP or CIDR → kind=ip / cidr, value=IP or CIDR.
- **Do not** register company scope (company)—task just created, asset system usually doesn't have this company yet, can't register, company-level scope handled by subsequent plan phase.
Other rules:
- Only register scope **clearly written in goal/description**; strictly prohibit fabricating or inferring unmentioned domain/IP.
- reason briefly explains which sentence the basis comes from, for audit.
- If there is no explicit asset scope in goal/description, then **do not** call add_task_scope.
Register scope via add_task_scope first (if any), then call set_goals to submit goals.`

// GoalSpec is one decomposed objective.
type GoalSpec struct {
	Text      string `json:"text"`
	VulnClass string `json:"vulnclass,omitempty"`
}

// DecomposeGoals asks the LLM to break a pentest task goal into discrete,
// independently-verifiable objectives (each becomes a goal node). Returns nil if
// no provider is configured or the call yields nothing — the caller then falls
// back to a rule-based split so goal nodes always exist.
//
// prov is supplied by the caller (rather than built here from a Config) so goal
// decomposition rides the SAME provider instance as the rest of the engine — it
// shares the rate limiter, gets recorded by llmrec, and participates in LLM
// failover instead of quietly bypassing all three.
//
// desc is the task's free-text description (背景：靶标范围/数量要求/交战说明等).
// It is fed alongside the goal so the decomposer no longer splits blind — the
// prompt still forbids inventing anything the two texts don't state.
//
// emit, when non-nil, receives every LLM step (thinking/tool_use/result) with
// Worker="planner" so the round-0 goal-decomposition activity is visible in the UI.
//
// as + taskID, when non-nil/positive, wire the add_task_scope tool so the
// decomposer can register the explicit asset scope it extracts from the goal.
//
// ts is the task's exploration store: set_goals writes the decomposed goal nodes
// straight into it (the same managed tool the main agent uses to add goals at
// runtime). The returned specs are read back from the store so callers can emit
// per-goal activity and detect the "LLM produced nothing" case for their fallback.
func DecomposeGoals(ctx context.Context, prov llm.Provider, dataDir, goalText, desc string, as *db.AssetStore, ts *db.ExplorationStore, taskID int64, emit func(db.Activity)) []GoalSpec {
	if prov == nil {
		return nil
	}
	return DecomposeGoalsWithProvider(ctx, prov, dataDir, goalText, desc, as, ts, taskID, false, 0, emit)
}

// DecomposeGoalsWithProvider is the task-runtime variant used when a task has an
// ordered provider chain. It preserves the same tools and write behavior while
// letting the caller own provider selection/failover. maxTokens is the profile's
// per-reply output cap (0 = send none).
func DecomposeGoalsWithProvider(ctx context.Context, prov llm.Provider, dataDir, goalText, desc string, as *db.AssetStore, ts *db.ExplorationStore, taskID int64, nonStreaming bool, maxTokens int, emit func(db.Activity)) []GoalSpec {
	if prov == nil {
		return nil
	}
	// 目标拆解是一次性调用：不挂 transcript store，所以 agentcore 不会往 ctx 上挂
	// session id（它只在有 writer 时才挂，见 agentcore.Prompt）。而按 session-id 头
	// 做提示缓存/粘性路由的网关（opencode zen 缺 x-opencode-session 直接 400
	// MissingSessionID）读的就是 ctx 上这个值——不补就是「对话正常、拆解 400」。
	// 显式挂一个稳定 id：同一探索的拆解请求共享它（利于命中缓存），且命名与
	// planner/worker 不冲突，能被 llmrec.parseSession 正确归因。
	if ts != nil {
		ctx = transcript.WithSessionID(ctx, fmt.Sprintf("exp%d-goals", ts.ID()))
	}
	// worker="goals" tags the goal nodes' provenance; ts/taskID let set_goals link
	// each goal under the task root. This is the catalog's real set_goals tool, so a
	// web-edited description/schema on it applies here too.
	tsx := &ToolSet{as: as, ts: ts, taskID: taskID, worker: "goals"}
	// Description rides in the user message (same channel as the goal), NOT via the
	// {{.EngagementDescription}} template var — else a prompt that references the var
	// would inject the description twice. System prompt stays pure static instructions.
	sys := renderSystem("goals", goalsDefaultTmpl, GoalsVars{DataDir: dataDir, Now: nowStr()})
	// set_constraints 始终可用(不依赖 asset store):正文已含「先抽操作约束再拆目标」这步
	// (可在 agent 编辑页改措辞),这里只需接上工具。
	tools := []actool.CoreTool{tsx.setGoals(), tsx.setConstraints()}
	// Wire add_task_scope only when we have a real asset store + task to write to.
	// The scope-extraction tail is appended in lockstep so the prompt never asks for
	// a tool that isn't present.
	if as != nil && taskID > 0 {
		tools = append(tools, tsx.addTaskScope())
		sys += goalsScopeTail
	}
	userMsg := "Task goal:\n" + goalText
	if d := strings.TrimSpace(desc); d != "" {
		userMsg += "\n\nTask description (background info, may contain target scope/quantity requirements/engagement instructions; for reference only, do not fabricate content not mentioned therein):\n" + d
	}
	// Use captureRun so every LLM step is emitted as an activity record (visible in
	// the plan tab under the round-0 marker). Falls back gracefully when emit is nil.
	captureEmit := func(r db.Activity) {
		if emit != nil {
			r.Worker = "planner"
			emit(r)
		}
	}
	captureRun(ctx, agentcore.Options{
		Provider:               prov,
		SystemPrompt:           []string{sys},
		Tools:                  tools,
		PermissionMode:         acperm.ModeBypass,
		DisableBackgroundTasks: true,
		// 3 步(抽约束 → 登记范围 → 拆目标)各需一次工具调用,给足回合避免收尾前漏调 set_goals。
		MaxTurns:     8,
		NonStreaming: nonStreaming, // 该 profile 选非流式时走 Provider.Complete
		MaxTokens:    maxTokens,    // 0 = 不发上限,由服务端默认值决定
	}, userMsg, captureEmit)
	// set_goals persisted the goals directly; read them back so the caller sees what
	// was written (empty slice ⇒ the LLM produced nothing ⇒ caller falls back).
	if ts == nil {
		return nil
	}
	nodes, _ := ts.ListByKind(db.KindGoal, 10000)
	var out []GoalSpec
	for _, n := range nodes {
		var p struct {
			Text      string `json:"text"`
			VulnClass string `json:"vulnclass"`
		}
		_ = json.Unmarshal(n.Payload, &p)
		if strings.TrimSpace(p.Text) != "" {
			out = append(out, GoalSpec{Text: p.Text, VulnClass: p.VulnClass})
		}
	}
	return out
}
