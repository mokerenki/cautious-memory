---
name: api-recon
description: Call this skill when collecting website API interfaces.
---

# API Recon (Frontend API Recon)

Under **authorized** premises, discover as completely as possible: **backend API** (paths, methods, parameters, response bodies), **frontend routes**, **UI function trigger points** (Tabs, popups, table operations, etc).

---

## Boundaries and Prohibitions (Agent Must Read · Violation = Out of Bounds)

This skill **only performs API / parameter surface recon**, not vulnerability mining or penetration exploitation phases.

### Task Boundaries

| Scope | Allowed | Prohibited |
|---|---|---|
| **Target** | Enumerate paths, methods, parameters, routes, UI trigger points | SQLi/XSS/privilege escalation/brute force/fuzz vulnerabilities, packet tampering attacks, destructive operations |
| **Auth** | Hook + stub/mock bypass of **client** login gate | Requesting or guessing account passwords from users; attempting real login form submission |
| **Runtime** | Hook interfaces without credentials, use mock responses to let SPA enter post-login shell | Processes that depend on real backend sessions to continue |

### Credentialless Dynamic Analysis (Phase 3 Default)

1. Through `preload.js` / `runtime_harvest.js` **intercept and stub** login, permissions, menus, etc. bootstrap interfaces;
2. For business query interfaces, return mock body with **correct structure, business code success, data may be empty**;
3. Enable frontend to render post-login pages even without backend or in 401 environment, triggering more XHR/fetch/WebSocket;
4. **Empty data, blank tables, placeholder UI are all expected**—do not turn to real login or vulnerability testing for this reason.

**In one sentence**: use mock to prop up frontend routes and component mounting, **only record outbound requests**; what the backend returns doesn't matter, what matters is which interfaces the frontend **will still send**.

### Process Hard Prohibitions

| Prohibited | Alternative |
|---|---|
| grep/curl/Read main entry `index-*.js` to extract API paths before Phase 1 is complete | Run `OUTDIR/harvest_static.py` |
| Hand-write `extract_apis.py` and other scripts replacing harvest | Modify `OUTDIR/harvest_static.py` then re-run |
| Repeating the same grep/command after ≥2 failures | Change strategy: read tool_logs, modify harvest, check reference |
| Skip gates A/B, run `scripts/` original directly | Copy to OUTDIR and modify per target |
| Real usernames/passwords, OTP, OAuth, etc. auth | stub/mock (see above) |
| Skip stub on grounds of "getting real data", do privilege escalation/injection testing | Only record outbound, falls within recon boundary |
| Delete, export sensitive data, batch writes, and other irreversible operations | Same for coverage clicks |
| Claim all pages and interfaces obtained without completing runtime + dynamic enumeration | See "Definition of Done" or mark limitations |
| Claim all parameters mastered without completing parameter trigger matrix + diff | Phase 3b matrix + Phase 5 diff |
| Infer required/optional from a single runtime sample | Multi-sample diff or validation rules/error inference |

---

## Two-Layer Model + Runtime Mode

| Layer | Output | Limit |
|---|---|---|
| **Static** (JS bundle) | Full endpoint paths, route drafts, packaging point field candidates | No HTTP methods; parameters require Phase 1b; misses runtime-concatenated URLs |
| **Runtime** (live session) | Method + body + response + dynamic URL + WS/SSE; multi-sample diff completes parameters | Pages must actually render to send requests; single sample insufficient to define required/optional |

| Runtime Mode | Engine | Suitable For |
|---|---|---|
| **depth** | `runtime_harvest.js` (Puppeteer) | API list, METHOD/params/response body, WS/SSE, reproducible batch runs |
| **coverage** | browser + `preload.js` | Click Tabs/popups/tables, deeper feature point coverage |
| **both** | depth first, then coverage | Most complete, longest duration |

**Parameter Methodology** (no universal script): paths use harvest/regex; parameters use **anchor window expansion + UI binding chain + multi-sample diff + error inference** (grep recipes see [reference.md](reference.md) section J).

---

## Definition of Done

All must be satisfied to claim recon is complete:

