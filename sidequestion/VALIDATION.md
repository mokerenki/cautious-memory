# `/btw` Validation Record

Date: 2026-09-10. Branch: `codex/btw-side-question`. Baseline: `8dae851b9b622f2ff2631f332fde9719d0b16fba`.

Uses an independent PostgreSQL test database and data directory; real model credentials were injected only into the independent test environment, were not written into code or this record, and the product default model was not modified. Go 1.26.3, norma v0.3.6, Next.js 16.2.9.

Real model conversations, returned objects, engineering assertions, and the Qwen raw review text are saved in [validation-2026-09-10.json](validation-2026-09-10.json), which contains no API credentials.

## Engineering Checks

| Scope | Result | Evidence |
| --- | --- | --- |
| Structured messages, deep copy of tool parameters | Pass | `TestCheckpointDeepCopyAndBoundaries` |
| Summary / compaction requests not overwritten, complete reply and terminal state published, half-stream reply excluded | Pass | `TestCheckpointDeepCopyAndBoundaries`, `TestSnapshotExcludesPartialStreamAndSelectsPoolMember` |
| Actual model pool member identity | Pass | `TestSnapshotExcludesPartialStreamAndSelectsPoolMember` |
| Tool pairing, 20-group replay, budget clipping and over-limit errors | Pass | `TestBuildRequestCompactionToolPairingAndBudget` |
| Main and side parallel, bidirectional cancellation isolation | Pass | Blocking Provider, `TestMainSideConcurrencyAndIndependentCancellation` |
| No tool execution, streaming / non-streaming, existing usage on failure | Pass | `TestServiceNoToolsAndUsageOnFailure` |
| Real norma ChatAgent + local Read tool, main transcript / activity isolation | Pass | `TestSideActualChatCheckpointToolResultAndTranscriptIsolation`, streaming and non-streaming subtests |
| Persistence, paging, idempotency, restart preserving partial answers | Pass | `TestSideHistoryIdempotencyPagingAndRecovery` |
| Clearing and late-writer race, parent resource deletion, version comparison | Pass | `TestSideClearLateWritersAndDeletedParent` |
| MainAgent / Worker archive and recovery, v1/v2/v3 | Pass | `TestSideTaskArchiveVersions` |
| Three parent interfaces, authentication, resource ownership, Worker logical deletion | Pass | `TestSideHTTPGlobalLimitTaskWorkerAndDeletion`, `TestSideCheckpointPersistsBeforeAdmissionAndRestart` |
| Busy main session can side-question, independent SSE reconnect / disconnect, cancel, clear | Pass | `TestSideHTTPBusyIsolationClearAndReconnect` |
| 1 per parent session / 4 global concurrency | Pass | Two `TestSideHTTPâ€¦` cases |
| Snapshot persisted before admission, restart continues questioning, old sessions cannot forge snapshots | Pass | `TestSideCheckpointPersistsBeforeAdmissionAndRestart` |
| Cached config deleted or model changed then refuse to continue | Pass | `TestSideRejectsDeletedOrChangedCachedProfile` |
| Cancel before archiving and wait for the final answer and usage to persist | Pass | `TestSideTaskDrainPersistsBeforeArchive` |
| Usage recorded once on early stream consumer cancellation and side-question attribution | Pass | `TestSideUsageRecordedOnceOnConsumerCancellation` |
| Worker / deadline runtime context auto-restored on restart continues publishing new snapshots | Pass | `TestSideRestoredWorkerRuntimePublishesNewCheckpoint` |
| Race check of relevant packages | Pass | The following commands |
| TypeScript and production build | Pass | `npx tsc --noEmit`, `npm run build` |
| New frontend module Biome | Pass | `biome check`, 3 new modules |

Automated checks can be reproduced after configuring `CAUTIOUSMEMORY_PG_DSN` in a separate disposable database (do not point it at a production database):

```sh
go test -race ./agent ./db ./server ./sidequestion ./llmrec ./llmpool \
  -run 'Test(Side|Checkpoint|Snapshot|BuildRequest|Service|MainSide|CaptureRun|TaskArchive|CompleteForwards|StopIntent|CancelIntent)' -count=1
cd web
npx tsc --noEmit
npx biome check src/lib/side-questions.ts src/hooks/use-side-questions.ts src/components/side-question-workspace.tsx
npm run build
```

The full Go regression is not all green: two existing tests in the `server` package fail during temporary directory cleanup, both reporting `TempDir RemoveAll â€¦ directory not empty`:

- `TestInheritedActivityDetailAndRelationDeletion`
- `TestTaskMetadataPatchReturnsRenameAndPin`

