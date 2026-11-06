package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

// Missing host/setting means off. Rechecked on every write for running sessions.
var IntelligenceEnabled func() bool
var IntelligenceRecord func(context.Context, int64, int64, string, db.IntelligenceInput) (int64, error)
var CredentialRecorded func()

func intelligenceEnabled() bool { return IntelligenceEnabled != nil && IntelligenceEnabled() }
func intelligenceSchema() map[string]any {
	return obj(map[string]any{
		"kind":       map[string]any{"type": "string", "enum": []string{"credential", "lead", "sensitive_data"}},
		"title":      str("Short title, must not contain password, Token or sensitive data original text"),
		"content":    str("Actual obtained information. Credentials saved as JSON string for account/password/Token etc.; sensitive data only small samples, quantity and file location, prohibited from pasting entire database"),
		"evidence":   str("Real source: command/request/response/file location and key observations, prohibited from fabricating"),
		"confidence": map[string]any{"type": "string", "enum": []string{"observed", "inferred"}},
		"status":     str("Credentials unverified/verified/invalid; leads pending/in_progress/dismissed/converted; sensitive data suspected/confirmed/dismissed. When omitted, conservatively default; verified requires real authentication success evidence"),
		"asset_ids":  map[string]any{"type": "array", "items": map[string]any{"type": "integer"}},
	}, "kind", "title", "content", "evidence", "confidence")
}
func (t *ToolSet) saveIntelligence(ctx context.Context, nodeID int64, in db.IntelligenceInput) (int64, error) {
	if !intelligenceEnabled() || IntelligenceRecord == nil {
		return 0, fmt.Errorf("task intelligence auto-recording has been turned off")
	}
	taskID := t.taskID
	if taskID <= 0 {
		taskID = RunInfoFrom(ctx).TaskID
	}
	if taskID <= 0 {
		return 0, fmt.Errorf("task intelligence requires current task context")
	}
	if nodeID <= 0 {
		nodeID = t.ownerNode
	}
	if nodeID <= 0 {
		nodeID = RunInfoFrom(ctx).IntentID
	}
	id, err := IntelligenceRecord(ctx, taskID, nodeID, t.worker, in)
	if err == nil {
		t.writes.Intelligence++
	}
	return id, err
}
func (t *ToolSet) recordIntelligence() actool.CoreTool {
	return writeTool("record_intelligence", "Record credentials, important leads or sensitive data actually obtained in current task. Task and execution source bound by system; credential discovery does not equal verification success. Duplicate records are deduplicated. Existing insert_assets.auth automatically registered credentials, no need to call repeatedly.", intelligenceSchema(), func(ctx context.Context, raw json.RawMessage) (actool.Result, error) {
		var in db.IntelligenceInput
		if err := json.Unmarshal(raw, &in); err != nil {
			return actool.Errorf("invalid intelligence parameters"), nil
		}
		id, err := t.saveIntelligence(ctx, 0, in)
		if err != nil {
			return actool.Errorf(err.Error()), nil
		}
		return actool.Text(fmt.Sprintf("intelligence recorded: %d", id)), nil
	})
}
func intelligenceWorkflow(ctx context.Context, tools []actool.CoreTool) ([]actool.CoreTool, string) {
	enabled := intelligenceEnabled() && RunInfoFrom(ctx).TaskID > 0
	out := make([]actool.CoreTool, 0, len(tools))
	hasRecord := false
	hasFact := false
	for _, tool := range tools {
		if tool.Name() == "record_intelligence" {
			if !enabled {
				continue
			}
			hasRecord = true
		}
		if tool.Name() == "record_fact" {
			raw, _ := json.Marshal(tool.InputSchema())
			var schema map[string]any
			if json.Unmarshal(raw, &schema) == nil {
				props, _ := schema["properties"].(map[string]any)
				if props != nil {
					delete(props, "intelligence")
					if enabled {
						props["intelligence"] = map[string]any{"type": "array", "maxItems": 20, "description": "Write back intelligence together with facts, fields same as record_intelligence; no need to call that tool separately", "items": intelligenceSchema()}
						hasFact = true
					}
				}
				tool = DecorateTool(tool, tool.Description(), schema)
			}
		}
		out = append(out, tool)
	}
	guide := ""
	if enabled && (hasRecord || hasFact) {
		guide = "\n\n**Task intelligence recording enabled**: when discovering credentials, leads worth following up or real sensitive data, immediately write back structured, do not wait for user inquiry; before ending check for omissions. record_fact can attach intelligence array to write back together, each fact/batch facts elements support; standalone recording use record_intelligence (if available). insert_assets.auth already automatically registered by backend, do not call repeatedly. Only record new, valuable information, ordinary fingerprints/port status still only make fact. title must not contain secret original text; body carries actual content and source. Credentials default unverified, only real authentication success marks verified; inference must be inferred; sensitive data write type, quantity, small samples, file location, do not paste entire database."
	}
	return out, guide
}
