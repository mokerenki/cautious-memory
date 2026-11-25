package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Autumn-27/cautiousmemory/intercept"
	actool "github.com/Autumn-27/norma/tool"
)

type findingReviewDecision struct {
	FindingID         int64  `json:"finding_id"`
	Verdict           string `json:"verdict"`
	Reason            string `json:"reason"`
	RootCauseEvidence string `json:"root_cause_evidence"`
}
type findingReviewConclusion struct {
	Summary   string                  `json:"summary"`
	Decisions []findingReviewDecision `json:"decisions"`
}

func (s *Server) isHistoricalFindingReview(conv int64) (bool, error) {
	var exists bool
	err := s.m.pg.QueryRow(`SELECT EXISTS(SELECT 1 FROM finding_case_review_runs WHERE conversation_id=$1)`, conv).Scan(&exists)
	return exists, err
}

func (s *Server) toolCompleteFindingCaseReview() actool.CoreTool {
	decision := objSchema(map[string]any{
		"finding_id":          idParam("æ‰€é€‰ç‹¬ç«‹æ¼æ´žè®°å½• ID"),
		"verdict":             map[string]any{"type": "string", "enum": []string{"independent", "merged", "suggested"}},
		"reason":              strParam("åˆ¤æ–­ç†ç”±ï¼Œå¼•ç”¨å·²è¯æ®åŒ–çš„å…±åŒç‚¹æˆ–ä¸åŒä»£ç è·¯å¾„ï¼›ä»…ç±»åž‹ã€æ ‡é¢˜æˆ–èµ„äº§ç›¸åŒä¸è¶³ä»¥è¯æ˜Žå…±åŒæ ¹å› "),
		"root_cause_evidence": strParam("Specific evidence from the original report or evidence supporting the same or different root cause. For suggested, explain the specific commonality, missing key evidence, and saved group. Mark as independent when there is no specific commonality.")}, "finding_id", "verdict", "reason", "root_cause_evidence")
	return wrTool("complete_finding_case_review", "Before finishing the review, submit and validate a root-cause decision for every selected record. All records must have been read; suspected duplicates must already have a pending group suggestion. Merges only need to exist in the active folder; the unified report can be completed later. Do not report completion if validation fails.", objSchema(map[string]any{"summary": strParam("Summary of the review results"), "decisions": map[string]any{"type": "array", "items": decision}}, "summary", "decisions"), func(ctx context.Context, raw json.RawMessage) (actool.Result, error) {
		var a findingReviewConclusion
		if err := json.Unmarshal(raw, &a); err != nil {
			return actool.Errorf(err.Error()), nil
		}
		conv := intercept.ConvIDFromContext(ctx)
		if err := s.saveFindingReviewConclusion(ctx, conv, a); err != nil {
			return actool.Errorf(err.Error()), nil
		}
		return actool.Text("åŽ†å²æ•´ç†éªŒæ”¶é€šè¿‡ï¼›åŽŸå§‹è®°å½•å’ŒæŠ¥å‘Šä¿ç•™ï¼Œé€æ¡æ ¹å› åˆ¤æ–­å·²ä¿å­˜ã€‚"), nil
	})
}

func (s *Server) saveFindingReviewConclusion(ctx context.Context, conv int64, a findingReviewConclusion) error {
	if conv <= 0 {
		return errors.New("Only for historical organizing sessions with context")
	}
	return s.m.pg.WithEvidenceTx(ctx, func(tx *sql.Tx) error {
		var idsRaw, readsRaw json.RawMessage
		var state string
		if err := tx.QueryRow(`SELECT finding_ids,reviewed_ids,state FROM finding_case_review_runs WHERE conversation_id=$1 FOR UPDATE`, conv).Scan(&idsRaw, &readsRaw, &state); err != nil {
			return err
		}
		if state != "running" {
			return errors.New("Organizing session is not running, cannot submit acceptance")
		}
		var ids, reads []int64
		if err := json.Unmarshal(idsRaw, &ids); err != nil {
			return err
		}
		if err := json.Unmarshal(readsRaw, &reads); err != nil {
			return err
		}
		read := map[int64]bool{}
		for _, id := range reads {
			read[id] = true
		}
		decisions := map[int64]findingReviewDecision{}
		for _, d := range a.Decisions {
			if _, ok := decisions[d.FindingID]; ok {
				return errors.New("Duplicate judgment number")
			}
			decisions[d.FindingID] = d
		}
		if strings.TrimSpace(a.Summary) == "" || len(ids) != len(decisions) {
			return errors.New("Must provide a summary and per-record judgments covering all selected records")
		}
		for _, id := range ids {
			d, ok := decisions[id]
			if !ok || !read[id] {
				return fmt.Errorf("Please get_finding_record first to read and judge selected record #%d", id)
			}
			if strings.TrimSpace(d.Reason) == "" || strings.TrimSpace(d.RootCauseEvidence) == "" {
				return fmt.Errorf("Record #%d missing judgment reason or root cause evidence", id)
			}
			var cid sql.NullInt64
			if err := tx.QueryRow(`SELECT m.case_id FROM findings f LEFT JOIN finding_case_members m ON m.finding_id=f.id WHERE f.id=$1`, id).Scan(&cid); err != nil {
				return err
			}
			switch d.Verdict {
			case "merged":
				if !cid.Valid {
					return fmt.Errorf("Record #%d not yet merged", id)
				}
			case "suggested":
				var pending bool
				if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM finding_case_suggestions s
                    JOIN finding_case_suggestion_members sm ON sm.suggestion_id=s.id
                    WHERE s.state='pending' AND sm.finding_id=$1)`, id).Scan(&pending); err != nil {
					return err
				}
				if !pending {
					return fmt.Errorf("Record #%d must first save pending confirmation suggestion", id)
				}
			case "independent":
				if cid.Valid {
					return fmt.Errorf("Record #%d already in folder, cannot declare as independent", id)
				}
			default:
				return errors.New("Invalid judgment type")
			}
			if cid.Valid {
				var active bool
				if err := tx.QueryRow(`SELECT active FROM finding_cases WHERE id=$1 FOR SHARE`, cid.Int64).Scan(&active); err != nil {
					return err
				}
				if !active {
					return fmt.Errorf("Folder #%d already closed", cid.Int64)
				}
			}
		}
		raw, err := json.Marshal(a)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE finding_case_review_runs SET conclusion=$2 WHERE conversation_id=$1`, conv, string(raw))
		return err
	})
}