- [ ] **Static**: Phase 1 harvest outputs `api_static.txt`, `routes.txt`, `js/`
- [ ] **Runtime**: At least one of depth or coverage; coverage/both must have **Hook active + dynamic enumeration loop**
- [ ] **Shell entry**: Accessing business path is not `/login` (note hash routes)
- [ ] **Parameters**: coverage/both complete parameter trigger matrix + `param_samples.json`; Phase 5 merges `params_merged.json`
- [ ] **Depth** (if module page is blank): Phase 4 permission tree restoration and re-run, until **module-level API** appears (not only locale/bootstrap)
- [ ] **Delivery**: Phase 5 outputs complete (see Phase 5 output table); `insert_assets` writes service and endpoint assets

---

## Scripts and Gates

`scripts/` is only a reference template, **prohibited** from running the original directly and treating it as the final result.

**Rule**: read first → modify per target → write to `OUTDIR` (e.g. `recon/`) → record `CHANGES.md`; if not matching, rewrite per methodology, borrowing only structure.

| Gate | When | Reference Script → OUTDIR Copy | Common Required Changes |
|---|---|---|---|
| **A (Static)** | After Phase 0, **before first** harvest/spider run | `harvest_static.py` / `spider_mpa.py` | **Most sites' default regex can run directly**; only change endpoint regex, webpack/Vite `publicPath`, MPA exclude/cookie when manifest/dialect doesn't match |
| **B (Runtime)** | After Phase 2, before depth/coverage run | `runtime_harvest.js` / `preload.js` + `config.json` | Cookie/localStorage keys, neutralize success values, stubs, login regex, api prefix, hash/history |

**SPA Forced Order** (not interchangeable; Phase numbering takes precedence over "explore first, then script"):

| Step | Must | Prohibited |
|---|---|---|
| After Phase 0 complete | Next Bash = `python3 OUTDIR/harvest_static.py <URL> OUTDIR` | curl/grep/Read main entry `index-*.js` (usually >500KB) |
| Gate A | Copy script → small tweaks as needed → **run immediately** | Manually extract APIs first then decide whether to harvest |
| Before Phase 1 complete | `wc -l` validate outputs; 404 modify harvest and retry | Hand-write extract scripts; repeatedly grep undownloaded URLs |
| From Phase 1b | grep only `OUTDIR/js/*.js` | Use main bundle instead of harvest |

- ✅ Copy `harvest_static.py` → (optional) modify regex → **run immediately**
- ❌ curl main bundle → grep multiple times → write temp extract → harvest only at the end
- **MPA**: After Phase 0, next Bash = `python3 OUTDIR/spider_mpa.py ...`

---

## Tool and Output Constraints

| Constraint | Description |
|---|---|
| Large files | `index-*.js` >100KB **prohibited** from Read/grep into context; use OUTDIR scripts for batch processing |
| grep output | Must `\| head -20` or `-m 5`; conversation only retains path summary, do not paste bundle fragments |
| Validation | Use `wc -l`, `ls \| wc -l`; do not Read entire directory |
| regex initial probe | Optional, ≤1 time, only ≤50KB small chunk or HTML; official static based on harvest |
| reference | Recipes/templates/troubleshooting see [reference.md](reference.md), do not repeat inline full text |

---

## Execution Roadmap

```
Phase 0 分类 + OUTDIR
  → 门禁 A → Phase 1 harvest（★ 立刻运行 ★）
  → Phase 1b 参数逆向
  → Phase 2 鉴权三道门 → config.json
  → 门禁 B → Phase 3 运行时 + 参数矩阵
  → Phase 4 权限树（必要时）→ 重跑 Phase 3
  → Phase 5 合并报告 + insert_assets批量插入所有发现的服务、端点api资产，无论如何插入时不允许漏掉已发现的资产
```

Check off in order; **the previous item must be completed before entering the next Phase**.

