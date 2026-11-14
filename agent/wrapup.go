package agent

import (
	"strings"

	"github.com/Autumn-27/norma/harness"
)

// wrap-up prompt (settlement prompt): when an agent is terminated due to [steps exhausted (MaxTurns)] or
// [timeout (run_seconds/MaxDuration)], the SDK's settlement phase injects this prompt,
// so the agent first persists identified-but-unwritten content, then outputs a summary sentence to avoid tail-loss.
//
// each agent's wrap-up prompt can be overridden in the admin UI (stored in agents.wrapup_prompt), empty falls back to
// the built-in default here. only [prompt body] is editable; which tools to disable and how many wrap-up turns are code-fixed policy.

// WrapupOverride, if set, returns the stored wrap-up prompt for an agent key and
// whether a non-empty one exists. Wired by the server to the agents table (like
// PromptOverride for system prompts). nil / empty → the built-in default is used.
var WrapupOverride func(agentKey string) (string, bool)

// WrapupMaxTurnsOverride, if set, returns the admin-configured turn budget for the
// wrap-up phase of an agent and whether a positive one exists. Wired to the agents
// table. nil / ≤0 → the built-in per-agent default (wrapupTurnDefaults) is used.
var WrapupMaxTurnsOverride func(agentKey string) (int, bool)

// built-in default wrap-up prompts, indexed by agent key. worker reuses the historically hardcoded settleWrapUpPrompt
// (defined in worker.go), planner/mainagent each have one; misses (custom agents) fall through to generic fallback.
var wrapupDefaults = map[string]string{
	"worker":    settleWrapUpPrompt,
	"planner":   plannerWrapUpDefault,
	"mainagent": mainAgentWrapUpDefault,
}

// wrapupTurnDefaults: each agent's wrap-up phase turn budget built-in default (overridable by admin >0).
// all get 10 turns to ensure wrap-up has enough steps to persist. misses use genericWrapupTurns.
var wrapupTurnDefaults = map[string]int{
	"worker":    10,
	"planner":   10,
	"mainagent": 10,
}

const genericWrapupTurns = 10

const plannerWrapUpDefault = "Your steps for this round are about to be used up—note it's only [this round] ending, the system will still wake you up again as situation changes to continue planning, not task termination, you don't need to converge the entire planning here. Please land conclusions you've already figured out this round, don't let this round run in vain, but also [do not force-fabricate intents for wrap-up] (0 intents this round is still a completely normal result): (1) if you've judged [exploration directions that should be dispatched now], batch submit via one add_intent (don't hold back what you've thought of); (2) for goals already achieved by certain discoveries/facts, call prove_goal to mark met (don't miss judgments); (3) if you identify serial exploit chains needing step-by-step dispatch, record with TodoWrite for easy continuation on next wake-up. After done, end this round directly, no need to output summary text."

const mainAgentWrapUpDefault = "Your steps are about to be used up, this interaction is about to end. Do not initiate new exploration/operations. Please **summarize current progress, key conclusions, and suggested next steps to the user in one standalone plain-text sentence**."

const genericWrapUpDefault = "You are about to be terminated due to budget exhaustion. Please first write back results already completed but not persisted, then **summarize what you did and what key conclusions you reached in one standalone plain-text sentence** (this sentence will be displayed as this run's result)."

// WrapupDefault returns the built-in default wrap-up prompt for an agent key —
// used by the admin UI as the "restore default" value and empty-field placeholder.
func WrapupDefault(agentKey string) string {
	if d, ok := wrapupDefaults[agentKey]; ok {
		return d
	}
	return genericWrapUpDefault
}

// WrapupTurnsDefault returns the built-in wrap-up turn budget for an agent key —
// used by the admin UI as the "0 = default N" hint.
func WrapupTurnsDefault(agentKey string) int {
	if n, ok := wrapupTurnDefaults[agentKey]; ok {
		return n
	}
	return genericWrapupTurns
}

// resolveWrapup returns the effective wrap-up prompt: the DB override (if set and
// non-empty) over the built-in default.
func resolveWrapup(agentKey string) string {
	if WrapupOverride != nil {
		if t, ok := WrapupOverride(agentKey); ok && strings.TrimSpace(t) != "" {
			return t
		}
	}
	return WrapupDefault(agentKey)
}

// resolveWrapupTurns returns the effective wrap-up turn budget: a positive DB
// override over the built-in per-agent default.
func resolveWrapupTurns(agentKey string) int {
	if WrapupMaxTurnsOverride != nil {
		if v, ok := WrapupMaxTurnsOverride(agentKey); ok && v > 0 {
			return v
		}
	}
	return WrapupTurnsDefault(agentKey)
}

