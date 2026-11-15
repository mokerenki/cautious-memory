package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

// assetInterceptCandidates extracts domain/IP/URL candidate strings from an asset input item for intercept matching.
// URL host is split out so "URL-only" service/endpoint assets can also be matched by domain/IP rules.
func assetInterceptCandidates(item assetInputItem) (domains, ips, urls []string) {
	add := func(dst *[]string, s string) {
		if s = strings.TrimSpace(s); s != "" {
			*dst = append(*dst, s)
		}
	}
	add(&domains, item.Domain)
	for _, d := range item.BoundDomains {
		add(&domains, d)
	}
	add(&ips, item.IP)
	add(&ips, item.ServiceIP)
	add(&urls, item.URL)
	if item.URL != "" {
		if u, err := url.Parse(item.URL); err == nil {
			if h := u.Hostname(); h != "" {
				if net.ParseIP(h) != nil {
					add(&ips, h)
				} else {
					add(&domains, h)
				}
			}
		}
	}
	return domains, ips, urls
}

// assetInputLabel returns a short label for an asset input item, used in intercept messages.
func assetInputLabel(item assetInputItem) string {
	typ := strings.TrimSpace(item.Type)
	var target string
	switch {
	case strings.TrimSpace(item.Domain) != "":
		target = strings.TrimSpace(item.Domain)
	case strings.TrimSpace(item.URL) != "":
		target = strings.TrimSpace(item.URL)
	case strings.TrimSpace(item.IP) != "":
		target = strings.TrimSpace(item.IP)
	case strings.TrimSpace(item.ServiceIP) != "":
		target = strings.TrimSpace(item.ServiceIP)
	default:
		target = "(unknown)"
	}
	if typ != "" {
		return fmt.Sprintf("[%s] %s", typ, target)
	}
	return target
}

// =====================================================================
// Unified asset insertion tools
// =====================================================================

// SetAssetStore wires the asset store and company store onto this ToolSet
// so the insert_assets, add_company_scope, and list_assets tools are active.
func (t *ToolSet) SetAssetStore(as *db.AssetStore, cs *db.CompanyStore) {
	t.as = as
	t.cs = cs
}

// assetInputItem is one element of the insert_assets "assets" array.
type assetInputItem struct {
	Type string `json:"type"` // root_domain|ip|subdomain|app|service|endpoint

	// ---- root_domain / subdomain ----
	Domain      string   `json:"domain"`
	ICP         string   `json:"icp"`
	RecordType  string   `json:"record_type"`
	RecordValue []string `json:"record_value"`

	// ---- ip ----
	IP           string           `json:"ip"`
	BoundDomains []string         `json:"bound_domains"`
	OpenPorts    []db.PortService `json:"open_ports"`

	// ---- app ----
	AppName     string `json:"app_name"`
	BundleID    string `json:"bundle_id"`
	Category    string `json:"category"`
	Description string `json:"description"`
	AppICP      string `json:"app_icp"`
	CompanyID   *int64 `json:"company_id"` // explicit company link (app only; others auto-attribute via scope)

	// ---- service (http) ----
	URL           string           `json:"url"`
	Technologies  []string         `json:"technologies"`
	StatusCode    *int             `json:"status_code"`
	ContentLength *int64           `json:"content_length"`
	PageTitle     string           `json:"page_title"`
	FaviconMMH3   string           `json:"favicon_mmh3"`
	Auth          []map[string]any `json:"auth"`
	ServiceName   string           `json:"service_name"`
	ServiceIP     string           `json:"service_ip"` // optional enrichment IP

	// ---- service (other) ----
	Port  int    `json:"port"`
	Proto string `json:"proto"`

	// ---- endpoint ----
	Method string           `json:"method"`
	Params []map[string]any `json:"params"`
}

