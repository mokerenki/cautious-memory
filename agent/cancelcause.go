package agent

import (
	"context"
	"errors"
	"fmt"
)

// AbortCause names why an agent run's context was cancelled. Every cancellation
// site should attach one so the activity trace can report the real initiator.
type AbortCause struct {
	Code  string
	Short string
	Text  string
}

func (c *AbortCause) Error() string { return c.Text }

func cause(code, short, text string) *AbortCause {
	return &AbortCause{Code: code, Short: short, Text: text}
}

// Causef builds a cause that includes runtime-specific detail.
func Causef(code, short, format string, args ...any) *AbortCause {
	return &AbortCause{Code: code, Short: short, Text: fmt.Sprintf(format, args...)}
}

var (
	// Task-level execution context.
	AbortPausedBySchedule = cause("paused_by_schedule", "Schedule window ended, task paused", "Task will resume at the next schedule window, this execution has stopped")
	AbortPausedByProxy    = cause("paused_by_proxy", "Global proxy exception, task paused", "Global egress proxy connection failed, task paused; task will automatically continue after proxy recovers")
	AbortPausedByUser     = cause("paused_by_user", "User paused the task",
		"User paused the task via task control interface (POST /api/tasks/{id}/control, action=pause). This Planner/Worker run was actively cancelled; running intents will return to frontier(open), resume task and re-claim to re-execute from the beginning")
	AbortPausedByOrchestrator = cause("paused_by_orchestrator", "Orchestrator Agent paused the task",
		"Orchestrator Agent called pause_task tool to pause this task. This Planner/Worker run was actively cancelled; running intents will return to frontier(open), resume and re-execute")
	AbortTaskDeleted = cause("task_deleted", "Task deleted",
		"Task is being deleted (DELETE /api/tasks/{id}), deletion barrier has cancelled this task's running Planner, Worker and Main Agent; this run result will not be used again")
	AbortPausedOnReload = cause("paused_on_reload", "Backend restored task pause state",
		"Backend restored task pause state based on persisted state in database at startup. This run was cancelled; normally no Agent is running during recovery phase")
	AbortGoalMet = cause("goal_met", "Planner determined task goal achieved",
		"Planner determined task goal achieved and set task to done, then cancelled still-running Workers; these intents are marked as stopped, not failed")
	AbortSettleDrainTimeout = cause("settle_drain_timeout", "Task timeout drain wait exhausted",
		"After task reached timeout, waited for running Worker to gracefully finish, but 90 second drain grace period was still insufficient, so hard cancel was executed; intents are marked as exhausted, facts and assets written during finalization phase are retained")

	// Per-work context.
	AbortKilledByPlanner = cause("killed_by_planner", "Planner terminated this intent",
		"Planner called kill_work to actively terminate this intent, usually indicates direction went off track or no further value; intent marked as stopped, will not be automatically re-claimed")
	AbortWorkPausedByUser = cause("work_paused_by_user", "User paused this Worker intent",
		"User paused the running Worker. This call was cancelled, intent turned to paused; registered intents, facts, findings and activity records are all retained; resume from the beginning after recovery")
	AbortWorkCancelledByUser = cause("work_cancelled_by_user", "User deleted this Worker intent",
		"User deleted the running Worker. This call was cancelled; after Worker exits the write area, server handles this intent according to user's selected deletion mode—soft delete only marks as deleted and retains all output, hard delete cascades to remove this intent and downstream nodes supported only by it")
	AbortWorkFinished = cause("work_finished", "Worker finished and released context",
		"Worker normally finished, engine releases context resources in detachWork. This is not a run interruption; if it appears in interruption messages, it means cancellation and finalization events had a race")
	AbortPausedRaceGuard = cause("paused_race_guard", "Task paused, refusing to start new run",
		"When task is paused, engine refuses to issue new execution context to prevent race between claim and pause causing Worker to continue starting; claimed intents will return to frontier")

	// Main Agent and standalone conversation contexts.
	AbortChatStoppedByUser = cause("chat_stopped_by_user", "User stopped this round of conversation",
		"User clicked stop, actively aborting this round of Main Agent or Session Agent run. Activity records already generated are retained; you can continue sending the next message")
	AbortChatPausedWithTask = cause("chat_paused_with_task", "Task paused, conversation aborted",
		"When user paused task, running Main Agent conversation was also synchronously cancelled. Activity records already generated are retained; after task resumes, this round of messages will not be automatically replayed")
	AbortChatTurnFinished = cause("chat_turn_finished", "Conversation finished, context released",
		"This round of conversation normally finished, server is releasing this round's context resources. This is not a run interruption; if it appears in interruption messages, it means cancellation and finalization events had a race")

	// Process-level and per-run hard backstop.
	AbortShutdown = cause("shutdown", "Backend process is shutting down",
		"Backend process received SIGINT or SIGTERM, restarting, updating or shutting down. All running Agents will be cancelled; after restart, residual running intents will be reset to open and re-executed")
	AbortRunHardTimeout = cause("run_hard_timeout", "Run hard timeout backstop triggered",
		"Single run exceeded soft wall clock budget plus extra grace, indicating model request or some tool has not returned for a long time, preventing normal round boundary finalization. Please focus on checking the last unreturned tool call before the interruption")
)

// AbortReason resolves the named cause attached to a cancelled run context.
func AbortReason(ctx context.Context) (code, short, text string, ok bool) {
	c := context.Cause(ctx)
	if c == nil {
		return "", "", "", false
	}
	var ac *AbortCause
	if errors.As(c, &ac) {
		return ac.Code, ac.Short, ac.Text, true
	}
	switch {
	case errors.Is(c, context.DeadlineExceeded):
		return "deadline_exceeded", "Upstream context reached deadline",
			"Upstream context reached deadline, but the setter did not attach a named reason via WithTimeoutCause: " + c.Error(), true
		case errors.Is(c, context.Canceled):
			return "canceled_no_cause", "Canceller did not attach a named reason",
				"Upstream context was cancelled, but the canceller did not attach a named reason via context.WithCancelCause; please register the reason in agent/cancelcause.go and connect it at this cancellation point", true
	default:
		return "other", firstLine(c.Error(), 80), c.Error(), true
	}
}
