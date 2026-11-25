package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Autumn-27/cautiousmemory/agent"
	"github.com/Autumn-27/cautiousmemory/db"
	"github.com/Autumn-27/cautiousmemory/intercept"
	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/llm"
	"github.com/jackc/pgx/v5/pgconn"
)

// isFKViolation reports whether err is a Postgres foreign-key violation (SQLSTATE
// 23503) â€” e.g. an activity insert whose exploration_id has no parent row.
func isFKViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// dropReason classifies why an activity write was dropped, so the log can be
// grouped/analysed by cause rather than by raw error text.
func dropReason(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23503":
			return "fk_violation(23503, parent exploration does not exist)"
		case "23505":
			return "unique_violation(23505)"
		default:
			return "pg_error(" + pgErr.Code + ")"
		}
	}
	return "write_error"
}

// bumpDrop increments and returns the running count of dropped (unpersistable)
// activity records for a task. Concurrent planner + worker emits race here, so the
// counter is an atomic behind sync.Map. The count in the log shows loss scale at a
// glance instead of forcing a grep-and-count.
func (e *Engine) bumpDrop(taskID string) int64 {
	v, _ := e.dropCnt.LoadOrStore(taskID, new(int64))
	return atomic.AddInt64(v.(*int64), 1)
}

// preview collapses newlines and trims s to a short rune-safe snippet for one-line
// log output (avoids dumping a multi-KB summary/detail into the log).
func preview(s string, n int) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "â€¦"
	}
	return string(r)
}

// model_error (provider/API failure: transient retries exhausted at the LLM layer, or the stream dropped after it started)
// A work item that ends this way is neither â€œtried but unfinishedâ€ nor a true failure; itâ€™s an external disruption. Treating it as permanently
// blocked would silently discard an intent, so retry this terminal state a few extra times, with backoff between attempts, to give the
// provider time to recover; if paused/terminated/canceled during retries, immediately defer to the corresponding branch.
const (
	modelErrorRetries      = 2               // Number of extra retries after model_error cleanup
	modelErrorRetryBackoff = 3 * time.Second // Backoff before each retry
	workControlWaitTimeout = 30 * time.Second
)

var errWorkControlConflict = errors.New("work control conflict")

// retryableWorkerModelError excludes errors already handled by the task router.
// In particular, a quota error after partial streaming advances the task cursor
// for the next LLM call but must not replay this whole intent on the backup.
func retryableWorkerModelError(reason harness.TerminalReason, err error) bool {
	return reason == harness.ReasonModelError && !isTaskLLMRuntimeError(err)
}

// Engine drives the event-driven exploration loop with real LLM agents
// (docs Â§4.3/Â§4.4): on asset/exploration-graph change (debounced) it wakes the
// planner, which reads the route, queries assets, judges goals and emits intents;
// N concurrent work agents claim intents and execute them. There is no
// simulation mode â€” an LLM provider is required. The planner/worker can be
// (re)installed at runtime (LLM configured from the UI); the loops always run
// but idle until an LLM is set.
type Engine struct {
	m        *Manager
	debounce time.Duration

	bc *Broadcaster // live activity pub/sub (SSE)

	started  sync.Map // taskID -> bool, so Run is idempotent per task
	lastAct  sync.Map // taskID -> int64 unix, last planner/worker activity (heartbeat)
	llmCalls sync.Map // taskID -> *int64, actual planner/worker/main-agent LLM calls
	paused   sync.Map // taskID -> bool, user-paused (planner + workers idle but loops alive)
	deleting sync.Map // taskID -> bool, delete barrier (no new task-owned writes)
	dropCnt  sync.Map // taskID -> *int64, running count of dropped (unpersistable) activity records

	// deleteMu makes installing the delete barrier atomic with registering a new
	// task operation. Once BeginDelete returns, every admitted writer is reflected
	// in inflight and every later writer is rejected.
	deleteMu sync.RWMutex

	// Every long-lived task goroutine (planner, workers and deadline coordinator)
	// runs under one task-scoped context. Successful deletion cancels that context,
	// waits for all goroutines, then releases every task-level Engine reference.
	runtimeMu sync.Mutex
	runtimes  map[string]*taskRuntime

	// per-task execution context: each planner.Plan / worker.Execute runs under it,
	// so pausing can CANCEL an in-flight run (not just skip the next one). Recreated
	// on resume since cancelling is one-shot. Every cancellation carries a named
	// cause so the activity trace can identify the initiating control path.
	execMu     sync.Mutex
	execCancel map[string]context.CancelCauseFunc
	execCtx    map[string]context.Context

	// Per-work control lets the planner kill a worker and lets the UI pause/cancel
	// one intent without pausing the whole task. The done channel closes only after
	// runWorkerStep has stopped writing and committed its final state.
	workMu sync.Mutex
	work   map[int64]*workExecution

	// steerBox queues planner course-corrections for a running work (keyed by intent
	// id). The worker's PreToolUse hook drains it before its next tool call and hands
	// the message to the model (blocking that call) so it re-plans â€” no kill needed.
	steerMu  sync.Mutex
	steerBox map[int64][]string

	plannerRound sync.Map // taskID -> int, planner round counter (for UI round separators)

	// Task-level timeout (see the task timeout and wrap-up design document):
	settling     sync.Map // taskID -> bool, whether the task has entered the wrap-up sequence (stop dispatching/claiming new intents)
	deadline     sync.Map // taskID -> int64 unix, absolute deadline (stamped on first run; 0/default = unlimited)
	stamped      sync.Map // taskID -> bool, whether first_run_at has been stamped (stamped only once per process)
	inflight     sync.Map // taskID -> *int64, count of running planner.Plan + worker.Execute (for drain)
	coordStarted sync.Map // taskID -> bool, whether the deadline coordinator has started (deduplicate Run/reload)

	// resolve returns a task's dedicated planner/worker (wired by the server as the
	// authoritative task-router). nil,nil means this task is deliberately unavailable
	// (for example an exhausted failover chain) â€” there is no global-pair fallback.
	resolve              func(t *Task) (*agent.Planner, *agent.Worker)
	resolveAuthoritative bool
	workerResolve        func(t *Task, slot WorkerSlot) (*agent.Worker, error)
	slotState            sync.Map // task/slot -> slotAvailability
	slotPlans            sync.Map // task -> map[slot name]profile ID; immutable for a run
	// readiness reports whether a global LLM provider is configured â€” the signal behind
	// Ready()/the llm_configured indicator. Wired once at startup; nil â†’ not ready.
	readiness    func() bool
	proxyFailure func(taskID string, err error)
}

func (e *Engine) SetProxyFailureHandler(fn func(string, error)) { e.proxyFailure = fn }

type slotAvailability struct {
	available bool
	lastEmit  time.Time
}

type taskRuntime struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type workExecution struct {
	cancel context.CancelCauseFunc
	done   chan error
	action string // user action: pause | cancel
}

// nextPlannerRound returns the next planner round number for a task (1-based).
func (e *Engine) nextPlannerRound(taskID string) int {
	v, _ := e.plannerRound.LoadOrStore(taskID, 0)
	n := v.(int) + 1
	e.plannerRound.Store(taskID, n)
	return n
}

// Pause stops a task: marks it paused AND cancels any in-flight planner/worker run
// for it (a long worker.Execute would otherwise keep going until it finishes).
func (e *Engine) Pause(taskID string, cause error) {
	e.paused.Store(taskID, true)
	e.cancelExec(taskID, cause)
}