// insertAssets is the unified insert_assets agent tool.
func (t *ToolSet) insertAssets() actool.CoreTool {
	return writeTool(
		"insert_assets",
		"bulk register newly discovered assets, multiple types allowed in one call (see type enum).\n"+
			"required fields per type: root_domain→domain; ip→ip (must be IPv4/IPv6, not hostname); subdomain→domain; app→app_name; service(HTTP)→url; service(non-HTTP)→service_name+port (ip/domain at least one); endpoint→url+method. other fields per their descriptions.\n"+
			"auth/technologies/params are append-merge, don't overwrite existing.\n"+
			"returns: {results:[{index,id,type}], errors:[{index,error}]}",
		obj(map[string]any{
			// task_id not exposed to model: worker's task ownership assigned by program via SetTaskID (see handler).
			"assets": map[string]any{
				"type":        "array",
				"description": "asset array, each element is one asset record",
				"items": obj(map[string]any{
					"type": map[string]any{
						"type":        "string",
						"enum":        []string{"root_domain", "ip", "subdomain", "app", "service", "endpoint"},
						"description": "asset type",
					},
					// root_domain / subdomain
					"domain":      str("root domain or subdomain (required for root_domain/subdomain)"),
					"icp":         str("ICP record number (optional)"),
					"record_type": str("DNS record type: A/AAAA/CNAME/MX etc. (subdomain optional)"),
					"record_value": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "DNS record value list (subdomain optional, e.g. [\"1.2.3.4\",\"2.3.4.5\"])",
					},
					// ip
					"ip": str("IP address, must be IPv4/IPv6, not hostname (use type=subdomain domain for hostname); required for ip type; service/endpoint types optional, for IP association"),
					"bound_domains": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "domains bound to this IP (ip type optional)",
					},
					"open_ports": map[string]any{
						"type":        "array",
						"description": "open port list (ip type optional)",
						"items": obj(map[string]any{
							"port":    intp("port number"),
							"service": str("service name, e.g. http/ssh/mysql etc. (optional)"),
						}, "port"),
					},
					// app
					"app_name":    str("app name (required for app type)"),
					"bundle_id":   str("bundle ID (app type optional)"),
					"category":    str("app category (optional)"),
					"description": str("app description (optional)"),
					"app_icp":     str("app ICP record (optional)"),
					"company_id":  intp("owning company id (app type optional; app can't auto-attribute via scope, must specify explicitly. id from add_company_scope)"),
					// service (http)
					"url":         str("full URL with protocol and port (HTTP service required; service_type auto-set to http)"),
					"status_code": intp("HTTP response status code, e.g. 200/301/403/404 (optional)"),
					"content_length": map[string]any{
						"type":        "integer",
						"description": "HTTP response body byte count (optional)",
					},
					"page_title":   str("page <title> content (optional)"),
					"favicon_mmh3": str("favicon MMH3 hash (optional)"),
					"technologies": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "fingerprint/tech stack list, e.g. [\"Nginx\",\"Vue\",\"Bootstrap\"] (optional)",
					},
					"auth": map[string]any{
						"type":        "array",
						"description": "discovered auth info list, each with type/username/password etc. (optional, append not overwrite)",
						"items":       map[string]any{"type": "object"},
					},
					// service (other, non-HTTP)
					"service_name": str("service name, e.g. ssh/mysql/redis (required for non-HTTP service)"),
					"port":         intp("port number (required for non-HTTP service)"),
					// endpoint
					"method": str("HTTP method: GET/POST/PUT/PATCH/DELETE etc. (endpoint required)"),
					"params": map[string]any{
						"type":        "array",
						"description": "list of request parameters, each with location (query/body/header/path)/name/value/type (optional, appended without overwriting)",
						"items":       map[string]any{"type": "object"},
					},
				}, "type"),
			},
		}, "assets"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if t.as == nil {
				return actool.Errorf("insert_assets is not enabled: AssetStore is not initialized"), nil
			}
			var a struct {
				Assets []assetInputItem `json:"assets"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf("invalid input: " + err.Error()), nil
			}
			// task_id is assigned authoritatively by the program (worker: SetTaskID) and is not accepted from model input,
			// to avoid assets being left unassigned or assigned to the wrong task when the model omits or mis-passes it. Callers without task context (auto/pentest/chat) have t.taskID == 0.
			taskID := t.taskID

			type result struct {
				Index int    `json:"index"`
				ID    int64  `json:"id"`
				Type  string `json:"type"`
			}
			type errEntry struct {
				Index int    `json:"index"`
				Error string `json:"error"`
			}

			var results []result
			var errs []errEntry

			// Load asset gate rules once; if loading fails, skip evaluation (do not block insertion).
			// Block rules = global union task-level block rules; allow rules = task-level allow rules.
			blockRules, _ := t.as.ListAssetInterceptRules()
			var allowRules []db.AssetInterceptRule
			if t.taskID > 0 {
				if tb, ta, err := t.as.TaskInterceptRulesSplit(t.taskID); err == nil {
					blockRules = append(blockRules, tb...)
					allowRules = ta
				}
			}

			for i, item := range a.Assets {
				// Asset gate: block first, then allow. Rejected assets must not be inserted (skip Upsert and all subsequent side effects).
				domains, ips, urls := assetInterceptCandidates(item)
				if d := db.EvaluateAssetGate(blockRules, allowRules, domains, ips, urls); !d.Allowed {
					errs = append(errs, errEntry{
						Index: i,
						Error: fmt.Sprintf("asset %s %s, insertion is not allowed", assetInputLabel(item), d.Reason),
					})
					continue
				}

				typ := strings.TrimSpace(item.Type)
				var id int64
				var err error

				switch typ {
				case "root_domain":
					id, err = t.as.UpsertRootDomain(db.UpsertRootDomainReq{
						Domain: item.Domain,
						ICP:    item.ICP,
						TaskID: taskID,
					})

				case "ip":
					id, err = t.as.UpsertIP(db.UpsertIPReq{
						IP:           item.IP,
						BoundDomains: item.BoundDomains,
						OpenPorts:    item.OpenPorts,
						TaskID:       taskID,
					})

				case "subdomain":
					id, err = t.as.UpsertSubdomain(db.UpsertSubdomainReq{
						Domain:      item.Domain,
						RecordType:  item.RecordType,
						RecordValue: item.RecordValue,
						ICP:         item.ICP,
						TaskID:      taskID,
					})

				case "app":
					id, err = t.as.UpsertApp(db.UpsertAppReq{
						Name:        item.AppName,
						BundleID:    item.BundleID,
						Category:    item.Category,
						Description: item.Description,
						ICP:         item.AppICP,
						CompanyID:   item.CompanyID,
						TaskID:      taskID,
					})

				case "service":
					// distinguish HTTP vs other by presence of url
					if item.URL != "" {
						// agent may send "ip" or "service_ip" for the enrichment IP; accept both
						svcIP := item.ServiceIP
						if svcIP == "" {
							svcIP = item.IP
						}
						id, err = t.as.UpsertHTTPService(db.UpsertHTTPServiceReq{
							URL:           item.URL,
							Technologies:  item.Technologies,
							StatusCode:    item.StatusCode,
							ContentLength: item.ContentLength,
							PageTitle:     item.PageTitle,
							FaviconMMH3:   item.FaviconMMH3,
							Auth:          item.Auth,
							IP:            svcIP,
							TaskID:        taskID,
						})
					} else {
						id, err = t.as.UpsertOtherService(db.UpsertOtherServiceReq{
							Domain:      item.Domain,
							IP:          item.IP,
							Port:        item.Port,
							ServiceName: item.ServiceName,
							Auth:        item.Auth,
							TaskID:      taskID,
						})
					}

				case "endpoint":
					id, err = t.as.UpsertEndpoint(db.UpsertEndpointReq{
						URL:    item.URL,
						Method: item.Method,
						Params: item.Params,
						IP:     item.ServiceIP,
						TaskID: taskID,
					})

				default:
					errs = append(errs, errEntry{Index: i, Error: "unknown type: " + typ})
					continue
				}

				if err != nil {
					errs = append(errs, errEntry{Index: i, Error: err.Error()})
					continue
				}
				if intelligenceEnabled() && typ == "service" && taskID > 0 {
					for _, auth := range item.Auth {
						raw, _ := json.Marshal(auth)
						input := db.IntelligenceInput{Kind: "credential", Title: fmt.Sprintf("authentication info for service #%d", id), Content: string(raw), Evidence: fmt.Sprintf("reported via insert_assets, service asset #%d, source intent #%d", id, t.ownerNode), Confidence: "observed", Status: "unverified", AssetIDs: []int64{id}}
						if _, err := t.saveIntelligence(ctx, 0, input); err != nil {
							errs = append(errs, errEntry{Index: i, Error: "asset saved, but credential intelligence was not saved: " + err.Error()})
						}
					}
				}
				results = append(results, result{Index: i, ID: id, Type: typ})
				t.writes.Assets++
				t.anchorOwner(id)
				if taskID > 0 {
					var sourceNodeID *int64
					if t.ownerNode > 0 {
						nodeID := t.ownerNode
						sourceNodeID = &nodeID
					}
					summary := "registered by agent via insert_assets"
					if t.ownerNode > 0 {
						summary = fmt.Sprintf("registered by worker intent #%d via insert_assets", t.ownerNode)
					}
					_ = t.as.SetTaskAssetSource(taskID, id, "agent", summary, sourceNodeID)
				}
				// Automatically add to test scope (source='auto'): only for this top-level item explicitly inserted by the worker,
				// add a conservative scope based on its type. Assets derived as side effects do not pass through here, so the scope is not expanded blindly. No-op when taskID == 0.
				// Independent of the coverage toggle: task_scope is the task's scope boundary (the filter baseline for list/query operations),
				// while the coverage toggle only controls whether it is used as the denominator for metrics, not whether the scope itself is accumulated.
				{
					svcIP := item.ServiceIP
					if svcIP == "" {
						svcIP = item.IP
					}
					_ = t.as.AddAutoScope(taskID, typ, item.Domain, item.URL, svcIP)
				}
			}

			return jsonResult(map[string]any{
				"results": results,
				"errors":  errs,
			})
		},
	)
}

// addCompanyScope writes to company_scope table and triggers asset attribution.
func (t *ToolSet) addCompanyScope() actool.CoreTool {
	return writeTool(
		"add_company_scope",
		"Add domains/IPs/CIDRs/ICP filings/enterprise keywords to a company's [asset scope] - domains, networks, and ICP filings will automatically claim matching assets; keywords are only provided to the Agent as scope hints.\n"+
			"Company names are unique: if company does not exist it is created, if it already exists it is reused (only the scope is merged in).\n"+
			"One scope entry per line; the system auto-detects: root domain / URL / single IP / CIDR range / ICP filing / enterprise keyword.\n"+
			"You must provide reason to explain the ownership basis (whois/certificate/ASN, etc.).\n"+
			"Guardrails: bare TLDs and overly broad ranges are rejected (IPv4 prefix must be /16-/32, IPv6 prefix must be /32-/128); invalid lines are skipped and returned in errors.",
		obj(map[string]any{
			"company": str("company name (created if missing, reused if it exists; names are unique)"),
			"scope":   str("asset scope, one entry per line: domain / URL / IP / CIDR / ICP filing / enterprise keyword"),
			"reason":  str("ownership basis (evidence/source), required"),
			"logo":    str("company logo URL (optional; only used when creating a new company)"),
		}, "company", "scope"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.cs == nil {
				return actool.Errorf("add_company_scope is not enabled: CompanyStore is not initialized"), nil
			}
			var a struct {
				Company string `json:"company"`
				Scope   string `json:"scope"`
				Reason  string `json:"reason"`
				Logo    string `json:"logo"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if strings.TrimSpace(a.Company) == "" {
				return actool.Errorf("company must not be empty"), nil
			}
			companyID, _, err := t.cs.UpsertCompany(a.Company, a.Logo)
			if err != nil {
				return actool.Errorf("failed to create/get company: " + err.Error()), nil
			}
			lines := splitLines(a.Scope)
			added, skipped, invalid, errMsgs := t.cs.AddScope(companyID, lines, a.Reason)
			out := map[string]any{
				"company_id": companyID,
				"added":      added,
				"skipped":    skipped,
				"invalid":    invalid,
			}
			if len(errMsgs) > 0 {
				out["errors"] = errMsgs
			}
			return jsonResult(out)
		},
	)
}

