# Changelog

All notable changes to this project are documented in this file. The format is based on [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/).

## [Unreleased]

## [0.3.18] - 2026-10-12

### Approval Review

- Added script review snapshots that provide the review model with the path, size, SHA-256, and source of directly referenced scripts; when a snapshot is incomplete, it forces escalation to manual approval.
- Added side-question sessions for approvals, supporting continuous follow-up questions based on the approval context; the side model cannot execute tools or change the approval status.
- Added full-text search of approval records, precise location by approval number, filtering by date range, and combined filtering by status and decision source.
- Fixed script area clipping in approval details, script expansion state display, and chat layout after the side panel is closed.

## [0.3.17] - 2026-10-10

### Scheduled Tasks

- Added calendar-style scheduled tasks, supporting creating, editing, starting/stopping, and deleting tasks by time rules.
- The task list supports viewing the scheduled status and next run time, and adds a scheduled task management page.
- Supports filtering scheduled tasks by date range, a calendar-style run timeline, and batch selection operations.
- Improved immediate run, pause source, and status color display, and refreshes the timeline after scheduled status changes.

## [0.3.16] - 2026-10-09

### Access Control

- Added optional HTTP Basic Auth access control, configurable in system settings, and fixed the issue where config persistence held the request lock.

### Merging Duplicate Reports of the Same Vulnerability

- The Reporter judges the same actual defect against candidate evidence in the same task after generating an independent report; when duplication is clear, it merges, and when evidence is insufficient it submits a recommendation for manual confirmation.
- Added expandable vulnerability folders, a unified report, raw severity distribution, independent vulnerability/report record statistics, and merge history; each original report, evidence, and traffic continues to be retained.
- Supports manually organizing selected history records, removing members, and preventing automatic re-merging; inherited tasks remain read-only.
- The unified report uses evidence version validation; the export defaults to the unified report and can include the original sub-reports; when not generated or expired, it clearly indicates this and retains the raw materials.

## [0.3.15] - 2026-10-07

### Deployment and Release Maintenance

- Migrated to the standalone `RuoJi6/ARTEX` repository, preserving the original commit history and branches.
- The page in-place update source is `RuoJi6/ARTEX`; GitHub Actions builds five platform packages and SHA256 checksum files.
- Builds Docker Hub images on native AMD64 and ARM64 runners, and verifies program startup, database connections, and the embedded frontend.
- After both architectures pass verification, publishes `v0.3.15`, `latest` images, and corresponding packages.
- Compose and deployment instructions use this repository's images, and adds migration steps for existing deployments that preserve data.

### Interception

#### Added Features

- **Added a built-in interception rule "delete-type API paths"**: the previously built-in HTTP destructive rule only recognized the DELETE **method**, but most applications' delete endpoints can be triggered with GET/POST, so calls like `curl 'http://t/api/user/delete?id=1'` would actually delete target data. A `deny` rule now covers `/delete /del /remove /unlink /erase /destroy` (a separator must follow the verb, so `/delivery`, `/details`, and `/delta` are not mistakenly blocked). Existing instances receive it after upgrading, and it can be disabled or deleted in "System → Command Interception".
- **The token consumption of fallback model approval can now be viewed separately**: approval calls previously had no independent metering attribution, so how much was spent could only be guessed. It is now metered separately, with a new usage card below the "Fallback Model Approval" switch in "System → Command Interception" — cumulative statistics across five items: call count, input, output, cache reads, and cache writes, plus a mini bar chart of daily consumption over the last 30 days.

### Vulnerability Push

#### Added Features

- **Vulnerability discoveries support push to IM**: a new "System → Notification Push" page can push scan discoveries to six channels: **DingTalk, Feishu, WeCom, generic Webhook, Telegram, and email**. Each type can be configured with any number of instances, and each instance has independent enable/disable, rate limiting, and filtering rules.
- **Push timing has two modes: real-time and digest**: real-time mode sends each hit as it occurs; digest mode merges hits into a single message by a global period (default 30 minutes), with the new count and severity distribution given at the top. To achieve "real-time for high-severity, digest for the rest", create two channels with separate configurations.
- **Filter rules support four dimensions**: minimum severity level, restricted task/asset scope, vulnerability type keyword inclusion and exclusion (exclusion takes precedence), and whether to receive disposition status changes (off by default). Messages carry a "View Details" button, whose target address is configured by the global "Backlink Address", and if left empty the button is omitted.
- **Delivery history and manual resend**: lists each delivery's status, attempt count, failure reason, and owning channel, filterable by channel and status; failed items can be resent with one click (resending resets the retry counter).
- **Channel credentials are masked on display**: Webhook URLs, signing keys, Bot tokens, SMTP passwords, and the like only display a masked value with a trailing-digit hint; submitting the value unchanged means "no change", while clearing the input deletes that field.

#### Fixed Issues

> This section is the conclusion of a round of security audits after the initial feature release. Each item was first reproduced, then fixed, then covered with regression tests.

- **Fixed a mask bypass via "change target address, keep credentials" (critical)**: config merging preserves the original value for any "unmentioned key", so changing only the address and avoiding any mention of credentials could make the server send the real credentials stored in the database to any address, completely silently. Now, once the target address changes, every credential field must be explicitly addressed — either given a new value, or explicitly left empty to indicate it is no longer needed.
- **Fixed zero escaping of untrusted content in markdown-based channels**: vulnerability titles and summaries come from model output, and asset URLs come from scan results; a crafted title would render as a clickable external link in DingTalk/WeCom/Feishu, and image syntax would cause the client to fetch the image during rendering and leak the reader's IP. Now a uniform single-line normalization + markdown metacharacter escaping is applied, and escaping happens at each channel's rendering exit rather than in a shared title function.
- **Fixed the SSRF surface of delivery addresses**: loopback, intranet, and cloud metadata addresses were previously all deliverable, and the first 200 bytes of a failure response body would go into `last_error` and be shown in delivery history, forming a semi-blind read primitive. Now defenses are placed at the **dial stage**, covering DNS rebinding, and cross-host redirects are rejected; loopback and link-local addresses require explicitly setting `ARTEX_NOTIFY_ALLOW_LOCAL=1`, and RFC1918 private ranges are still allowed (in-house SMTP relays on the intranet are very common).
- **Fixed push credentials leaking through error messages**: DingTalk's `access_token`, WeCom's `key`, and Telegram's bot token are all in the URL, and on delivery failure the full URL would flow into `last_error`, delivery history, server logs, and frontend prompts in four places. Now they are uniformly desensitized, keeping only `scheme://host` and the underlying reason.
- **Fixed silent loss caused by truncated digest messages marking the whole batch as delivered**: truncated vulnerabilities appeared neither in the message nor in the failure list, yet delivery history showed success. Now packing is done per **entire message**, marking only those actually included; the rest are returned to the queue to continue with the next message without consuming retry counts, and the message header also states how many items will continue in the next message.
- **Fixed the minimum severity threshold silently failing due to a typo**: when `min_severity` is written as something like `hgih`, the unknown level's ordinal is 0, so the decision degrades to always-true, and the user thinks only high-severity items are received while actually all vulnerabilities are flooded into the group. Now values are validated at the write path and the allowed values are listed.
- **Fixed `rate_per_min = 0` (no rate limiting) being unreachable**: the database layer silently changed an explicit 0 to the default value, so the operator thought rate limiting was lifted when it was actually still in effect. The default is now moved to the interface layer and filled in when the field is omitted.
- **Fixed digest channels completely bypassing the token bucket**: `rate_per_min` previously had no effect for digest mode. Now the number of items claimed is constrained by both the current round's quota and an in-memory upper bound.
- **Fixed failure disposition being judged by the batch-wide maximum attempt count, causing old deliveries to drag down new ones**: an old delivery that had already retried twice would drag brand-new deliveries in the same batch into `failed`, so new vulnerabilities were permanently lost without a single retry. Now the decision is made per item.
- **Fixed deliveries whose snapshot could not be parsed in a digest batch being silently marked successful**: such items would be skipped at the rendering stage yet counted as successful along with the whole batch. Now they are explicitly judged as failed with a reason.
- **Fixed per-channel per-round delivery count potentially exceeding the lease duration**: under multi-instance deployment, rows with expired leases would be claimed again, sent repeatedly, and have their attempt counts double-incremented. Now the per-round limit is derived backward from "lease / single timeout", and assertions pin down the relationship among these constants.
- **Fixed Telegram truncation potentially cutting HTML entities**: truncation avoided only half a tag, not entity fragments like `&amp`, and the parser might reject the **entire** message as a result. Now both are avoided.
- **Fixed the email channel judging temporary SMTP failures as permanent failures**: a 4xx response like greylisting `450` is a temporary rejection that should be retried later, but was previously all treated as fatal; servers with greylisting enabled would fail every push after the first attempt. Now the leading digit of the response code distinguishes them: 4xx is retryable, 5xx is a permanent failure.
- **Fixed committing a nested structure actually writing mask literals into the database**: object fields like `webhook.headers` can only be masked or submitted as a whole; inserting a mask sentinel inside the object would be stored as a real value, causing authentication to silently fail. Such submissions are now explicitly rejected.

#### Design Notes

- **The vulnerability write transaction does only one blind INSERT**: `notification_events` and the vulnerability are written within the same transaction, so commit guarantees atomic consistency of both. This INSERT deliberately does not read the channel table and does not run the user's filter rules, and is wrapped by a `SAVEPOINT` — otherwise a misconfigured filter condition could abort the transaction and prevent high-severity vulnerabilities from being stored.
- **Delivery uses lease claiming rather than a long transaction**: after claiming with `FOR UPDATE SKIP LOCKED`, the row is set to `sending` and `next_attempt_at` is pushed into the future as the lease, so no database lock is held during delivery; rows left behind by a crashed process are re-claimed after the lease expires, self-healing and without infinite retries.
- **Rate limiting does not consume retry budget**: the current round's quota is computed from the token bucket first, and that number is then used to claim items. Reversing the order would make rate-limited deliveries count a wasted attempt, and after the three-attempt budget is exhausted by pure waiting they would fall into failure.

### Test Infrastructure

#### Fixed Issues

