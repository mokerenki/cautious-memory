package server

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Autumn-27/cautiousmemory/db"
)

// generateFindingReport requeues the existing reporter workflow for a finding.
// This is intentionally separate from report_finding: a restart can leave a
// finding persisted while the one-shot trigger conversation is gone.
func (s *Server) generateFindingReport(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok || id <= 0 {
		writeErr(w, http.StatusBadRequest, "bad finding id")
		return
	}
	f, err := s.m.pg.GetFinding(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if f == nil {
		writeErr(w, http.StatusNotFound, "finding not found")
		return
	}
	if f.NodeID == nil || *f.NodeID <= 0 {
		writeErr(w, http.StatusConflict, "Finding has no available exploration node, cannot generate reproduction steps")
		return
	}
	reporter, err := s.m.pg.GetAgentByKey("reporter")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if reporter == nil || !reporter.Enabled {
		writeErr(w, http.StatusConflict, "Reporter Agent not enabled, please enable reporter in Agent Management")
		return
	}
	if s.resolveChatAgent(&db.Conversation{AgentKey: reporter.Key}) == nil {
		writeErr(w, http.StatusServiceUnavailable, s.chatUnavailableReason())
		return
	}
	if s.ctx.Err() != nil {
		writeErr(w, http.StatusServiceUnavailable, "Service is shutting down")
		return
	}

	var taskID int64
	var taskDesc, taskGoal string
	if f.TaskID != nil {
		taskID = *f.TaskID
		if task := s.m.ResolveTask(strconv.FormatInt(taskID, 10)); task != nil {
			taskDesc, taskGoal = task.Description, task.Goal
		}
	}
	message := manualFindingReportMessage(f, taskID)
	conversation, err := s.m.pg.CreateConversation("reporter", fmt.Sprintf("Vulnerability report Â· finding#%d", f.ID), nil)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Failed to create report conversation: "+err.Error())
		return
	}
	if err := s.m.pg.SetFindingReportConversationID(f.ID, conversation.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, "Failed to associate report conversation: "+err.Error())
		return
	}
	s.StartTriggeredRunWithConversation(
		"reporter",
		fmt.Sprintf("Manually generate steps and report Â· finding#%d", f.ID),
		message,
		taskID,
		false,
		taskDesc,
		taskGoal, conversation.ID,
	)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"queued":          true,
		"agent_key":       "reporter",
		"finding_id":      f.ID,
		"finding_node_id": *f.NodeID,
		"conversation_id": conversation.ID,
	})
}

func manualFindingReportMessage(f *db.DBFinding, taskID int64) string {
	var b strings.Builder
	b.WriteString("[Manually regenerate steps and report] This is a registered vulnerability; please supplement structured reproduction steps and a complete detailed report for it; do not create new vulnerability records, nor modify or delete original evidence.\n")
	fmt.Fprintf(&b, "ç‹¬ç«‹æ¼æ´žè®°å½• IDï¼ˆfinding_idï¼‰=%dï¼›æŽ¢ç´¢èŠ‚ç‚¹ IDï¼ˆfinding_node_idï¼‰=%dã€‚\n", f.ID, *f.NodeID)
	if taskID > 0 {
		fmt.Fprintf(&b, "æ¥æºä»»åŠ¡ ID=%dã€‚\n", taskID)
	}
	b.WriteString("å…ˆè¯»å– get_finding_traffic(finding_id) çš„å½“å‰è¯æ®æ¸…å•åŠ versionï¼Œå†è¯»å–èŠ‚ç‚¹è¯¦æƒ…å’Œä»»åŠ¡æ‰§è¡Œè®°å½•ï¼›åªä½¿ç”¨å®žé™…ä¿å­˜çš„è¯·æ±‚ã€å“åº”å’Œæ‰§è¡Œè¯æ®ï¼Œä¸è‡†é€ æ­¥éª¤æˆ–æ•°æ®åŒ…ã€‚\n")
	b.WriteString("æŠ¥å‘Šå¿…é¡»åŒ…å«â€œæ¦‚è¿°â€â€œå½±å“ä¸Žå±å®³â€â€œå—å½±å“èŒƒå›´â€â€œå¤çŽ°æ­¥éª¤â€â€œæ ¹å› åˆ†æžâ€â€œä¿®å¤å»ºè®®â€ï¼Œå¹¶åœ¨å¤çŽ°æ­¥éª¤ä¸­æŒ‰â€œæ­¥éª¤ 1ã€æ­¥éª¤ 2 â€¦â€å®Œæ•´å±•å¼€ã€‚æ•°æ®åŒ…åªèƒ½å¼•ç”¨è¯¥æ¼æ´žå·²ç»‘å®šçš„çœŸå®žæµé‡ã€‚\n")
	b.WriteString("æœ€åŽè°ƒç”¨ update_finding_reportï¼šfinding_id ä½¿ç”¨æŽ¢ç´¢èŠ‚ç‚¹ IDï¼Œevidence_version ä½¿ç”¨åˆšè¯»å–çš„è¯æ®ç‰ˆæœ¬ï¼›æŠ¥å‘Šæ­£æ–‡ä¿å­˜ä¸º Markdownã€‚")
	return b.String()
}

