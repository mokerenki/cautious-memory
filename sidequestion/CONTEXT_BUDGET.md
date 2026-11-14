# Side Question Long Conversations and Context Budget

Fixed on 2026-09-11. The original implementation directly treated the character count of the request JSON as tokens and inherited the main task's 32K output reservation, so when there were many HTML, JS, and tool results it would reject normal side questions prematurely.

## Open-Source Implementation Review

- [Grok CLI side-question context](https://github.com/superagent-ai/grok-cli/blob/fb97af83f06dca873281d60168430f06c8de6324/src/agent/agent.ts#L739): extracts fragments from the most recent user and assistant text, with a character budget of about 2000 and each item truncated to at most 400 characters. Does not maintain a continuous side-question Q&A history on this path.
- [Grok CLI independent request](https://github.com/superagent-ai/grok-cli/blob/fb97af83f06dca873281d60168430f06c8de6324/src/utils/side-question.ts): an independent cancellation signal; an output limit of 2048 tokens when the model supports it; no tools provided.
- [Grok CLI main session compaction](https://github.com/superagent-ai/grok-cli/blob/fb97af83f06dca873281d60168430f06c8de6324/src/agent/compaction.ts): estimates tokens, keeps recent content, updates new content into an old summary, and handles cross-round truncation.
- [OpenCode session compaction](https://github.com/anomalyco/opencode/blob/b3f1a96c6dd7adeb28b36dd11add1998fc84d67b/packages/core/src/session/compaction.ts): full request estimation, output/buffer reservation, recent content plus rolling summary, no-tool summary request; this implementation defaults to a recent budget of 8000 and a summary output limit of 4096 tokens.
- [OpenCode overflow recovery](https://github.com/anomalyco/opencode/blob/b3f1a96c6dd7adeb28b36dd11add1998fc84d67b/packages/core/src/session/runner/llm.ts): overflow recovery is attempted only before assistant output has started, and the call after recovery no longer enters the same overflow recovery path.

ARTEX borrows the ideas of an independent output budget, recent content plus rolling summary, and limited recovery. It keeps norma v0.3.6's structured messages and tool pairing, does not copy Grok's text extraction; and does not write OpenCode's main session compaction events into the ARTEX main transcript.

## Request Budget and Execution

- Messages use norma's per-content-block UTF-8 byte estimation and a 4/3 margin; the system prompt, tool schema, and message-wrapping overhead are additionally counted. The estimation is not the model's exact token count.
- The side output defaults to at most 8192 tokens, and also does not exceed the output limit already set in the main config. The service environment variable `ARTEX_BTW_MAX_OUTPUT_TOKENS` can set an upper limit of 256–32768; it does not modify the product default model or main task parameters.
- The input budget is the context window minus the output limit and a safety margin; for an unknown window, the platform default of 200K is used. The safety margin is 5% of the window, with a minimum of 128 and a maximum of 8192 tokens.
- Successful Q&A are loaded at most 20 groups per batch by incrementing ordinal. At most 20 groups of raw text are retained, whose token budget is at most 1/4 of the input budget, and no more than 16K.
- Excess Q&A are updated into a rolling summary. The summary carries the historical source and context time; historical assistant answers are not equivalent to new tool evidence, and on conflict the latest main snapshot takes precedence.
- When the main context is still too long, only the old messages in the copy are summarized, keeping at most 8K tokens of recent content; the truncation point will not split a tool call from its result. An overly large single group enters the summary as a whole.
- The summary input is UTF-8-safe chunked by the actual remaining window, with an output limit of 2048 tokens; an empty summary, truncation, a tool call, or exceeding the summary budget is not written to cache. A single side question makes at most 12 summary calls, also subject to the same 120-second timeout limit; reaching the limit clearly fails and does not loop infinitely.
- If the model returns a context-exceeded error on the first try and has not yet output text or a tool call, it is retried at most once after further reduction; if the estimated size does not drop, recovery stops immediately. Other model errors and partial streaming output do not trigger this recovery.
- All usage obtained, including summaries, failed attempts, and usage at cancellation, is accumulated into the same side-question request. When the Provider returns no usage, it can only record zero and must not disguise an estimate as actual usage.

## Persistence and UI

`side_question_sessions.memory` stores the old Q&A summary, the covered ordinal, and the main context summary cached by snapshot identity. `side_question_requests.context_info` stores the preparation phase, the number of groups actually replayed, summary usage, and budget estimation.

Summaries are saved only when the original request is still running and the cleanup version matches; clearing simultaneously clears the cache, and a late write does not restore cleared data. A new snapshot does not reuse an old snapshot summary. Summary fields are saved along with v3 task archiving; when restoring an old v3 missing a field, an empty object is filled in, and v1/v2 continue to be compatible.

POST first accepts and returns the request; preparation and compaction run in the background, holding neither an admission lock nor a database transaction. SSE/history shows the preparation, Q&A organization, copy compaction, and answer phases; a compaction failure is saved as the failed terminal state of that side-question request. The frontend retains the error and restores the draft of this failed question, without a floating notification covering the input box; history polling no longer clears the submission error.

## Validation Record

- 19/20/21/50 group replay, old conclusions retained across 20 groups, summary cache reuse after restart: automated checks passed.
- Overly long Chinese answers and code context, chunked request budget, tool pairing, snapshot immutability, new snapshot cache invalidation: automated checks passed.
- Summary failure/cancel/truncation/over-length/tool return, clearing race, call limit, only-once overflow recovery, and no retry on partial stream: automated checks passed.
- Paging on an independent PostgreSQL, restart, v1/v2/v3 archiving, summary cache and budget metadata archive recovery, old v3 missing new fields, 20 parent sessions sharing four concurrency slots: passed.
- Go candidate service build, frontend TypeScript check, Biome check of modified components, and Next.js production build in an independent directory: passed.
- The built-in browser uses an independent UI fixture, verifying the organization phase, summary range prompt, draft recovery after failure, no floating error notification, no horizontal overflow, and no console errors at 1280×720 and 390×844. Temporary fixtures were removed.
- Read-only replay of the local existing Worker snapshot passed with the new budget check; for example, Worker #3's 293085-character snapshot is no longer mistakenly rejected by local character counting. This item had no external model call.
- This time, sending a private Worker snapshot to Grok's real conversation test was rejected by the automated approval review and was not executed; it is not counted as a passing item.
- At 2026-09-11 00:37 the local backend was restarted at the user's request, using the original database, data directory, and login configuration. The running binary matched the candidate binary's SHA-256; both the backend and the frontend proxy's `/api/health` returned normal.

Validation commands (using only the independent test database):

```sh
go test -race ./sidequestion ./db ./server -run 'TestSide|TestCheckpoint|TestSnapshot|TestBuildRequest|TestService|TestMainSide|TestTaskArchive' -count=1
go build ./cmd/artex
npx tsc --noEmit
npm run build -- --webpack
```

The frontend production build uses an independent copy to avoid overwriting the currently previewed `.next`. The candidate service is located at `/private/tmp/artex-btw-budget-candidate`, copied to `/private/tmp/artex-btw-preview/artex` and started; the original binary is backed up as `artex.before-context-budget` in the same directory.
