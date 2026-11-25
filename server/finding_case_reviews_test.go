package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Autumn-27/cautiousmemory/intercept"
)

func TestFindingCaseReviewTaskMessagePolicy(t *testing.T) {
	message := fmt.Sprintf(findingCaseReviewTaskMessage, `[101,102,103]`)
	for _, want := range []string{
		"selected finding_ids for this task are [101,102,103]",
		"merge them directly only when the common cause is clear",
		"same endpoint, handler/backend component, parameter, or missing check",
		"lacks key proof of a shared root cause",
		"one suspected-duplicate group of 2â€“20 records from the same task",
		"Do not split the group into pairwise suggestions",
		"Matching types or assets alone is insufficient",
		"mark the records independent",
		"Findings from different code paths may be marked independent",
		"Make a reasonable effort to update the unified report",
		"a missing or outdated unified report does not block complete_finding_case_review",
		"merge unselected records",
		"probe targets",
		"Honor human rejections",
		"do not submit a suggestion merely because the evidence is uncertain",
	} {
		if !strings.Contains(message, want) {
			t.Errorf("task message missing policy: %s", want)
		}
	}
	schema, err := json.Marshal((&Server{}).toolCompleteFindingCaseReview().InputSchema())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(schema), "Mark as independent when there is no specific commonality.") {
		t.Fatal("completion schema must not require suggestions solely because of uncertainty")
	}
}

const completeFindingReviewReport = `# åŒä¸€æƒé™ç¼ºé™·
## æ¦‚è¿°
åŒä¸€å¯¹è±¡æƒé™æ£€æŸ¥ç¼ºå¤±ã€‚
## å½±å“
å·²éªŒè¯è¯»å–å…¶ä»–è´¦å·çš„è®¢å•ã€‚
## å‰ç½®æ¡ä»¶
å·²å­˜åœ¨çš„ä½Žæƒä¼šè¯å’Œè®¢å•ç¼–å·ã€‚
## å¤çŽ°æ­¥éª¤
å¯¹ç…§è‡ªå·±çš„è®¢å•ï¼Œå†æŸ¥çœ‹å…¶ä»–è´¦å·è®¢å•ï¼Œå“åº”è¯æ˜ŽåŒä¸€æ£€æŸ¥ç¼ºå¤±ã€‚
## è¯æ®
åŽŸæŠ¥å‘Šçš„ä¿å­˜å“åº”åŠå‘½ä»¤è¾“å‡ºï¼Œæœªé‡æ–°æŽ¢æµ‹ã€‚
## PoC
å®Œæ•´å‘½ä»¤åœ¨åŽŸå§‹è¯æ®ä¸­ï¼Œæ­¤æµ‹è¯•ä½¿ç”¨éš”ç¦»ææ–™ã€‚
## æ ¹å› åˆ†æž
åŒä¸€è®¢å• handler æœªéªŒè¯è®¢å•æ‰€æœ‰è€…ã€‚
## ä¿®å¤å»ºè®®
ç»Ÿä¸€éªŒè¯è¯·æ±‚è€…ä¸Žè®¢å•æ‰€æœ‰è€…ï¼Œå¹¶å›žå½’å¯¹è±¡æƒé™æ£€æŸ¥ã€‚
`

func TestUnifiedFindingReportRejectsIncompleteAndTruncated(t *testing.T) {
	if err := validateUnifiedFindingReport(completeFindingReviewReport); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{strings.Split(completeFindingReviewReport, "## æ ¹å› åˆ†æž")[0], completeFindingReviewReport + "\n```bash\nunclosed", strings.Replace(completeFindingReviewReport, "ç»Ÿä¸€éªŒè¯è¯·æ±‚è€…ä¸Žè®¢å•æ‰€æœ‰è€…ï¼Œå¹¶å›žå½’å¯¹è±¡æƒé™æ£€æŸ¥ã€‚", "", 1)} {
		if err := validateUnifiedFindingReport(s); err == nil {
			t.Fatal("incomplete report accepted")
		}
	}
	// Section labels appearing inside a script are not real report sections.
	if err := validateUnifiedFindingReport("## æ¦‚è¿°\ntext\n```bash\n" + completeFindingReviewReport + "\n```"); err == nil {
		t.Fatal("script comments counted as sections")
	}
}

func TestFindingCaseReviewCompletionRequiresReadsNotFreshReport(t *testing.T) {
	s, task, ids := testCaseServer(t)
	runs, err := s.m.pg.CreateFindingCaseReviews(t.Context(), map[int64][]int64{task: ids})
	if err != nil {
		t.Fatal(err)
	}
	conv := runs[0].ConversationID
	defer s.m.pg.Exec(`DELETE FROM conversations WHERE id=$1`, conv)
	if _, err := s.m.pg.Exec(`UPDATE finding_case_review_runs SET state='running' WHERE conversation_id=$1`, conv); err != nil {
		t.Fatal(err)
	}
	ctx := intercept.WithConvID(context.Background(), conv)
	a := findingReviewConclusion{Summary: "æ ¹æ®å¯¹è±¡æƒé™æ£€æŸ¥çš„å…·ä½“è¯æ®åˆ¤æ–­"}
	for _, id := range ids {
		a.Decisions = append(a.Decisions, findingReviewDecision{FindingID: id, Verdict: "merged", Reason: "åŒä¸€ handler", RootCauseEvidence: "å„è®°å½•çš„ baseline/proof æŒ‡å‘åŒä¸€å¯¹è±¡æ‰€æœ‰è€…æ ¡éªŒç¼ºå¤±"})
	}
	if err := s.saveFindingReviewConclusion(ctx, conv, a); err == nil {
		t.Fatal("unread findings accepted")
	}
	for _, id := range ids {
		if _, err := s.performFindingCaseTool(ctx, "get_finding_record", findingCaseRequest{FindingID: json.RawMessage(fmt.Sprint(id))}); err != nil {
			t.Fatal(err)
		}
	}
	cid, err := s.m.pg.MergeFindingCase(ctx, task, ids, "orders", "same concrete check", "reporter")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.saveFindingReviewConclusion(ctx, conv, a); err != nil {
		t.Fatal(err)
	}
	if err := s.validateFindingReviewCompletion(conv); err != nil {
		t.Fatal(err)
	}
	if err := s.checkCaseReviewScope(ctx, "update_finding_case_report", findingCaseRequest{CaseID: json.RawMessage(fmt.Sprint(cid))}); err == nil {
		t.Fatal("sealed review can still mutate")
	}
	if _, err := s.m.pg.Exec(`UPDATE findings SET evidence='new proof' WHERE id=$1`, ids[0]); err != nil {
		t.Fatal(err)
	}
	if err := s.validateFindingReviewCompletion(conv); err != nil {
		t.Fatalf("stale unified report should not block merged conclusion: %v", err)
	}
}