- **Fixed asset permanent residue caused by cleanup order in the company ICP attribution test**: `defer d.Close()` runs first when the function returns, followed by `t.Cleanup`, so all cleanup statements fell on an already-closed connection, errors were discarded again, and test assets and the company remained permanently in the database, even causing other tests that assert by asset count to fail inexplicably. Now closing the connection also goes through `t.Cleanup` and is registered first, and cleanup failures are made visible.

### Traffic

#### Added Features

- **The traffic list supports content search, path/status code/length filtering, and column sorting**: the filter row supports filtering by **response content** (via the full-text index), **path**, **status code** (exact value or bucketed like `2xx`), and **response length range**; table headers can be sorted by time, status code, and response length, and sort preferences are remembered locally. Two new indexes are added and take effect automatically after existing instances upgrade; the Agent tool's query contract is unchanged.
- **The traffic list adds "Clear All"**: deletes all traffic records at once (ignoring the current filter), cleans up historical host directories no longer recorded by the index, and after clearing performs a one-time full compaction and reports how much space was actually freed. Traffic evidence bound to vulnerabilities exists in a separate evidence store and is unaffected.

#### Fixed Issues

- **Fixed disk space not being released after deleting traffic**: the index database was created without `auto_vacuum` enabled, so the file never shrank; the full-text index was also `contentless_delete`, so `DELETE` only wrote a tombstone without reclaiming the original postings — deleting traffic actually made the index larger (measured: capturing 6MB of body → 16MB index, still 16MB after deleting everything). Newly created index databases now enable `auto_vacuum=incremental`, and after each deletion, full-text index merges and incremental reclamation are executed in chunks in the background, gradually returning space to the system (falling back to 104KB in the same scenario); reclamation releases the write lock between chunks, so it does not block traffic recording.

> Upgrade note: `auto_vacuum` can only be set at database creation, so **existing instances' index databases remain in the old mode**, and incremental reclamation on them is a no-op (a note is printed at startup). For such databases, the growth of tombstones is now halted after upgrade, and the already-occupied volume can be reclaimed once with "Clear All" — that step converts the database to incremental reclamation mode, and daily deletions thereafter take effect on their own.

### LLM

#### Fixed Issues

- **Fixed custom session headers not taking effect on the "one-off LLM call" path**: goal decomposition (round 0) and cold-node compaction are both one-off calls without a transcript store attached, so there is no session id on the context, and the gateway side cannot read the header — for endpoints like opencode zen that directly return 400 when the header is missing, this manifests as "round 0 fails, but subsequent planner rounds work perfectly". These two paths now explicitly attach a session id stabilized by exploration, and token usage can also be correctly attributed to the corresponding exploration (previously it could not be recorded at all).

### Tools

#### Fixed Issues

- **Fixed MCP and custom tool output truncation not taking effect, flooding the context with large results**: MCP tool returns previously went through no truncation at all, while HTTP-type custom tools kept using a hardcoded 6000 characters instead of the session-configured limit because an empty tool context was passed. Both paths now go through `actool.Capture`: truncation by session `MaxOutputChars` (default 30000), and when `ToolOutputDir` is configured, the full output is spilled to disk and only the leading fragment and a file pointer remain in the message.
- **Upgraded norma to v0.4.3**: the harness layer uniformly executes `CaptureOnce`, so no tool (including any added in the future) will flood the context by missing truncation.

### Deployment

#### Fixed Issues

- **Fixed the activity stream not connecting under reverse-proxy deployment**: the SSE address was previously unconditionally assembled as `${hostname}:8787`; in deployments reverse-proxied to 443, the page loaded from `https://domain/` while the SSE was pointed at port 8787, which is not exposed to the public network, manifesting as repeated activity-stream reconnections. Now it is distinguished by `NODE_ENV`: production defaults to same-origin, and only `next dev` keeps `:8787` to bypass Next.js's buffering of SSE; the `NEXT_PUBLIC_SSE_BASE` override is retained. The README adds reverse-proxy deployment instructions.

### Findings

#### Fixed Issues

- **Fixed the "By Asset" view's asset list not scrolling after overflow**: the outer card only set `max-height` without a definite height, so the scroll viewport's percentage height could not be resolved, and when there were many assets the list either broke out of the card or was truncated and unscrollable. Now the height cap is applied directly to the native scroll container of the asset tree, adapting to the window height.

### Database

#### Fixed Issues

- **Set an upper limit on the connection pool**: `database/sql` by default does not limit the number of connections the application itself can open; when there is no idle connection in the pool, a new one is unconditionally created, all the way up to PostgreSQL's `max_connections` (default 100) before being rejected. Now capped at 32, and excess queries wait for an idle connection instead, manifesting as slowness rather than errors under the same load; if `max_connections` has been lowered, this should be lowered correspondingly.
- **Improved connection reuse**: `MaxIdleConns` defaults to 2, so when concurrency exceeds 2 connections are closed after use, and the next request must redo the TCP and PostgreSQL authentication handshake. Now the idle limit is raised to match the total limit, and a 30-minute connection lifetime and 5-minute idle eviction are added, letting the pool naturally replace long-lived bad connections.

### Contributors