1. [ ] **Phase 0**: Initial probe SPA/MPA; create `OUTDIR` → [Phase 0](#phase-0--分类)
2. [ ] **Gate A + Phase 1**: Copy script → **immediately** harvest → `wc -l` validate → [Phase 1](#phase-1--静态)
3. [ ] **Phase 1b**: Anchor window expansion + binding layer → `param_candidates.json` → [Phase 1b](#phase-1b--参数逆向)
4. [ ] **Phase 2**: Three auth gates → `config.json` → [Phase 2](#phase-2--鉴权三道门)
5. [ ] **Gate B**: Adjust runtime scripts → [Phase 3](#phase-3--运行时)
6. [ ] **Phase 3**: depth / coverage / both; confirm shell entry; parameter trigger matrix → `param_samples.json`
7. [ ] **Phase 4** (if needed): Permission tree → patch stubs → re-run Phase 3 → [Phase 4](#phase-4--权限树还原)
8. [ ] **Phase 5**: Merge outputs + report + `insert_assets` → [Phase 5](#phase-5--合并与报告)

---

## Phase 0 — Classification

Pull entry HTML, **create `OUTDIR`** (do not modify `scripts/` within skill):

- **SPA**: shell + `<div id=app>` + chunk → Phase 1–5
- **MPA**: SSR + `<form>`, no endpoint bundle → after gate A:

```bash
python3 recon/spider_mpa.py <BASE_URL> <OUTDIR> [--cookie "session=..."] [--max 300] [--depth 5] [--exclude "logout|delete|destroy"]
```

Outputs `forms.txt`, `links.txt`, `api_inline.txt`. SPA if forms ≈ 0 → switch to Phase 1.

---

## Phase 1 — Static

Follow [Scripts and Gates](#脚本与门禁) · [Tool and Output Constraints](#工具与输出约束).

```bash
python3 recon/harvest_static.py <BASE_URL> <OUTDIR>
```

harvest: parse HTML script → webpack/Vite manifest → download all lazy chunks → output `js/`, `api_static.txt`, `routes.txt`, `chunkmap.txt`.

```bash
wc -l OUTDIR/api_static.txt OUTDIR/routes.txt
ls OUTDIR/js | wc -l
```

- chunk count vs manifest: 404 must modify harvest and retry, do not hand-curl each chunk individually
- `api_static.txt` too few → relax endpoint regex inside OUTDIR then re-run (see reference)

### Phase 1b — Parameter Reverse Engineering

path comes from Phase 1; parameter fields require separate recon. Grep rules see [Tool and Output Constraints](#工具与输出约束).

**Completion standard**: key interfaces can answer—field name, transmission location, type inference, whether required, sample value, confidence.

#### 1b.0 — Transmission Form

| Form | Parameter Location | Static Priority |
|---|---|---|
| REST JSON | body + query | `(params\|data\|body)\s*:\s*\{` near path anchor |
| GraphQL | `variables` | gql template, `$page: Int` |
| Traditional form | urlencoded | `<form>`, `FormData` |
| File upload | multipart | `FormData.append` |
| Path parameter | `/user/:id` | route table + `useParams` / `$route.params` |
| Encryption/Signature | wrapped in `sign`/`data` | Hook encryption function input params (reference section D) |

Output: annotate each interface `transport: query|json|form|graphql|encrypted`.

#### 1b.1 — Anchor Window Expansion

With known path as anchor, expand window to find packaging objects:

```bash
grep -n '"/api/user/list"' OUTDIR/js/*.js | head -20
grep -rhoaE '.{0,120}("/api[^"]+").{0,200}' OUTDIR/js/*.js | head -20
grep -rhoaE '(params|data|body|payload)\s*:\s*\{' OUTDIR/js/*.js | head -20
```

| Wrapper Layer | Parameter Clues |
|---|---|
| axios instance | `data` / `params` |
| Unified request | Interceptor injects global fields |
| OpenAPI client | Generated method signatures |
| React Query / SWR | hook second argument |
| Vue composable | composable input params |

Type remnants: `yup`/`zod`/rules, `Form.Item name=`, embedded Swagger.

→ `param_candidates.json`：`{ path, fields[], source: "static-callsite", confidence }`

#### 1b.2 — Binding Layer

```
Form field → onFinish/handleSubmit → transform → API payload
```

| Binding Source | Technique |
|---|---|
| Form submit | Follow submit → transform → API |
| Table search | `getFieldsValue()` → `params` |
| Route | `:id` / `?tab=` |
| Interceptor | Global `tenantId`, pagination, sign |
| Enumeration select | `options` → API enum values |

DevTools call stack from `fetch`/`XHR.send` traces upward to packaging function.

#### 1b.3 — Three Packaging Questions (≠ Phase 2 Three Auth Gates)

| Question | What to Answer |
|---|---|
| **Assembly** | Where payload is built, transform traces |
| **Validation** | required, pattern, enum |
| **Transmission** | path / query / body / multipart / header |

Interceptor gate (Phase 2) also reads global injected fields (Authorization, `X-Tenant-Id`, sign).

#### 1b.4 — Connection with Phase 3

Candidate fields come from static/binding layer; **required/optional/conditional dependency** must be determined by Phase 3 parameter matrix + diff + Phase 5 error inference.

---

## Phase 2 — Three Auth Gates

Grep within `OUTDIR/js/` (with `head`), write to `config.json` (recipes see reference):

| Gate | Question | Keywords |
|---|---|---|
| **Render Gate** | How to determine logged in? | `isLogin`, `getToken`, Cookie/localStorage |
| **Interceptor Gate** | What triggers redirect to `/login`? | `response_code`, `errno`, axios interceptor |
| **Content Gate** | Where do menus/permissions come from? | `menu`, `permission`, `role`, `acl`, `routes` |

Do not treat localStorage key names as credentials—must confirm from chunk/request chain.

**Exit = Gate B**: conclusions land in `config.json`, and modify `OUTDIR/runtime_harvest.js` / `preload.js`.

### Phase 2b — API Observation (Optional)

Use `preload.js` within OUTDIR to confirm session key names, Authorization, nested API URLs:

| Configuration | Output |
|---|---|
| `recordDetail: true` | `__API_RECON_DETAIL__` |
| `observe.xhrHeaders: true` | headers observation |
| `extractUrlsFromResponse: true` | sub APIs within response |
| `observe.storageReads/cookieReads: true` | backfill config |
| `neutralizeVueRouter: true` | `__API_RECON_ROUTES__` |

coverage exports per round: `__API_RECON_LOG__`, `__API_RECON_DETAIL__`, `__API_RECON_ROUTES__`, `__API_RECON_OBSERVE__`.

---

## Phase 3 — Runtime

Must have passed Gate B; follow [Boundaries and Prohibitions](#边界与禁止agent-必读--违反即越界) · credentialless mock strategy.

`config.json` settings `"runtimeMode": "depth" | "coverage" | "both"` (template see reference).

### Hook and Stub (depth + coverage shared)

| Layer | Scope | Purpose |
|---|---|---|
| L1 precise | auth/permission/bootstrap stub | Pass first-screen auth |
| L2 negative correction | All JSON responses | Unlogged code → success |
| L3 fallback | `/api` etc. missed by L1 | Empty success body, prop up UI |

- **depth**: fake auth + `forward` modify business code + `stubs`; traverse `routes` (hash/history); output `runtime_api.json`
- **coverage**: **document-start** inject `preload.js` (CDP `addScriptToEvaluateOnNewDocument` or Userscript)

Validation: `window.__API_RECON_PRELOAD__` exists; business path does not redirect to `/login`.

```bash
cd recon && npm install
node runtime_harvest.js config.json
```

### 3b — Coverage Dynamic Enumeration (Required)

1. Main navigation/sidebar — click each item, wait for network 1–3s
2. Tab — `role=tab`, `.ant-tabs-tab`
3. Table — view/edit/detail on first row
4. Toolbar — export, filter, new (**avoid irreversible deletion**)
5. Upon each module entry — merge API/routes
6. SPA — for paths not covered by `routes.txt`, control `pushState` (MPA prohibited)

**Parameter Trigger Matrix** (required): record once per module by operation type, **diff multi-samples**:

| Operation | Usually Additional Parameters |
|---|---|
| List first screen | Pagination + default filters |
| Click search | keyword, filter |
| Advanced filter | More optional |
| New/Edit | Full entity |
| Batch/export/sort | `ids[]`, `exportType`, `sortField` |

**stub outbound body/headers remain real under stub**—request prevails. Record → `scan_raw.json`, `param_samples.json`, `api_detail.json`.

- **Vue**: `neutralizeVueRouter: true` + document-start preload
- **React**: `routes.txt` + sidebar clicks + `pushState`
- **both**: first 3a depth, then 3b coverage

---

## Phase 4 — Permission Tree Restoration

**Trigger**: module page blank / only bootstrap per route (e.g. locale) → content gate not passed.

| Phenomenon | Meaning |
|---|---|
| Shell entry successful | Render gate + interceptor gate passed |
| Sidebar missing items / blank click | stub shape or permission codes incomplete |
| Same few APIs per route | `v-if permission` not passed |
| `routes.txt` far fewer than bundle | Must supplement from auth module |

```bash
grep -rhoaE '"/api[^"]*(permission|perm|role|menu|acl)[^"]*"' OUTDIR/js/*.js | sort -u | head -30
grep -rhoaE 'userRouteAuth|getResultTree|routeMap|routeLink|menuList|authList' OUTDIR/js/*.js | head -20
```

Typical chain: `role_permissions` (flat codes) + `permissions/all` (tree) → `getResultTree` → `userRouteAuth[CODE].url`.

```bash
python3 recon/extract_route_map.py recon/js recon/
python3 recon/build_perm_tree.py recon/js recon/ --config recon/config.json
```

Intermediate outputs: `route_map.json`, `userRouteAuth.json`, `permissions_tree.json`, `*_stub.json`, `perm_codes_all.txt`.

stub check: outer `response_code` consistent with interceptor gate; flat codes aligned with tree; `routes` covers all `route_map` links.

After updating `config.json` **re-run Phase 3**. Large SPA can adjust `waitUntil`, `routeTimeout`, `perRouteMs` (see reference A3/I section).

---

## Phase 5 — Merge and Report

### Output Table

| File | Phase | Content |
|---|---|---|
| `js/`, `api_static.txt`, `routes.txt`, `chunkmap.txt` | 1 | Static bundle and paths |
| `param_candidates.json` | 1b | Static parameter field candidates |
| `config.json` | 2 | Three gates + runtime configuration |
| `runtime_api.json` | 3a | depth detailed recording (incl. WS/SSE) |
| `param_samples.json`, `scan_raw.json`, `api_detail.json` | 3b | Multi-samples, click logs, detail |
| `route_map.json` etc. | 4 | Permission tree intermediate files (if executed) |
| `params_merged.json` | 5 | Merged parameter fields + confidence |
| `api_merged.txt` | 5 | `METHOD /path [params] [static|runtime|both]` |
| `site_map.json` | 5 | Routes, APIs, params, feature points, limitations |
| **insert_assets** | 5 | Write all service and endpoint assets to asset library |

### 5b — Parameter Merge

Diff from `param_samples.json`; **no universal merge script**. Confidence rules see reference J7 (high/medium/low/pending trigger).

### 5c — Error Inference

Within authorized scope, may send incomplete requests to read 400 (**belongs to parameter recon, not vulnerability testing**): `field 'x' is required`, enum errors, etc. Note `data` wrapping, `variables`, pre-encryption `bizData`.

Report must note: runtimeMode, static/runtime API count, parameter confidence, uncovered modules, `CHANGES.md` summary relative to reference scripts.

`site_map.json` suggested structure:

```json
{
  "site": "https://example.com",
  "runtimeMode": "both",
  "appType": "vue-spa",
  "routeGuardStrategy": ["nav-neutralize", "L1-auth", "L2-patch", "forward"],
  "apisFromStatic": [],
  "apisFromRuntime": [],
  "apis": [],
  "params": [{ "method": "POST", "path": "/api/user/list", "transport": "json", "fields": [] }],
  "frontendRoutes": [],
  "routesVerifiedByClick": [],
  "featuresTriggered": [],
  "limitations": ""
}
```

More fields and grep recipes see [reference.md](reference.md).

---

## General Notes

- **Framework-agnostic**: webpack/Vite/Angular lazy load methods are the same
- **Transmission**: REST/JSON, GraphQL, WebSocket, SSE; gRPC-web out of scope
- **SSR**: client-side fetch can be recorded; RSC/Server Actions not fully enumerable
- **Blind spots**: JSVMP, WASM, HMAC/mTLS strong verification → static + mark limitations
- **Parameter blind spots**: conditional linkage, hidden params, WASM packaging → "pending trigger" / "unreachable"
- **Static is safety net**: when runtime is blocked, static can still enumerate endpoints

---

## Additional Resources

- Grep recipes, `config.json` template, troubleshooting, Hook, parameter reverse engineering section J, site_map template: **[reference.md](reference.md)**
- Reference script paths see [Scripts and Gates](#脚本与门禁) table