// BeginDelete installs an execution barrier before task data/files are removed.
// The temporary pause is not a user pause. The server serializes this transition
// with lifecycle admission and tells AbortDelete whether the persisted task is
// paused/queued if cleanup fails.
func (e *Engine) BeginDelete(taskID string) bool {
	e.deleteMu.Lock()
	if _, loaded := e.deleting.LoadOrStore(taskID, true); loaded {
		e.deleteMu.Unlock()
		return false
	}
	e.paused.Store(taskID, true)
	e.deleteMu.Unlock()
	e.cancelExec(taskID, agent.AbortTaskDeleted)
	return true
}

func (e *Engine) AbortDelete(taskID string, keepPaused bool) {
	e.deleteMu.Lock()
	if !e.IsDeleting(taskID) {
		e.deleteMu.Unlock()
		return
	}
	e.deleting.Delete(taskID)
	if !keepPaused {
		e.paused.Delete(taskID)
	}
	e.deleteMu.Unlock()
	if !keepPaused && e.m != nil {
		if t, ok := e.m.Task(taskID); ok {
			t.Notify()
		}
	}
}

func (e *Engine) IsDeleting(taskID string) bool {
	_, ok := e.deleting.Load(taskID)
	return ok
}

// registerTaskRoutines reserves count goroutines in the task runtime. Callers
// hold deleteMu for reading so StopTask cannot race WaitGroup.Add with Wait.
func (e *Engine) registerTaskRoutines(parent context.Context, taskID string, count int) *taskRuntime {
	e.runtimeMu.Lock()
	defer e.runtimeMu.Unlock()
	rt := e.runtimes[taskID]
	if rt == nil {
		ctx, cancel := context.WithCancel(parent)
		rt = &taskRuntime{ctx: ctx, cancel: cancel}
		e.runtimes[taskID] = rt
	}
	rt.wg.Add(count)
	return rt
}

func runTaskRoutine(rt *taskRuntime, fn func(context.Context)) {
	go func() {
		defer rt.wg.Done()
		fn(rt.ctx)
	}()
}

// StopTask permanently stops every long-lived goroutine and removes all Engine
// state for a successfully deleted task. The delete barrier remains installed
// until cleanup finishes, so no new task operation can race the teardown.
func (e *Engine) StopTask(taskID string) {
	e.deleteMu.Lock()
	e.deleting.Store(taskID, true)
	e.deleteMu.Unlock()

	e.cancelExec(taskID, agent.AbortTaskDeleted)
	e.runtimeMu.Lock()
	rt := e.runtimes[taskID]
	if rt != nil {
		rt.cancel()
	}
	e.runtimeMu.Unlock()
	if rt != nil {
		rt.wg.Wait()
	}

	e.execMu.Lock()
	if cancel := e.execCancel[taskID]; cancel != nil {
		cancel(agent.AbortTaskDeleted)
	}
	delete(e.execCancel, taskID)
	delete(e.execCtx, taskID)
	e.execMu.Unlock()

	e.runtimeMu.Lock()
	if e.runtimes[taskID] == rt {
		delete(e.runtimes, taskID)
	}
	e.runtimeMu.Unlock()

	e.started.Delete(taskID)
	e.lastAct.Delete(taskID)
	e.llmCalls.Delete(taskID)
	e.paused.Delete(taskID)
	e.dropCnt.Delete(taskID)
	e.plannerRound.Delete(taskID)
	e.settling.Delete(taskID)
	e.deadline.Delete(taskID)
	e.stamped.Delete(taskID)
	e.inflight.Delete(taskID)
	e.coordStarted.Delete(taskID)
	e.slotPlans.Delete(taskID)
	e.slotState.Range(func(key, _ any) bool {
		keyText, ok := key.(string)
		if ok && strings.HasPrefix(keyText, taskID+"/") {
			e.slotState.Delete(key)
		}
		return true
	})
	e.deleteMu.Lock()
	e.deleting.Delete(taskID)
	e.deleteMu.Unlock()
}

// cancelExec cancels a task's current per-task exec context (any in-flight
// planner.Plan / worker.Execute), if present. Shared by Pause and the settle
// sequence's hard-drain backstop.
func (e *Engine) cancelExec(taskID string, cause error) {
	e.execMu.Lock()
	if cancel := e.execCancel[taskID]; cancel != nil {
		cancel(cause)
	}
	e.execMu.Unlock()
}

// Resume un-pauses a task and nudges a fresh planning round. The next exec under
// it gets a fresh (uncancelled) context.
func (e *Engine) Resume(t *Task) {
	// BeginDelete owns the pause barrier once deletion starts. A concurrent
	// resume must never clear it and let a planner/worker re-enter while cleanup
	// is waiting for task operations to drain.
	if t == nil {
		return
	}
	e.deleteMu.RLock()
	defer e.deleteMu.RUnlock()
	if e.IsDeleting(t.ID) {
		return
	}
	e.paused.Delete(t.ID)
	t.Notify()
}

// execContextFor returns a live per-task context derived from parent, recreating
// it if a prior pause cancelled it.
func (e *Engine) execContextFor(parent context.Context, taskID string) context.Context {
	e.execMu.Lock()
	defer e.execMu.Unlock()
	if e.IsPaused(taskID) {
		// never hand out a live context while paused (guards the claimâ†’Execute race)
		c, cancel := context.WithCancelCause(parent)
		cancel(agent.AbortPausedRaceGuard)
		return c
	}
	if c := e.execCtx[taskID]; c != nil && c.Err() == nil {
		return c
	}
	c, cancel := context.WithCancelCause(parent)
	e.execCtx[taskID] = c
	e.execCancel[taskID] = cancel
	return c
}

// IsPaused reports whether a task is user-paused.
func (e *Engine) IsPaused(taskID string) bool {
	v, ok := e.paused.Load(taskID)
	return ok && v.(bool)
}

// Started reports whether the engine loops are running for a task.
func (e *Engine) Started(taskID string) bool {
	_, ok := e.started.Load(taskID)
	return ok
}

// LastActivity returns the unix time of the last planner/worker activity for a
// task (0 if none yet).
func (e *Engine) LastActivity(taskID string) int64 {
	if v, ok := e.lastAct.Load(taskID); ok {
		return v.(int64)
	}
	return 0
}

// BeginLLMCall/EndLLMCall track actual provider calls separately from the
// scheduler's task-operation counter. A task can have live loops while all of
// them are waiting for a trigger; that state must remain idle in the UI.
func (e *Engine) BeginLLMCall(taskID string) {
	v, _ := e.llmCalls.LoadOrStore(taskID, new(int64))
	atomic.AddInt64(v.(*int64), 1)
}

func (e *Engine) EndLLMCall(taskID string) {
	if v, ok := e.llmCalls.Load(taskID); ok {
		p := v.(*int64)
		if atomic.AddInt64(p, -1) <= 0 {
			atomic.StoreInt64(p, 0)
		}
	}
}

func (e *Engine) ActiveLLMCalls(taskID string) int64 {
	if v, ok := e.llmCalls.Load(taskID); ok {
		return atomic.LoadInt64(v.(*int64))
	}
	return 0
}

func (e *Engine) touch(taskID string) { e.lastAct.Store(taskID, time.Now().Unix()) }

func NewEngine(m *Manager) *Engine {
	return &Engine{m: m, debounce: 800 * time.Millisecond, bc: NewBroadcaster(),
		execCancel: map[string]context.CancelCauseFunc{}, execCtx: map[string]context.Context{},
		work: map[int64]*workExecution{}, steerBox: map[int64][]string{},
		runtimes: map[string]*taskRuntime{}}
}