// addTaskScope lets the plan agent add test scope to THE CURRENT TASK — the coverage
// denominator and the task's authorization edge. Worker discoveries are auto-scoped
// (precise host) by insertAssets; this tool is for DELIBERATELY WIDENING: pull a whole
// root domain or whole company into scope, or add a specific subdomain / ip.
func (t *ToolSet) addTaskScope() actool.CoreTool {
	return writeTool(
		"add_task_scope",
		"Add test scope to [this task] - this is the authorization boundary for this task and the denominator for asset test coverage.\n"+
			"Supported kinds: company (all assets under a company name) / root_domain (entire root domain, including all subdomains) / subdomain (a single exact subdomain) / ip / cidr / icp / keyword.\n"+
			"Note: hosts encountered one by one by workers are [automatically] added to scope by the system (exact subdomain); this tool is for [deliberately widening] scope - adding an entire root domain/company, or adding a specific subdomain/IP.\n"+
			"value: for company pass the company name or id (the company must already exist); for root_domain/subdomain pass a domain; for ip/cidr pass an IP or range; for icp/keyword pass a filing number or enterprise keyword.\n"+
			"You must provide reason to explain the basis (auditable). Use the entries array for multiple items.",
		obj(map[string]any{
			"entries": map[string]any{"type": "array", "description": "Batch: [{kind, value}]. kind in company/root_domain/subdomain/ip/cidr/icp/keyword.", "items": map[string]any{"type": "object"}},
			"kind":    str("[single] company / root_domain / subdomain / ip / cidr / icp / keyword"),
			"value":   str("[single] company name or id / domain / IP / CIDR / ICP / keyword"),
			"reason":  str("basis for adding (for audit), required"),
		}),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.as == nil {
				return actool.Errorf("add_task_scope is not enabled: AssetStore is not initialized"), nil
			}
			if t.taskID <= 0 {
				return actool.Errorf("add_task_scope requires task context (no current task)"), nil
			}
			type scopeEntry struct {
				Kind  string `json:"kind"`
				Value string `json:"value"`
			}
			var a struct {
				Entries    []scopeEntry `json:"entries"`
				scopeEntry              // single-entry mode
				Reason     string       `json:"reason"`
			}
			_ = json.Unmarshal(in, &a)
			items := a.Entries
			if len(items) == 0 {
				items = []scopeEntry{a.scopeEntry}
			}
			var added []map[string]any
			errs := map[string]string{}
			for i, e := range items {
				ts, err := t.as.AddAgentScope(t.taskID, strings.TrimSpace(e.Kind), e.Value, a.Reason, "agent")
				if err != nil {
					errs[strconv.Itoa(i)] = err.Error()
					continue
				}
				added = append(added, map[string]any{"kind": ts.Kind, "domain": ts.Domain, "net": ts.Net, "value": ts.Value, "company_id": ts.CompanyID})
			}
			out := map[string]any{"added": added}
			if len(errs) > 0 {
				out["errors"] = errs
			}
			return jsonResult(out)
		},
	)
}