func TestFindingCaseReviewProtectsOriginalToolWrites(t *testing.T) {
	s, f, _ := trafficEvidenceServer(t)
	original, err := s.m.pg.GetFinding(f.FindingID)
	if err != nil {
		t.Fatal(err)
	}
	runs, err := s.m.pg.CreateFindingCaseReviews(t.Context(), map[int64][]int64{*original.TaskID: {original.ID}})
	if err != nil {
		t.Fatal(err)
	}
	conv := runs[0].ConversationID
	defer s.m.pg.Exec(`DELETE FROM conversations WHERE id=$1`, conv)
	ctx := intercept.WithConvID(context.Background(), conv)
	workflowCall(t, ctx, s.toolUpdateFindingReport(), map[string]any{"finding_id": f.NodeID, "report": "OVERWRITE", "evidence_version": original.EvidenceVersion}, true)
	if err := s.agentFindingTrafficAccess(ctx, f.FindingID, true); err == nil {
		t.Fatal("historical review can change evidence")
	}
	after, _ := s.m.pg.GetFinding(original.ID)
	if after.Report != original.Report || after.EvidenceVersion != original.EvidenceVersion || after.Status != original.Status || after.Severity != original.Severity {
		t.Fatal("original changed")
	}
	// Normal reporting is still able to write its own standalone report.
	workflowCall(t, context.Background(), s.toolUpdateFindingReport(), map[string]any{"finding_id": f.NodeID, "report": "normal report", "evidence_version": original.EvidenceVersion}, false)
}

func TestFindingCaseReviewSuggestionNeedsSavedSuggestion(t *testing.T) {
	s, task, ids := testCaseServer(t)
	runs, err := s.m.pg.CreateFindingCaseReviews(t.Context(), map[int64][]int64{task: ids[:2]})
	if err != nil {
		t.Fatal(err)
	}
	conv := runs[0].ConversationID
	defer s.m.pg.Exec(`DELETE FROM conversations WHERE id=$1`, conv)
	s.m.pg.Exec(`UPDATE finding_case_review_runs SET state='running' WHERE conversation_id=$1`, conv)
	ctx := intercept.WithConvID(context.Background(), conv)
	a := findingReviewConclusion{Summary: "éœ€äººå·¥ç¡®è®¤å…±åŒç»„ä»¶"}
	for _, id := range ids[:2] {
		s.performFindingCaseTool(ctx, "get_finding_record", findingCaseRequest{FindingID: json.RawMessage(fmt.Sprint(id))})
		a.Decisions = append(a.Decisions, findingReviewDecision{FindingID: id, Verdict: "suggested", Reason: "åŒä¸€è¾¹ç•Œä½†æ ¹å› æœªç¡®è®¤", RootCauseEvidence: "åŽŸæŠ¥å‘ŠæŒ‡å‘å¯¹è±¡æƒé™è¾¹ç•Œï¼Œç¼ºå°‘å…±åŒç»„ä»¶è¯æ®"})
	}
	if err := s.saveFindingReviewConclusion(ctx, conv, a); err == nil {
		t.Fatal("unpersisted suggestion accepted")
	}
	if _, err := s.m.pg.SuggestFindingCase(ctx, task, []int64{ids[0], ids[1]}, "orders", "common component unknown"); err != nil {
		t.Fatal(err)
	}
	if err := s.saveFindingReviewConclusion(ctx, conv, a); err != nil {
		t.Fatal(err)
	}
}

func TestFindingCaseReviewCandidatesStayWithinSelection(t *testing.T) {
	s, task, ids := testCaseServer(t)
	runs, err := s.m.pg.CreateFindingCaseReviews(t.Context(), map[int64][]int64{task: ids[:2]})
	if err != nil {
		t.Fatal(err)
	}
	conv := runs[0].ConversationID
	defer s.m.pg.Exec(`DELETE FROM conversations WHERE id=$1`, conv)
	ctx := intercept.WithConvID(context.Background(), conv)
	a := findingCaseRequest{FindingID: json.RawMessage(fmt.Sprint(ids[0]))}
	v, err := s.performFindingCaseTool(ctx, "search_finding_duplicates", a)
	if err != nil {
		t.Fatal(err)
	}
	if v.(map[string]any)["total"].(int) != 1 {
		t.Fatal("unselected candidates leaked", v)
	}
	v, err = s.performFindingCaseTool(context.Background(), "search_finding_duplicates", a)
	if err != nil || v.(map[string]any)["total"].(int) != 2 {
		t.Fatal("normal candidate search changed", v, err)
	}
}