// registerWork records the cancel for the work currently running intentID.
func (e *Engine) registerWork(intentID int64, cancel context.CancelCauseFunc) {
	e.workMu.Lock()
	e.work[intentID] = &workExecution{cancel: cancel, done: make(chan error, 1)}
	e.workMu.Unlock()
}

// detachWork removes the live control handle once Execute has returned. complete
// must be called after the final intent state write so a waiting cancel handler can
// safely delete the worker's blackboard output without racing a late write.
func (e *Engine) detachWork(intentID int64) (action string, complete func(error)) {
	e.workMu.Lock()
	run := e.work[intentID]
	if run != nil {
		delete(e.work, intentID)
		action = run.action
		run.cancel(agent.AbortWorkFinished) // release resources (no-op if already cancelled)
	}
	e.workMu.Unlock()
	e.steerMu.Lock()
	delete(e.steerBox, intentID) // drop any undelivered steering for a finished work
	e.steerMu.Unlock()
	if run == nil {
		return action, func(error) {}
	}
	return action, func(err error) { run.done <- err }
}

// ControlWork requests a user-visible pause or cancellation and waits until the
// worker has fully stopped writing. Cancellation cleanup is performed by the API
// handler after this returns; pause state is committed by runWorkerStep itself.
func (e *Engine) ControlWork(ctx context.Context, intentID int64, action string) error {
	if action != "pause" && action != "cancel" {
		return fmt.Errorf("unsupported work action %q", action)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	e.workMu.Lock()
	run := e.work[intentID]
	if run == nil {
		e.workMu.Unlock()
		return fmt.Errorf("%w: Intent %d has no running work (may have ended or not been claimed)", errWorkControlConflict, intentID)
	}
	if run.action != "" {
		e.workMu.Unlock()
		return fmt.Errorf("%w: Intent %d is executing %s operation", errWorkControlConflict, intentID, run.action)
	}
	run.action = action
	done := run.done
	cause := error(agent.AbortWorkPausedByUser)
	if action == "cancel" {
		cause = agent.AbortWorkCancelledByUser
	}
	run.cancel(cause)
	e.workMu.Unlock()

	timer := time.NewTimer(workControlWaitTimeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		e.releaseWorkControl(intentID, run, action)
		return fmt.Errorf("Waiting for intent %d %s to finish: %w", intentID, action, ctx.Err())
	case <-timer.C:
		e.releaseWorkControl(intentID, run, action)
		return fmt.Errorf("Waiting for intent %d %s to finish: %w", intentID, action, context.DeadlineExceeded)
	}
}

// releaseWorkControl drops only this caller's reservation after its wait is
// cancelled. The work context stays cancelled; runWorkerStep recognizes the
// named cancellation cause and settles the intent into the recoverable paused
// state even if the HTTP caller has gone away.
func (e *Engine) releaseWorkControl(intentID int64, run *workExecution, action string) {
	e.workMu.Lock()
	if current := e.work[intentID]; current == run && current.action == action {
		current.action = ""
	}
	e.workMu.Unlock()
}

func transitionIntentState(store *db.ExplorationStore, intentID int64, expected, state string) error {
	changed, err := store.CompareAndSetIntentState(intentID, expected, state)
	if err != nil {
		return err
	}
	if !changed {
		return fmt.Errorf("%w: Intent %d is no longer in %s state", db.ErrIntentStateConflict, intentID, expected)
	}
	return nil
}

// SteerWork queues a mid-run course-correction for the work running intentID (the
// planner's steer_work tool). The worker delivers it before its next tool call and
// re-plans â€” no kill. Errors if no work is currently running that intent.
func (e *Engine) SteerWork(intentID int64, msg string) error {
	if strings.TrimSpace(msg) == "" {
		return fmt.Errorf("Steering message cannot be empty")
	}
	e.workMu.Lock()
	running := e.work[intentID] != nil
	e.workMu.Unlock()
	if !running {
		return fmt.Errorf("Intent %d has no running work (may have ended or not been claimed)", intentID)
	}
	e.steerMu.Lock()
	e.steerBox[intentID] = append(e.steerBox[intentID], msg)
	e.steerMu.Unlock()
	return nil
}

// drainSteer pops the oldest queued steering message for intentID (FIFO), if any.
func (e *Engine) drainSteer(intentID int64) (string, bool) {
	e.steerMu.Lock()
	defer e.steerMu.Unlock()
	q := e.steerBox[intentID]
	if len(q) == 0 {
		return "", false
	}
	msg := q[0]
	if len(q) == 1 {
		delete(e.steerBox, intentID)
	} else {
		e.steerBox[intentID] = q[1:]
	}
	return msg, true
}

// steerHooks wraps the guard's hook runner so the planner can steer a running work:
// before each tool call it drains a queued course-correction (if any) and blocks the
// call, handing the message back to the model â€” which re-plans its next step instead
// of running the tool. No queued message â†’ the guard behaves exactly as before.
// It also handles resuming after â€œidle turnsâ€; see Stop.
type steerHooks struct {
	inner harness.HookRunner
	drain func() (string, bool)
	// nudges is the number of idle-turn resumptions injected for this intent, capped at limit. harness holds a
	// copy of steerHooks by value, so the counter must be shared.
	nudges *atomic.Int64
	// limit is the maximum number of empty-turn nudges, parsed by Engine.emptyTurnNudgeLimit() from the "empty response retry
	// count". <=0 = do not intervene (the user explicitly turned off this layer).
	limit int
	// label is of the form "worker-1 Â· #42", used only for logging.
	label string
}

// Default number of continuation attempts for empty turns (thinking only, with neither body nor tool calls), matching the
// SDK's built-in default for empty response retries (norma/llm/openai.go's emptyResponseRetries)â€”both layers share the same
// knob, so behavior when unconfigured should also match. See Engine.emptyTurnNudgeLimit for parsing.
//
// Note that this number is the "total for one intent", not "consecutive attempts": harness's own stopHookActive
// already limits consecutive empty turns to a single nudgeâ€”if the next turn is still empty, the Stop hook won't be called again, and run directly
// Wrap-up; the quota refreshes only after an actual tool turn has occurred (norma/harness/query.go:534). So this
// guard blocks the pathological loop â€œtool â†’ idle â†’ push â†’ tool â†’ idleâ€ from burning through the intent budget.
const defaultEmptyTurnNudges = 2

// emptyTurnNudge is the continuation instruction injected for an idle turn.
//
// The harness sees this turn as a natural end (stop_reason=end_turn with no tool_use), so none of the five layers of
// LLM retries applyâ€”it isn't an error; the model â€œfinished thinking but didn't act.â€ The SDK's empty-response retry also
// can't reach it: it detects emptiness by whether any events were yielded, and thinking deltas themselves are events (norma/llm/openai.go's
// SEThinkingDelta), so thinking-only doesn't count as empty. Besides, that layer resends the entire prompt unchanged,
// and for an idle turn determined by the context shape, resending only makes the model think it through again. Here we append an instruction to make it
// Continue with the thoughts already produced; only changed input justifies different behavior.
const emptyTurnNudge = "[Empty turn reminder] Your last round only output thinking, with no text reply and no tool callsâ€”this round produced nothing. Directly execute the next step you just thought of: either call a tool or give conclusion text. Do not repeat thinking."

// isThinkingOnlyTurn reports whether the latest assistant turn produced neither
// text nor a tool call â€” i.e. the model spent the whole round thinking.
func isThinkingOnlyTurn(messages []llm.Message) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m.Role != llm.RoleAssistant {
			continue
		}
		return strings.TrimSpace(m.Text()) == "" && len(m.ToolUses()) == 0
	}
	return false
}

