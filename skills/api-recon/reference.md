# api-recon — Reference Manual

Grep recipes, `config.json` templates, and troubleshooting. All greps are run against the `js/` directory. When a bundle is on a single line, first run `js-beautify` or `sed 's/}/}\n/g'`; usually a raw grep with a context window is enough.

## Script notes

All files in `scripts/` are **reference templates** and must be adjusted for the target site before execution. Typical changes:

| Script | Common items to adjust |
|---|---|
| `harvest_static.py` | endpoint regex, webpack/Vite manifest parsing, micro-frontend publicPath, retries/concurrency |
| `runtime_harvest.js` | neutralize field names and success values, stub match rules and body structure, routes source, WS recording, `waitUntil`/`routeTimeout`/`proxy` |
| `preload.js` | `loginPathRe`, L1 stubs, `neutralize.fields`, `apiPattern`, whether L3 is enabled, `recordDetail`, `observe.*`, `neutralizeVueRouter` |
| `spider_mpa.py` | `--exclude` destructive links, cookies, depth/max, same-domain filtering |
| `extract_route_map.py` | `routeMap` / `routeLink` regex, KEY naming pattern |
| `build_perm_tree.py` | `userRouteAuth` parsing, `ROOTS`/`PREFIX_PARENT` hierarchy heuristics, stub outer field names |
| `config.json` | single entry point for all the above site-specific parameters |

Adjusted files are best placed in the task working directory (such as `recon/`), and the report should note the specific changes relative to the reference script.

---

## A. Reverse-engineering the three gates

### A1. Rendering gate — "How to tell whether logged in?"

```bash
grep -rhoaE '.{0,40}(isLogin|isAuthenticated|loggedIn|hasLogin|requireAuth)\b.{0,80}' js | head
grep -rhoaE 'function (getUser|getToken|getAuth)[0-9]?\([^)]*\)\{.{0,200}' js | head
grep -rhoaE '(localStorage|sessionStorage)\.getItem\("[^"]+"\)' js | sort -u
grep -rhoaE '(Cookies?|cookie)\.(get|load)\("[^"]+"\)' js | sort -u
grep -rhoaE '\batob\(|JSON\.parse\(|jwt|decode' js | head
```

Find the chain `isLogin = f(getUser())` → `getUser = decode(storage.read(KEY))`, and determine the **storage key**, **container** (Cookie vs localStorage), and **encoding**:

| Encoding | How to forge in config |
|---|---|
| Plain string / `"1"` / token | `"value": "anything-truthy"` |
| `JSON.parse(x)` | `"value": "json:{\"id\":1,\"username\":\"admin\"}"` |
| `JSON.parse(atob(x))` | `"value": "b64json:{\"id\":1,\"username\":\"admin\"}"` |
| JWT | Unsigned/`alg:none` JWT, or signed with a key found in the bundle |
| Encrypted (SM2/AES/RSA) | Find the hardcoded key; forgeable when the rendering gate only needs a decodable blob; otherwise fall back to static |

→ Write into `cookies` / `localStorage`.

### A2. Interceptor gate — "What triggers a redirect to /login?"

```bash
grep -rhoaE '.{0,60}(interceptors\.response|axios|request\.use).{0,120}' js | head
grep -rhoaE '.{0,40}(response_code|errcode|errno|\bcode\b|\bret\b|\bstatus\b)\s*[=!]==?\s*[\-0-9]{1,4}.{0,60}' js | head -20
grep -rhoaE '.{0,40}(未登录|请重新登录|登录已过期|unauthorized|登录失效|授权|token.{0,10}invalid).{0,40}' js | head
grep -rhoaE '.{0,30}(location\.href|router\.(push|replace)|navigate)\([^)]*login[^)]*\)' js | head
```

Determine the **field name**, **success value** (usually `0` or `200`), and **the failure value that triggers the redirect**. Verify with a junk session:

```bash
curl -sk -X POST -H 'Cookie: <fakekey>=junk' https://target/api/<protected> -d '{}' -H 'Content-Type: application/json'
```

→ Write into `neutralize.fields` + `neutralize.success`.

### A3. Content gate — "Where do menus/permissions come from?"

```bash
grep -rhoaE '"/api[^"]*(permission|perm|role|menu|acl|resource|nav)[^"]*"' js | sort -u
grep -rhoaE '.{0,30}(menus|permissions|menuList|routeList|authList|role_permissions)\b.{0,120}' js | head
grep -rhoaE 'userRouteAuth|getResultTree|routeMap|routeLink|hasPermission|checkAuth' js | head
grep -rhoaE '([A-Z_][A-Z0-9_]*):\{name:"[^"]*",link:"/[^"]+"\}' js | head
```