// wrapupSettlement builds the settlement config for an agent's run. Prompt and the
// turn budget are admin-editable per agent; disabled tools are code-owned policy so
// a user can't edit away the "stop probing" guardrail. Resolved fresh each run
// (reads DB live), so edits apply on the next run without a restart.
func wrapupSettlement(agentKey string, disabledTools []string) *harness.Settlement {
	return &harness.Settlement{
		Prompt:        resolveWrapup(agentKey),
		DisabledTools: disabledTools,
		MaxTurns:      resolveWrapupTurns(agentKey),
	}
}

// ---------- task-level timeout wrap-up (see docs/task-timeout-and-wrapup-design.md) ----------
//
// separate from per-run wrap-up: per-run = "this run's budget exhausted"; task timeout = "entire task due, ending".
// semantics often opposite (especially planner: per-run says "don't stop, keep planning"; task timeout says "time's up, stop planning, make final judgment"). only configured for worker/planner.
//
// WrapupTaskTimeoutOverride / …TurnsOverride: task timeout wrap-up prompt and turns DB override
// (wired to agents.task_timeout_wrapup_prompt / _max_turns, worker/planner only).
var (
	WrapupTaskTimeoutOverride      func(agentKey string) (string, bool)
	WrapupTaskTimeoutTurnsOverride func(agentKey string) (int, bool)
)

var taskTimeoutWrapupDefaults = map[string]string{
	"worker":  workerTaskTimeoutDefault,
	"planner": plannerTaskTimeoutDefault,
}

const workerTaskTimeoutDefault = "**The entire task has reached the timeout limit and is about to end** (not your run's budget, but the whole exploration is due). Last chance: (1) [all] content you've identified but not yet written back must be persisted—new assets insert_assets, exploration conclusions/facts record_fact, confirmed vulnerabilities report_finding; (2) do not start any new commands/probes; (3) **finally summarize key conclusions on this intent in one standalone plain-text sentence**."

const plannerTaskTimeoutDefault = "**The entire task has reached the timeout limit and is about to end** (not this round, but the entire task termination). Please make the final goal judgment based on [all] facts and findings currently: for goals proven achieved by evidence, call prove_goal to mark met (don't miss judgments). **Do not generate any new intents** (dispatching intents now won't be executed anymore). After judging, conclude directly, no need to output summary text."

// TaskTimeoutWrapupDefault returns an agent's built-in task timeout wrap-up default (for admin UI placeholder/restore default).
func TaskTimeoutWrapupDefault(agentKey string) string {
	return taskTimeoutWrapupDefaults[agentKey] // unconfigured (mainagent/chat) returns empty
}

// resolveTaskTimeoutWrapup: DB override (non-empty) > built-in default. empty means agent has no task timeout prompt
// (non worker/planner), caller should fall back to per-run prompt.
func resolveTaskTimeoutWrapup(agentKey string) string {
	if WrapupTaskTimeoutOverride != nil {
		if t, ok := WrapupTaskTimeoutOverride(agentKey); ok && strings.TrimSpace(t) != "" {
			return t
		}
	}
	return TaskTimeoutWrapupDefault(agentKey)
}

func resolveTaskTimeoutTurns(agentKey string) int {
	if WrapupTaskTimeoutTurnsOverride != nil {
		if v, ok := WrapupTaskTimeoutTurnsOverride(agentKey); ok && v > 0 {
			return v
		}
	}
	return resolveWrapupTurns(agentKey) // defaults to per-run turns
}

// wrapupSettlementForTask builds settlement for a worker/planner run that is aware
// of the task deadline. See §5 of the design doc:
//   - clamped=true  → this run clamped by task deadline: Timeout wrap-up = task due → task timeout prompt;
//     MaxTurns wrap-up = steps exhausted within clamp window, task has minutes left → fall back to per-run prompt.
//   - clamped=false → task not due yet: both reasons use per-run prompt (degrades to wrapupSettlement).
//
// handed to harness, PromptByReason picks by [actual] reason at settlement time, no build-time mismatch.
func wrapupSettlementForTask(agentKey string, disabledTools []string, clamped bool) *harness.Settlement {
	perRun := resolveWrapup(agentKey)
	st := &harness.Settlement{
		Prompt:        perRun, // fallback (also value for both reasons when not clamped)
		DisabledTools: disabledTools,
		MaxTurns:      resolveWrapupTurns(agentKey),
	}
	if clamped {
		if tt := resolveTaskTimeoutWrapup(agentKey); tt != "" {
			st.PromptByReason = map[harness.TerminalReason]string{
				harness.ReasonTimeout:  tt,     // task due
				harness.ReasonMaxTurns: perRun, // steps exhausted first, task has time left
			}
			st.MaxTurns = resolveTaskTimeoutTurns(agentKey)
		}
	}
	return st
}