func (h steerHooks) PreToolUse(ctx context.Context, name string, input []byte) (bool, string, []byte) {
	if msg, ok := h.drain(); ok {
		return true, "[Planner real-time correction] " + msg +
			"\n(This is the planner's immediate instruction for this intent; this tool call was not executed, please adjust the next step accordingly. If it conflicts with your current plan, take this as authoritative.)", nil
	}
	if h.inner != nil {
		return h.inner.PreToolUse(ctx, name, input)
	}
	return false, "", nil
}

func (h steerHooks) PostToolUse(ctx context.Context, name string, input, result []byte, isErr bool) {
	if h.inner != nil {
		h.inner.PostToolUse(ctx, name, input, result, isErr)
	}
}

// Stop adds a retry for "empty turns" on top of guard's existing semantics: when the model only outputs thoughts, neither produces a body
// nor calls a tool, the harness treats it as a natural end and finishes with an empty summary (query.go's
// ReasonCompleted + asst.Text()), leaving an intent that wasn't finished cut off midway. Inject
// a continuation instruction so the model can proceed with its existing thoughts.
func (h steerHooks) Stop(ctx context.Context, messages []llm.Message) (bool, []string, string) {
	var (
		prevent  bool
		blocking []string
		msg      string
	)
	if h.inner != nil {
		prevent, blocking, msg = h.inner.Stop(ctx, messages)
	}
	// If inner has decided to stop hard or is about to inject its own continuation message â†’ respect it; don't add another.
	// limit<=0 = the user configured the "empty response retry count" as -1, explicitly disabling this layer.
	if prevent || len(blocking) > 0 || h.nudges == nil || h.limit <= 0 || !isThinkingOnlyTurn(messages) {
		return prevent, blocking, msg
	}
	n := h.nudges.Add(1)
	if n > int64(h.limit) {
		log.Printf("[work %s] Idle turns (thinking only, no body or tools) reached the continuation limit of %d; allowing wrap-up", h.label, h.limit)
		return prevent, blocking, msg
	}
	log.Printf("[work %s] Idle turn (thinking only, no body or tools); injecting continuation instruction (%d/%d)", h.label, n, h.limit)
	return false, []string{emptyTurnNudge}, ""
}

// KillWork cancels the in-flight work running intentID (planner's kill_work tool).
// The work's agent-core session honors ctx cancellation and aborts promptly.
func (e *Engine) KillWork(intentID int64) error {
	e.workMu.Lock()
	run := e.work[intentID]
	e.workMu.Unlock()
	if run == nil {
		return fmt.Errorf("Intent %d has no running work (may have ended or not been claimed)", intentID)
	}
	run.cancel(agent.AbortKilledByPlanner)
	return nil
}

// Broadcaster exposes the engine's live activity pub/sub (used by the SSE handler).
func (e *Engine) Broadcaster() *Broadcaster { return e.bc }

// emitActivity persists one captured step AND fans it out to live subscribers,
// from a single point so storage and the SSE stream never diverge.
func (e *Engine) emitActivity(t *Task, r db.Activity) db.Activity {
	id, err := e.appendActivity(t, r)
	if err != nil {
		// NO LONGER SILENT: dropping a record breaks commandâ†”result pairing in the
		// trace â€” a tool_use whose tool_result was lost remains stuck showing "Running" forever, and
		// a lost 'result'/'round' record leaves the session with no summary ("no summary").
		// Everything needed to analyze the root cause goes into ONE error-level line: reason class,
		// summary preview, running drop count for this task, and â€” on the FK case â€” a
		// live probe of WHY the parent exploration is unreachable.
		n := e.bumpDrop(t.ID)
		diag := ""
		// On the FK-parent failure (23503) probe the live DB so the log records WHY the
		// exploration is unreachable (row gone / wrong expID) instead of just that it is.
		if isFKViolation(err) {
			storeID := t.Store.ID()
			if exists, refs, maxID, dErr := e.m.pg.ExplorationDiag(storeID); dErr != nil {
				diag = fmt.Sprintf(" | FK diagnosis query failed(store.expID=%d task.ExpID=%d): %v", storeID, t.ExpID, dErr)
			} else {
				diag = fmt.Sprintf(" | FK diagnosis: store.expID=%d task.ExpID=%d exploration exists=%v tasks referencing it=%d MAX(exploration.id)=%d",
					storeID, t.ExpID, exists, refs, maxID)
			}
		}
		log.Printf("[activity] task %s: dropped activity record (cumulative record %d for this task) worker=%s kind=%s tool=%s tuid=%s reason=%s summary=%q: %v%s",
			t.ID, n, r.Worker, r.Kind, r.Tool, r.ToolUseID, dropReason(err), preview(r.Summary, 80), err, diag)
		e.touch(t.ID)
		return r
	}
	r.ID = id
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now()
	}
	e.bc.Publish(t.ID, r)
	e.touch(t.ID)
	return r
}

// appendActivity persists one activity row, retrying briefly on write failure.
// Concurrent planner + worker inserts into the same exploration's activity log
// occasionally fail; a couple of quick retries recover most. Crucially, every
// failure is now LOGGED (it used to be swallowed by an `if err == nil`), so the
// underlying DB error is finally visible for diagnosis.
func (e *Engine) appendActivity(t *Task, r db.Activity) (int64, error) {
	var id int64
	var err error
	for attempt := 1; attempt <= 3; attempt++ {
		if id, err = t.Store.AppendActivity(r); err == nil {
			if attempt > 1 {
				log.Printf("[activity] task %s: write succeeded on retry %d (worker=%s kind=%s tool=%s)",
					t.ID, attempt, r.Worker, r.Kind, r.Tool)
			}
			return id, nil
		}
		log.Printf("[activity] task %s: write failed (attempt %d/3, worker=%s kind=%s tool=%s expID=%d): %v",
			t.ID, attempt, r.Worker, r.Kind, r.Tool, t.Store.ID(), err)
		time.Sleep(time.Duration(attempt) * 25 * time.Millisecond)
	}
	return 0, err
}

// SetReadiness wires the global "an LLM provider is configured" predicate (read by
// Ready() / the llm_configured indicator). Called once at startup.
func (e *Engine) SetReadiness(fn func() bool) { e.readiness = fn }

// SetAgentResolver installs a per-task planner/worker resolver (wired by the server).
// Called once at startup before any task loop runs, so no lock is needed on reads.
func (e *Engine) SetAgentResolver(fn func(t *Task) (*agent.Planner, *agent.Worker)) {
	e.resolve = fn
	e.resolveAuthoritative = false
}

// SetAuthoritativeAgentResolver installs a resolver whose nil result must not
// fall through to the global provider. Task-level failover chains use this so a
// fully exhausted chain cannot silently bypass its configured boundary.
func (e *Engine) SetAuthoritativeAgentResolver(fn func(t *Task) (*agent.Planner, *agent.Worker)) {
	e.resolve = fn
	e.resolveAuthoritative = true
}

func (e *Engine) SetWorkerResolver(fn func(t *Task, slot WorkerSlot) (*agent.Worker, error)) {
	e.workerResolve = fn
}