**Two layers of data** (common in enterprise admin backends):

| API | Typical payload | Consumer |
|---|---|---|
| `.../role_permissions` | `{ permissions: string[], role_type }` | Route guard, button-level ACL |
| `.../permissions/all` | `tree[{ code, position, children }]` | Sidebar menu rendering |
| `userRouteAuth` in the bundle | `{ CODE: { url, name? } }` | code → frontend path |
| `routeMap` in the bundle | `{ KEY: { name, link } }` | Alias resolution (webpack `o.DASHBOARD`) |

Read the consumer code to confirm: how `getResultTree(tree, permissions)` filters, and which field the `v-if` / `hasAuth(code)` check looks at.

**Manual forge** (small sites): build a permissive payload → `stubs`.

**Full permission tree reconstruction** (large sites, sidebar/submodules still blank): see **Section I**.

---

## B. config.json template

```json
{
  "baseUrl": "https://target/",
  "runtimeMode": "both",
  "chromium": "/usr/bin/chromium",

  "cookies": [
    { "name": "auth", "value": "b64json:{\"id\":1,\"username\":\"admin\",\"role\":\"admin\",\"func\":{},\"permissions\":[\"*\"]}" }
  ],
  "localStorage": { "token": "faketoken", "isLogin": "1" },

  "neutralize": {
    "fields": ["response_code", "code", "errno", "ret", "status"],
    "success": 0,
    "flags": { "success": true, "message": "ok" }
  },
  "forward": true,
  "loginUrlPattern": "/login",
  "apiPattern": "/api/|/rest/|/graphql",

  "mockTier": "L1+L2",
  "recordDetail": true,
  "observe": {
    "storageReads": false,
    "cookieReads": false,
    "xhrHeaders": true
  },
  "neutralizeVueRouter": true,
  "stubs": [
    {
      "match": "permissions/all|/menu|role_permissions",
      "body": {
        "response_code": 0, "code": 0,
        "data": {
          "permissions": ["*"],
          "menus": [
            { "name": "dashboard", "path": "/dashboard", "show": true, "children": [] },
            { "name": "alert", "path": "/alert", "show": true, "children": [] }
          ]
        }
      }
    }
  ],

  "explore": {
    "clickTabs": true,
    "clickTables": true,
    "pushStateFallback": true,
    "maxMenuItems": 50
  },

  "routes": ["/dashboard", "/alert", "/asset", "/device", "/report", "/config", "/system"],
  "waitMs": 1500, "perRouteMs": 900, "headless": true,
  "waitUntil": "domcontentloaded",
  "routeTimeout": 12000,
  "proxy": "",

  "captureResponses": true, "recordWs": true, "respMax": 600
}
```

Field notes:
- `runtimeMode`: `depth` (Puppeteer), `coverage` (browser MCP), `both`
- `cookies[].value` prefix: `b64json:` → base64(JSON); `json:` → raw JSON; no prefix → literal
- `forward: true` forwards real requests and rewrites the code fields; `false` is fully offline stubs
- `mockTier`: the preload enablement tier in coverage mode, such as `L1+L2`, `L1+L2+L3`
- `routes` come from `routes.txt`; after forging menus, the harness automatically appends `<a href>`
- `captureResponses` / `recordWs` only work in depth mode
- `waitUntil`: use `domcontentloaded` for large SPAs to avoid `networkidle2` hanging
- `routeTimeout`: `page.goto` timeout per route (milliseconds)
- `proxy`: Puppeteer `--proxy-server`; you can also set `HTTP_PROXY` / `HTTPS_PROXY`

### B1. Dual stub template (role_permissions + permissions/all)

```json
"stubs": [
  {
    "match": "role_permissions",
    "body": {
      "response_code": 0,
      "data": {
        "permissions": ["MONITOR", "MONITOR_ALERT", "THREAT", "ASSETS_RISK"],
        "role_type": "SUPER_ADMIN"
      }
    }
  },
  {
    "match": "permissions/all",
    "body": {
      "response_code": 0,
      "data": [
        {
          "code": "MONITOR",
          "position": 1,
          "children": [
            { "code": "MONITOR_ALERT", "position": 1, "children": [] }
          ]
        }
      ]
    }
  }
]
```

The outer field names (`response_code` / `code` / `data`) must match the A2 interceptor gate; `permissions` must cover all leaf codes in the tree.

---

## C. coverage mode: preload configuration

Edit the `CONFIG` object at the top of `scripts/preload.js`, or replace it before CDP injection:

```javascript
const CONFIG = {
  loginPathRe: /\/(login|signin)(\/|$|\?)/i,
  mockTier: 'L1+L2',
  forward: true,
  recordDetail: true,
  extractUrlsFromResponse: true,
  neutralizeVueRouter: true,
  observe: { storageReads: false, cookieReads: false, xhrHeaders: true },
  neutralize: { fields: ['response_code', 'code'], success: 0 },
  stubs: [ /* 同 config.json stubs */ ],
  apiPattern: /\/(api|apis|v\d+|dev|internal|graphql)\//i,
};
```

Verify: `window.__API_RECON_PRELOAD__ === true` and the pathname is stable.

Export the recording results:

```javascript
JSON.stringify({
  apis: [...window.__API_RECON_LOG__],
  detail: window.__API_RECON_DETAIL__,
  routes: [...(window.__API_RECON_ROUTES__ || [])],
  observe: window.__API_RECON_OBSERVE__,
}, null, 2)
```

---

## D. preload / runtime hook capabilities

Built-in browser hook capabilities of preload (coverage) and runtime_harvest (depth), and their coverage:

| Hook capability | Value for API discovery | Coverage |
|---|---|---|
| Hook fetch / XHR.open | Record request URL/method | ✅ `recordDetail` + `__API_RECON_LOG__` |
| Hook XHR.setRequestHeader | Discover headers such as Authorization | ✅ `observe.xhrHeaders` |
| Hook localStorage/cookie reads | Confirm session key names | ⚠️ Optional `observe.storageReads/cookieReads` |
| Vue route retrieval | Complete frontendRoutes | ✅ `__API_RECON_ROUTES__` (loaded routes) |
| Neutralize Vue route guards / block login redirects | Expand modules to trigger APIs | ✅ `neutralizeVueRouter` + native redirect neutralization |
| React route retrieval | Supplement routes | ⚠️ Static + clicks; no dedicated hook |
| Block page navigation (login path) | Stay on page for analysis | ⚠️ Only blocks login paths, to avoid blocking business navigation |
| Hook crypto libraries (CryptoJS/SM, etc.) | Encrypted params → plaintext API body | ❌ Must manually hook the encryption function's inputs; write conclusions to config |
| Anti-debugging bypass | Otherwise runtime cannot record APIs | ❌ Must be handled manually; static still usable |

---

## E. Endpoint extraction regex (when static finds too few)

Loosen `extract_endpoints` in `harvest_static.py`, or do it manually:

```bash
grep -rhoaE '"/[a-z][A-Za-z0-9_/\-]{3,}"' js | sort -u
grep -rhoaE '/api/[a-zA-Z0-9_./-]+' js | sort -u
```

---

## F. Troubleshooting

| Symptom | Cause → Handling |
|---|---|
| Static APIs very few | endpoint dialect mismatch → loosen the regex (Section D) |
| chunk count ≪ manifest | CSS-only or undeployed chunks; 404s already retried |
| runtime still shows the login page | rendering gate error → recheck A1: key name, container, encoding, domain |
| enters the shell but modules are blank | content gate → forge menus (A3); the `routes` path may be wrong |
| each route only has bootstrap/locale | incomplete permission codes → Section I permission tree reconstruction; check the `role_permissions` + `permissions/all` dual stub |
| sidebar has items but subpages are blank | the tree lacks intermediate nodes, or the code does not match `userRouteAuth` |
| every API redirects to login | interceptor gate → confirm `neutralize`; nested fields need extended walk logic |
| WS frames are 0 | subscribe happens only after user interaction; increase `perRouteMs` |
| response body empty | real responses exist only when `forward: true` |
| Chromium missing | install chromium or set `config.chromium` / `CHROMIUM` |
| many mocks but still returns to login | hook is too late or missing the `location.href` setter → document-start + preload |
| list is entirely empty | L3 empty arrays are normal; keep clicking Tab/settings/details |
| mistaking a Redux action for a route | filter out internal paths containing get/set/change/clear/toggle/upload |
| Vue still redirects to login | preload is not at document-start → change the injection timing; or clear guards manually when `neutralizeVueRouter: false` |
| response contains a URL but it is not logged | enable `extractUrlsFromResponse`; or extract it manually from `__API_RECON_DETAIL__` |
| do not know the Authorization header name | enable `observe.xhrHeaders` or inspect request headers in DevTools |
| runtime extremely slow / timing out | change to `waitUntil: domcontentloaded`; lower `routeTimeout`; do not use `networkidle2` |
| proxy connection fails | check `proxy` / environment variables; Puppeteer and curl must use the same proxy port |

---

## G. Hardened targets

