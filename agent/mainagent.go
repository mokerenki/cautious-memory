package agent

import (
	"context"
	"fmt"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Autumn-27/norma/transcript"
)

// MainAgent is the thin human-interface orchestrator (docs §4.2 / §7). The human
// chats with it; it observes (read tools), and steers by injecting hints
// (→planner) or direct high-priority intents (→frontier). It does NOT run the
// autonomous intent-generation loop (that is the planner's job).
type MainAgent struct {
	findingRecorder FindingRecorder
	prov            llm.Provider
	model           string
	tx              *transcript.Store // raw LLM conversation persistence (nil = off)
	window          int               // context window in tokens (for compaction)
	windowFn        func() int        // optional dynamic task-chain minimum
	maxTurns        int               // max agent turns per run (0 = unlimited)
	proxyAddr       string            // recording proxy for WebFetch (empty = direct)
	proxyAddrFn     func() string
	proxyCACert     string                                 // recording proxy's CA cert path (HTTPS verify)
	webSearch       WebSearchOpts                          // web_search tool backend selection (off by default)
	workDir         string                                 // shared work dir (surfaced in prompt as artifact-output target)
	steerWork       func(intentID int64, msg string) error // engine callback: steer a running work (nil = off)
	nonStreamingFn  func() bool                            // resolver: use non-streaming (Complete) path? (nil = streaming)
	noaEnabledFn    func() bool                            // resolver: use experimental noa compaction? (nil = off)
	maxTokensFn     func() int                             // resolver: per-reply output cap (nil/0 = send no cap)
}

// SetNoaEnabled wires a resolver deciding whether runs use the experimental noa
// context-compression mechanism. nil/unset = off (built-in compaction). Read per
// run so the settings toggle takes effect without rebuilding the agent.
func (m *MainAgent) SetNoaEnabled(fn func() bool) { m.noaEnabledFn = fn }

// SetNonStreaming wires a resolver deciding whether runs use the non-streaming
// model path (true = non-streaming). nil/unset = streaming (default).
func (m *MainAgent) SetNonStreaming(fn func() bool) { m.nonStreamingFn = fn }

func (m *MainAgent) nonStreaming() bool { return m.nonStreamingFn != nil && m.nonStreamingFn() }

// SetMaxTokens wires a resolver for the per-reply output cap. nil/unset or 0 =
// send no cap and let the endpoint decide. Read per run, like nonStreaming.
func (m *MainAgent) SetMaxTokens(fn func() int) { m.maxTokensFn = fn }

func (m *MainAgent) maxTokens() int {
	if m.maxTokensFn == nil {
		return 0
	}
	return m.maxTokensFn()
}

func NewMainAgent(prov llm.Provider, model, workDir string, tx *transcript.Store, window, maxTurns int) *MainAgent {
	return &MainAgent{prov: prov, model: model, workDir: workDir, tx: tx, window: window, maxTurns: maxTurns}
}

func (m *MainAgent) SetCompactionWindowResolver(fn func() int) { m.windowFn = fn }

func (m *MainAgent) compactionWindow() int {
	if m.windowFn != nil {
		return m.windowFn()
	}
	return m.window
}

// SetProxy points the main agent's WebFetch at the recording proxy plus the CA
// cert it trusts to verify HTTPS through it (empty addr = direct).
func (m *MainAgent) SetProxy(addr, caCert string)      { m.proxyAddr, m.proxyCACert = addr, caCert }
func (m *MainAgent) SetProxyProvider(fn func() string) { m.proxyAddrFn = fn }

// SetWebSearch selects the web_search backend for the main agent (off by default).
func (m *MainAgent) SetWebSearch(o WebSearchOpts) { m.webSearch = o }

// SetSteerWork wires the engine callback that lets the main agent's steer_work
// tool inject a mid-run course-correction into a running work (nil = tool off).
func (m *MainAgent) SetSteerWork(fn func(intentID int64, msg string) error) { m.steerWork = fn }

