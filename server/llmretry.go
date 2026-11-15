package server

import (
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
)

// Server-side parsing of retry policies; see the LLM retry design document. Across the five layers:
//   - Connection setup / empty response / same-provider safety window are "endpoint-bound"; each LLM config can override
//     the global default (an unset profile field inherits the global value; if that is also unset, the built-in default is used);
//   - Circuit breaking / intent retries are process-wide, with only one global setting.
//
// Global policy reads one settings row from DB; call sites are all on low-frequency paths (provider construction, work completion,
// saving configuration), so another cache layer isn't worthwhile; circuit-breaker parameters are the exception—they must be read on every failure path, so
// applyRetryPolicy pushes them to Registry for storage.

// retryPolicy reads the global policy; a nil DB yields the zero policy (all
// layers on their built-in defaults).
func (s *Server) retryPolicy() db.LLMRetryPolicy {
	if s.m == nil || s.m.pg == nil {
		return db.LLMRetryPolicy{}
	}
	return s.m.pg.LLMRetryPolicy()
}

// resolveRetry layers one profile's override on top of the global policy and
// converts the result into the form agent.Config carries. Rules combine field by
// field, so a profile that only pins an interval still inherits the global count.
func resolveRetry(o db.RetryOverride, pol db.LLMRetryPolicy) agent.RetryConfig {
	connect := o.Connect.Or(pol.Connect)
	empty := o.Empty.Or(pol.Empty)
	stream := o.Stream.Or(pol.Stream)
	return agent.RetryConfig{
		// Attempts retain the raw semantics here: 0=default / negative=disabled: SDK's MaxRetries /
		// EmptyResponseRetries are exactly analogous, so let it parse them itself.
		ConnectAttempts: connect.Attempts, ConnectInterval: connect.Interval(),
		EmptyAttempts: empty.Attempts, EmptyInterval: empty.Interval(),
		StreamAttempts: stream.Attempts, StreamInterval: stream.Interval(),
	}
}

// applyProfileRetry fills cfg.Retry for a profile read from the DB.
func (s *Server) applyProfileRetry(cfg *agent.Config, p *db.LLMProfile) {
	if p == nil {
		return
	}
	cfg.Retry = resolveRetry(p.Retry, s.retryPolicy())
}

// Circuit breaker (polling cooldown) defaults match those built into llmpool —— only overridden when “user configured a value”.
// Defaults for intentional retries are in engine.go's modelErrorRetries / modelErrorRetryBackoff.

// applyRetryPolicy pushes the process-wide layers of the policy into the objects
// that consume them on a hot path: the circuit-breaker registry. Called at
// startup and whenever the policy is saved.
func (s *Server) applyRetryPolicy() {
	pol := s.retryPolicy()
	if s.llmHealth != nil {
		s.llmHealth.SetPolicy(pol.Breaker.Attempts, pol.Breaker.Interval())
	}
}

// modelErrorRetryPolicy resolves the intent-level replay knobs (layer ⑤): how
// many times a model_error work is re-run and how long to back off between runs.
func (e *Engine) modelErrorRetryPolicy() (retries int, backoff time.Duration) {
	retries, backoff = modelErrorRetries, modelErrorRetryBackoff
	if e == nil || e.m == nil || e.m.pg == nil {
		return retries, backoff
	}
	rule := e.m.pg.LLMRetryPolicy().Intent
	if rule.Attempts != 0 {
		retries = max(rule.Attempts, 0)
	}
	if d := rule.Interval(); d > 0 {
		backoff = d
	}
	return retries, backoff
}

// emptyTurnNudgeLimit resolves how many empty-turn continuations one work may
// inject (see steerHooks.Stop). It deliberately reuses layer ②'s knob —— 「empty response
// Retries": these are two ways of handling the same thing. The SDK layer handles "no content blocks at all" by
// resending the exact same request; this layer handles "thinking only, with neither a response nor tools" by appending an instruction to
// have the model continue with its existing thoughts (resending verbatim is pointless for an idle turn caused by the context shape). The criteria for an empty response
// differ because the SDK checks "whether any event has been yielded," and thinking deltas are events—but when users configure
// "how many times to retry an empty response," they mean "try again if the model produces no substantive content," so sharing one retry count across both layers
// matches that mental model.
//
// Read the global policy rather than a profile override: a run may switch profiles midstream due to failover, and this
// is the total budget for the entire intent, so it should not change when the endpoint changes. Its semantics match SDK's emptyRetries():
// 0 = default defaultEmptyTurnNudges;-1 (negative) = disable empty-turn continuation;>0 = use this value.
func (e *Engine) emptyTurnNudgeLimit() int {
	if e == nil || e.m == nil || e.m.pg == nil {
		return defaultEmptyTurnNudges
	}
	switch n := e.m.pg.LLMRetryPolicy().Empty.Attempts; {
	case n == 0:
		return defaultEmptyTurnNudges
	case n < 0:
		return 0
	default:
		return n
	}
}
