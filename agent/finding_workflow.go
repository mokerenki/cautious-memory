package agent

import (
	"encoding/json"
	"fmt"

	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

const findingIDGuidance = "\n\n**Vulnerability ID convention**: finding_id is independent vulnerability record ID; finding_node_id is exploration node ID. list_findings / list_task_findings / node_detail / get_task_node_detail ids remain exploration node IDs, read independent number from finding_id in the same return. get_finding_traffic / bind_finding_traffic use independent finding_id. Old update_finding_report finding_id parameter still passes finding_node_id. Do not use the number on the first line of report_finding for evidence tools, and do not guess other numbers when encountering ID errors."

// The server supplies the persisted setting. A missing setting/host is off.
// Consulted at assembly and again on writes so an already-running session
// cannot keep binding after the user switches the feature off.
var FindingTrafficBindingEnabled func() bool

func findingTrafficBindingEnabled() bool {
	return FindingTrafficBindingEnabled != nil && FindingTrafficBindingEnabled()
}

// Applied after ToolResolve: user descriptions and prompts remain intact, while
// all actual reporters (including Planner and custom chat agents) see the same
// API contract. Disabled/unbound tools are never reintroduced here.
func findingWorkflowTools(agentKey string, tools []actool.CoreTool) ([]actool.CoreTool, string) {
	if !findingTrafficBindingEnabled() {
		out := make([]actool.CoreTool, 0, len(tools))
		for _, tool := range tools {
			if tool.Name() == "bind_finding_traffic" {
				continue
			}
			if agentKey == "reporter" && (tool.Name() == "traffic_search" || tool.Name() == "traffic_get" || tool.Name() == "traffic_blob") {
				continue
			}
			switch tool.Name() {
			case "report_finding", "add_hint", "add_task_hint":
				// Work on a copy: toggling back on must restore the original schema.
				raw, _ := json.Marshal(tool.InputSchema())
				var schema map[string]any
				if json.Unmarshal(raw, &schema) == nil {
					stripTrafficParameters(schema)
					tool = DecorateTool(tool, tool.Description(), schema)
				}
			}
			out = append(out, tool)
		}
		return out, ""
	}
	out := append([]actool.CoreTool(nil), tools...)
	has := map[string]bool{}
	for i, tool := range out {
		has[tool.Name()] = true
		note := ""
		switch tool.Name() {
		case "report_finding":
			note = "\nDefaulted to be verified and traffic bound by report agent before writing report. Uploader retains verification commands, key outputs, existing real traffic IDs and their purposes in evidence, for report agent to cross-check execution records; no need to query packets additionally for binding. Compatible explicit immediate binding: traffic_refs or evidence_hint_id can submit verified references, the latter reads structured references of the specified hint in this task; if either is invalid, this upload fails entirely. TCP/packetless does not need these optional parameters. Returns finding_id and finding_node_id representing independent record and exploration node respectively."
		case "add_hint", "add_task_hint":
			note = "\nWhen handing off confirmed vulnerabilities, retain verified traffic ID, purpose, description and order in corresponding hint's traffic_refs (single one at top level, batch in corresponding hints elements), and in text explain the specific vulnerability it proves. Caller cannot just hand off text while discarding existing traffic references. Unverified candidates cannot be passed as evidence."
		case "get_finding_traffic", "bind_finding_traffic", "list_findings", "list_task_findings", "node_detail", "get_task_node_detail", "update_finding_report":
			note = findingIDGuidance
		}
		if note != "" {
			out[i] = DecorateTool(tool, tool.Description()+note, tool.InputSchema())
		}
	}
	guidance := ""
	if has["report_finding"] || has["add_task_hint"] || has["add_hint"] {
		guidance = "\n\n**Traffic evidence handoff (optional)**: automatic binding defaulted to be completed by report agent after vulnerability entry and before writing report. Uploader should retain verification commands, key outputs, existing real traffic IDs and their purposes in evidence, task carries intent_id, for report agent to trace back; no need to query packets additionally for binding. When Auto/Planner uploads on behalf, do not discard executor's existing references. add_hint / add_task_hint can hand off via traffic_refs; explicit immediate binding still compatible with report_finding's traffic_refs / evidence_hint_id. TCP or packetless normally register, cannot guess ID, nor re-probe target just to supplement packets."
		if has["add_task_hint"] && !has["add_hint"] {
			guidance += "\nWhen platform conversation has no task context, do not directly call report_finding; hand off to existing corresponding task via add_task_hint, let Task Agent register, and use list_task_findings to check results."
		}
		if has["prove_goal"] || has["goal_met"] {
			guidance += "\nBefore judging goal complete, first complete upload/handoff of existing evidence in this round. Do not end task, cancel Worker just because text vulnerability is already registered before evidence handoff is complete. Packetless does not require waiting or forced packet capture."
		}
	}
	if has["update_finding_report"] && has["bind_finding_traffic"] && has["get_finding_traffic"] {
		guidance += "\n\n**Automatic traffic association before report (enabled)**: You are responsible for verifying and binding traffic for vulnerabilities triggered this round, then writing the report. First obtain clear finding_id and finding_node_id from report_finding return JSON or get_task_node_detail / list_task_findings. Read vulnerability details, corresponding intent's execution records and existing evidence list, prioritize using real IDs handed off by uploader. If this verification is HTTP and traffic tools available, use traffic_search to filter candidates, then use traffic_get to verify request/response indeed support this vulnerability one by one; domain and time only used for filtering, do not prove association. Associate confirmed evidence in reproduction order using bind_finding_traffic(finding_id, traffic_refs), choose baseline / proof / verification / supporting and explain purpose. Only operate on this vulnerability, do not create duplicate vulnerabilities or re-probe targets. After binding success, re-call get_finding_traffic to get latest version, read required body, then pass actually read version as evidence_version to update_finding_report (its finding_id parameter still uses finding_node_id). Existing bindings do not need duplicate addition. TCP, not collected, tool unavailable or no exact match when, skip automatic binding, write report normally based on text/command evidence and explain reason, must not guess to fill traffic. Binding failure must not claim success; retain existing evidence and explain reason for not binding in report."
	}
	if guidance != "" || has["get_finding_traffic"] || has["update_finding_report"] {
		guidance += findingIDGuidance
	}
	return out, guidance
}

func stripTrafficParameters(schema map[string]any) {
	props, _ := schema["properties"].(map[string]any)
	delete(props, "traffic_refs")
	delete(props, "evidence_hint_id")
	if required, ok := schema["required"].([]any); ok {
		kept := required[:0]
		for _, key := range required {
			if key != "traffic_refs" && key != "evidence_hint_id" {
				kept = append(kept, key)
			}
		}
		schema["required"] = kept
	}
	if hints, ok := props["hints"].(map[string]any); ok {
		if items, ok := hints["items"].(map[string]any); ok {
			stripTrafficParameters(items)
		}
	}
}

// HintTrafficSchema is shared by the task-local and cross-task hint tools.
func HintTrafficSchema() map[string]any {
	return map[string]any{"type": "array", "description": "Optional: verified traffic references corresponding to specific vulnerabilities in this hint, order preserved; after handoff report_finding can pass evidence_hint_id carrying these references.", "items": obj(map[string]any{"traffic_id": str("real traffic ID"), "role": str("baseline / proof / verification / supporting"), "note": str("what conclusion this traffic supports")}, "traffic_id")}
}

func (t *ToolSet) findingRefsFromHint(hintID int64, explicit []db.TrafficRef) ([]db.TrafficRef, error) {
	if hintID <= 0 {
		return db.NormalizeTrafficRefs(explicit)
	}
	n, err := t.ts.GetNode(hintID) // local store only: inherited hints cannot supply evidence
	if err != nil {
		return nil, err
	}
	if n == nil || n.Kind != db.KindHint {
		return nil, fmt.Errorf("evidence_hint_id=%d must be a hint node of this task (inherited hints cannot be directly used for binding)", hintID)
	}
	var payload struct {
		Refs []db.TrafficRef `json:"traffic_refs"`
	}
	if err := json.Unmarshal(n.Payload, &payload); err != nil {
		return nil, err
	}
	return db.NormalizeTrafficRefs(append(append([]db.TrafficRef{}, explicit...), payload.Refs...))
}