func (e *Engine) reportSlotAvailability(t *Task, slot WorkerSlot, err error) {
	key := t.ID + "/" + slot.Name
	now := time.Now()
	value, loaded := e.slotState.LoadOrStore(key, slotAvailability{available: err == nil, lastEmit: now})
	state := value.(slotAvailability)
	if err == nil {
		if loaded && !state.available {
			e.slotState.Store(key, slotAvailability{available: true, lastEmit: now})
			e.emitActivity(t, db.Activity{Worker: "system", Kind: "text", Summary: fmt.Sprintf("%s bound LLM config #%d recovered, can continue claiming tasks", slot.Name, slot.ProfileID)})
		}
		return
	}
	if loaded && state.available {
		state.available = false
		state.lastEmit = now
		e.slotState.Store(key, state)
	} else if loaded && now.Sub(state.lastEmit) < 5*time.Minute {
		return
	} else {
		state = slotAvailability{available: false, lastEmit: now}
		e.slotState.Store(key, state)
	}
	summary := fmt.Sprintf("%s bound LLM config #%d unavailable (deleted or invalid config), this session pauses task claiming and will not fall back to default model", slot.Name, slot.ProfileID)
	e.emitActivity(t, db.Activity{Worker: "system", Kind: "text", IsError: true, Summary: summary, Detail: err.Error()})
	log.Printf("[worker %s] task %s pinned profile #%d unavailable: %v", slot.Name, t.ID, slot.ProfileID, err)
}

// snapshotFor returns the planner/worker a task should run on, from the task-router
// resolver. nil,nil means the task is deliberately unavailable (e.g. an exhausted
// failover chain); there is no global-pair fallback.
func (e *Engine) snapshotFor(t *Task) (*agent.Planner, *agent.Worker) {
	if e.resolve != nil {
		p, w := e.resolve(t)
		if (p != nil && w != nil) || e.resolveAuthoritative {
			return p, w
		}
	}
	return nil, nil
}

// Ready reports whether a global LLM provider is configured (via the readiness
// predicate wired at startup).
func (e *Engine) Ready() bool {
	return e.readiness != nil && e.readiness()
}

// ReadyFor reports whether a specific task can resolve a planner/worker pair.
// An explicit task profile chain can be runnable even when no global default
// provider is configured, so task status must not rely on Ready alone.
func (e *Engine) ReadyFor(t *Task) bool {
	p, w := e.snapshotFor(t)
	return p != nil && w != nil
}

// Run starts the planner loop + N worker loops for a task. The loops always run
// but no-op until an LLM is configured (so a task created while idle picks up
// automatically once LLM is set from the UI).
func (e *Engine) Run(ctx context.Context, t *Task) {
	slots, err := workerSlotsForTask(t, e.m.WorkerPools(), e.m.Workers())
	if err != nil {
		log.Printf("[engine] task %s worker pool invalid, using legacy workers: %v", t.ID, err)
		slots, _ = ExpandWorkerSlots(nil, workerCountForTask(t, e.m.Workers()))
	}
	e.deleteMu.RLock()
	if e.IsDeleting(t.ID) {
		e.deleteMu.RUnlock()
		return
	}
	if _, loaded := e.started.LoadOrStore(t.ID, true); loaded {
		e.deleteMu.RUnlock()
		t.Notify() // already running â€” just nudge a planning round
		return
	}
	rt := e.registerTaskRoutines(ctx, t.ID, 1+len(slots))
	plan := make(map[string]int64, len(slots))
	for _, slot := range slots {
		plan[slot.Name] = slot.ProfileID
	}
	e.slotPlans.Store(t.ID, plan)
	e.deleteMu.RUnlock()
	e.touch(t.ID)
	runTaskRoutine(rt, func(loopCtx context.Context) { e.plannerLoop(loopCtx, t) })
	for _, slot := range slots {
		slot := slot
		runTaskRoutine(rt, func(loopCtx context.Context) { e.workerLoop(loopCtx, t, slot) })
	}
	e.startDeadlineCoordinator(ctx, t) // Task-level timeout timer (only timeout>0; deduplication)
	// Kick off first-round planning only when there are Â«no active intents (open+running)Â». For tasks with seeded intents: the seed is already
	// open, or has already been claimed as running by a worker started aboveâ€”either way, there is work to do, so always skip
	// the first-round planner; the worker directly picks up the seeded intent and starts running, then NotifyDone/heartbeat wakes the planner when it finishes.
	// âš ï¸ Do not use Frontier (counts only open): a race between the worker claiming an intent (openâ†’running) and this check can cause an erroneous kick.
	// During automatic restart recovery, only running intents may remain; skip in that case too.
	if has, _ := t.Store.HasActiveIntent(); !has {
		t.Notify() // kick the first planning round (acted on once LLM is ready)
	}
}

// workerSlotsForTask preserves an explicit task concurrency override. Tasks
// using the global default inherit its model pools and their pinned profiles.
func workerSlotsForTask(t *Task, pools []WorkerPool, global int) ([]WorkerSlot, error) {
	if t != nil && t.WorkerCount > 0 {
		return ExpandWorkerSlots(nil, workerCountForTask(t, global))
	}
	return ExpandWorkerSlots(pools, global)
}

func workerCountForTask(t *Task, global int) int {
	if t != nil && t.WorkerCount > 0 {
		// Direct database edits can bypass validation; cap those values so the
		// engine never starts more than the supported per-task worker count.
		if t.WorkerCount > db.MaxTaskWorkerCount {
			return db.MaxTaskWorkerCount
		}
		return t.WorkerCount
	}
	return global
}

// plannerHeartbeatInterval parses the planner heartbeat interval for a task. db.CreateTask has already normalized it
// (values below 600 are raised to 600); apply a fallback here too in case of abnormal in-memory values.
func plannerHeartbeatInterval(t *Task) time.Duration {
	sec := t.PlanHeartbeatSeconds
	if sec < db.MinPlanHeartbeatSeconds { // Lower bound=default=600(10min)
		sec = db.MinPlanHeartbeatSeconds
	}
	return time.Duration(sec) * time.Second
}

// resetPlannerTimer safely rearms a possibly fired Timer (standard Stopâ†’drainâ†’Reset pattern).
func resetPlannerTimer(timer *time.Timer, d time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(d)
}

