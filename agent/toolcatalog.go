package agent

import (
	"context"
	"encoding/json"

	actool "github.com/Autumn-27/norma/tool"
)

// this file turns "built-in tools" from pure code into an enumerable, DB-overridable catalog:
//   - BuiltinToolSeeds(): expands the three execution agents' built-in tool sets into seed records (key +
//     description + param schema + default bound agent), for server to idempotently seed into tools table at startup.
//   - ToolResolve hook: at runtime, filters/overrides assembled tools by DB tools rows per agent
//     (filter by agent + override description/schema + inject param defaults). key/handler stay in code, DB only changes "prose and defaults".
// handler (Call behavior) ALWAYS comes from code â€” DB can't change it, only what model sees (description and param defaults).

// ToolSeed is a built-in tool's seedable snapshot: key = CoreTool.Name() (bound to handler,
// UI read-only), Desc/Schema from code tool definition, Agents = code default agent bindings.
type ToolSeed struct {
	Key    string         // = CoreTool.Name(), primary key, immutable
	Desc   string         // top-level description (UI overridable)
	Schema map[string]any // param JSON-Schema (structure read-only, description/default UI editable)
	Agents []string       // default bound agent keys (worker/planner/mainagent)
}

// builtinToolsByAgent uses a "read-only shell" ToolSet (nil stores) to construct each execution agent's
// domain tool set. tool constructors only stuff closures into Spec, don't dereference store at construct time, so nil is safe â€”
// these tools only read Name()/Description()/InputSchema() here, never Call.
//
// deliberately EXCLUDES SDK common tools actool.DefaultTools() (Read/Write/Edit/MultiEdit/LS/Glob/
// Grep/Bash): every agent has them fixed, no "bound to who" choice, and their descriptions mostly live in Prompt()
// (this catalog only covers Description(), would cause half-override confusion). no seed â†’ no DB row â†’ ToolResolve
// passes through unchanged, behavior same as before. only CAUTIOUSMEMORY's own domain tools enter table for management.
func builtinToolsByAgent() map[string][]actool.CoreTool {
	ts := NewToolSet(nil, "")
	return map[string][]actool.CoreTool{
		"mainagent": ts.MainAgentTools(),
		"planner":   ts.PlannerTools(),
		"worker":    ts.WorkerTools(),
// goals (goal decomposer) defaults bound to set_goals + set_constraints: uses them to write decomposed
// goals and extracted operation constraints to DB. shares same managed tool with mainagent, web can edit description/schema, per-agent checkbox.
		"goals": {ts.setGoals(), ts.setConstraints()},
		// auto defaults bound to vuln reporting + asset management tools, other domain tools opt-in via UI checkbox.
		// new DB seeded from this; old DB migrated by seedAutoDefaultBindings.
		"auto": {ts.addFinding(), ts.insertAssets(), ts.addCompanyScope(), ts.listAssets(), ts.listCompanies()},
		// asset_mapper read-only asset DB, for asset organization and topology explanation.
		"asset_mapper": {ts.listAssets(), ts.listCompanies()},
		// pentest (standalone pentest agent) defaults bound: query assets / insert assets / report vuln / query vulns / query companies.
		// new DB seeded from this; old DB migrated by seedPentestDefaultBindings.
		"pentest": {ts.listAssets(), ts.insertAssets(), ts.addFinding(), ts.listFindings(), ts.listCompanies()},
	}
}

// defaultUnbound: these system tools enter catalog normally (visible in web, manually checkboxable per agent), but
// default NOT bound to any agent â€” ToolResolve drops empty-binding tools for all agents, explicit opt-in required.
// reason they stay in some agent's base toolset (e.g. goal_met in PlannerTools): one, lets seed construct it to get desc/schema; two, when user manually binds it back, runtime base has it, ToolResolve retains it.
//
// goal_met: bypasses per-goal prove_goal, declares [entire task complete] globally, high weight and misjudgment risk, also duplicates "prove_goal marks last goal â†’ auto wrap-up", so default unbound from all agents, manual bind when needed.
var defaultUnbound = map[string]bool{"goal_met": true}