// listUntestedAssets lets the plan agent pull the current + directly inherited
// scope's not-yet-tested assets on demand (filter by type, paginated).
func (t *ToolSet) listUntestedAssets() actool.CoreTool {
	return readTool(
		"list_untested_assets",
		"Query assets within the scope of [this task and directly associated tasks] that are not yet covered by fact anchors (associated scope is read-only for you to judge whether additional testing is needed; it does not decide for you).\n"+
			"Optionally filter by asset type: root_domain/subdomain/service/app/endpoint/ip.\n"+
			"Pagination: page starts at 1, page_size defaults to 10. Returns {assets:[{id,type,label}], total, page, page_size}. Only available in task context.",
		obj(map[string]any{
			"type":      str("asset type filter (optional): root_domain/subdomain/service/app/endpoint/ip"),
			"page":      intp("page number, starting at 1 (default 1)"),
			"page_size": intp("items per page (default 10)"),
		}),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.as == nil {
				return actool.Errorf("list_untested_assets is not enabled: AssetStore is not initialized"), nil
			}
			if t.taskID <= 0 || t.ts == nil {
				return actool.Errorf("list_untested_assets requires task context"), nil
			}
			var a struct {
				Type     string `json:"type"`
				Page     int    `json:"page"`
				PageSize int    `json:"page_size"`
			}
			_ = json.Unmarshal(in, &a)
			if a.Page <= 0 {
				a.Page = 1
			}
			if a.PageSize <= 0 {
				a.PageSize = 10
			}
			offset := (a.Page - 1) * a.PageSize
			assets, total, err := t.as.ListUntestedAssetsWithSources(t.taskID, strings.TrimSpace(a.Type), a.PageSize, offset)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return jsonResult(map[string]any{
				"assets": assets, "total": total, "page": a.Page, "page_size": a.PageSize,
			})
		},
	)
}