func (e *Engine) plannerLoop(ctx context.Context, t *Task) {
	interval := plannerHeartbeatInterval(t)
	// The heartbeat timer is armed at loop entry = time from task start: even for seed tasks that skip the first planner round
	// (frontier is non-empty in Run, so the first round isn't kicked off), if blocked here, the heartbeat still fires at â€œtask start + intervalâ€
	// to trigger the first planning round. Each subsequent wakeup (edge/heartbeat) rearms it = time since any previous planning trigger.
	heartbeat := time.NewTimer(interval)
	defer heartbeat.Stop()

	// runRound runs one planning round (including debounce merging + all guards). src is only used in logs to distinguish the trigger source.
	runRound := func(src string) {
		// debounce: coalesce a burst of changes into one planning round
		timer := time.NewTimer(e.debounce)
	drain:
		for {
			select {
			case <-t.notify:
			case <-timer.C:
				break drain
			}
		}
		planner, _ := e.snapshotFor(t)
		if planner == nil {
			return // idle until LLM configured
		}
		if e.IsPaused(t.ID) {
			return // user-paused: don't plan
		}
		if e.IsDeleting(t.ID) {
			return
		}
		// terminal task (goals all met â†’ done, or failed): the run is over. A
		// resume/nudge â€” e.g. auto-resume of the active task on restart â€” must NOT
		// re-plan (it would burn an LLM round and re-confirm a settled result).
		if isTerminalStatus(t.lifecycleSnapshot().Status) {
			return
		}
		// Task-level timeout cleanup in progress: discard ordinary wakeupsâ€”worker cleanup writes and Resume's Notify no longer
		// trigger regular planning rounds; the final round is driven directly by the coordinator (settleTask), not through here.
		if e.isSettling(t.ID) {
			return
		}
		// goalless (manual direct injection) branch: when the task has no open targets, the planner doesn't runâ€”it would re-evaluate
		// metâ†’cancelExec kills intents directly submitted by the user through the main agent. Whether to finish is now determined by the frontier:
		// if any open/running intents remain â†’ stay running and wait silently; if all intents are exhausted â†’ mark done.
		// This entire section is pure Go, triggers no LLM calls, and emits no planning-round marker.
		if open, err := t.Store.HasOpenGoal(); err == nil && !open {
			t.drainTriggers() // Discard accumulated done/finding triggers to avoid unbounded growth in long goalless sessions
			if active, err := t.Store.HasActiveIntent(); err == nil && !active {
				// Frontier drained and no intents running â†’ finish. Use the Guarded version for CAS to avoid conflicting with concurrent
				// state transitions for pause/delete/timeout completion.
				if won, err := e.m.SetTaskStatusGuarded(t.ID, "done"); err != nil {
					log.Printf("[goalless] task %s: failed to persist wrap-up as done: %v", t.ID, err)
				} else if won {
					e.emitActivity(t, db.Activity{Worker: "system", Kind: "text",
						Summary: "ç›®æ ‡å·²å…¨éƒ¨è¾¾æˆï¼Œç›´æŠ•æ„å›¾å·²æ‰§è¡Œå®Œæ¯•ï¼Œä»»åŠ¡ç»“æŸ"})
				}
			}
			return // The goalless branch never enters planner.Plan
		}
		if !e.beginTaskOperation(t.ID) {
			return
		}
		defer e.decInflight(t.ID)
		if err := e.m.StartTaskRunSegment(t.ID); err != nil {
			log.Printf("[runtime] task %s start segment failed: %v", t.ID, err)
		}
		e.stampFirstRun(t) // First actual planning â†’ set first_run_at + compute deadline (only for tasks with timeout)
		e.touch(t.ID)
		emit := func(r db.Activity) {
			if r.Kind == "tool_result" && r.IsError && e.proxyFailure != nil && isGlobalProxyDialFailure(errors.New(r.Detail+" "+r.Summary)) {
				e.proxyFailure(t.ID, errors.New(r.Detail+" "+r.Summary))
			}
			e.emitActivity(t, r)
		}
		ectx := e.clockCtx(e.execContextFor(ctx, t.ID), t, false) // cancellable by Pause; task deadline
		if ectx.Err() != nil || e.IsDeleting(t.ID) {
			return
		}
		log.Printf("[planner] task %s planningâ€¦(%s triggered)", t.ID, src)
		// round marker: each Plan() is one planner round; emit a boundary so the
		// UI can separate rounds in the transcript (kind='round').
		e.emitActivity(t, db.Activity{Worker: "planner", Kind: "round",
			Summary: fmt.Sprintf("Planning round %d", e.nextPlannerRound(t.ID))})
		// what fired this round (worker done / finding; may be several â€” debounce
		// coalesces a burst; empty for time/heartbeat wakes).
		triggers := t.drainTriggers()
		taskIDInt, _ := strconv.ParseInt(t.ID, 10, 64)
		e.BeginLLMCall(t.ID)
		met, reason, err := planner.Plan(ectx, taskIDInt, e.m.assets, t.Store, t.Goal, triggers, emit)
		e.EndLLMCall(t.ID)
		switch {
		case err != nil && ectx.Err() == nil:
			log.Printf("[planner] task %s encountered an issue while planning: %v", t.ID, err)
			if e.proxyFailure != nil && isProxyFailure(err) {
				e.proxyFailure(t.ID, err)
			}
		case met:
			log.Printf("[planner] task %s determined the goal was achieved: %s", t.ID, reason)
			// All targets achieved â†’ persist the task status as done (the frontend DTO will prioritize this terminal state).
			if err := e.m.SetTaskStatus(t.ID, "done"); err != nil {
				log.Printf("[planner] task %s: failed to persist completion marker: %v", t.ID, err)
			}
			// Task deemed complete â†’ immediately cancel running workers: there's no point in letting their current intents finish.
			// The next worker loop will hit the terminal-state gate and stop claiming new intents; this cancelled batch follows the "task completed" branch below
			// Classify it as stopped (not blocked).
			e.cancelExec(t.ID, agent.AbortGoalMet)
		default:
			log.Printf("[planner] task %s planning complete", t.ID)
		}
		e.touch(t.ID)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.notify:
			runRound("edge") // worker ends / finding / kill / resume / seed first round
		case <-heartbeat.C:
			// Periodic fallback: deadlock recovery + wake to supervise in-flight workers (steer/kill) + periodic recheck.
			runRound("heartbeat")
		}
		// Re-arm the heartbeat after each wake (edge or heartbeat): any planning trigger recalculates this idle timer.
		resetPlannerTimer(heartbeat, interval)
	}
}

func (e *Engine) workerLoop(ctx context.Context, t *Task, slot WorkerSlot) {
	name := slot.Name
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		var worker *agent.Worker
		var resolveErr error
		if slot.ProfileID > 0 {
			if e.workerResolve == nil {
				resolveErr = errors.New("pinned worker resolver unavailable")
			} else {
				worker, resolveErr = e.workerResolve(t, slot)
				if worker == nil && resolveErr == nil {
					resolveErr = errors.New("pinned worker resolver returned no worker")
				}
			}
			e.reportSlotAvailability(t, slot, resolveErr)
			if resolveErr != nil {
				worker = nil
			}
		} else {
			_, worker = e.snapshotFor(t)
		}
		if worker == nil {
			if sleepCtx(ctx, 1500*time.Millisecond) {
				return
			}
			continue
		}
		if e.IsPaused(t.ID) {
			if sleepCtx(ctx, 1000*time.Millisecond) {
				return
			}
			continue // user-paused: don't claim/execute intents
		}
		if e.IsDeleting(t.ID) {
			return
		}
		if e.isSettling(t.ID) {
			if sleepCtx(ctx, 1000*time.Millisecond) {
				return
			}
			continue // Task timeout cleanup in progress: stop claiming new intents (running ones clean up on their own; the coordinator waits for them to drain)
		}
		if isTerminalStatus(e.m.TaskStatus(t.ID)) {
			if sleepCtx(ctx, 1000*time.Millisecond) {
				return
			}
			continue // Task has reached a terminal state (done/failed/timeout): stop claiming leftover intents; don't spin on the frontier after completion
		}
		if !e.beginTaskOperation(t.ID) {
			return
		}
		claimed := e.runWorkerStep(ctx, t, name, worker)
		e.decInflight(t.ID)
		if !claimed && sleepCtx(ctx, 800*time.Millisecond) {
			return
		}
	}
}

// runWorkerStep claims one intent from the frontier and fully settles it via
// runIntent. Returns false when nothing was claimable. The pool worker loop is its
// only caller.
func (e *Engine) runWorkerStep(ctx context.Context, t *Task, name string, worker *agent.Worker) bool {
	intent := e.claimNext(t, name)
	if intent == nil {
		return false
	}
	log.Printf("[worker %s] task %s claimed intent #%d", name, t.ID, intent.ID)
	return e.runIntent(ctx, t, name, worker, intent, "", "")
}