When the server progressively validates sessions (unforgeable signed cookies, server-rendered menus that cannot be stubbed), runtime will get stuck at the shell. Expected behavior:

- **Static is enough for endpoint enumeration** — module paths are in the code
- If authorization permits, run the same harness with a **real session**: `forward: true`, no neutralize needed, capturing real methods/params/responses

---

## H. Single-task checklist

1. Confirm the authorization scope
2. **Read** `scripts/harvest_static.py` → adjust for the target → run → review `api_static.txt`, `routes.txt`
3. **Phase 1b**: path anchor window expansion + binding layer → `param_candidates.json` (Section J)
4. Reverse-engineer A1/A2/A3 → write a site-specific `config.json`
5. **Read and adjust** `runtime_harvest.js` / `preload.js` before running
6. `runtimeMode=depth`: `npm install` → run the adjusted harvest script
7. `runtimeMode=coverage/both`: inject the adjusted preload at document-start → browser MCP dynamic enumeration + **parameter trigger matrix**
8. modules do not render → **Section I permission tree reconstruction** → patch stubs → rerun
9. multi-sample parameter diff + error back-inference → `params_merged.json`
10. merge → `site_map.json` + `api_merged.txt`, honestly noting coverage, gaps, and script changes

---

## I. Permission tree reconstruction (Phase 4 deep dive)

Use this when forging simple `menus: [{ path, show: true }]` has no effect and submodules still do not mount.

### I1. Locate the auth module

```bash
grep -l 'userRouteAuth' js/*.js
grep -l 'routeMap\|routeLink' js/*.js
grep -rhoaE 'getResultTree|role_permissions|permissions/all' js | head
```

Record: **permission API path**, **response field names**, **consumer chunk file name**.

### I2. Extract routeMap

```bash
python3 scripts/extract_route_map.py recon/js recon/
# 产出 recon/route_map.json
```

If `[!] no routeMap pattern found`: loosen the regex in `extract_route_map.py`, or grep manually:

```bash
grep -rhoaE '([A-Z_][A-Z0-9_]*):\{name:"[^"]*",link:"/[^"]+"\}' js | head -20
```

### I3. Build the permission tree + stub

```bash
python3 scripts/build_perm_tree.py recon/js recon/ --config recon/config.json
```

Script logic:
1. Parse `userRouteAuth={MONITOR:{url:...},...}` (including webpack aliases like `He=o.DASHBOARD`)
2. Use `route_map.json` to resolve alias → real path
3. Infer the parent by code prefix (`MONITOR_ALERT` → `MONITOR`)
4. Output `permissions_tree.json`, `permissions_all_stub.json`, `role_permissions_stub.json`
5. With `--config`, automatically write into `config.json`'s `stubs` and extend `routes`

**Adjust per target** (at the top of the script):
- `DEFAULT_ROOTS`: list of top-level module codes
- `DEFAULT_PREFIX_PARENT`: `PREFIX_` → parent mapping
- `DEFAULT_EXTRA_PARENT`: orphan nodes with non-prefix relationships

### I4. Verify stub consistency

```bash
# permissions 数量应 ≈ userRouteAuth 条目数
wc -l recon/perm_codes_all.txt
# routes 应覆盖 route_map 全部 link
python3 -c "import json; m=json.load(open('recon/route_map.json')); r=set(json.load(open('recon/config.json'))['routes']); print('missing', [v['link'] for v in m.values() if v['link'] not in r])"
```

### I5. Rerun runtime and compare

```bash
node recon/runtime_harvest.js recon/config.json
# 对比 forge 前后 runtime_api.json 条数；检查 /attack、/asset 等是否出现模块 API
```

| Before forge | After forge (success) |
|---|---|
| Same 3–5 bootstrap entries per route | Different routes trigger different module APIs |
| Only `/api/locale/language` | `/api/web/...` module endpoints appear |
| single-digit routes in `routes.txt` | 80–110+ `routes` from route_map |

### I6. When it still fails

- **coverage mode**: click the sidebar + tabs; permission gating may only request after interaction
- **stub fields**: compare the real API (curl + real session) with the stub's nesting
- **extra guards**: grep button-level checks such as `hasPermission|checkRole|func.`, and extend `role_permissions.permissions`
- **static fallback**: module API paths are still in `api_static.txt`; runtime only adds METHOD/body; keep params in `param_candidates.json` + recorded samples

---

## J. Parameter reverse-engineering (Phase 1b / 5b / 5c)

**Methodology, not a general-purpose script.** Use regex to find paths; use anchor window expansion + UI binding chain + multi-sample diff + error back-inference to find parameters.