// mainAgentDefaultTmpl is the built-in EDITABLE body (section [A]) of the main agent
// prompt, seeded into agent_prompts. Goal is a {{.Goal}} template var; the intermediate
// artifact output spec tail is code-owned (artifactSpec), appended after rendering.
const mainAgentDefaultTmpl = `You are the "main agent" of an authorized penetration testing system, the human operator's interface. You do not personally explore, nor autonomously and continuously generate intents (that is the planner's job). Your responsibilities:

1. Observe: use graph_overview / list_findings / list_facts / list_assets / get_worker_output to answer the human's questions about current progress.
2. Steer (translate human intent into system actions):
   - Human wants "change direction / emphasize a class of vulnerabilities / focus on a certain area" → use add_hint to write a hint (the planner will read it next time).
   - Human wants "immediately test a specific target" → use add_intent to directly inject a high-priority intent (priority 8-10). The system will automatically pull the completed task back into running state and let a worker take this intent to execute, returning to completed state after finishing.
     **When all task goals are met** (all goals in graph_overview are met): before issuing, first judge whether this intent implies a "new result to be achieved". If yes, restate your guessed goal to the human in one sentence and **ask whether to register it as a formal goal**—if yes, use set_goals to register (the task then enters regular planning and the planner autonomously proceeds); if no / just want to explore temporarily → only add_intent to issue this one, worker executes and task returns to completed state (will not autonomously continue). If this intent is obviously a one-time verification and does not imply a new goal, just add_intent directly, no need to ask every time.
   - Human wants "real-time course correction for a running intent (work) (don't go X anymore, focus on Y)" → use steer_work (no interruption, no loss of existing progress, takes effect before worker's next action); first use get_worker_output to see what it's doing. If the direction is entirely wrong, use add_intent to issue a new intent instead.
   - Human wants "add a final goal to achieve" → use set_goals to add goals. The system writes the goal into the task graph and **automatically pulls completed/paused tasks back into running state to continue** (the planner will then re-judge whether it's met), no need to manually click resume.
   - Human wants "add/change test constraints (allow/deny certain operations, e.g. 'only test current port', 'no brute force', 'passive reconnaissance only')" → use set_constraints to register (type=allow allow / type=deny deny). Constraints will be injected into planner/worker prompts at the next planning round to frame exploration boundaries; can also be added/edited/deleted in the overview "constraint management".
3. Reply concisely in human language, explaining what you did.

Current task goal: {{.Goal}}

Do not fabricate findings; answer only based on real data returned by tools.`

func mainAgentSystem(goal, dataDir, workDir string) string {
	body := renderSystem("mainagent", mainAgentDefaultTmpl, MainVars{Goal: goal, DataDir: dataDir, Now: nowStr()})
	return body + artifactSpec(workDir)
}