// runIntent executes and fully settles one already-claimed (state=running) intent.
// Both the pool worker loop (via runWorkerStep) and the human-message handler (via
// runDetachedIntent, a dedicated goroutine outside the worker pool) call it, so the
// execute/retry/state-write logic lives in exactly one place. A non-empty message
// is injected as this turn's input through ExecuteWithMessage; requestID keys the
// transcript marker that dedups re-injection across model_error retries. The caller
// must already hold one task-operation admission for the whole sequence so a delete
// cannot observe quiescence between the LLM return and the final DB writes.
func (e *Engine) runIntent(ctx context.Context, t *Task, name string, worker *agent.Worker, intent *db.Node, requestID, message string) bool {
	hasChatMessage := message != ""
	e.stampFirstRun(t) // First actual execution â†’ set first_run_at + calculate deadline (only for tasks with a timeout)
	e.touch(t.ID)
	emit := func(r db.Activity) {
		if r.Kind == "tool_result" && r.IsError && e.proxyFailure != nil && isGlobalProxyDialFailure(errors.New(r.Detail+" "+r.Summary)) {
			e.proxyFailure(t.ID, errors.New(r.Detail+" "+r.Summary))
		}
		e.emitActivity(t, r)
	}
	ectx := e.clockCtx(e.execContextFor(ctx, t.ID), t, false) // cancellable by Pause; task deadline applies
	if ectx.Err() != nil || e.IsDeleting(t.ID) {
		if err := transitionIntentState(t.Store, intent.ID, "running", "open"); err != nil {
			log.Printf("[worker %s] task %s intent #%d: failed to roll back after claim: %v", name, t.ID, intent.ID, err)
		}
		return true
	}
	if err := e.m.StartTaskRunSegment(t.ID); err != nil {
		log.Printf("[runtime] task %s start segment failed: %v", t.ID, err)
	}
	// per-work child context so the planner's kill_work can stop just this work.
	workCtx, workCancel := context.WithCancelCause(ectx)
	e.registerWork(intent.ID, workCancel)
	// wrap the guard hooks so steer_work can inject a mid-run course-correction
	// for THIS intent (drained before the worker's next tool call).
	iid := intent.ID
	taskEmit := func(a db.Activity) {
		nid := iid
		a.NodeID, a.Worker = &nid, name
		emit(a)
	}
	label := fmt.Sprintf("%s #%d", name, iid)
	workCtx = intercept.WithTaskContext(workCtx, t.ID, label, taskEmit)
	// nudges are deliberately outside the model_error retry loop: the idle-resume limit is the total for "this intent",
	// so retrying a round should not reset the quota.
	hooks := steerHooks{
		inner:  t.Guard.Hooks(),
		drain:  func() (string, bool) { return e.drainSteer(iid) },
		nudges: &atomic.Int64{},
		limit:  e.emptyTurnNudgeLimit(),
		label:  label,
	}
	wTaskID, _ := strconv.ParseInt(t.ID, 10, 64)
	e.BeginLLMCall(t.ID)
	var reason harness.TerminalReason
	var wrote agent.WriteCounts
	var err error
	if hasChatMessage {
		reason, wrote, err = worker.ExecuteWithMessage(workCtx, name, wTaskID, e.m.assets, t.Store, intent, hooks, emit, e.m.enrich, t.NotifyFinding, requestID, message)
	} else {
		reason, wrote, err = worker.Execute(workCtx, name, wTaskID, e.m.assets, t.Store, intent, hooks, emit, e.m.enrich, t.NotifyFinding)
	}
	if err != nil && e.proxyFailure != nil && isProxyFailure(err) {
		e.proxyFailure(t.ID, err)
	}
	e.EndLLMCall(t.ID)
	// On model_error, retry a few extra times (with backoff). Retry only while the intent still belongs to this work and the task
	// is not paused/terminated/cancelled [and has not entered wrap-up]; otherwise defer to the corresponding branch (do not
	// Retry again to avoid backoff eating into other workers' graceful shutdown window).
	maxRetries, retryBackoff := e.modelErrorRetryPolicy()
	for attempt := 1; attempt <= maxRetries &&
		retryableWorkerModelError(reason, err) &&
		workCtx.Err() == nil && ectx.Err() == nil && !e.IsPaused(t.ID) && !e.isSettling(t.ID); attempt++ {
		log.Printf("[worker %s] task %s intent #%d ended with model_error; retrying after %v (%d/%d)",
			name, t.ID, intent.ID, retryBackoff, attempt, maxRetries)
		if sleepCtx(workCtx, retryBackoff) {
			break // Cancelled during backoff (termination/pause) â†’ handled by the branch below
		}
		e.BeginLLMCall(t.ID)
		if hasChatMessage {
			reason, wrote, err = worker.ExecuteWithMessage(workCtx, name, wTaskID, e.m.assets, t.Store, intent, hooks, emit, e.m.enrich, t.NotifyFinding, requestID, message)
		} else {
			reason, wrote, err = worker.Execute(workCtx, name, wTaskID, e.m.assets, t.Store, intent, hooks, emit, e.m.enrich, t.NotifyFinding)
		}
		e.EndLLMCall(t.ID)
	}
	// Capture kill state before detachWork cancels workCtx. kill = this work's
	// ctx was cancelled (planner kill_work) while the TASK ctx kept running; a
	// pause cancels the task ctx (ectx) instead. Checking workCtx.Err() AFTER
	// unregister would always be true (unregister cancels it) â†’ every completed
	// work would be wrongly marked stopped.
	workCause := context.Cause(workCtx)
	killed := workCtx.Err() != nil && ectx.Err() == nil
	action, completeWork := e.detachWork(intent.ID)
	// A caller may stop waiting and release its in-memory reservation before the
	// agent honors cancellation. The named context cause remains authoritative and
	// still settles the stopped run into a recoverable state.
	if action == "" {
		switch {
		case errors.Is(workCause, agent.AbortWorkPausedByUser):
			action = "pause"
		case errors.Is(workCause, agent.AbortWorkCancelledByUser):
			action = "cancel"
		}
	}
	var controlErr error
	defer func() { completeWork(controlErr) }()
	if action == "pause" {
		controlErr = transitionIntentState(t.Store, intent.ID, "running", "paused")
		if controlErr != nil {
			log.Printf("[worker %s] task %s intent #%d: failed to persist paused state: %v", name, t.ID, intent.ID, controlErr)
			return true
		}
		log.Printf("[worker %s] task %s intent #%d paused", name, t.ID, intent.ID)
		e.touch(t.ID)
		return true
	}
	if action == "cancel" {
		// Park the stopped run in paused before handing cleanup to the API. If the
		// request disconnects after cancellation, the intent remains recoverable and
		// a later cancel can finish cleanup instead of leaving a phantom running row.
		controlErr = transitionIntentState(t.Store, intent.ID, "running", "paused")
		if controlErr != nil {
			log.Printf("[worker %s] task %s intent #%d: failed to persist cancellation fence: %v", name, t.ID, intent.ID, controlErr)
			return true
		}
		log.Printf("[worker %s] task %s intent #%d halted, waiting for cancellation cleanup", name, t.ID, intent.ID)
		e.touch(t.ID)
		return true
	}
	// if a pause cancelled this run mid-flight, return the intent to the frontier
	// so it is re-claimed on resume â€” the worker will resume the prior LLM
	// conversation from its transcript instead of restarting from scratch.
	if ectx.Err() != nil && taskExecutionPaused(context.Cause(ectx)) {
		if err := transitionIntentState(t.Store, intent.ID, "running", "open"); err != nil {
			log.Printf("[worker %s] task %s intent #%d: failed to roll back task pause: %v", name, t.ID, intent.ID, err)
		}
		return true
	}
	// The hard fallback cancel for task timeout cleanup (not pause or kill) canceled this run â†’ classify it as exhausted (cleaned up),
	// not blocked. The worker has usually already written the result back during settlement.
	if ectx.Err() != nil && e.isSettling(t.ID) {
		if err := transitionIntentState(t.Store, intent.ID, "running", "exhausted"); err != nil {
			log.Printf("[worker %s] task %s intent #%d: failed to persist timeout wrap-up state: %v", name, t.ID, intent.ID, err)
		}
		log.Printf("[worker %s] task %s intent #%d ended during task timeout cleanup (exhausted), writing back %s", name, t.ID, intent.ID, wrote)
		e.touch(t.ID)
		return true
	}
	// The task was completed (done via the normal path) â†’ cancelExec above canceled this run. The intended result is no longer relevant,
	// so mark it stopped (not blocked); don't pollute the intent status of a completed task.
	if ectx.Err() != nil && isTerminalStatus(e.m.TaskStatus(t.ID)) {
		if err := transitionIntentState(t.Store, intent.ID, "running", "stopped"); err != nil {
			log.Printf("[worker %s] task %s intent #%d: failed to persist terminal stopped state: %v", name, t.ID, intent.ID, err)
		}
		log.Printf("[worker %s] task %s intent #%d canceled because the task is already complete (stopped)", name, t.ID, intent.ID)
		e.touch(t.ID)
		return true
	}
	// killed by the planner: mark stopped (don't write back results, don't auto-reclaim).
	if killed {
		if err := transitionIntentState(t.Store, intent.ID, "running", "stopped"); err != nil {
			log.Printf("[worker %s] task %s intent #%d: failed to persist planner stop: %v", name, t.ID, intent.ID, err)
		}
		log.Printf("[worker %s] task %s intent #%d terminated (stopped)", name, t.ID, intent.ID)
		e.touch(t.ID)
		t.Notify()
		return true
	}
	if err != nil {
		log.Printf("[worker %s] intent %d: %v", name, intent.ID, err)
	}
	// Terminal routing: hitting the step limit â‰  completion. max_turns â†’ exhausted (so the planner knows this direction
	// was tried but not actually completed and needs a different approach, rather than being treated as covered and permanently skipped); errors â†’ blocked; normal â†’ done.
	state := "done"
	switch {
	case err != nil:
		state = "blocked"
	case reason == harness.ReasonMaxTurns:
		state = "exhausted"
		log.Printf("[worker %s] intent %d hit the step limit (exhausted), writing back %s for this run", name, intent.ID, wrote)
	case reason == harness.ReasonTimeout:
		state = "exhausted"
		log.Printf("[worker %s] intent %d timed out (exhausted), writing back %s after cleanup", name, intent.ID, wrote)
	}
	if state == "blocked" && isTaskLLMChainExhausted(err) {
		_ = t.Store.SetIntentBlockedReason(intent.ID, db.IntentBlockedLLMQuota)
	} else {
		if stateErr := transitionIntentState(t.Store, intent.ID, "running", state); stateErr != nil {
			log.Printf("[worker %s] task %s intent #%d: failed to persist terminal state %s: %v", name, t.ID, intent.ID, state, stateErr)
		}
	}
	log.Printf("[worker %s] task %s intent #%d ended: %s (writing back %s)", name, t.ID, intent.ID, state, wrote)
	e.touch(t.ID)
	t.NotifyDone(intent.ID) // results changed the graph -> wake the planner (with the just-finished intent id)
	return true
}

