package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Autumn-27/norma/harness"
)

// runTrace retains the latest tool call so an interrupted run can identify the
// operation that was still in flight.
type runTrace struct {
	startedAt time.Time
	id        string
	name      string
	input     string
	at        time.Time
	pending   bool
}

func (t *runTrace) start(id, name, input string) {
	t.id, t.name, t.input, t.at, t.pending = id, name, input, time.Now(), true
}

func (t *runTrace) done(id string) {
	if id == t.id {
		t.pending = false
	}
}

var reasonHint = map[harness.TerminalReason]string{
	harness.ReasonCompleted:         "The model ended this round normally but left no text summary; facts and assets are based on this round's tool call records",
	harness.ReasonMaxTurns:          "Max turns reached: SDK performed wrap-up and wrote back facts and assets; intent marked as exhausted for the planner to change direction, not treated as failure",
	harness.ReasonTimeout:           "Max duration budget reached: running tools are interrupted and wrap-up is performed in place, identified facts and assets are written back, intent marked as exhausted",
	harness.ReasonModelError:        "Model or API call failed (network, auth, rate limiting, provider 5xx, etc.); after retries exhausted the intent is marked blocked—a transport-layer fault means this intent was essentially not explored; check its execution (get_worker_trace) before deciding to resend or change approach",
	harness.ReasonBlockingLimit:     "Context length hit hard limit; request was blocked before being sent; narrow intent granularity or compress tool returns",
	harness.ReasonPromptTooLong:     "Prompt too long and context compression retries exhausted; unable to continue execution",
	harness.ReasonImageError:        "Current model does not support multimodal content this round; switch to a vision-capable model or avoid tools returning images",
	harness.ReasonStopHookPrevented: "Stop hook prevented this round from ending and could not continue; check whether task Guard rules are too strict",
	harness.ReasonHookStopped:       "Tool or hook actively stopped continued execution, e.g. out-of-scope target or disabled command; check the interception note on the last tool_result",
	harness.ReasonAbortedStreaming:  "Run was cancelled during model output streaming stage",
	harness.ReasonAbortedTools:      "Run was cancelled during tool execution stage",
}

// terminalText renders a terminal event with no final text into a compact summary
// and a Markdown detail block.
func terminalText(ctx context.Context, term *harness.Terminal, tr *runTrace) (string, string) {
	reason := term.Reason
	aborted := reason == harness.ReasonAbortedStreaming || reason == harness.ReasonAbortedTools
	// Prompt may return ctx.Err directly without a terminal event. Preserve the
	// cancellation cause instead of falling back to an empty/unknown terminal reason.
	if reason == "" && ctx.Err() != nil {
		aborted = true
	}

	var sum string
	if aborted {
		_, short, _, ok := AbortReason(ctx)
		if !ok {
			short = "unable to determine cancellation reason"
		}
		stage := "during execution"
		switch reason {
		case harness.ReasonAbortedStreaming:
			stage = "during model output streaming"
		case harness.ReasonAbortedTools:
			stage = "during tool execution"
		}
		sum = "(Run interrupted: " + short + "; stopped at " + stage + progressSuffix(term, tr) + ", incomplete)"
	} else if reason == harness.ReasonMaxTurns || reason == harness.ReasonTimeout {
		sum = "(Run budget limit reached (" + string(reason) + "), wrap-up wrote back facts" + progressSuffix(term, tr) + "; no text summary this run)"
	} else {
		hint := terminalReasonHint(reason)
		sum = "(No text summary, terminal state " + terminalReasonLabel(reason) + ": " + firstLine(hint, 80) + ")"
	}

	var b strings.Builder
	b.WriteString(sum)
	b.WriteString("\n\n")
	displayReason := terminalReasonLabel(reason)
	fmt.Fprintf(&b, "- **Terminal state**: `%s` - %s\n", displayReason, terminalReasonHint(reason))
	if aborted {
		code, _, why, ok := AbortReason(ctx)
		if ok {
			fmt.Fprintf(&b, "- **Interrupt reason** (`%s`): %s\n", code, why)
		} else {
			b.WriteString("- **Interrupt reason**: unable to determine; the cancelling party may not have attached a named reason via context.WithCancelCause\n")
		}
	}
	if term.Err != nil {
		fmt.Fprintf(&b, "- **Underlying error**: `%v`\n", term.Err)
	}
	if aborted && strings.TrimSpace(term.Text) != "" {
		b.WriteString("- **Partial output generated before cancellation**:\n\n")
		b.WriteString(term.Text)
		b.WriteString("\n\n")
	}
	if term.Turns > 0 {
		fmt.Fprintf(&b, "- **Completed**: %d model turns\n", term.Turns)
	}
	if !tr.startedAt.IsZero() {
		fmt.Fprintf(&b, "- **Run duration**: %s\n", roundDur(time.Since(tr.startedAt)))
	}
	if u := term.Usage; u.InputTokens+u.OutputTokens+u.CacheReadTokens+u.CacheWriteTokens > 0 {
		fmt.Fprintf(&b, "- **Cumulative tokens**: input %d / output %d / cache read %d / cache write %d\n",
			u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheWriteTokens)
	}
	if tr.name == "" {
		b.WriteString("- **Tool calls**: this run ended without issuing any tool calls\n")
	} else if tr.pending {
		fmt.Fprintf(&b, "- **Tool in progress at interruption**: `%s` (ran for %s, **no result returned**)\n\n  ```json\n  %s\n  ```\n",
			tr.name, roundDur(time.Since(tr.at)), firstLine(tr.input, 300))
	} else {
		fmt.Fprintf(&b, "- **Last tool before interruption**: `%s` (returned normally)\n", tr.name)
	}
	return sum, b.String()
}

func terminalReasonLabel(reason harness.TerminalReason) string {
	if reason == "" {
		return "context_canceled"
	}
	return string(reason)
}

func terminalReasonHint(reason harness.TerminalReason) string {
	if hint := reasonHint[reason]; hint != "" {
		return hint
	}
	if reason == "" {
		return "Run's context was cancelled but the underlying layer produced no Terminal event"
	}
	return "Unknown terminal state; harness may have added a new TerminalReason, please add to reasonHint"
}

func progressSuffix(term *harness.Terminal, tr *runTrace) string {
	var parts []string
	if term.Turns > 0 {
		parts = append(parts, fmt.Sprintf("%d turns", term.Turns))
	}
	if !tr.startedAt.IsZero() {
		parts = append(parts, roundDur(time.Since(tr.startedAt)))
	}
	if len(parts) == 0 {
		return ""
	}
	return ", ran " + strings.Join(parts, " / ")
}

func roundDur(d time.Duration) string {
	switch {
	case d < time.Minute:
		return d.Round(100 * time.Millisecond).String()
	case d < time.Hour:
		return d.Round(time.Second).String()
	default:
		return d.Round(time.Minute).String()
	}
}
