package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/intercept"
	actool "github.com/Autumn-27/norma/tool"
)

type findingCaseRequest struct {
	TaskID         json.RawMessage   `json:"task_id"`
	FindingID      json.RawMessage   `json:"finding_id"`
	IDs            []json.RawMessage `json:"finding_ids"`
	CaseID         json.RawMessage   `json:"case_id"`
	Version        int64             `json:"version"`
	Title          string            `json:"title"`
	Reason         string            `json:"reason"`
	Report         string            `json:"report"`
	Severity       string            `json:"severity"`
	SeverityReason string            `json:"severity_reason"`
	Page           int               `json:"page"`
}

func (a findingCaseRequest) ids() []int64 {
	out := []int64{}
	for _, raw := range a.IDs {
		out = append(out, parseProfileID(raw))
	}
	return out
}
func caseHTTPError(w http.ResponseWriter, err error) {
	code := 400
	if errors.Is(err, db.ErrFindingCaseConflict) || errors.Is(err, db.ErrFindingCaseReviewBusy) {
		code = 409
	}
	writeErr(w, code, err.Error())
}
func decodeCaseRequest(w http.ResponseWriter, r *http.Request, a any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(a); err != nil {
		writeErr(w, 400, "Invalid request body")
		return false
	}
	return true
}
func (s *Server) caseFindingDTO(f *db.DBFinding) FindingDTO {
	return findingFromDB(f, s.resolveAssetIDs(f.AssetIDs))
}
func (s *Server) listFindingCases(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page := findingPaginationParam(q.Get("page"), 1, 0)
	size := findingPaginationParam(q.Get("limit"), 20, 100)
	rows, total, err := s.m.pg.ListFindingCases(findingFilterFromQuery(q), page, size)
	if err != nil {
		caseHTTPError(w, err)
		return
	}
	caseIDs := []int64{}
	for _, row := range rows {
		if row.Case != nil {
			caseIDs = append(caseIDs, row.Case.ID)
		}
	}
	contexts, err := s.m.pg.FindingCaseListContexts(caseIDs)
	if err != nil {
		caseHTTPError(w, err)
		return
	}
	assetIDs := []int64{}
	for _, c := range contexts {
		assetIDs = append(assetIDs, c.AssetIDs...)
	}
	assets := s.resolveAssetIDs(assetIDs)
	items := []map[string]any{}
	for _, row := range rows {
		v := map[string]any{"case": row.Case, "matched_ids": row.MatchedIDs}
		if row.Case != nil {
			c := contexts[row.Case.ID]
			v["task_name"], v["task_description"], v["last_found_at"], v["asset_count"] = c.TaskName, c.TaskDescription, c.LastFoundAt, c.AssetCount
			v["assets"] = append([]FindingAssetDTO{}, findingFromDB(&db.DBFinding{AssetIDs: c.AssetIDs}, assets).Assets...)
		}
		if row.FindingID > 0 {
			f, err := s.m.pg.FindingCaseSummary(row.FindingID, q.Get("original_rows") == "1")
			if err != nil {
				caseHTTPError(w, err)
				return
			}
			if f == nil {
				continue
			}
			f.Report = ""
			v["finding"] = s.caseFindingDTO(f)
		}
		items = append(items, v)
	}
	stats, err := s.m.pg.DistinctFindingStats(q.Get("task_id"))
	if err != nil {
		caseHTTPError(w, err)
		return
	}
	matchingReports, err := s.m.pg.MatchingFindingReportCount(findingFilterFromQuery(q))
	if err != nil {
		caseHTTPError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": total, "matching_reports": matchingReports, "page": page, "page_size": size, "stats": stats})
}
func (s *Server) findingCaseAccess(w http.ResponseWriter, r *http.Request, mutation bool) (*db.FindingCase, bool) {
	id := int64(atoiDefault(r.PathValue("id"), 0))
	c, err := s.m.pg.GetFindingCase(id)
	if err != nil {
		caseHTTPError(w, err)
		return nil, false
	}
	if c == nil {
		writeErr(w, 404, "Finding group not found")
		return nil, false
	}
	if ctx := r.URL.Query().Get("context_task"); ctx != "" {
		t := s.m.ResolveTask(ctx)
		_, inherited, allowed := findingProvenanceInTask(t, c.TaskID)
		if !allowed {
			writeErr(w, 404, "This finding group is not part of the task")
			return nil, false
		}
		if mutation && inherited {
			writeErr(w, 403, "Inherited finding groups are read-only")
			return nil, false
		}
	}
	return c, true
}
func (s *Server) getFindingCase(w http.ResponseWriter, r *http.Request) {
	c, ok := s.findingCaseAccess(w, r, false)
	if !ok {
		return
	}
	events, err := s.m.pg.FindingCaseEvents(c.ID)
	if err != nil {
		caseHTTPError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"case": c, "events": events})
}
func (s *Server) getFindingCaseMembers(w http.ResponseWriter, r *http.Request) {
	c, ok := s.findingCaseAccess(w, r, false)
	if !ok {
		return
	}
	page := findingPaginationParam(r.URL.Query().Get("page"), 1, 0)
	size := findingPaginationParam(r.URL.Query().Get("limit"), 20, 100)
	fs, total, err := s.m.pg.FindingCaseMembers(c.ID, page, size)
	if err != nil {
		caseHTTPError(w, err)
		return
	}
	assets := s.resolveFindingAssets(fs)
	items := []FindingDTO{}
	for _, f := range fs {
		dto := findingFromDB(f, assets)
		if ctx := r.URL.Query().Get("context_task"); ctx != "" {
			dto.SourceTaskID, dto.Inherited, _ = findingProvenanceInTask(s.m.ResolveTask(ctx), f.TaskID)
		}
		items = append(items, dto)
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": total, "page": page, "page_size": size})
}
func (s *Server) removeFindingCaseMember(w http.ResponseWriter, r *http.Request) {
	c, ok := s.findingCaseAccess(w, r, true)
	if !ok {
		return
	}
	fid := int64(atoiDefault(r.PathValue("fid"), 0))
	var body struct {
		Reason string `json:"reason"`
	}
	if !decodeCaseRequest(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Reason) == "" {
		body.Reason = "Manually removed from finding group"
	}
	if err := s.m.pg.RemoveFindingCaseMember(r.Context(), c.ID, fid, body.Reason); err != nil {
		caseHTTPError(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Server) caseSuggestions(w http.ResponseWriter, r *http.Request) {
	items, err := s.m.pg.FindingCaseSuggestions(int64(atoiDefault(r.URL.Query().Get("task_id"), 0)))
	if err != nil {
		caseHTTPError(w, err)
		return
	}
	writeJSON(w, 200, items)
}
func (s *Server) resolveCaseSuggestion(w http.ResponseWriter, r *http.Request) {
	var a struct {
		Accept     bool     `json:"accept"`
		ExcludeIDs []string `json:"exclude_ids"`
	}
	if !decodeCaseRequest(w, r, &a) {
		return
	}
	exclude := make([]int64, 0, len(a.ExcludeIDs))
	for _, raw := range a.ExcludeIDs {
		id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil || id <= 0 {
			caseHTTPError(w, errors.New("Invalid excluded finding ID"))
			return
		}
		exclude = append(exclude, id)
	}
	id, err := s.m.pg.ResolveFindingCaseSuggestion(r.Context(), int64(atoiDefault(r.PathValue("id"), 0)), a.Accept, exclude)
	if err != nil {
		caseHTTPError(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"case_id": i64s(id)})
}
func (s *Server) mergeFindingCase(w http.ResponseWriter, r *http.Request) {
	var a findingCaseRequest
	if !decodeCaseRequest(w, r, &a) {
		return
	}
	id, err := s.m.pg.MergeFindingCase(r.Context(), parseProfileID(a.TaskID), a.ids(), a.Title, a.Reason, "human")
	if err != nil {
		caseHTTPError(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"case_id": i64s(id)})
}
func (s *Server) updateCaseReport(w http.ResponseWriter, r *http.Request) {
	c, ok := s.findingCaseAccess(w, r, true)
	if !ok {
		return
	}
	var a findingCaseRequest
	if !decodeCaseRequest(w, r, &a) {
		return
	}
	if err := validateUnifiedFindingReport(a.Report); err != nil {
		caseHTTPError(w, err)
		return
	}
	if err := s.m.pg.UpdateFindingCaseReport(r.Context(), c.ID, a.Version, a.Title, a.Report, a.Severity, a.SeverityReason); err != nil {
		caseHTTPError(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) findingCaseTools() []actool.CoreTool {
	props := map[string]any{"finding_id": idParam("Standalone finding record ID, not an exploration node ID"), "finding_ids": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "List of standalone finding record IDs"}, "task_id": idParam("ID of the common source task"), "case_id": idParam("Finding group ID"), "version": idParam("Version returned by the actual get_finding_case call"), "page": idParam("Page number, starting at 1, with 20 records per page"), "title": strParam("Name of the same underlying defect"), "reason": strParam("Specific evidence and rationale for grouping; matching types alone is insufficient"), "report": strParam("Complete unified Markdown report based on the evidence from all members"), "severity": strParam("critical|high|medium|low"), "severity_reason": strParam("Rationale based on verified impact; do not automatically choose the highest severity")}
	specs := []struct {
		name, desc string
		required   []string
	}{
		{"search_finding_duplicates", "Read-only search for candidate summaries and assets from the same task. Results are ranked by score and include score, tier, and hit_reasons: strong means the finding type and assets both overlap; weak means they do not both overlap. Review hit_reasons; results are suggestions only. Different severity levels may still be duplicates. Supports pagination; read details as needed.", []string{"finding_id"}},
		{"get_finding_record", "Read the complete original evidence and report for a standalone finding. Returns the exploration node ID for tracing execution records.", []string{"finding_id"}},
		{"merge_finding_records", "Merge records only when evidence clearly establishes the same defect. Preserve every original record. Do not merge across tasks or change original severity, and never override a human rejection.", []string{"task_id", "finding_ids", "title", "reason"}},
		{"suggest_finding_merge", "When evidence shows a specific commonality but lacks key proof, submit one suspected-duplicate group of 2–20 records from the same task for human review. Include all suspected members in one group; do not split them into pairwise suggestions. This does not merge them automatically. Matching types or assets alone is insufficient.", []string{"task_id", "finding_ids", "title", "reason"}},
		{"get_finding_case", "Read a finding group and its paginated members. Record the version first, then read each member's original report, evidence, and traffic. If the version changes, read the group again.", []string{"case_id"}},
		{"update_finding_case_report", "Save the unified report and severity rationale. The version must come from an actual read. Preserve each original report and do not create new findings.", []string{"case_id", "version", "title", "report", "severity", "severity_reason"}},
	}
	out := []actool.CoreTool{}
	for _, spec := range specs {
		spec := spec
		handler := func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a findingCaseRequest
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			v, err := s.performFindingCaseTool(ctx, spec.name, a)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			raw, _ := json.Marshal(v)
			return actool.Text(string(raw)), nil
		}
		schemaProps := map[string]any{}
		for _, key := range spec.required {
			schemaProps[key] = props[key]
		}
		if spec.name == "search_finding_duplicates" || spec.name == "get_finding_case" {
			schemaProps["page"] = props["page"]
		}
		schema := objSchema(schemaProps, spec.required...)
		if strings.HasPrefix(spec.name, "get_") || strings.HasPrefix(spec.name, "search_") {
			out = append(out, roTool(spec.name, spec.desc, schema, handler))
		} else {
			out = append(out, wrTool(spec.name, spec.desc, schema, handler))
		}
	}
	out = append(out, s.toolCompleteFindingCaseReview())
	return out
}
func (s *Server) performFindingCaseTool(ctx context.Context, name string, a findingCaseRequest) (any, error) {
	if err := s.checkCaseReviewScope(ctx, name, a); err != nil {
		return nil, err
	}
	fid := parseProfileID(a.FindingID)
	cid := parseProfileID(a.CaseID)
	page := a.Page
	if page < 1 {
		page = 1
	}
	switch name {
	case "search_finding_duplicates":
		scope, err := s.historicalFindingReviewSelection(ctx)
		if err != nil {
			return nil, err
		}
		var scopes [][]int64
		if scope != nil {
			scopes = append(scopes, scope)
		}
		items, total, strongTotal, err := s.m.pg.FindingCaseCandidates(fid, page, 20, scopes...)
		return map[string]any{"items": items, "total": total, "strong_total": strongTotal, "page": page, "page_size": 20,
			"note": "Results are ranked by score. A weak match does not have both matching finding type and overlapping assets. Review hit_reasons; results are suggestions only and usually should not be used as the basis for a merge."}, err
	case "get_finding_record":
		f, err := s.m.pg.GetFinding(fid)
		if err != nil {
			return nil, err
		}
		if f == nil {
			return nil, errors.New("Finding not found")
		}
		dto := s.caseFindingDTO(f)
		cid, err := s.m.pg.FindingCaseID(f.ID)
		if err != nil {
			return nil, err
		}
		if cid > 0 {
			dto.CaseID = i64s(cid)
		}
		if conv := intercept.ConvIDFromContext(ctx); conv > 0 {
			if _, err := s.m.pg.Exec(`UPDATE finding_case_review_runs SET reviewed_ids=CASE WHEN reviewed_ids @> jsonb_build_array($2::bigint) THEN reviewed_ids ELSE reviewed_ids || jsonb_build_array($2::bigint) END WHERE conversation_id=$1 AND finding_ids @> jsonb_build_array($2::bigint)`, conv, f.ID); err != nil {
				return nil, err
			}
		}
		return map[string]any{"finding": dto, "finding_node_id": f.NodeID}, nil
	case "merge_finding_records":
		id, err := s.m.pg.MergeFindingCase(ctx, parseProfileID(a.TaskID), a.ids(), a.Title, a.Reason, "reporter")
		return map[string]string{"case_id": i64s(id)}, err
	case "suggest_finding_merge":
		ids := a.ids()
		if len(ids) < 2 || len(ids) > 20 {
			return nil, errors.New("A suspected-duplicate group must contain 2 to 20 records")
		}
		id, err := s.m.pg.SuggestFindingCase(ctx, parseProfileID(a.TaskID), ids, a.Title, a.Reason)
		return map[string]any{"suggestion_id": i64s(id), "finding_ids": ids}, err
	case "get_finding_case":
		c, err := s.m.pg.GetFindingCase(cid)
		if err != nil {
			return nil, err
		}
		if c == nil {
			return nil, errors.New("Finding group not found")
		}
		fs, total, err := s.m.pg.FindingCaseMembers(cid, page, 20)
		members := []FindingDTO{}
		for _, f := range fs {
			members = append(members, s.caseFindingDTO(f))
		}
		return map[string]any{"case": c, "members": members, "total": total, "page": page, "page_size": 20}, err
	case "update_finding_case_report":
		if err := validateUnifiedFindingReport(a.Report); err != nil {
			return nil, err
		}
		err := s.m.pg.UpdateFindingCaseReport(ctx, cid, a.Version, a.Title, a.Report, a.Severity, a.SeverityReason)
		return map[string]bool{"ok": err == nil}, err
	}
	return nil, errors.New("Unknown finding-group tool")
}
func idParam(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}
func (s *Server) seedFindingCaseTools() {
	_, _ = s.m.pg.Exec(`UPDATE finding_case_review_runs SET state='failed',error='服务重启中断，请重新提交整理' WHERE state IN ('queued','running')`)
	a, err := s.m.pg.GetAgentByKey("reporter")
	if err != nil || a == nil {
		return
	}
	const flag = "finding_case_tools_v3_review"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	names := []string{}
	previous, _, _ := s.m.pg.GetSetting("finding_case_tools_v1")
	// v3 refreshes descriptions and schemas without repeating the v2 binding migration.
	previousReview, _, _ := s.m.pg.GetSetting("finding_case_tools_v2_review")
	for _, t := range s.findingCaseTools() {
		schema, _ := json.Marshal(t.InputSchema())
		if err := s.m.pg.RefreshToolDefaults(t.Name(), t.Description(), schema); err != nil {
			log.Printf("[finding-cases] refresh %s schema failed: %v", t.Name(), err)
		}
		if previousReview == "true" {
			continue
		}
		if err := s.m.pg.SeedTool(t.Name(), t.Description(), schema, json.RawMessage(`["reporter"]`)); err != nil {
			return
		}
		if previous != "true" || t.Name() == "complete_finding_case_review" {
			names = append(names, t.Name())
		}
	}
	if previousReview != "true" {
		if err := s.m.pg.AddAgentToToolBinding("reporter", names); err != nil {
			return
		}
		if err := s.m.pg.SetSetting("finding_case_tools_v2_review", "true"); err != nil {
			return
		}
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

const findingCaseReviewTaskMessage = "Human-selected finding review (read-only original records): the selected finding_ids for this task are %s. Read each existing report and determine only whether findings share the same underlying cause; merge them directly only when the common cause is clear. Submit one suspected-duplicate group of 2–20 records from the same task only when evidence shows a specific commonality, such as the same endpoint, handler/backend component, parameter, or missing check, but lacks key proof of a shared root cause. Do not split the group into pairwise suggestions. Matching types or assets alone is insufficient. If there is no specific commonality, mark the records independent; do not submit a suggestion merely because the evidence is uncertain. Findings from different code paths may be marked independent. Make a reasonable effort to update the unified report for existing or newly created groups; a missing or outdated unified report does not block complete_finding_case_review and may be completed in a later run or by a human. Do not create or overwrite original reports, change severity or status, attach evidence, merge unselected records, or probe targets. Honor human rejections. Before finishing, call complete_finding_case_review with a decision for each selected finding and pass its validation."

func (s *Server) reviewFindingCases(w http.ResponseWriter, r *http.Request) {
	var a findingCaseRequest
	if !decodeCaseRequest(w, r, &a) {
		return
	}
	ids := a.ids()
	if cid := parseProfileID(a.CaseID); cid > 0 {
		c, err := s.m.pg.GetFindingCase(cid)
		if err != nil || c == nil || !c.Active {
			writeErr(w, 400, "Finding group is unavailable")
			return
		}
		if ctx := r.URL.Query().Get("context_task"); ctx != "" && (c.TaskID == nil || ctx != i64s(*c.TaskID)) {
			writeErr(w, 403, "Inherited finding groups are read-only")
			return
		}
		fs, _, err := s.m.pg.FindingCaseMembers(cid, 1, 100000)
		if err != nil {
			caseHTTPError(w, err)
			return
		}
		ids = nil
		for _, f := range fs {
			ids = append(ids, f.ID)
		}
	}
	if len(ids) == 0 || (len(a.CaseID) == 0 && len(ids) > 200) {
		writeErr(w, 400, "Select between 1 and 200 original finding reports")
		return
	}
	reporter, err := s.m.pg.GetAgentByKey("reporter")
	if err != nil || reporter == nil || !reporter.Enabled || s.chatAgentRef() == nil {
		writeErr(w, 409, "The Report Agent is disabled or its model is unavailable")
		return
	}
	batches := map[int64][]int64{}
	for _, fid := range ids {
		f, err := s.m.pg.FindingCaseSummary(fid)
		if err != nil || f == nil || f.TaskID == nil {
			writeErr(w, 400, "A selected finding's source task is unavailable")
			return
		}
		batches[*f.TaskID] = append(batches[*f.TaskID], fid)
	}
	// Validate and snapshot every source before starting any batch.
	taskContexts := map[int64]triggeredRun{}
	for taskID := range batches {
		t, ok := s.m.Task(i64s(taskID))
		if !ok {
			writeErr(w, 409, "Task is unavailable")
			return
		}

		taskContexts[taskID] = triggeredRun{taskDesc: t.Description, taskGoal: t.Goal}
	}
	reservations, err := s.m.pg.CreateFindingCaseReviews(r.Context(), batches)
	if err != nil {
		caseHTTPError(w, err)
		return
	}
	runs := []map[string]any{}
	for _, reservation := range reservations {
		taskID, fids := reservation.TaskID, reservation.FindingIDs
		taskContext := taskContexts[taskID]
		raw, _ := json.Marshal(fids)
		msg := fmt.Sprintf(findingCaseReviewTaskMessage, raw)
		item := triggeredRun{agentKey: "reporter", title: "Finding review · task#" + i64s(taskID), message: msg, taskID: taskID, taskDesc: taskContext.taskDesc, taskGoal: taskContext.taskGoal, conversationID: reservation.ConversationID}
		cfg := s.readTriggerBehavior("reporter")
		s.queueMu.Lock()
		s.triggerCfg["reporter"] = cfg
		s.triggerQ["reporter"] = append(s.triggerQ["reporter"], item)
		s.pumpLocked("reporter")
		s.queueMu.Unlock()
		runs = append(runs, map[string]any{"conversation_id": reservation.ConversationID, "task_id": i64s(taskID)})
	}
	writeJSON(w, 202, map[string]any{"runs": runs})
}
func (s *Server) caseReviewRuns(w http.ResponseWriter, r *http.Request) {
	rows, err := s.m.pg.Query(`SELECT conversation_id,task_id,state,error FROM finding_case_review_runs ORDER BY conversation_id DESC LIMIT 50`)
	if err != nil {
		caseHTTPError(w, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var conv int64
		var task sql.NullInt64
		var state, msg string
		if err := rows.Scan(&conv, &task, &state, &msg); err != nil {
			caseHTTPError(w, err)
			return
		}
		items = append(items, map[string]any{"conversation_id": conv, "task_id": i64s(task.Int64), "state": state, "error": msg})
	}
	if err := rows.Err(); err != nil {
		caseHTTPError(w, err)
		return
	}
	writeJSON(w, 200, items)
}

// Manual reviews cannot mutate a finding outside the explicit selection.
func (s *Server) checkCaseReviewScope(ctx context.Context, name string, a findingCaseRequest) error {
	conv := intercept.ConvIDFromContext(ctx)
	if conv == 0 {
		return nil
	}
	var raw, conclusion json.RawMessage
	err := s.m.pg.QueryRow(`SELECT finding_ids,conclusion FROM finding_case_review_runs WHERE conversation_id=$1`, conv).Scan(&raw, &conclusion)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	var ids []int64
	if err := json.Unmarshal(raw, &ids); err != nil {
		return err
	}
	if string(conclusion) != "{}" && (name == "merge_finding_records" || name == "suggest_finding_merge" || name == "update_finding_case_report") {
		return errors.New("Finding review decisions have been submitted and cannot be changed after validation")
	}
	allowed := map[int64]bool{}
	for _, id := range ids {
		allowed[id] = true
	}
	switch name {
	case "search_finding_duplicates":
		if !allowed[parseProfileID(a.FindingID)] {
			return errors.New("Historical review may search only for candidates among the selected records")
		}
	case "update_finding_report", "bind_finding_traffic", "report_finding":
		return errors.New("Historical review cannot modify original reports, findings, or evidence; it may only update group relationships and unified reports")
	case "merge_finding_records", "suggest_finding_merge":
		checkIDs := a.ids()
		for _, id := range checkIDs {
			if !allowed[id] {
				return errors.New("Manual review may merge only the selected records")
			}
		}
	case "update_finding_case_report":
		fs, _, err := s.m.pg.FindingCaseMembers(parseProfileID(a.CaseID), 1, 100000)
		if err != nil {
			return err
		}
		found := false
		for _, f := range fs {
			if allowed[f.ID] {
				found = true
			}
		}
		if !found {
			return errors.New("This finding group is outside the human-selected scope")
		}
	}
	return nil
}