// listAssets lets an agent query the asset table.
func (t *ToolSet) listAssets() actool.CoreTool {
	return readTool(
		"list_assets",
		"Query the asset store: search with a DSL expression, or fetch directly by id/ids; pagination supported. By default only assets of this task and directly associated tasks are returned; global assets can be queried after the user enables \"Share global assets\".\n"+
			"DSL: field=value fuzzy (ILIKE) | field==value exact | field!=value exclude | numeric fields support > >= < <= | bare term = full-text fuzzy; combine with AND/OR (AND has higher precedence), parentheses for grouping. Asset type uses the separate type parameter, not the DSL.\n"+
			"When id/ids are not provided, dsl must be non-empty (unconditional full scans are not allowed).\n"+
			"Available fields: domain (root/sub/service domain), root_domain, ip, url, page_title, icp, service_name, app_name, method (e.g. GET/POST), service_type (http|other), record_type (e.g. A/CNAME), technology (array, = fuzzy == exact), port/status_code/company_id (integers).\n"+
			"Examples: status_code>=400 AND technology=shiro ; (port==80 OR port==443) AND technology=nginx",
		obj(map[string]any{
			"dsl":    str(`DSL query expression (see tool description for syntax/fields). Required when id/ids are not provided.`),
			"type":   str("asset type filter: root_domain|ip|subdomain|app|service|endpoint (separate field, can be combined with dsl; type alone is not sufficient, dsl is still required)"),
			"id":     intp("fetch directly by a single asset id (optional, mutually exclusive with dsl/type)"),
			"ids":    map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "fetch directly by multiple asset ids (optional, mutually exclusive with dsl/type)"},
			"limit":  intp("maximum results to return, default 10 (optional)"),
			"offset": intp("pagination offset, default 0 (optional)"),
		}),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.as == nil {
				return actool.Errorf("list_assets is not enabled: AssetStore is not initialized"), nil
			}
			var a struct {
				DSL    string  `json:"dsl"`
				Type   string  `json:"type"`
				ID     int64   `json:"id"`
				IDs    []int64 `json:"ids"`
				Limit  int     `json:"limit"`
				Offset int     `json:"offset"`
			}
			_ = json.Unmarshal(in, &a)
			if a.Limit <= 0 {
				a.Limit = 10
			}

			var assets []*db.Asset
			var err error
			switch {
			case a.ID > 0:
				assets, err = t.as.GetByIDsInScope(t.taskID, []int64{a.ID})
			case len(a.IDs) > 0:
				assets, err = t.as.GetByIDsInScope(t.taskID, a.IDs)
			case a.DSL != "":
				assets, err = t.as.QueryDSLInScope(a.DSL, a.Type, t.taskID, a.Limit, a.Offset)
			default:
				return actool.Errorf("dsl must not be empty when id/ids are not provided: unconditional queries for all assets are not allowed, please provide query conditions"), nil
			}
			if err != nil {
				return actool.Errorf("DSL error: " + err.Error()), nil
			}
			return jsonResult(map[string]any{
				"count":  len(assets),
				"assets": assets,
			})
		},
	)
}