### J1. Anchor window expansion — find the packing object from the path

```bash
# 以 Phase 1 已知 path 为锚
grep -n '"/api/user/list"' js/*.js
grep -rhoaE '.{0,120}("/api[^"]+").{0,200}' js | head
grep -rhoaE '(params|data|body|payload)\s*:\s*\{' js | head
grep -rhoaE '(get|post|put|delete|patch)\([^,]+,\s*\{' js | head
```

### J2. Wrapper layers and transport shapes

```bash
# axios / 统一 request
grep -rhoaE '(axios|request)\.(get|post|put|delete|patch)\(' js | head
grep -rhoaE 'interceptors\.(request|response)' js | head

# GraphQL
grep -rhoaE '(query|mutation)\s+\w+|gql`|graphql\(' js | head
grep -rhoaE '\$[a-zA-Z_]+\s*:\s*(Int|String|Boolean|\[)' js | head

# FormData / multipart
grep -rhoaE 'FormData|\.append\(' js | head

# 路径参数
grep -rhoaE 'path:\s*"/[^"]*:[^"]+"' js | head
grep -rhoaE 'useParams|route\.params|\$route\.params' js | head
```

### J3. Validation gate — required / format / enum

```bash
grep -rhoaE '(required|message|pattern|enum|validator)\s*:' js | head
grep -rhoaE 'yup\.|zod\.|async-validator|Form\.Item|a-form-item|el-form-item' js | head
grep -rhoaE 'rules\s*:\s*\[|name:\s*["\'][a-zA-Z_]+["\']' js | head
grep -rhoaE 'label.*value|options\s*:\s*\[' js | head
```

### J4. Binding layer — form → API

```bash
grep -rhoaE 'onFinish|handleSubmit|getFieldsValue|validateFields' js | head
grep -rhoaE '(pick|omit|transform|dayjs|moment)\(' js | head
```

Runtime backfill: DevTools → Network → request → **Initiator** (call stack): trace upward from `fetch`/`send` to find the packing function.

### J5. Encrypted parameters

```bash
grep -rhoaE 'encrypt|decrypt|sign|CryptoJS|sm2|sm3|sm4|RSA|AES' js | head
```

**Do not guess fields on ciphertext** — hook the encryption function's **inputs**, record the plaintext payload before encryption; write conclusions to `config.json` / `param_candidates.json`.

### J6. Parameter trigger matrix (required in Phase 3)

For each module, record once per operation, and diff the request body/query:

| Operation | Focus |
|---|---|
| First-screen list load | pagination defaults |
| Search | keyword, filters |
| Advanced filters | optional fields |
| Create/edit | complete entity |
| Batch/export | `ids[]`, `exportType` |
| Sort/paginate | `sortField`, `order` |

Produce `param_samples.json`: `[{ "path", "method", "action": "search", "body", "query", "headers" }]`

### J7. Confidence rules

| Confidence | Condition |
|---|---|
| **High** | static callsite + runtime ≥2 consistent samples |
| **Medium** | static only, or only 1 runtime sample |
| **Low** | inferred from responses/errors, not re-verified |
| **Pending trigger** | field known statically, UI/permissions not reached |

### J8. Scenario quick reference

| Scenario | Order |
|---|---|
| REST list page | J1 packing object → J6 four-way diff → J3 rules |
| Create/edit form | J3 Form name → J4 submit chain → runtime submit + deliberately leave blank to see 400 |
| GraphQL | J2 variables declaration → runtime records variables per operation |
| Encrypted body | J5 hook inputs → fields before encryption are the real params |

### J9. Mapping to the api-recon phases

| api-recon | Parameter recon |
|---|---|
| Phase 1 static | J1 anchor window expansion |
| Phase 2 A2 interceptor | globally injected fields (tenantId, sign) |
| Phase 3 runtime | J6 trigger matrix + `param_samples.json` |
| Phase 4 permission tree | different modules have different forms → full fields are only triggered with sufficient permissions |
| Phase 5 merge | `params_merged.json` + confidence; do not mark a field required from a single sample |

### J10. Troubleshooting

| Symptom | Handling |
|---|---|
| static has the field name but runtime never shows it | mark "Pending trigger"; supplement the permission tree / click advanced filters / expand each select option |
| same path but different body shapes | normal — record separately by `action`, do not force-merge the schema |
| stub response is fake but you want to see params | **look at the outbound request** body/headers; do not infer from the stub response |
| 400 reports a nested field | note the outer wrapper `data`/`bizData`/`variables` |
| GraphQL only shows the operation name | expand the `variables` JSON; find `$var: Type` statically |

---