// BuiltinToolSeeds deduplicates and merges each agent's built-in tool sets into a seed list: same-name tools (e.g. list_assets
// in multiple agents) merged into one, Agents = union; tools in defaultUnbound forced to empty binding.
func BuiltinToolSeeds() []ToolSeed {
	byAgent := builtinToolsByAgent()
	order := []string{"mainagent", "goals", "planner", "worker", "auto", "asset_mapper", "pentest"}

	type acc struct {
		tool   actool.CoreTool
		agents []string
	}
	m := map[string]*acc{}
	var keys []string
	for _, ak := range order {
		for _, t := range byAgent[ak] {
			a, ok := m[t.Name()]
			if !ok {
				a = &acc{tool: t}
				m[t.Name()] = a
				keys = append(keys, t.Name())
			}
			a.agents = append(a.agents, ak)
		}
	}

	out := make([]ToolSeed, 0, len(keys))
	for _, k := range keys {
		a := m[k]
		agents := a.agents
		if defaultUnbound[k] {
			agents = []string{} // in catalog, manually bindable, but default not given to any agent (store [] not null, consistent with other tools)
		}
		out = append(out, ToolSeed{
			Key:    k,
			Desc:   a.tool.Description(),
			Schema: a.tool.InputSchema(),
			Agents: agents,
		})
	}
	return out
}

// ToolResolve, if set, post-processes an agent's fully-assembled tool list against
// the DB tools table: it drops tools not bound to this agent (or globally disabled)
// and wraps the rest so the model sees the DB-overridden description/schema and
// default params get injected. Tools with no matching DB row (MCP/skill/host tools like
// traffic) pass through untouched. nil = tools unchanged. Wired in server/assembly.go.
var ToolResolve func(ctx context.Context, agentKey string, tools []actool.CoreTool) []actool.CoreTool

// DecorateTool wraps t so Description()/InputSchema() report the DB overrides and
// Call() injects scalar parameter defaults (from schema's "default" props) whenever
// the model omitted them. Name/Prompt/permission/scheduler flags delegate to t, so
// the tool's identity and handler are unchanged. Empty desc/schema fall back to t's.
func DecorateTool(t actool.CoreTool, desc string, schema map[string]any) actool.CoreTool {
	if desc == "" {
		desc = t.Description()
	}
	if len(schema) == 0 {
		schema = t.InputSchema()
	}
	return &overriddenTool{CoreTool: t, desc: desc, schema: schema}
}

// overriddenTool is a CoreTool decorator: it embeds the original (so all behavioral
// methods â€” Prompt/IsReadOnly/IsConcurrencySafe/CheckPermissions/Name â€” delegate)
// and overrides only the model-facing description/schema plus default injection.
type overriddenTool struct {
	actool.CoreTool
	desc   string
	schema map[string]any
}

func (o *overriddenTool) Description() string         { return o.desc }
func (o *overriddenTool) InputSchema() map[string]any { return o.schema }

func (o *overriddenTool) Call(ctx context.Context, in json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
	return o.CoreTool.Call(ctx, injectDefaults(in, o.schema), tc)
}

// injectDefaults fills scalar parameter defaults declared in the (possibly edited)
// schema into the input JSON whenever the model omitted the field or left it empty/
// null. Structure (names/types/required) is untouched â€” only defaults are merged in.
func injectDefaults(in json.RawMessage, schema map[string]any) json.RawMessage {
	defs := scalarDefaults(schema)
	if len(defs) == 0 {
		return in
	}
	m := map[string]json.RawMessage{}
	if len(in) > 0 {
		if err := json.Unmarshal(in, &m); err != nil {
			return in // non-object input: don't touch it
		}
	}
	changed := false
	for k, dv := range defs {
		if cur, ok := m[k]; !ok || isEmptyJSON(cur) {
			m[k] = dv
			changed = true
		}
	}
	if !changed {
		return in
	}
	b, err := json.Marshal(m)
	if err != nil {
		return in
	}
	return b
}

// scalarDefaults extracts properties[k]["default"] for scalar params (string/
// integer/number/boolean). Array/object defaults are skipped: merging them is
// ambiguous and not worth the surprise.
func scalarDefaults(schema map[string]any) map[string]json.RawMessage {
	props, _ := schema["properties"].(map[string]any)
	if len(props) == 0 {
		return nil
	}
	out := map[string]json.RawMessage{}
	for name, raw := range props {
		p, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		dv, ok := p["default"]
		if !ok || dv == nil {
			continue
		}
		switch p["type"] {
		case "string", "integer", "number", "boolean":
			if b, err := json.Marshal(dv); err == nil {
				out[name] = b
			}
		}
	}
	return out
}

func isEmptyJSON(raw json.RawMessage) bool {
	s := string(raw)
	return s == "null" || s == `""`
}