- [@Autumn-27](https://github.com/Autumn-27)

## [0.3.14] - 2026-09-24

### Task List

#### Added Features

- **The task list adds a "Vulnerabilities" column**: displays counts by severity bucket (critical / high / medium / low), with non-zero buckets colored by severity, so the vulnerability scale and distribution of each task are clear at a glance.

#### Changed Features

- **The "Description / Goal" columns are narrowed**: overly long content is truncated, and hovering with the mouse shows the full text, reducing the horizontal space that long text takes up in the list.

### Exploration Graph and Planning Situation Overview

#### Changed Features

- **Slimmed down the planning situation overview (graph_overview) and set count limits for each list**: recent facts, ended intents, pending intents, cold-zone summaries, and confirmed vulnerability details are all changed to "latest window + count fallback", with unshown items queryable on demand; this significantly reduces the per-round LLM context size and avoids context bloat in long tasks (the cold zone of the associated task overview is also rate-limited).

#### Fixed Issues

- **Eliminated the reverse duplicate edges between digest and finding in the exploration graph after collapsing cold nodes**.

### Contributors

- [@Autumn-27](https://github.com/Autumn-27)

## [0.3.13] - 2026-09-19

### Asset Interception

#### Added Features

- **Added global asset interception rules (blacklist)**: supports entering exact and fuzzy-matching domains / IPs / URLs as well as CIDR ranges, provides add/delete/edit/query and enable/disable of rules, with the management page located at "System → Asset Interception"; by default, fuzzy interception of government (`.gov` / `.gov.cn`) and education (`.edu` / `.edu.cn`) sites is built in.
- **Asset interception is integrated into the execution chain**: before the Agent dispatches an intent (`add_intent`) and inserts assets (`insert_assets`), it performs an interception check on the target assets — intercepted intents are not dispatched, intercepted assets are not inserted, and the Agent is returned the asset information and the interception reason.
- **Added task-level interception / allow (whitelist) rules**: independent of global rules and effective only for this task, with the decision order being "deny first, then allow" — a match on a deny rule is forbidden; if no deny rule matches but this task has allow rules configured and none match, then "not allowed to test"; when no allow rules are configured, the whitelist is not enabled. Can be entered when creating a task, or added/deleted/enabled/disabled in the task detail "Overview".
- **Task templates support preset categories and task-level interception/allow rules**: a template can save a task category and a set of task-level rules, and applying the template brings them into the new task form.

### Operation Review

#### Fixed Issues

- **Tightened the model judge output protocol to reduce false allow due to truncation**: the upper limit for the decision explanation (comment) is lowered from 500 Chinese characters to 120 Chinese characters, and "output only JSON, no preamble or code block" is enforced, avoiding the decision being truncated by `MaxTokens` and thus unparseable, which would lead to allow-by-model-failure-policy (fail-open).

### Task Archiving

#### Fixed Issues

- **Skip symbolic links instead of failing the whole archive when encountering them**: the archive format end-to-end only supports regular files and directories, so previously any symbolic link appearing in the working directory caused the entire task archive to fail; now it skips that symbolic link and logs a record, and the remaining files are archived normally (no following links, no escaping the directory tree).

### Account and Compliance

#### Added Features

- **Added a "Terms of Use and Disclaimer" dialog before login**: login requires checking the agreement first.

### License and Dependencies

#### Changed Features

- **The project adopts the AGPL-3.0 open-source license**, and the README's license and disclaimer descriptions are improved.
- **Upgraded norma to v0.4.1**.

### Contributors

- [@Autumn-27](https://github.com/Autumn-27)

## [0.3.12] - 2026-09-17

### Exploration Chain Broadcast Board

#### Added Features

- **The task detail page adds an "Exploration Chain Broadcast Board"** (#144): presents the task's exploration nodes (origin/goal/intent/fact/vulnerability/hint/compression) in a timeline-stream perspective, supporting filtering by type, keyword search, forward/reverse order, pagination, and auto-refresh; when at "the first page with newest first" it is the live position and refreshes each round, and when leaving that position it only accumulates an unread count without disturbing current reading, with a one-click return to the latest via "N new broadcasts · Back to latest". Grouped by day, and paging through long tasks still identifies "which day this was".
- **Each broadcast board row displays the node id** (#145), making it easy to compare against the exploration chain graph and locate a specific node.
- **Broadcast board node details expand to show upstream/downstream and anchored assets** (#147): expanding a broadcast shows the node's upstream (where it came from) / downstream (what it produced) relationships, and hovering over a related item pops up the corresponding node's card (type/status/source/time/summary/payload fragment); it also lists the assets anchored to that node (type tag + identifiable text). The related data is delivered together with the broadcast page, and expanding does not send extra requests.
- **Broadcast board search supports filtering by node id** (#150): the search box adds node id matching besides "content / source", and entering pure digits or the "form #41" displayed in the UI precisely locates the corresponding node.

### Intent Management

#### Added Features

- **Intent deletion supports two modes: "soft delete / hard delete"** (#149): pending, running, and paused intents can all be deleted, deletion requires entering a reason, and the deletion mode is selected in the confirmation dialog.
  - **Soft delete (default)**: the intent is set to "deleted" and the reason is recorded in a separate field, preserving the intent node and all its outputs and lineage.
  - **Hard delete**: physically removes the intent and the exclusive descendant nodes that are "supported only by it" (cascading along the output / intent chain to leaves), avoiding leaving orphaned data; the token metering of the deleted intent is archived and retained by the original date; shared nodes (still referenced by other intents), goals, and task-root facts are always retained, and the hard-delete dialog shows the estimated number of affected nodes in the cascade.

  Both modes notify the planner that "this intent was deleted by the user + reason" and re-plan accordingly.

### Operation Review

#### Added Features

- **Approval records support filtering by status and decision source** (#139): filter approval records by approval status and decision source to quickly locate target records.

#### Fixed Issues

- **Pending approval requests load independently of history pagination** (#133): pending requests are no longer affected by history list pagination and remain fully visible when browsing history.

### Agent

#### Changed Features

- **Worker role description changed to a generic cybersecurity platform statement** (#138).

#### Fixed Issues

- **Fixed cold-node compaction never triggering**: the planner running per task is now connected to a Compactor; cold-node compaction (cold-digest) previously never ran because it was not wired in, and is now restored.

### Chat

#### Added Features

- **Chat supports @ references to multiple types of records and scroll pagination** (#135): can @ reference various types of records in chat, and the reference candidates support scroll pagination loading.

#### Fixed Issues

- **Long message bubbles are constrained within the conversation panel** (#137): overly long message bubbles no longer overflow the conversation area.
- **Fixed "missing upload file" when uploading a file before a new conversation is created**: the Composer's file selection snapshots the `FileList` into an array before clearing the input and then calls back; previously the draft state required first creating a session asynchronously, and when execution resumed the `FileList` live-bound to the input had already been cleared, causing the upload to lack the file field and the backend to return 400.

### Traffic

#### Fixed Issues

- **The traffic recording proxy listens on 127.0.0.1 by default** (#129, #130): avoids exposing the proxy on other network interfaces under the default configuration.

### Web

#### Fixed Issues

- **Fixed the static-export task list page missing the global header**.
- **Completed the demo vulnerability detail page's related-traffic mock and fixed the blank page**.

### Contributors

- [@Autumn-27](https://github.com/Autumn-27)
- [@RuoJi6](https://github.com/RuoJi6)
- [@dingpotian](https://github.com/dingpotian)

## [0.3.11] - 2026-09-15

### Operation Review

#### Added Features

- **Approval records support source location** (#125): clicking the "Source" of an approval record jumps back to the tool execution that triggered that approval — opens the original full conversation, auto-paginates to the target position, expands the command and result in place and centers/highlights them in the scroll area, keeping the messages before and after readable; auto-correction stops when the user manually scrolls. Across normal conversations, task Workers, the planner, and main Agent segmented sessions, it is precisely located via a persisted `tool_use_id` and task mapping (a new in-scope call ID index is added), and when missing, duplicated, or ambiguous, it clearly indicates this rather than jumping to another execution. A clear prompt is given when the conversation is deleted or the task is archived/records are missing.
- **Approval records support paging** (#115).

#### Changed Features

- **Slimmed down the model review input** (#124, #125): the model review (LLM judge after no rule matches) input is converged to a versioned JSON of "the current single complete tool call + explicitly selected short background + local working directory". The background only takes the real user message (the current user message of the chat/task main Agent); the Worker no longer attaches an intent summary and also clears the background inherited from the upper-level Agent; the planner and auto-triggered sessions do not fabricate a user message. The task description, goal, operation constraints, global exploration situation, historical calls, or the Worker's full intent are no longer attached — these are still used per the original logic for Agent execution and independent session auditing, they just do not enter operation review. All decisions require outputting JSON `decision`/`comment`, and the explanation must include "the actual operation, the consequence if it succeeds, the matched rule"; instructions in parameters or background cannot change the review policy. A snapshot and fingerprint of the input actually sent to the model are saved, old snapshots are retained and versioned, and old inputs are not fabricated with current data.

#### Fixed Issues

- **The model decision is no longer silently allowed when wrapped in a code block** (#126): if the decision JSON returned by the model is wrapped in a ``` code block, previously the parse failure would be silently allowed via the model-failure policy; now the code block fence is stripped first before parsing, and only if parsing still fails is the configured failure policy applied.

### Agent

#### Added Features

- **Added experimental noa context compaction** (norma upgraded to v0.4.0): an experimental feature that can be enabled in system settings, off by default. When enabled, noa (model-driven context compaction) takes over context compaction for the four Agent types: main Agent, planner, Worker, and chat, replacing the built-in compaction; the compaction source text is persisted and archived, centrally located under `<workDir>/noa/<session-id>/` (not scattered in each task directory), with a globally unique directory per session ID. If the integration fails, it automatically falls back to built-in compaction without interrupting real tasks; the switch is read once per run, and toggling it only affects runs started afterward.

#### Fixed Issues

- **The exploration graph tool refuses rather than crashing on nil when there is no task context**: calling exploration-graph-related tools in a non-task context returns a clear error, no longer crashing due to dereferencing an empty store.

### MCP

#### Added Features

- **Support for legacy SSE MCP services** (#117): compatible with MCP servers that only provide the legacy SSE transport.

### Traffic

#### Fixed Issues

- **Traffic search matches the host of a record in a port-aware way** (#114): the host matching of `traffic_search` includes the port, avoiding records of the same host on different ports from interfering with each other.
- **A `traffic_search` description migration error no longer interrupts subsequent reporter migration**: a single migration failure is isolated and does not affect the execution of subsequent migrations.

### Web

#### Changed Features

- **Added a divider to the task retest vulnerability options** (#122): added separation between the retest vulnerability choices for clearer visuals.

#### Fixed Issues

- **Fixed the demo task detail page crashing the whole page**: in mock mode the task detail "Sessions" tab fetches `GET /api/tasks/<id>/side-questions` (side-question history), but the mock handler has no such route, and the read fallback returns `[]` by the "path ends with s means a collection" rule, causing `data.items` to be `undefined`, and the side hook's `merge()` to throw `TypeError: t is not iterable` when iterating it; this exception occurs in the `setItems` updater and is deferred by React to the render phase where it is re-thrown, so the caller's `catch` cannot catch it, and the whole page is taken over by the error boundary showing "This page couldn't load". Now the mock handler explicitly returns an empty side-question history, and `sideAPI.history` also normalizes the return value (non-array `items` is uniformly coerced to `[]`) as defense in depth.

### Contributors

- [@Autumn-27](https://github.com/Autumn-27)
- [@RuoJi6](https://github.com/RuoJi6)

## [0.3.10] - 2026-09-13

### Network

#### Added Features

- **Network search adds the DeepSeek official source**: it directly reuses the currently active LLM configuration. It is different in nature from the other three sources — DeepSeek has no directly callable search API; search only exists inside its Anthropic-compatible interface (`web_search_20250305` server-side tool), executed by the DeepSeek server. Therefore it **only supports the DeepSeek official endpoint + anthropic protocol** (OpenAI protocol endpoints will directly reject the server-side tool), and each search **additionally consumes one model call**, the request **does not go through the search egress proxy**, and it is **not counted in traffic records**; the returned result has **only a title and link** (no summary; when the body is needed, WebFetch fetches it). The settings page explains the above limitations but **does not perform validation blocking**; whether they are met is confirmed by the user themselves, and the "Test Search" button can run one real check.

### Agent

#### Added Features

- **The worker gains cross-work review capability**: added `search_all_worker_traces` (without needing to know the intent_id first, globally searches all work execution processes in this task for hits by keyword) and `get_worker_trace` (after locking a specific work, lists the step stream, searches in place by keyword, and fetches full content by step_id), used to reuse observations seen by another work but not written into a fact, avoiding duplicate labor.
- **`node_detail` delegated to the worker**: together with the review tool above, the worker can directly query a node's full details after obtaining the intent_id / node id.
- **Planning rounds triggered by `add_hint` are now explicitly broadcast**: previously adding a hint only folded the hint into the situation overview and let the planner discover it; now one `add_hint` records a trigger to the planner saying "a person added N strategic hints: …" (batched as one, not flooding one by one), and the planner is explicitly told "this round was triggered by newly added hints" and directly sees the hint content.

#### Changed Features

- **The global situation hint is changed to a "loosening" tone**: encourages exploratory divergence and timely reporting of cross-intent leads, rather than premature convergence.
- **Slimmed down the worker boundary wording**: initial blockage does not mean exhaustion; focus on "exhausting the bypass means within this intent before concluding".
- `insert_assets` removes the per-asset `related` input: its only purpose was to decide whether to add this asset to the current task scope, and the value is not stored (the same asset registered again voids it, and the UI cannot tell who was judged irrelevant), so it actually just made the model make one extra unrecordable judgment.
- **Test scope (`task_scope`) is no longer affected by the asset coverage toggle**: the auto-in-scope (`source='auto'`) of `insert_assets` runs regardless of whether coverage is enabled, and `add_task_scope` is always provided to the planner / task main Agent / goal decomposition Agent. Scope is the task's authorization boundary and the filtering basis for asset queries; the coverage toggle only decides whether to use it as the denominator for computing metrics, and should not decide whether to accumulate the scope itself. Previously disabling coverage caused both the `auto`/`agent` write paths to fail, and `task_scope` was left with only manually added rows from the UI. `list_untested_assets` is still hidden when coverage is disabled (it is itself a pure coverage-perspective view).

#### Fixed Issues

- **Fixed assets crossing between tasks** (#59): `list_assets` previously hardcoded the task id to 0 and queried the entire shared asset library, and the model had no way to write a scope filter condition, so it took assets of other tasks (especially IPs) as this task's targets, misdirecting the testing direction. Now it only returns assets within the test scope (`task_scope`) of [this task and its directly associated tasks], matching by **ownership** rather than literal value: if the scope contains a root domain, all its subdomains/services/endpoints can be found; if it contains a network segment, the hosts and services within can be found; and fetching an asset outside the scope directly by id also returns nothing. Non-task contexts (Auto/pentest) have no scope to rely on and still fall back to the full library. Also fixed the ownership blind spot where an IP directly connected host (e.g., `http://1.2.3.4/api`) could not be matched by a network segment scope. The UI's "Test Assets" view (filtered by task producer) is unaffected.
- **Fixed the worker cross-work review tool being mistakenly deleted**: after `search_all_worker_traces` / `get_worker_trace` (and `node_detail`) were added to the worker's default tool set, an old "converge worker tool surface" migration unbound them again at startup, so the worker actually could not access these tools. They have been moved out of that unbind list, and for databases that have already run the old migration they are re-bound to the worker once.
- **Fixed occasional `/btw` side-question persistence failures**: before the side_question checkpoint is persisted, strips the NUL (`\u0000`) escape not supported by JSONB, avoiding errors when writing content containing that character.

### Triggers

#### Fixed Issues

- **Merged trigger sessions deduplicate task description/goal by task**: when multiple tasks are merged into one trigger, the same task's description/goal is no longer repeatedly concatenated into the message, avoiding long goals piling up repeatedly and blowing up the context.

### Contributors

- [@Autumn-27](https://github.com/Autumn-27)

## [0.3.9] - 2026-09-11

### Agent

#### Added Features

- The task main Agent supports **multiple sessions**: you can create, switch, and independently reset the context, and each session is interactive.
- Supports persistent `/btw` **side questions**: follow-up questions without interrupting the main thread, Q&A is persisted, and survives restarts.
- `spawn_task` adds `source_task_ids`, allowing a new task to **read-only inherit** the assets and findings of source tasks.

#### Changed Features

- Disabled the worker's cross-engagement memory and converged its default tool surface: reading context and cross-work review are planning responsibilities; the worker only handles the execution and write-back of a single intent.
- Slimmed down the worker default prompt; `get_worker_output` removes the `terminated` and `worker_name` fields; slimmed down the descriptions of `insert_assets` / `list_assets`.

#### Fixed Issues

- Fixed work being interrupted early due to an **idle turn**: the model sometimes uses a whole round to only output thinking, giving neither body text nor calling a tool; at this point the harness sees a natural end (`end_turn` with no `tool_use`), and directly ends with `completed` + an empty summary — an intent not yet finished is cut off halfway, manifesting as "the task ended normally but there is no text summary". None of the five LLM retry layers can reach it: it is not an error, the entry for same-provider safety-window retry is "stream failure", the circuit breaker directly judges success when `err == nil`, and intent re-run only recognizes `model_error`; the SDK's empty-response retry also cannot reach it, because that layer judges emptiness by "whether any event has been yielded", and thinking increments are themselves events. Now the work's Stop hook recognizes such a round and injects a continue instruction, letting the model continue with the thinking already produced — deliberately not choosing "resend as-is", because such idling is usually determined by the prompt and context shape and is stable behavior rather than a random glitch, and resending would only make the model think again.
- Reused the **empty-response retry count** in the "Retries and Backoff" section of the LLM page (both answer "the model finished but produced no substantive content", just with different criteria and means), defaulting to 2 times, and entering `-1` turns it off and reverts to the original "idle ends the round" behavior. It is a **total** upper limit for one intent rather than a consecutive count: the harness itself already limits consecutive idling to only one push, and only when a tool round has actually occurred does the quota refresh, so this number blocks loops like "tool → idle → push → tool → idle" from exhausting the intent budget. When triggered, the log records `[work <worker> · #<intent>] idle turn…inject continue instruction (n/N)`. See `docs/LLM重试设计.md` §1.1 for design.
- Fixed the context budget and input layout of `/btw` in long conversations; in a non-secure context (non-HTTPS access) it downgrades when generating the side-request ID.

### Traffic

#### Added Features

- A vulnerability supports **associating multiple traffic evidence items**, sortable, with notes, and distinguishable by role (request/response/corroboration).
- The report Agent will **automatically associate** relevant traffic evidence before writing the report.
- Added the Agent **traffic-binding toggle**, and completed the handoff of evidence between Agents.

### Tasks

#### Added Features

- Supports **session-level vulnerability retest**: the retest runs in a separate Agent session, and the list and details show the running status.

### Interception

#### Added Features

- Approval records add **details and execution auditing**: view the tool request context, the model/rule initial judgment, the execution output, and the parameter fingerprint.

### Assets

#### Added Features

- Task test assets support **DSL search**.

### LLM

#### Fixed Issues

- Fixed the connection test not resending custom session headers, causing opencode zen to return 400.

### Network

#### Added Features

- MCP's HTTP transport supports **skipping TLS certificate verification**, making it easy to connect to services with self-signed certificates.

### UI

#### Added Features

- The session list is grouped by Agent, supporting expand/collapse and independent pinning; conversations support filtering by Agent.
- The attack chain graph renders compaction (digest) nodes and collapses their members.

#### Fixed Issues

- Fixed the blank page caused by unsynchronized login credentials.
- The interception prompt wording is changed to explicitly indicate "platform control", avoiding being misread as target-side defense.

### Deployment and Update

#### Added Features

- Supports **one-click page update**: a "Version and Update" card is added to the system configuration page, and the top bar lights up a hint when a new version is available. The flow is: download the release package → verify `SHA256SUMS` → smoke test → stage → exit and be re-launched by the daemon script to complete the swap, and the page auto-refreshes.
- Added the daemon startup scripts `start.sh` / `start.bat` as the official startup entry (the release package and Docker image both include them, `install.sh` unchanged), which decide whether to re-launch based on the exit code and are responsible for forwarding SIGTERM to artex. The verification and swap logic are all in Go, and the scripts stay simple.
- Failure fallback: if verification or the smoke test fails, it is discarded and the current version keeps running; if the new version fails to start 3 times in a row, it rolls back to the previous version. The settings page also has a manual rollback (note that the database schema does not roll back).
- Updates only recognize GitHub domains and enforce HTTPS; the release source is not configurable; one-click update is disabled for development builds. GitHub query results are cached for 30 minutes to avoid the top-bar hint exhausting the API quota.

#### Known Limitations

- Under Docker, only the program is swapped, not the image: the toolchain does not upgrade with it, and rebuilding the container reverts to the image's built-in version, so use `docker compose pull artex` when needed.
- The `skills/` in the release package are not synchronized, and built-in skills newly added in the new version do not take effect automatically.
- Updating means restarting, which interrupts running tasks.

### Dependencies

#### Changed Features

- Upgraded norma to v0.3.7.

### Contributors

- [@Autumn-27](https://github.com/Autumn-27)
- [@RuoJi6](https://github.com/RuoJi6)

## [0.3.8] - 2026-09-09

### LLM

#### Added Features

- The LLM page adds a "Retries and Backoff" tab: the **count** and **interval** of the five retry layers are both configurable. A model call's failure goes through five layers from inside to outside: connect retry (SDK, connection reset / timeout / 429 / 5xx before the stream starts), empty-response retry (SDK, normal end but no content at all, openai format only), same-provider safety-window retry (stream-break replay before any output is delivered to the caller), polling circuit breaker (cooldown and skip after consecutive failures reach a threshold), and intent re-run (the whole intent is re-run after the worker ends with model_error), with the inner layer used up before the outer layer takes over. Each layer has two knobs with unified semantics: leave empty = use the original default count and exponential backoff; enter a count = use that count; enter an interval = replace exponential backoff with a fixed interval; enter -1 = turn off that layer of retry. The first three layers follow the endpoint and can be overridden per field in each model configuration to override the global default (those pinning only the interval still inherit the global count); the circuit breaker and intent re-run are process-level semantics, with only one global instance. Changes take effect hot after saving, without a restart. All empty means the current behavior, and old databases are byte-for-byte unchanged after upgrade (new columns default to 0, and a missing settings key means all defaults). The connection test deliberately does not carry these parameters — it has a 30s hard timeout, and layering the user-configured retries on top would only turn a usable endpoint into a timeout failure. See `docs/LLM重试设计.md` for design.
- Each LLM configuration supports a custom **session header** (`session_header_key`): when non-empty, every LLM request carries that HTTP header, with the header value being the current running session id (chat sessions are `conv-<id>`, workers are `exp<x>-worker-i<intent>`, etc.), used by some gateways that do prompt caching / sticky routing by session-id header. In implementation, the session id is read from the request context and injected, without needing to modify norma, and the same shared provider can also send different header values per session. Old databases get a migration, and the frontend LLM configuration dialog adds an input box.

#### Fixed Issues

- Fixed saving the LLM configuration erroring with `23502` when the session header field is empty: the column is `NOT NULL DEFAULT ''`, and previously `NULLIF($n,'')` was mistakenly applied, writing an unfilled session header as NULL and triggering the non-null constraint; now it is passed directly per empty-string semantics.

### Agent

#### Added Features

- Wall-clock timeout is changed to **graceful in-place wrap-up**: relying on norma v0.3.6, when `MaxDuration` expires, it interrupts the running tool and continues on the live ctx for the wrap-up rounds (writing back recognized content + a summary, with the final state being timeout), no longer relying on the worker/planner's separate `maxDur+90s` external hard ctx to kill a stuck run as `aborted_tools`. Chat goes through the same harness and automatically gets the same behavior; mainagent is unaffected as it has no `MaxDuration`.
- Stall fallback: when the planner is woken by a heartbeat / no-change wakeup and there are no open or running intents anywhere in the graph, the opening remarks change to a stall alert, clearly stating that no worker is running and no direction is queued, and this round must produce one or more new intents that do not duplicate each other (must not produce 0 intents).

#### Changed Features

- Worker prompt refactor: the intent / startup instructions / anchoring-asset raw JSON are moved into the system prompt, re-assembled each round and never compacted away, and continuation does not depend on the first message of the transcript being retained; the startup user message is slimmed down to just the global situation overview (degradable, tolerant of stale). The cost is that the system prompt mixes in per-intent data and loses cross-intent cache reuse, a deliberate trade-off for "not losing the intent".
- Slimmed down the planner / worker default body text and fixed several real-world issues: the planner distinguishes `recent_done` by status (blocked/exhausted first check the trace before deciding, neither treating as a dead end nor blindly re-running), negative conclusions are changed to "observation / doubtful, not conclusive" and evidence is checked before believing, if the goal is unmet and there are no open/running intents it must produce (hard bottom line), depth takes priority over coverage; the worker's negative-type conclusions only write "observation + tentative reading", the judgment right belongs to the planner, cross-intent leads are written into a fact's summary for the planner, not chased by itself. A reseed appends the new defaults as a new version and switches to it, while user-customized / old versions are retained in history and can be rolled back.
- Converged the work agent's default tool set and asset write-back hints: the worker is only responsible for the execution and write-back of a single intent, and reading context / cross-work review are planning responsibilities, so `list_facts` / `node_detail` / `list_companies` and cross-work search (`search_all_worker_traces` / `list_worker_traces` / `get_worker_trace`) are removed from its default tools, leaving only `list_findings` (dedup before reporting a vulnerability) + `add_finding` / `record_fact` + `insert_assets` / `list_assets`; the prompt deletes the stale field descriptions that conflict with the `insert_assets` schema (`type=tech`/`on_url`/`props`). Old databases get a one-time migration stripping the corresponding bindings; the planner/main's identically named bindings are untouched.

### Exploration Graph

#### Added Features

- **Exploration graph cold-node compaction (cold-digest)**: folds old and long-inactive intents / facts into digest nodes displayed in `graph_overview`, with the original nodes permanently retained and fully restorable by id (lossless storage, only compressing the presentation, folding is reversible). The cold/hot determination is reverse-reachable + any active branch means hot, with R=6 rounds of debouncing and connected-component grouping; background compaction (minor folds uncovered cold blocks, major re-sources and re-compresses merged fragments) carries activity re-check and cooldown mutual exclusion, never going on the hot path and never overwriting revived nodes. The overview side provides `cold_digests` and an asset-indexed `cold_index`, and `expand_digest` / `expand_index` handle restoration. The overview of associated / inherited tasks also reuses their own folded view, and `expand_digest` supports cross-task read-only restoration. `expand_digest` / `expand_index` are given only to the planner and main Agent, not to the worker. All come with old-database migrations.
- `graph_overview` outputs the full `finding_list`: findings are the highest-value output of a task and are usually not many in a single task, so they are now all brought out in the overview (unlike facts, which only give a recent window), letting the planner/worker see all confirmed vulnerabilities at a glance each round without having to call `list_findings` again. Each is condensed to `{id, summary, evidence?, from_intent?, assets?}`, where affected assets are given directly as readable content (url / domain / ip:port) rather than a bare id.

#### Changed Features

- `graph_overview` no longer tiles `hosts`: the coverage block removes the host list (in large-scope tasks it carries up to 500 host strings per round, of limited value to planning decisions), keeping only `host_count`, with specific hosts queried on demand via `list_assets`. A new top-level `done_intents_total` (total ended intents) is parallel to `recent_done_intents` (truncated to ≤15), so the planner knows whether truncation occurred when deduplicating.

### Tools

#### Changed Features

- The default binding of `add_company_scope` is changed from worker to planner: defining the enterprise asset scope is the responsibility of planning / main control / Auto, and the worker only executes exploration. New databases seed the default binding as mainagent/planner/auto, and old databases get a one-time migration.
- The planner's default binding of `list_assets`: now it simultaneously has `list_assets` (full-library DSL search) and `list_untested_assets` (untested within scope), with old databases backfilled once without overriding user unbindings.

### Tasks

#### Added Features

- The task list adds a "Running Workers" column: counts the number of intent nodes in that task with `state='running'`, with the same definition as the "Running Workers" in the task detail page overview, and shows 0 when there are no running workers.

### Network

#### Added Features

- Added **global egress proxy** configuration: all target traffic can exit the network through a unified global proxy (http/https/socks5, supporting `user:pass`). When traffic capture is enabled, it serves as the upstream of the MITM recording proxy (traffic is recorded and then exits through the proxy; both the interception and passthrough paths go through the upstream and do not leak the source IP); when capture is disabled, it is injected directly into the agent's bash environment and WebFetch (`proxyEnv` adds `ALL_PROXY` support for socks5). The configuration is stored in the settings KV table (no migration needed), and the frontend system configuration page adds a "Global Proxy" card, independent of the network search proxy and LLM proxy.

### UI

#### Fixed Issues

- Fixed semantic errors in intent status labels: `exhausted` "Exhausted" → "Budget Exhausted" (actually means the step/time budget was cut off midway, writing back only partial results, not that the direction has been fully explored), `blocked` "Blocked" → "Execution Error" (actually means model/API/network failure retries were used up and the intent was basically not really explored, not target/WAF blocking), and added the previously missing `stopped` "Stopped" (work manually stopped by the user).

### Dependencies

#### Changed Features

- Upgraded norma to v0.3.4: MCP tool output adds truncation and disk spilling (see `651b961`), and subsequent v0.3.6 supports in-place wall-clock wrap-up.

### Contributors

- [@Autumn-27](https://github.com/Autumn-27)

## [0.3.7] - 2026-08-31

### LLM

#### Added Features

- Model configuration adds "Output Limit", and lets you choose which request field name to use for the limit: the limit restricts how many tokens a single reply generates at most, sent with each request, 0 (default) = do not send that field, leaving it to the server default; it is a different thing from the "context window" — the latter is the model's total capacity, only used locally to compute the compaction threshold, and does not appear in the request. The field-name switch only matters for the `openai` (Chat Completions) format: empty (default) sends `max_tokens`, which almost all compatible gateways only accept; OpenAI's official reasoning models (o series / GPT-5) conversely only accept `max_completion_tokens`, and receiving `max_tokens` will directly error with `unsupported_parameter`, so such endpoints need to be manually switched to the new field. Both formats have their field names fixed (Anthropic uses `max_tokens`, the Responses API uses `max_output_tokens`), so for non-openai formats this switch is grayed out and cleared on save. At the same time, a previously never-connected link is completed: planner / worker / chat / main Agent / goal decomposition all five places never set an output limit, the openai series simply does not send it, Anthropic falls back to the SDK's 8192, so just adding a configuration item without wiring this line means the entered number would not take effect; now the value is resolved per round by configuration like "streaming output", and after a failover switch it takes effect on the next round. Old databases behave completely unchanged after upgrade (new columns default to 0 and empty field name).
- When clicking "Test" in the background, the status code of each HTTP attempt and the gateway's raw response body are logged to the server log (response body truncated to 4K), making it easy to diagnose 401, quota messages, empty frames, returned HTML pages, etc., no longer only seeing the ok/err after UI folding. This log does not depend on the "LLM Records" toggle.

#### Changed Features

- The task LLM configuration chain is changed to be modifiable in any state, no longer restricted to "running / paused / config chain exhausted": after a task ends (done / failed / timeout), the main Agent conversation still goes through this chain, and when a model on the chain has a problem it previously could neither be changed nor continue interaction. The backend HTTP and DB transaction terminal-state blocking are both removed, and the frontend dialog also opens editing and saving for terminal-state tasks. Saving a terminal-state task no longer reopens the quota-blocking of intents (those intents will be moved to open, with no worker executing them and no longer meeting the "re-run intent" condition); to keep running, still go through re-run intent / add goal, which pull the task back into the running state.

### Agent

#### Changed Features

- "Sending a message to a running Worker to steer direction" is changed to reuse the existing pause/resume + transcript resume mechanism, with behavior consistent with the main Agent conversation: previously it went through a self-built intervention persistence protocol that touched the scheduling barrier, resume flow, and a dozen-plus active queries filtering. Now messages are injected via the next round's input, the intent runs directly in a dedicated goroutine, not subject to the 3-slot worker pool limit, running as soon as sent; the frontend keeps the message box and restores the "Continue Directly" button, and sending is carried by SSE. No DB schema changes. Trade-off: messages are only stored in memory with no crash recovery, and when sending a message the task may momentarily exceed concurrency by one worker (low-frequency scenario, acceptable).

### Tasks

#### Fixed Issues

- Fixed the out-of-memory (OOM) crash when archiving large tasks: archiving is changed to write snapshots in a streaming manner, no longer reading the whole task into memory at once; at the same time, three recovery gaps on the cold-archive path are completed — modern installations store traffic only in SQLite, so previously a crash between the PostgreSQL commit and the SQLite commit would leave an archived task's traffic stuck in hot storage with no way to recover; now a staging log is written whether or not there is a historical directory, and a missing `journal.json` is treated as discardable.
- Fixed system lag when there are many tasks: there was noticeable delay when loading conversations and loading the UI. Queries for the task list, task context, and exploration records are optimized together, and the frontend dashboard, conversation page, and task detail page also reduce duplicate requests.

### Assets

#### Changed Features

- Removed the "at most 256 rules" limit on enterprise asset scope: enterprises entering scope IP by IP / domain by domain easily hit this limit, and after hitting it they can only split into multiple enterprises, yet the scope itself is not slower for having more rules. The limit is removed in all three places — frontend, backend, and demo mock; a single rule is still limited to 1024 characters, and the 2 MiB request body limit is retained as a fallback (about forty to fifty thousand rules).

### Skills

#### Fixed Issues

- Fixed uploading a skill archive reporting "Upload failed: zip: unsupported compression": the Go standard library only has the Store / Deflate decompressors built in, and bzip2 and Zstandard packages written by compression software at non-default levels cannot be opened. Now these two decompressors are added (pure Go implementations, no new external dependencies); Deflate64 / LZMA / XZ / PPMd and the like, which indeed cannot be decompressed, as well as encrypted archives, are changed to report a Chinese prompt before decompression, directly naming which file uses which compression method and how to repackage, no longer throwing the underlying English error at the user.
- Fixed skill filename validation wrongly treating Chinese filenames as illegal: the path validation was originally an ASCII whitelist of `[A-Za-z0-9-_./]`, so if a zip contains even one Chinese-named file (like `参考/说明.md`), the whole upload would fail with "archive contains an illegal path". It is changed to a Unicode blacklist: filenames in various languages and spaces are allowed, while still rejecting control characters, invalid UTF-8, zero-width and bidi control characters (RLO filename spoofing), `\ % # ? * : " < > |`, and `..` / absolute paths / empty path segments; the behavior of preventing directory traversal is unchanged. Skill names are also opened up — ASCII is still limited to lowercase letters, digits, and hyphens (agentskills.io spec), and Chinese and other non-ASCII letters can be used directly as skill names, but spaces, dots, and path separators are not accepted.
- Fixed garbled filenames or whole-archive rejection after decompressing Chinese archives produced by Windows compression software: such zips do not set the UTF-8 flag and write filenames in GBK; now GBK fallback decoding is applied before path validation. At the same time, quoted frontmatter like `name: "中文技能"` can also correctly obtain the skill name.

### UI

#### Added Features

- The findings page adds a flat "All Findings" view and sets it as the default, toggled via a page-header Tab with the original "Grouped by Task" view: the grouped view requires expanding each task to see the vulnerabilities, which is a detour when you just want to glance at a list across tasks. The flat view is one large cross-task table (10/20/50/100 per page), with behavior completely consistent with the grouped view — check to export, expand inline to see evidence and the detailed report, edit name/category/severity inline, change disposition status, drill in, delete. Both views share the statistics cards and filter bar, switching does not lose filters, and the current view and filters are remembered locally; polling only hits the current view, and inline changes are written to both caches synchronously, so switching over does not show stale data.
- The findings page adds a "By Asset" view: the left side is an asset tree (enterprise → root domain / IP → subdomain → service → endpoint, with the enterprise level only appearing when the asset actually has an ownership), and the right side is the findings under the entire subtree of the selected node, sharing the same table and the same set of filters with the other two views. The tree only collects assets that have findings, and ancestor chains are filled in on demand (when findings are only attached to the deepest endpoint, the upper levels can still be assembled), and the count on a node is a subtree aggregation deduplicated by finding; findings whose assets have been deleted or that were never associated with an asset go into an "Unassociated Assets" bucket. Unlike the other two views, the asset view does not poll — it only queries when entering the view, changing filters, adding/deleting/editing findings on the page, or clicking the refresh button on the tree; the left tree is a navigation structure, and there is no need to recompute every 5 seconds. Each level of the tree shows only the increment relative to the previous level (subdomains drop the root domain suffix, services show `https :443`, and endpoints show only the path), with the full value in the hover tooltip and breadcrumbs; when there are too many nodes, the entire level drops the endpoint/service levels and gives a hint, and the counts still count toward the upper level.

#### Fixed Issues

- Fixed the conversation records being squeezed and not fully visible in the conversation details on mobile: the conversation list is collapsed on narrow screens, giving the width to the records themselves.

### Dependencies

#### Changed Features

- Upgraded norma v0.3.2 → v0.3.3: added `Config.MaxTokensField`, letting OpenAI Chat Completions' output limit be sent as `max_completion_tokens` (reasoning models only accept this key). The two keys are mutually exclusive and only one is sent, and the default is still `max_tokens`.
- Upgraded `golang.org/x/mod` v0.37.0 → v0.40.0, fixing two dependency security advisories.

### Contributors

- [@Autumn-27](https://github.com/Autumn-27)
- [@neouks](https://github.com/neouks)
- [@begininvoke](https://github.com/begininvoke)

## [0.3.6] - 2026-08-27

### LLM

#### Fixed Issues

- Fixed the "non-streaming" toggle reverting to "streaming" when reopening after saving (#69): the config list endpoint's DTO was missing the `streaming` field, so the response never returned it, the frontend read `undefined` and always fell back to the default streaming; the actual write and DB storage were fine, it just could not be read back. The DTO now restores that field (without adding `omitempty`, so `false` must also appear in the response).
- The connection test is changed to verify that the model actually replies, and to test according to that config's real send/receive mode (#65): previously it only judged whether HTTP succeeded, and a request that got through but the model gave zero replies (thinking exhausted the budget / body swallowed by safety policy / compatibility layer dropped `content`) was also reported as "connection successful", disjoint from the "not replying" behavior in the conversation; and the test always went streaming, so a non-streaming config actually tested a different channel. Now an empty reply is directly judged as failed, on success the model's reply is shown in the prompt, and the current streaming toggle is also brought into the test, keeping "test passed" consistent with "the conversation runs".

### Agent

#### Changed Features

- `list_facts` is changed to pagination + keyword filtering, avoiding blowing up the context in a single call when there are many facts (#74): by default returns the latest 20 items, supports `limit` (upper bound 100) / `before` cursor / `q` summary keyword, and returns `{facts, total, has_more, next_before}`; an overly long single summary is truncated by character count (the full text still goes through `node_detail`). The worker / planner prompts are synchronized to pagination semantics. Old databases refresh the new parameter schema into the tool catalog table via a one-time migration (`SeedTool` inserts only on first insertion, otherwise the tool management page shows "no parameters").

### Tools

#### Added Features

- The "Tool Execution" page adds tool call count statistics (#72): the toolbar "Statistics" button opens a dialog, showing call count, proportion, and failure count by tool (in descending order of count); it reuses the list's task / keyword filters, and the statistics are over the whole result set rather than the current page, fetched only when the dialog opens.

### Dependencies

#### Changed Features

- Upgraded norma v0.3.1 → v0.3.2: fixed silently discarded reasoning/refusal in OpenAI-series responses (Responses adds `reasoning_text`), reasoning field three-name deduplication, bounded retry for empty responses (zero content blocks), and gateway 400 caused by compaction boundary cutting tool pairing (also fixed existing orphans already baked into the transcript).

### Contributors

- [@Autumn-27](https://github.com/Autumn-27)

## [0.3.5] - 2026-08-25

### LLM

#### Added Features

- Each LLM configuration supports a streaming/non-streaming toggle (streaming by default): on uses streaming SSE; off uses true non-streaming (`stream:false`, returning the complete JSON at once), which can bypass some gateways' poor SSE implementations (empty frames, thinking field dropped frames), at the cost of losing real-time progress and real-time token counts during a run. worker / planner / mainagent / chat / goals all dynamically take the value from the currently active config; `llmpool` / `llmrec` / the task runtime's three-layer Provider wrappers are all compatible with non-streaming; `llm_profiles` adds a `streaming` column and `ALTER`s to fill old databases (default `true`, old configs are unaware).
- Supports LLM configurations in OpenAI Responses API format: each config adds a third format `openai-responses` (posts to `POST /v1/responses`), parallel to Chat Completions / Anthropic; `BaseURL` normalization, default model `gpt-5`; the `format` constraint of `llm_profiles` adds `openai-responses` and migrates old databases idempotently; the frontend format dropdown adds "OpenAI (Responses API)". Dependency upgraded to norma v0.3.1 (including the `reasoning_content` return fix).
- Records LLM request/response HTTP raw text: captures the real wire body at the HTTP transport layer, preserving the tool schemas, `tool_use` blocks, and raw SSE frames that the normalized view cannot see; the multiple attempts of norma's internal retries are retained one by one; `llm_records` adds `raw_request` / `raw_response` columns and `ALTER`s to fill old databases; the recording page detail panel adds a "Raw" view toggle and request/response copy buttons (with an `execCommand` fallback for non-secure contexts).

### Conversation

#### Changed Features

- The conversation list multi-select is changed to a "Multi-select" mode toggle: the default list no longer has a permanent checkbox on every row (cleaner look), and the header is changed to an "N total + Multi-select" button; clicking "Multi-select" enters selection mode (checkboxes, select all, batch delete), "Done" exits and clears the selection, and it auto-exits after all batch deletes succeed (if there are failures it stays in selection mode for retry); single-item rename / pin / delete still goes through the per-row ⋯ menu.

### UI

#### Changed Features

- Task management, conversation operations, and traffic viewing experience enhancements (#57): task list column sort preferences can be persistently remembered, inline rename is changed to be triggered directly by an icon (no longer via a menu), and drawer (sheet) interaction and traffic viewing details are optimized.

#### Fixed Issues

- Worker asset tags only show domains and IPs, and correctly handle the scenario of empty tags.

### Agent

#### Added Features

- The planner adds a "quantified acceptance check" when judging goals: when a goal contains quantifiable acceptance conditions (asset test coverage reaches X%, obtain N flags, gain a certain permission), before `prove_goal` it must check the measured value in `graph_overview` (`coverage.pct`, findings counts, etc.); if the measured value does not meet the target, `prove_goal` is forbidden and it must dispatch intents to make up the gap, and must not mark it met early on the grounds of "the main part is done / mostly achieved". Fixed the problem where a goal requiring 100% coverage but with only 40% measured was judged complete.

#### Changed Features

- Rewrote the "0 intents this round" determination basis in the planner prompt: the original text described 0 intents as "the most common and most important principle", which would make the planner stop too early when the goal is unmet and there is still an untested surface in scope. It is now changed to only produce 0 intents in two specific situations — ① all the directions thought of are already covered by open/running/recent_done intents; ② the next step depends on the output of a currently running work, and that output has not yet appeared (should wait until it finishes, and plan on the next wakeup after the graph updates). And a reverse constraint is added: if there is a new direction not covered and not dependent on a running work, or the goal is unmet and there is still an untested surface, do not stop on the grounds that "0 intents is common".
- Strengthened the "evidence threshold for negative conclusions" in the worker prompt: for negative conclusions like "not injectable / port closed / no login entry" that could make the planner abandon an entire direction, it is required to exhaust the reasonable means within that intent before concluding (switch encoding / parameter / path / method); means not exhausted or evidence weak are uniformly marked `confidence=inferred`, avoiding welding an entire route shut with a hasty `observed` negative (an erroneous negative early in a task is especially likely to misdirect the direction and is hard to self-heal).
- The above planner / worker default prompt changes are appended as a new version via one-time migrations (`reseedPlannerPrompt` / `reseedWorkerPrompt`, each with a settings flag guard) and switched to the current version; old versions are retained in version history, and users who customized the prompt can restore them from the version records.

### Operations

#### Added Features

- Added `reset-password.sh` to reset the administrator (username fixed as `ARTEX`) password: supports local / docker deployments, connection info can be explicitly specified or automatically read from `--dsn`/`$ARTEX_PG_DSN`/`config.json`; it uses `pgcrypto` in the database to generate a bcrypt hash compatible with backend login and writes it back to `settings.auth.password_hash`, and no service restart is needed after reset. The password is passed via an environment variable, does not enter the process argv, and is escaped to prevent injection.

### Contributors

- [@Autumn-27](https://github.com/Autumn-27)
- [@neouks](https://github.com/neouks)

## [0.3.4] - 2026-08-24

### UI

#### Added Features

- After checking multiple tasks in the task list, their category can be batch-modified, and the target category supports "Uncategorized" to move tasks out of the current category; the whole batch write is completed in one transaction, and failed items can only be tasks that were deleted after being checked.

#### Changed Features

- The "Task Category" when creating a task is changed from a dropdown selection to a searchable input box: input filters existing categories immediately, and a name not in the database entered and pressed Enter (or clicking "Create" in the dropdown) is created and selected immediately, with the selected item shown as a removable tag; still limited to a single category.

### Conversation

#### Fixed Issues

- Fixed the issue where, when a conversation has a selected LLM config but there is no globally active config, sending a message was blocked by "LLM not configured, cannot chat". The pre-send check originally only looked at the global active config, while actual execution prioritizes the conversation's selected config, and the two parsing logics were inconsistent; now they are unified into the same set (conversation/Agent binding takes priority, global fallback).
- The prompt when unable to chat is subdivided by status: when there is no config at all it prompts "Add a config", when there is a config but it is not active it prompts "Activate or specify a config for this conversation", no longer uniformly showing "Not configured", making it easier to locate.

### Agent

#### Added Features

- For a task whose goals are all achieved, intents dispatched by the main Agent via `add_intent` can be directly claimed and executed by a Worker, and stop as soon as they finish: at this point the Planner no longer runs (a task with no open goals does not enter planning), avoiding it re-judging the goal as achieved and canceling the just-dispatched intent; after the frontier is drained, the task returns to completed. Before the main Agent dispatches an intent, it will ask the user whether to register it as a formal goal based on the intent content; if registered, the task resumes normal autonomous planning.

#### Changed Features

- `get_worker_trace` / `get_task_worker_trace` no longer directly error when `step_ids` exceeds the per-call limit (5); instead it returns the full content of the first 5 steps, and informs in the result via `returned_step_ids` / `omitted_step_ids` / `notice` which were fetched and which remain, and prompts "if already sufficient, no need to fetch again". Duplicate and illegal ids passed in are first deduplicated and removed before counting.

#### Fixed Issues

- The task list is changed to descending order by task id (newest created first), replacing the original sort by creation time. Multiple tasks created at the same moment have identical timestamps and unstable sorting, which, combined with 10-second polling and the random traversal of an in-memory map, would cause them to frequently swap positions in the list.

### Assets

#### Fixed Issues

- Fixed enterprise ownership recalculation being knocked down by single-row dirty data: when `assets.ip` stored a hostname (written by the Agent or the asset API), `a.ip::inet` would throw `22P02`, causing adding/modifying enterprise asset scope and deleting an enterprise to all fail and roll back. Changed to the safe conversion `try_inet()`, so illegal values are skipped rather than aborting the whole statement.
- Unparseable `ip` values are no longer silently skipped: after saving an enterprise scope, it clearly indicates how many assets' `ip` are not valid IPs (including the specific id and value), reminding that these assets will not be attributed by IP/CIDR rules; the same information is also written to the server log, covering paths with no frontend response (deleting an enterprise, scopesentry sync, Agent writes).
- The IP/CIDR matching of task test scope is also changed to `try_inet()`, replacing the original regex guard that only checked the character set (`abc.def`-type hostnames of all hex letters would previously still slip through and trigger the same error).

#### Changed Features

- The `ip` field of `ip`, `service`, and `endpoint` assets no longer accepts hostnames: `insert_assets` and the asset API return per-item errors with `index`, and directly give the correction method (use `type=subdomain` to fill `domain`, or resolve A/AAAA records first), and the Agent can fix them itself and re-insert. Other assets in the same batch are unaffected and are stored as usual.

### Contributors

- [@neouks](https://github.com/neouks)

## [0.3.3] - 2026-08-23

### Worker

#### Added Features

- Running Workers support pausing, resuming, and canceling individual items; pausing preserves intents, facts, and vulnerabilities, and canceling transactionally cleans up the current intent and its direct outputs after execution exits.
- Added the `paused` intent state, an execution barrier, and named termination reasons, preventing late blackboard writes after pausing, canceling, or deleting a task.
- Added an optional task concurrency limit; creating, resuming, vulnerability drill-down, and queue backfilling all go through a persisted FIFO admission path uniformly.

#### Changed Features

- The Worker's single-run wall-clock default duration is changed from 600 seconds to 1200 seconds; a timed-out task revival restarts the timer at the next real execution.
- The real execution states of Worker, Planner, and main Agent uniformly drive the task status badge, fixing the issue where the Worker still showed "Idle" while running.
- The Worker session retains per-item controls and the current session token summary, and the model name is moved to the right side of the current session title for display.

#### Removed Features

- Removed the Worker multi-select, select-all, and batch pause/resume UI, and removed the Worker batch control API and Mock contract.
- Removed the Token badge and Tooltip in Worker list rows; the underlying full Token ledger and task aggregation endpoint continue to be retained.

### LLM

#### Added Features

- Tasks support an ordered LLM configuration chain; when a provider quota shortage is clearly identified it automatically switches to the next configuration, and persists the current configuration, exhaustion state, and error summary.
- Running and paused tasks support editing the complete configuration chain, adjusting the order, and manually switching the current configuration, effective from the next LLM call.
- Auto-switch, manual switch, and full-chain exhaustion write structured system activities, and pop up a deduplicable reminder via the task activity stream.
- Added a task-role model resolution endpoint, uniformly resolving the configuration and model used by the main Agent, Planner, and Worker for their next call.
- Added an optional global LLM Pool, supporting configuration of call order, specified-model failure fallback, health status, cooldown recovery, and manual reset.
- Added an always-on LLM usage ledger, aggregating input, output, and cache tokens by task, session, model, and configuration.

#### Changed Features

- Goal decomposition, Planner, Worker, and main Agent share a task-level LLM runtime (see below for resolution priority).
- Failover only responds to clear quota, balance, or billing errors; ordinary rate limiting, auth, network, server, and context errors do not incorrectly switch.
- The LLM configuration page is changed to a configuration card list and a right-side drawer for editing, and provides Pool polling order, priority, exclusions, health status, and recovery operations.
- The config chain exhaustion information supports line wrapping on narrow screens; the current model is changed from a session-list icon to a text label next to the current session title plus a full Tooltip.
- The resolution order of each role's LLM within a task is adjusted to "Agent binding → task config chain → global": a role with an explicitly bound model always runs on that model, and only unbound roles fall to the task chain, and when the task chain is empty they further fall to global.
- An empty outbound proxy means direct connection, no longer falling back to the `HTTP_PROXY`/`HTTPS_PROXY` environment variables (an explicit `ARTEX_LLM_PROXY` is unaffected); the proxy input supports `socks5://user:pass@host:port` with account and password.

#### Fixed Issues

- A transient streaming failure before submission (before any output is produced) safely backs off and retries on the same provider, significantly reducing the "interrupted after running only one or two tools, no summary, final state `model_error`" situation; quota exhaustion, overly long context, and deterministic 4xx errors are not retried and are still handled respectively by failover and compaction recovery.

#### Removed Features

- Removed the LLM icon-style indicator in the session list, avoiding confusion with Worker status icons.

### UI

#### Added Features

- Tasks support creating, renaming, deleting, and filtering global categories; a new task can directly select a category.
- Added global task template CRUD and a right-side management drawer; a new task can load a preset description and goals, or save the current content as a template.
- New task creation supports associating multiple source tasks and multiple enterprise asset scopes; enterprise scopes are automatically recognized from a single multi-line text box for domains, URLs, IPs, CIDR, ICP, and enterprise keywords.
- Task test assets support real-time addition and deletion, recording the source such as manual, enterprise, inherited, or Agent-discovered; the Worker session title shows the current test assets and a source summary.
- The findings page is grouped by task and provides independent pagination within each group, supporting entering a description for a vulnerability and creating a high-priority drill-down intent.
- Conversations support rename, pin, unpin, and delete; the task list supports batch pause and resume of the current page.
- Traffic details and tool execution details are changed to right-side drawers; HTTP messages are completed with `Host` and highlight the request line, status code, headers, JSON, and markup-language bodies.
- The task list action column adds "Details" and "Pause/Resume" buttons; the conversation send key can be configured in system settings (`Enter` / `Cmd+Enter`, etc.), and the Web search area adds a proxy input box.
- The badge to the right of the conversation title is changed to show the LLM configuration name, and the model ID is moved to a hover Tooltip; tasks can set an optional name, and the task list adds a name column and search (falls back to the description when empty).
- The skills page adds usage count, last used time, and missing dependency statistics, making it easier to locate skills that are not in effect or not installed.

#### Changed Features

- Task titles are changed to focusable detail links; task assets, enterprise assets, finding task groups, and in-group vulnerabilities use server-side real pagination, stable sorting, and exact totals.
- New enterprise creation uses a right-side drawer, and scope entry is unified into multi-line text with real-time recognition, validation, and type preview.
- The main Agent input box supports auto-growth, `Enter` to send, `Shift+Enter` for a newline, and avoids accidental sending during the composition state of Chinese input methods.
- Agent preview, task reports, and related details uniformly use shared Markdown rendering; delete confirmation, long errors, and mobile drawer width are uniformly fixed.
- The task template selection box displays and searches by template name and submits by template ID; the system's original font size is restored, and the app version is uniformly displayed as `0.3.3`.
- Task totals and the current session Token only highlight input, cache-read, and output values; the send button uses a more concise up-arrow icon.
- The test scope in the task overview shows the enterprise name rather than the ID.

#### Fixed Issues

- Ordinary text containing dots is no longer mistakenly judged as an ICP filing number.
- Fixed the issue where variable directories collided with global runtime variables (like `{{.Now}}`), causing the Agent editor variable list to render duplicate keys.

#### Removed Features

- Removed the standalone "Enter" button on task cards; clicking the task title enters details uniformly.
- Removed the dashboard "New Task" button, the enterprise Logo URL input item, and the vulnerability count Badges at the top of the findings page and in task groups.
- Reverted the 10% global font enlargement style, and removed the Worker selection box, model icons, and row-level Token statistics in the session list.

### Agent

#### Added Features

- A new task can directly associate multiple existing tasks and inherit in real time and read-only the goals, facts, vulnerabilities, completed intents, asset scopes, and blackboard context of the direct source tasks.
- The blackboard read tool supports querying source task nodes, facts, vulnerabilities, and execution traces on demand; inherited nodes carry a source marker and all write tools refuse to modify them.
- The enterprise scope tool supports domains, URLs, IPs, CIDR, ICP, and enterprise keywords; keywords are only an Agent scope hint and do not participate in automatic asset attribution.
- The vulnerability drill-down operation creates a high-priority manual Worker intent in the original task with asset anchors and `derived_from` edges.
- The overview adds a "Goal Management" card, supporting manually viewing, adding, modifying, and deleting goals; adding or modifying notifies the Planner and revives the task, while deleting hard-deletes the goal node (cascading deletion of edges and anchors) but does not revive it.
- The main Agent gets the `steer_work` tool by default, which can inject corrective instructions into a running Worker in real time without interrupting or losing progress (still validates that the intent belongs to this task).

#### Changed Features

- Source task intents do not enter the new task frontier; the new task continues to use independent exploration, an execution queue, a working directory, and conversation history.
- Deleting a running intent no longer destroys data: it is changed to stop at `stopped`, require entering a deletion reason, attach the reason to that intent as a fact and write it into the payload, and the planner perceives the `cancelled` trigger (preserving the intent content and reason).
- Main Agent conversation is changed to be driven by the server activity stream, and input can be recovered after a send failure; when opening a session it auto-scrolls to the bottom and keeps the last reply visible after details lazy-load.
- Task pausing terminates the current main Agent, Planner, and Worker calls, but does not prevent the user from actively initiating a new main Agent orchestration session during the pause.
- Cancel, close, and stream interruption uniformly preserve the real termination reason, generated content, run rounds, elapsed time, Token, and unreturned tool calls.

#### Removed Features

- Removed the main Agent client optimistic message echo, avoiding duplicate messages and cross-session stream crossing under pause, failure, or concurrent activity.

### Constraints

#### Added Features

- In the goal decomposition phase, operation constraints (allow/deny) are extracted before decomposing goals, and the main Agent can supplement them at runtime; constraints are injected at the highest priority into the Planner and Worker system prompts, and after debiasing, diversity and surface exploration explicitly obey the constraints.
- The overview adds a "Constraint Management" card with add/delete/edit endpoints; the constraint injection scope can be toggled separately by Planner / Worker (both on by default, read each round and effective immediately). The new table `task_constraints` uses `CREATE TABLE IF NOT EXISTS`, and old databases are auto-created.

### Interception

#### Added Features

- Command interception adds fallback model approval beyond regex/string rules: when no rule matches, the model makes an `ALLOW`/`ASK`/`DENY` semantic judgment, and the failure fallback action and approval timeout action are configurable; the interception page is split into two tabs, "Interception Rules / Model Config", and model judgment results are prefixed with `[Model]` and a reason.

#### Fixed Issues

- Hardened the judge output parsing to avoid a correct `DENY` judgment being mistaken for allow.

### Traffic

#### Changed Features

- Traffic recording is changed to storing the whole exchange in SQLite (large bodies overflow to hash-deduplicated blob buckets), building a trigram full-text index on text bodies, supporting arbitrary substring and Chinese search; deletion degrades to a single SQL transaction, dropping from hours to milliseconds and no longer stalling recording during that time. Added large-body streaming download and the `traffic_search` full-text parameter; old file-tree data needs no migration and is still readable, searchable, and deletable.

### Tasks and Assets

#### Added Features

- Creating a task adds an "Asset Coverage" toggle (on by default): when off, coverage is not computed or displayed, the situation graph only shows assets, `task_scope` is not automatically accumulated, and related tools are removed from the Planner / main Agent; enterprise associations are unaffected by this toggle.
- `insert_assets` adds a `related` marker per asset (default `true`): it only takes effect when coverage is on, and `false` only enters the shared asset library and does not count toward this task's coverage (such as incidentally discovered side sites or unrelated assets).
- Enterprise scope and task test assets uniformly support recognition of domains, URLs, IPs, CIDR, ICP, and keyword text; domains/IPs can create or reuse global assets, and CIDR/ICP/keywords are kept as task scope context.

### Build

- `build.sh --release` supports building Linux amd64/arm64, macOS amd64/arm64, and Windows amd64 in one go, and generates a zip release package with `skills/`, a config example, and a README.
- Uses the Go linker to remove debug info and generate a zip release package; UPX is changed to explicit optional, avoiding its self-extracting ELF segfaulting on some Linux environments at startup.
- The Release Workflow performs a startup smoke test on the Linux amd64 binary, and provides `SHA256SUMS` with the release package.

### Contributors

- [@neouks](https://github.com/neouks)

## [0.3.2] - 2026-08-20

### Added

- Creating a task supports associating multiple existing tasks, inheriting in real time and read-only the facts, vulnerabilities, completed intents, asset scopes, and blackboard context of the direct source tasks; the new task still uses an independent execution queue, working directory, and conversation history.
- Tasks support an ordered LLM configuration chain. When a provider quota shortage is clearly identified, it automatically switches to the next configuration and persists the current configuration, exhaustion state, and structured audit activity.
- Running and paused tasks support editing the LLM configuration chain, adjusting the order, and manually switching the current configuration; auto-switch, manual switch, and full-chain exhaustion are prompted on the task detail page.
- Running Workers support pausing, resuming, and canceling. Pausing preserves blackboard data, and canceling transactionally cleans up the intent and its directly produced facts, vulnerabilities, and execution records after the Worker stops writing.
- Deleting a task can optionally clean up associated assets, traffic, vulnerabilities, and task landing files, and adds a deletion barrier, concurrency protection, and auditable deletion statistics.
- Added an optional task concurrency limit; new tasks that reach the limit queue in FIFO and start automatically when a running slot is released.
- Added task asset pagination, company asset pagination, task category statistics, and a paginated intent Mock contract.
- Added the `build.sh` single-binary build script, supporting static frontend export, asset embedding, cross-platform targets, and build version injection.

### Changed

- The task-level explicit LLM configuration chain coexists with the existing global LLM Pool; the explicit chain continues to use strict quota failover semantics, and when there is no explicit chain it follows Agent binding and global configuration rules.
- The main Agent input box supports auto-growing multi-line input, `Enter` to send, `Shift+Enter` for a newline, and avoids accidental sending during the composition state of Chinese input methods.
- Planner, Worker, and main Agent share a task-level LLM runtime; the task chain uses the smallest context window among the candidate models as the safe compaction threshold.
- The task detail page displays running, idle, and paused states according to real LLM call status; the facts, vulnerabilities, intents, asset references, and graph nodes of source tasks are uniformly marked with source and kept read-only.
- Agent preview, task reports, and related detail views uniformly use a shared Markdown rendering component.
- LLM model configuration adopts a card and drawer interaction, and the model list supports independent scrolling within the drawer.
- Task pausing no longer blocks main Agent conversation: the main Agent orchestration session is independent of task pausing, and new messages can still be sent while paused (pausing only terminates the currently in-progress round).
- The Worker's single-run wall-clock default duration is changed from 600 seconds to 1200 seconds.

### Fixed

- Removed the optimistic echo of the main Agent console and changed to pure server-side data rendering, fixing the message disorder and crossing into other sessions' content in scenarios like pausing or send failure.
- Opening the main Agent session defaults to scrolling to the bottom and fully displaying the last reply: after the last reply's full content is lazily expanded it auto-scrolls to the bottom, no longer being pushed off-screen.
- Fixed the issue where the main Agent session still kept running after a task was paused, and the issue where the orchestration Agent's pausing of a task did not synchronously stop the main Agent.
- Fixed the issue where, when a task was completed and the Planner, Worker, or main Agent was actually running, the task status badge and action button states were out of sync.
- Fixed the issue where late blackboard writes or residual files could be produced between Worker cancel, task deletion, and concurrent writes.
- Fixed the issues of Markdown preview failure, delete confirmation long-text overflow, mobile width, and some task detail buttons not aligned.
- Added named termination reasons to all Agent cancel paths; activity details can display the canceller, final state, run rounds, elapsed time, token usage, and unreturned tool calls.
- Fixed the race where the parent context could preemptively override the named `shutdown` reason when the backend closed, and the issue where the Chinese activity summary was garbled when truncated by bytes.
- Fixed the issue where cancel events with partial streaming output lost the real termination reason, while preserving the content generated before cancellation.

### Contributors

- [@Autumn-27](https://github.com/Autumn-27)
- [@neouks](https://github.com/neouks)

[Unreleased]: https://github.com/Autumn-27/ARTEX/compare/v0.3.10...HEAD
[0.3.10]: https://github.com/Autumn-27/ARTEX/compare/v0.3.9...v0.3.10
[0.3.9]: https://github.com/Autumn-27/ARTEX/compare/v0.3.8...v0.3.9
[0.3.8]: https://github.com/Autumn-27/ARTEX/compare/v0.3.7...v0.3.8
[0.3.7]: https://github.com/Autumn-27/ARTEX/compare/v0.3.6...v0.3.7
[0.3.6]: https://github.com/Autumn-27/ARTEX/compare/v0.3.5...v0.3.6
[0.3.5]: https://github.com/Autumn-27/ARTEX/compare/v0.3.4...v0.3.5
[0.3.4]: https://github.com/Autumn-27/ARTEX/compare/v0.3.3...v0.3.4
[0.3.3]: https://github.com/Autumn-27/ARTEX/compare/v0.3.2...v0.3.3
[0.3.2]: https://github.com/Autumn-27/ARTEX/compare/v0.3.1...v0.3.2
