package agent

import (
	"context"
	actool "github.com/Autumn-27/norma/tool"
)

type findingReviewKey struct{}

// Historical consolidation has a narrower capability set than new reporting.
func WithFindingCaseReview(ctx context.Context) context.Context {
	return context.WithValue(ctx, findingReviewKey{}, true)
}
func isFindingCaseReview(ctx context.Context) bool {
	v, _ := ctx.Value(findingReviewKey{}).(bool)
	return v
}

func historicalFindingCaseTools(tools []actool.CoreTool) []actool.CoreTool {
	allowed := map[string]bool{}
	for _, name := range []string{"search_finding_duplicates", "get_finding_record", "merge_finding_records", "suggest_finding_merge", "get_finding_case", "update_finding_case_report", "complete_finding_case_review", "get_finding_traffic", "traffic_search", "traffic_get", "traffic_blob", "get_task_node_detail", "list_task_findings", "get_task_graph", "list_task_worker_traces", "get_task_worker_trace", "Read"} {
		allowed[name] = true
	}
	out := make([]actool.CoreTool, 0, len(tools))
	for _, tool := range tools {
		if allowed[tool.Name()] {
			out = append(out, tool)
		}
	}
	return out
}

// This guidance is appended to the runtime prompt, including editable old prompts.
// Never reintroduce unbound tools or recreate a deleted Reporter.
func findingCaseWorkflow(key string, tools []actool.CoreTool) ([]actool.CoreTool, string) {
	if key != "reporter" {
		return tools, ""
	}
	has := map[string]bool{}
	for _, t := range tools {
		has[t.Name()] = true
	}
	if !has["search_finding_duplicates"] || !has["merge_finding_records"] || !has["get_finding_case"] || !has["update_finding_case_report"] {
		return tools, ""
	}
	return tools, `

[Consolidation of multiple reports for the same vulnerability] Retain this independent report, do not overwrite other original reports. The old guidance that report completion stops by default is extended here: first save this report, then search_finding_duplicates(finding_id=independent record ID) paginate-read candidate summaries in the same task (sorted by score; strong=same vulnerability type and asset overlap, prioritize verification; weak=does not simultaneously have same vulnerability type and asset overlap, combine with hit_reasons for verification, reference only), get_finding_record as needed to view evidence. Cannot merge based solely on vulnerability type, title, token, login entry similarity, or high/medium/low severity; must verify target, interface, parameters, permission boundaries, root cause, and actual verification evidence. Only handle the same task, respect manual veto and finding_case_blocks.
[Judgment criteria] If evidence indicates endpoint/route, processor or backend component, vulnerable parameter, or missing check belong to different code paths, can judge as independent; must explain respective evidence. Merely same/different path, interface, login entry, token, title, or vulnerability type cannot alone prove same or different root cause. Only call merge_finding_records(task_id,finding_ids,title,reason) directly when root cause is clearly the same. Only when there is a specific common point (both records evidence-point to the same endpoint, parameter, component or missing check) but lacking key evidence to confirm common root cause, call suggest_finding_merge; one submission covers a group of 2..20 same-task records, do not split into pairwise suggestions. Merely sharing vulnerability type or asset without specific common point must not submit suggestions; when no specific common point, keep independent, do not suggest just because uncertain. Do not re-report, delete records, or re-probe targets. When manual consolidation is chosen, can only judge selected records, cannot add unselected records (existing group members can serve as report context).
Upon successful merge or existing case_id, get_finding_case records actual version, paginate-read all members, get_finding_record/get_finding_traffic as needed to read original reports, PoC and saved traffic. When root cause is clear, merge is not blocked by unified report, should then use update_finding_case_report to save unified report as much as possible; if not completed this time, folder shows "unified report pending update", to be supplemented by subsequent reporter run or manual. Unified report must still contain overview, impact, prerequisites, reproduction steps, evidence, PoC, root cause analysis, remediation suggestions sections when saved, must not be truncated mid-way; rating and severity_reason must be based on verified impact, cannot directly take member highest severity. Concentrate on giving prerequisites, complete execution order, how input/output connect, runnable complete scripts and commands, do not scatter necessary steps into original reports; missing conditions, unverified steps honestly marked. Version conflicts re-read materials and regenerate, cannot just change version and retry. Members still have independent reports, ratings and evidence. Do not bypass configuration when tool is closed.`
}

const historicalFindingReviewGuidance = `

[Historical consolidation mode, prioritized over old report writing and automatic traffic binding] This time only consolidate already-existing reports. Original report even if empty or pending update, only record missing status, absolutely do not call update_finding_report, report_finding, bind_finding_traffic, do not modify original title, rating, status, evidence or report, do not execute Bash, write files or re-probe targets.
First get_finding_record each selected record one by one, reuse existing original report directly as judgment material, do not rewrite repeatedly. Only check for same actual defect within selected scope, read existing evidence as needed. Structured evidence showing different endpoints/routes, processors or backend components, parameters or missing checks belong to different code paths can judge independent; merely similar type, asset, title, token or login entry is not enough. Only when specific common point is clear and key root cause evidence missing, save one suspected group covering 2..20 records; without specific common point keep independent. Historical consolidation can only handle selected scope, must comply with same-task restriction and manual veto.
Historical mode prohibits calling update_finding_report, report_finding, bind_finding_traffic, does not modify original title, rating, status, evidence or report, does not execute Bash, write files or re-probe targets. Existing or new folder should read all members and use update_finding_case_report to write complete, latest unified report as much as possible; merge not blocked by temporary missing report, when not completed folder shows "unified report pending update", can be supplemented by subsequent run or manual. Unified report is separate material, does not cover original report. Finally must call complete_finding_case_review, submit verdict (independent/merged/suggested), reason and root_cause_evidence for each selected finding_id, and provide summary. Root cause evidence should explain specific same/different checks and evidence sources; suspected suggestions clearly mark evidence to be supplemented. Can only announce completion after server acceptance passes. Manual consolidation can only judge selected scope, cannot add unselected records. When tool unavailable or acceptance fails, report truthfully, cannot just claim completion with natural language.`