After exporting the source from the unmodified baseline and re-running the `server` package in the same isolated environment, these two cleanup failures are also reproduced. The baseline run additionally showed a target-node count assertion failure in `TestCoreTaskLifecyclePG`; the final modified `server` regression had no such assertion failure. Other packages passed, and this round's side-question cases and the race check passed. The baseline issues were not marked as passing this acceptance, and no existing assertions were modified to hide problems.

Next.js build output has the existing multi-lockfile / workspace root inference warnings; the build completed and all pages were generated successfully.

## Browser Checks

Uses the Codex In-app Browser, connecting to an independent local Go service and Next.js dev server. On desktop and a 390 Ã— 844 narrow screen, the following manual automated operations were performed, checking screenshots and browser logs:

- Enter `/btw` while a normal chat is running; the main content and side question display simultaneously; the desktop sidebar works normally.
- Continuous follow-up questions; after stopping the side question, the generated part is retained; the main flow continues.
- When closing the panel the request continues, and after reopening the completed answer is restored; after refreshing the page, an empty `/btw` restores the history.
- The narrow-screen Drawer's input, buttons, history, and close operations work normally, with no horizontal overflow.
- Clearing uses a confirmation dialog; after clearing the history disappears and the main transcript and snapshot are retained.
- The task MainAgent and two Workers ask questions separately and switch; the Agent tabs and history do not cross-talk.
- The blocking local model fixture keeps the Worker running; submitting `/btw` from the Worker main input, after stopping the side question the Worker still shows live running and its own pause button, and the side question saves a partial answer.
- The browser error / warning log is empty.

Controllable fixtures are used to precisely verify concurrency timing without depending on the real model's output speed. During debugging, two Worker runtime checks did not form an effective concurrency window (the task had already ended / the answer ended early); after fixing the fixture they were redone and passed; these initial operations are not counted as effective passes.

## Real Model Conversation

Probing prioritized `grok-4.6`, the OpenAI-compatible interface `http://127.0.0.1:12580/tingly/openai`. The probe returned HTTP 200 with model name `grok-4.6` and `READY`, taking 2.82 seconds. The preferred option was available, so the Tingly `glm` or Zhipu `glm-5.3` fallback chain was not enabled; these two fallback services were not validated this time.

| Scenario | Actual Result |
| --- | --- |
| Asking about assets, goals, and markers while the main session is running | Returned `redhaze.top`, the homepage read, and the summarized goal, `BTW-REAL-0910`; the side question completed, 16.97 seconds |
| Asking about the tool basis after the main session completed the homepage read | Correctly cited WebFetch 200, curl redirect 301 â†’ 302 â†’ 200, and the page title; 7.24 seconds |
| Side question asks Bash to create a test file | Refused to execute, the target file was not created; 7.74 seconds |
| A completed side question does not change the main context | The main transcript SHA-256 stays consistent with the main activity record; side tool execution count is 0 |
| After a real stop / restart of the Go service, continue questioning | Retained the previous 3 side-question history entries, answered directly from the persisted snapshot about assets, markers, and the title, without re-running the main Agent |
| New session using the Grok non-streaming config | Correctly answered about the assets and `ATOMIC-0910`; returned and saved usage: input 11734, output 138, cache_read 11520 |

The asset case's main session used WebFetch and Bash/curl to read a public homepage; the landing page is `https://id.redhaze.top/home`, titled "HongMu Technology RedHaze Group Â· Global Integrated Group Portal". Bash temporarily stored the response in a local test file; no write was executed against the remote. That fact and "the side question executed no tools" are verified separately.

The main transcript checksum: `e7e61f135a4a120954b539f357e8c4205d7d5cd7460dcaf3dc0fd066463e1d00`.

**Usage Limit:** Tingly's Grok streaming response did not return usage. A separate direct send of `stream_options.include_usage=true` was verified, with HTTP 200, 12 data frames, and 0 usage frames. Therefore the 0 in the streaming tests means the endpoint did not provide usage, and cannot be interpreted as no billing. Non-streaming usage and the fixture's failure / cancel usage are all correctly saved.

## Qwen Review

The review model `qwen-flash`, OpenAI-compatible interface `https://dashscope.aliyuncs.com/compatible-mode/v1`, HTTP 200. The first three real side-question conversations, the main session tool basis, and engineering assertions were provided; it returned `verdict: accept`, `concerns: []`, considering the answers consistent with the assets, markers, and page-read evidence, and the side tool refusal compliant with the constraints. Review usage: prompt 6625, completion 312, total 6937.

This Qwen review scope does not include the later added service restart and non-streaming tests. Qwen's summary of "no write" was too broad: the main session curl did create a local response temporary file, as clearly recorded above. Concurrency, zero tool execution, and transcript isolation are judged by engineering assertions, and the model review only aids in assessing answer quality.