func (s *Server) validateFindingReviewCompletion(conv int64) error {
	var raw json.RawMessage
	if err := s.m.pg.QueryRow(`SELECT conclusion FROM finding_case_review_runs WHERE conversation_id=$1`, conv).Scan(&raw); err != nil {
		return err
	}
	var a findingReviewConclusion
	if err := json.Unmarshal(raw, &a); err != nil {
		return err
	}
	if len(a.Decisions) == 0 {
		return errors.New("Review validation failed: per-finding root-cause decisions were not submitted with complete_finding_case_review. Check the execution history.")
	}
	// Recheck after the last model turn; concurrent evidence changes invalidate completion.
	return s.saveFindingReviewConclusion(context.Background(), conv, a)
}

// Require substantive sections and balanced fenced blocks. This validates report
// completeness, not the truth of a model's root-cause claim or a live exploit.
func validateUnifiedFindingReport(report string) error {
	aliases := [][]string{{"æ¦‚è¿°", "overview", "summary"}, {"å½±å“", "impact"}, {"å‰ç½®", "prerequisite", "precondition"}, {"å¤çŽ°", "reproduction", "reproduce"}, {"è¯æ®", "evidence"}, {"poc", "å®Œæ•´è„šæœ¬", "éªŒè¯è„šæœ¬", "proof of concept"}, {"æ ¹å› ", "root cause"}, {"ä¿®å¤", "remediation", "recommendation", "fix"}}
	names := []string{"æ¦‚è¿°", "å½±å“", "å‰ç½®æ¡ä»¶", "å¤çŽ°æ­¥éª¤", "è¯æ®", "PoC", "æ ¹å› åˆ†æž", "ä¿®å¤å»ºè®®"}
	found := make([]bool, len(aliases))
	current := -1
	hasContent := false
	fence := ""
	finish := func() {
		if current >= 0 && hasContent {
			found[current] = true
		}
	}
	for _, line := range strings.Split(report, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			marker := t[:3]
			if fence == "" {
				fence = marker
			} else if fence == marker {
				fence = ""
			}
			continue
		}
		if fence != "" {
			if t != "" {
				hasContent = true
			}
			continue
		}
		if strings.HasPrefix(t, "#") {
			heading := strings.TrimLeft(t, "#")
			if len(heading) == len(t) || !strings.HasPrefix(heading, " ") {
				continue
			}
			// Subheadings belong to the current section unless they start another required section.
			next := -1
			lower := strings.ToLower(heading)
			for i, words := range aliases {
				for _, word := range words {
					if strings.Contains(lower, word) {
						next = i
						break
					}
				}
				if next >= 0 {
					break
				}
			}
			if next >= 0 {
				finish()
				current = next
				hasContent = false
			}
			continue
		}
		if t != "" && t != "---" && t != "***" && t != ">" {
			hasContent = true
		}
	}
	finish()
	if fence != "" {
		return errors.New("Unified report code block not closed, may be truncated; please complete before saving")
	}
	missing := []string{}
	for i, ok := range found {
		if !ok {
			missing = append(missing, names[i])
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("Unified report missing content sections: %s; please complete and save", strings.Join(missing, "ã€"))
	}
	return nil
}

func (s *Server) historicalFindingReviewSelection(ctx context.Context) ([]int64, error) {
	conv := intercept.ConvIDFromContext(ctx)
	if conv == 0 {
		return nil, nil
	}
	var raw json.RawMessage
	err := s.m.pg.QueryRow(`SELECT finding_ids FROM finding_case_review_runs WHERE conversation_id=$1`, conv).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ids := []int64{}
	err = json.Unmarshal(raw, &ids)
	return ids, err
}