// listCompanies lets an agent enumerate companies with their scope + asset count.
func (t *ToolSet) listCompanies() actool.CoreTool {
	return readTool(
		"list_companies",
		"List [companies] visible to the current task with their asset scope and visible asset count; global companies can be queried after global asset sharing is enabled. Use this to see available companies and "+
			"obtain company_id (for associating apps via insert_assets or filtering list_assets by company_id). "+
			"Optionally filter by company name with a case-insensitive fuzzy search; leave empty to return all.",
		obj(map[string]any{
			"search": str("fuzzy filter by company name (optional, case-insensitive); leave empty to return all"),
		}),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.cs == nil {
				return actool.Errorf("list_companies is not enabled: CompanyStore is not initialized"), nil
			}
			var a struct {
				Search string `json:"search"`
			}
			_ = json.Unmarshal(in, &a)
			cos, err := t.cs.ListCompaniesForTask(t.taskID)
			if err != nil {
				return actool.Errorf("failed to query companies: " + err.Error()), nil
			}
			q := strings.ToLower(strings.TrimSpace(a.Search))
			type companyOut struct {
				ID         int64    `json:"id"`
				Name       string   `json:"name"`
				AssetCount int      `json:"asset_count"`
				Scope      []string `json:"scope"`
			}
			out := make([]companyOut, 0, len(cos))
			for _, c := range cos {
				if q != "" && !strings.Contains(strings.ToLower(c.Name), q) {
					continue
				}
				scope := make([]string, 0, len(c.Scope))
				for _, r := range c.Scope {
					scope = append(scope, r.Raw)
				}
				out = append(out, companyOut{ID: c.ID, Name: c.Name, AssetCount: c.AssetCount, Scope: scope})
			}
			return jsonResult(map[string]any{"count": len(out), "companies": out})
		},
	)
}