// runDetachedIntent runs one paused intent OUTSIDE the worker pool in its own
// goroutine â€” the human-message path. It transitions the intent paused->running
// itself (never through 'open'), so the pool, which only claims 'open', can never
// race it; the "at most one run per intent" invariant still holds because winning
// the CAS is the sole entry and work[intentID] was cleared when the pause settled.
// Because it does not compete for a frontier slot, a user message continues the
// worker immediately even when all pool slots are busy (mirroring how the
// main-agent chat handler starts its run directly). The spawned goroutine owns one
// task-operation admission for the whole run and roots its context at ctx (pass the
// server root, never the HTTP request, so a disconnect cannot strand the run while
// task pause/delete/shutdown still stops it). Returns an error if the run could not
// be started; the intent is left untouched in that case.
func (e *Engine) runDetachedIntent(ctx context.Context, t *Task, intentID int64, requestID, message, agentMessage string) error {
	if !e.beginTaskOperation(t.ID) {
		return fmt.Errorf("task is being deleted")
	}
	release := true
	defer func() {
		if release {
			e.decInflight(t.ID)
		}
	}()
	node, err := t.Store.GetNode(intentID)
	if err != nil {
		return err
	}
	if node == nil || node.Kind != db.KindIntent {
		return fmt.Errorf("intent not found")
	}
	owner := node.Owner
	var worker *agent.Worker
	if value, ok := e.slotPlans.Load(t.ID); ok {
		if plan, ok := value.(map[string]int64); ok {
			if profileID, pinned := plan[owner]; pinned && profileID > 0 {
				if e.workerResolve == nil {
					return fmt.Errorf("Intent's LLM config #%d unavailable: pinned worker resolver unavailable", profileID)
				}
				worker, err = e.workerResolve(t, WorkerSlot{Name: owner, ProfileID: profileID})
				if err != nil || worker == nil {
					if err == nil {
						err = errors.New("worker resolver returned no worker")
					}
					return fmt.Errorf("Intent's LLM config #%d unavailable: %w", profileID, err)
				}
			}
		}
	}
	if worker == nil {
		_, worker = e.snapshotFor(t)
		if worker == nil {
			return fmt.Errorf("worker not ready yet")
		}
	}
	changed, err := t.Store.CompareAndSetIntentState(intentID, "paused", "running")
	if err != nil {
		return err
	}
	if !changed {
		return fmt.Errorf("%w: Intent is no longer in paused state", db.ErrIntentStateConflict)
	}
	node.State, node.Owner = "running", "chat"
	// Record the human turn as a visible activity BEFORE the run starts, so it is
	// ordered ahead of any worker step and never appears without the run happening.
	// Keep the UI copy concise; ExecuteWithMessage writes the server-resolved
	// reference snapshot into the intent transcript as the LLM input.
	uid := intentID
	e.emitActivity(t, db.Activity{NodeID: &uid, Worker: "user", Kind: "user", Summary: message, Detail: message})
	release = false // ownership of the admission passes to the goroutine
	go func() {
		defer e.decInflight(t.ID)
		e.runIntent(ctx, t, "chat", worker, node, requestID, agentMessage)
	}()
	return nil
}

func taskExecutionPaused(cause error) bool {
	var abort *agent.AbortCause
	if !errors.As(cause, &abort) {
		return false
	}
	switch abort.Code {
	case "paused_by_schedule", "paused_by_user", "paused_by_orchestrator", "paused_by_proxy", "paused_on_reload", "paused_race_guard",
		"queued_for_admission", "llm_unavailable_queued", "task_deleted":
		return true
	default:
		return false
	}
}

func sleepCtx(ctx context.Context, d time.Duration) (done bool) {
	select {
	case <-ctx.Done():
		return true
	case <-time.After(d):
		return false
	}
}

func (e *Engine) claimNext(t *Task, name string) *db.Node {
	fr, _ := t.Store.Frontier(20)
	for _, in := range fr {
		if ok, _ := t.Store.ClaimIntent(in.ID, name); ok {
			return in
		}
	}
	return nil
}