// Chat handles one human message and returns the assistant reply. emit, if
// non-nil, receives each execution step (thinking / tool_use / tool_result /
// text / result) so the main-agent session shows its work — exactly like the
// worker/planner sessions — not just the final answer.
func (m *MainAgent) Chat(ctx context.Context, taskID int64, mainSeg int, as *db.AssetStore, ts *db.ExplorationStore, goal, message string, emit func(db.Activity), notify, resume func(), notifyGoal, notifyHint func([]string)) (string, error) {
	tsx := NewToolSet(ts, "human")
	tsx.SetFindingRecorder(m.findingRecorder)
	if as != nil {
		tsx.SetAssetStore(as, as.Companies())
	}
	tsx.SetTaskID(taskID)
	tsx.SetCoverageEnabled(as == nil || as.CoverageEnabled(taskID))
	tsx.SetNotify(notify)         // generic wakeup (write ops without dedicated callbacks go here, debounced)
	tsx.SetResumeTask(resume)     // set_goals adds goals → pull completed/paused tasks back to running
	tsx.SetNotifyGoal(notifyGoal) // set_goals adds goals → give planner a "human added goals: ..." trigger
	tsx.SetNotifyHint(notifyHint) // add_hint adds hints → give planner a "human added N strategic hints: ..." trigger
	tsx.steerWork = m.steerWork   // enable steer_work tool (nil = unavailable)
	// Domain tools + basic default tool set (Read/Write/Edit/MultiEdit/LS/Glob/Grep/Bash)
	// When asset coverage is disabled, drop add_task_scope/list_untested_assets (not in prompt).
	base := append(tsx.DropCoverageTools(tsx.MainAgentTools()), actool.DefaultTools()...)
	ctx = WithRunInfo(ctx, RunInfo{TaskID: taskID, ExplorationID: explorationID(ts)})
	tools, def, cleanup := AugmentTools(ctx, "mainagent", base)
	defer cleanup()
	// This task's working directory <workDir>/tasks/<taskID>, create it first.
	mainDir := ensureRunDir(m.workDir, taskID, 0)
	ctx = intercept.WithReviewWorkingDirectory(ctx, mainDir)
	system, boundary := deferredSystem(mainAgentSystem(goal, m.workDir, mainDir), def)
	proxyAddr := m.proxyAddr
	if m.proxyAddrFn != nil {
		proxyAddr = m.proxyAddrFn()
	}
	opts := agentcore.Options{
		Provider:        m.prov,
		SystemPrompt:    system,
		DynamicBoundary: boundary,
		Tools:           append(tools, deepSeekTools(m.webSearch)...),
		DeferredTools:   def.Deferred,
		UnlockSet:       def.Unlock,
		PermissionMode:  permission.ModeBypass,
		EnableWebFetch:  true, // route via recording proxy for traces; load proxy CA to verify MITM-re-signed HTTPS certs
		WebFetchProxy:   proxyAddr,
		WebFetchCACert:  m.proxyCACert,
		// Web search (optional). ddgs needs no key; brave-free needs BraveKey; tavily needs TavilyKey.
		// WebSearchProxy is a separate egress proxy (http/https/socks5), unrelated to the traffic-recording MITM proxy; empty = direct.
		EnableWebSearch:    m.webSearch.Enabled && m.webSearch.Backend != "deepseek",
		WebSearchBackend:   m.webSearch.Backend,
		BraveSearchAPIKey:  m.webSearch.BraveKey,
		TavilySearchAPIKey: m.webSearch.TavilyKey,
		WebSearchProxy:     m.webSearch.Proxy,
		BashEnv:            proxyEnv(proxyAddr, m.proxyCACert), // Bash subprocesses default to proxy + trust CA
		WorkingDir:         mainDir,                            // this task's working directory <workDir>/tasks/<taskID>
		ToolOutputDir:      cmdOutDir(mainDir),
		MaxTurns:           m.maxTurns,                             // 0 = unlimited (configurable in agent management)
		Compaction:         compactionConfig(m.compactionWindow()), // long chats stay within the window
		Todos:              actool.NewTodoStore(),                  // session-level temp todos (TodoWrite), planning-only, discarded on exit
		// Hit budget (turns) → SDK runs wrap-up: output one progress summary sentence to user. Prompt and wrap-up turns editable in admin (default 10 turns).
		Settlement:   wrapupSettlement("mainagent", nil),
		NonStreaming: m.nonStreaming(), // when this profile selects non-streaming, use Provider.Complete
		MaxTokens:    m.maxTokens(),    // 0 = send no limit, let server default decide
	}
	if m.tx != nil { // persist raw human↔AI conversation; one accumulating file per segment
		opts.Transcript = m.tx
		// Segment 0 keeps the legacy "exp%d-main" name so existing transcripts still
		// load; each new session (seg>=1) gets its own file for a clean context.
		opts.SessionID = fmt.Sprintf("exp%d-main", ts.ID())
		if mainSeg > 0 {
			opts.SessionID = fmt.Sprintf("exp%d-main-s%d", ts.ID(), mainSeg)
		}
	}
	// Experimental: when enabled, noa takes over context compression (archives centralized in <workDir>/noa/<SessionID>, persistent).
	// session id follows transcript rules (segment-aware), so archiving aligns with restore.
	noaSession := fmt.Sprintf("exp%d-main", ts.ID())
	if mainSeg > 0 {
		noaSession = fmt.Sprintf("exp%d-main-s%d", ts.ID(), mainSeg)
	}
	enableNoa(&opts, m.noaEnabledFn, m.workDir, noaSession, noaWarn(noaSession))
	ctx = attachSideCapture(ctx, &opts)
	s := agentcore.NewSession(opts)
	defer s.Close()
	// reload the prior conversation from the transcript so the agent has context
	// across turns (each Chat is a fresh session; without this it can't see earlier
	// messages). First turn: no file yet → Resume loads nothing and proceeds.
	if m.tx != nil {
		_ = s.Resume(opts.SessionID)
	}
	// C2: this session is fresh each turn; re-unlock skill-gated MCPs from prior
	// Skill() calls in the reloaded history so revealed tools stay callable.
	seedUnlockFromHistory(s.Messages(), def.UnlockSkill)
	text, _, err := captureRunSession(ctx, s, message, func(r db.Activity) {
		if emit != nil {
			r.Worker = "mainagent"
			emit(r)
		}
	})
	return text, err
}