// splitLines splits a multi-line string into non-empty trimmed lines.
func splitLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// WorkerTools returns the tool set for a work agent.
func (t *ToolSet) WorkerTools() []actool.CoreTool {
	return []actool.CoreTool{
		// list_findings is kept: check confirmed findings for this task before reporting to avoid duplicate reports.
		t.listFindings(),
		t.addFinding(), t.recordFact(), t.recordIntelligence(),
		// asset management (handlers guard nil store internally)。
		// add_company_scope is not given to workers: defining company asset scope is the responsibility of planning/control/Auto; workers only perform exploration.
		t.insertAssets(), t.listAssets(),
		// Cross-work review: workers can also reuse observations from other work to avoid duplicate effort.
		// search_all_worker_traces: search matching steps globally by keyword without knowing intent_id first;
		// get_worker_trace: after identifying a specific work item, list steps/search in place/fetch full content.
		t.searchAllWorkerTraces(), t.getWorkerTrace(),
		// node_detail: once a worker has an intent_id/node id, it can query full details for that node (used with the review tools above).
		t.nodeDetail(),
		// The following tools are still [not given] to workers and remain planner/main-only (reading context and cross-work review are planning responsibilities;
		// workers only execute and write back for a single intent): list_facts / list_companies / list_worker_traces.
	}
}

// MainAgentTools returns the human-interface tool set.
func (t *ToolSet) MainAgentTools() []actool.CoreTool {
	return []actool.CoreTool{
		t.graphOverview(), t.listFindings(), t.listFacts(), t.nodeDetail(),
		t.expandDigest(), // cold-digest §6.1
		t.getWorkerOutput(), t.getWorkerTrace(), t.searchAllWorkerTraces(), t.addHint(), t.addIntent(),
		// steer_work: allows a human to inject real-time corrective instructions into a running intent (work) without interrupting or losing progress.
		t.steerWorkTool(),
		// set_goals: allows a human to add a new final goal to this task at runtime (the planner re-evaluates achievement accordingly).
		t.setGoals(),
		// set_constraints: allows a human to add/modify operation constraints (allow/deny) at runtime, constraining the exploration boundaries of planner/worker.
		t.setConstraints(),
		// asset management (handlers guard nil store internally)
		t.insertAssets(), t.addCompanyScope(), t.listAssets(),
		t.addFinding(), t.recordFact(), t.recordIntelligence(),
		t.addTaskScope(),
		// list_untested_assets: query untested assets in this task's scope on demand (by type + pagination) and decide on additional testing.
		t.listUntestedAssets(),
	}
}

// AllDomainTools returns the union of all domain tools across all agent types,
// deduped by name (mainagent order wins). Used by the server to build a registry
// for injecting domain tools into agents (Auto, custom) that don't own a per-task
// ToolSet. The caller provides real stores; tools are callable at taskID=0 scope.
func (t *ToolSet) AllDomainTools() []actool.CoreTool {
	seen := map[string]bool{}
	var out []actool.CoreTool
	all := append(append(t.MainAgentTools(), t.PlannerTools()...), t.WorkerTools()...)
	for _, tool := range all {
		if !seen[tool.Name()] {
			seen[tool.Name()] = true
			out = append(out, tool)
		}
	}
	return out
}
