package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"slices"
	"strconv"

	"github.com/mokerenki/cautious-memory/agent"
	"github.com/mokerenki/cautious-memory/db"
	"github.com/mokerenki/cautious-memory/intercept"
	actool "github.com/Autumn-27/norma/tool"
)

func (s *Server) listFindingVerifications(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok || id <= 0 {
		writeErr(w, 400, "bad finding id")
		return
	}
	items, err := pg.ListFindingVerifications(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"verifications": items})
}

func (s *Server) startFindingVerification(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok || id <= 0 {
		writeErr(w, 400, "bad finding id")
		return
	}
	f, err := pg.GetFinding(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if f == nil {
		writeErr(w, 404, "finding not found")
		return
	}
	v, conv, created, err := s.enqueueFindingVerification(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, 404, "finding not found")
		return
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	code := http.StatusOK
	if created {
		code = http.StatusAccepted
	}
	writeJSON(w, code, map[string]any{"verification": v, "created": created, "conversation_id": conv})
}

// enqueueFindingVerification creates/runs a verifier session when the agent is enabled.
func (s *Server) enqueueFindingVerification(ctx context.Context, findingID int64) (*db.FindingVerification, *db.Conversation, bool, error) {
	a, err := s.m.pg.GetAgentByKey(db.FindingVerifierAgentKey)
	if err != nil {
		return nil, nil, false, err
	}
	if a == nil || !a.Enabled {
		return nil, nil, false, errors.New("验证 Agent 不存在或未启用，请在 Agent 管理中配置 verifier")
	}
	for _, key := range []string{"get_finding_verify_context", "record_finding_verify_result"} {
		t, err := s.m.pg.GetTool(key)
		if err != nil {
			return nil, nil, false, err
		}
		if t == nil || !t.Enabled || !slices.Contains(t.Agents, a.Key) {
			return nil, nil, false, errors.New("请为验证 Agent 启用并绑定工具：" + key)
		}
	}
	if s.resolveChatAgent(&db.Conversation{AgentKey: a.Key}) == nil {
		return nil, nil, false, errors.New(s.chatUnavailableReason())
	}
	if s.ctx.Err() != nil {
		return nil, nil, false, errors.New("服务正在停止")
	}
	v, conv, created, err := s.m.pg.CreateFindingVerification(ctx, findingID)
	if err != nil {
		return nil, nil, false, err
	}
	if created && conv != nil {
		busyKey := s.convBusyKey(conv.ID)
		s.chatMu.Lock()
		s.chatBusy[busyKey] = true
		s.chatMu.Unlock()
		s.runConversation(conv, v.InitialMessage(), busyKey)
	}
	return v, conv, created, nil
}

// maybeAutoVerify is called after a finding is recorded; best-effort enqueue.
func (s *Server) maybeAutoVerify(findingID int64) {
	if findingID <= 0 || s == nil || s.m == nil || s.m.pg == nil {
		return
	}
	a, err := s.m.pg.GetAgentByKey(db.FindingVerifierAgentKey)
	if err != nil || a == nil || !a.Enabled {
		return
	}
	go func() {
		ctx := context.Background()
		if _, _, _, err := s.enqueueFindingVerification(ctx, findingID); err != nil {
			log.Printf("[verifier] auto-enqueue finding %d: %v", findingID, err)
		}
	}()
}

func (s *Server) findingVerifyTools() []actool.CoreTool {
	return []actool.CoreTool{
		roTool("get_finding_verify_context", "读取当前验证会话关联的漏洞证据快照、流量绑定数量、复测无关的原始 claim。无参数，只能读取本会话。",
			objSchema(map[string]any{}), func(ctx context.Context, _ json.RawMessage) (actool.Result, error) {
				r, err := s.m.pg.FindingVerificationForConversation(ctx, intercept.ConvIDFromContext(ctx))
				if err != nil {
					return actool.Errorf(err.Error()), nil
				}
				if r == nil {
					return actool.Errorf("当前会话未关联验证记录"), nil
				}
				n, _ := s.m.pg.CountFindingTrafficBindings(ctx, r.FindingID)
				return jsonResult(map[string]any{
					"verification":  r,
					"traffic_count": n,
					"traffic_gate":  "verified 要求 traffic_count>0，否则只能 needs_human",
				})
			}),
		wrTool("record_finding_verify_result", "保存本会话唯一验证结论。HTTP 类漏洞无绑定流量时系统会将 verified 强制降为 needs_human。",
			objSchema(map[string]any{
				"conclusion":   map[string]any{"type": "string", "enum": []string{"verified", "false_positive", "downgraded", "needs_human"}},
				"severity":     strParam("critical/high/medium/low/none；downgraded 时为建议级别"),
				"summary":      strParam("结论摘要"),
				"evidence":     strParam("Markdown：最小复现步骤与观察"),
				"attribution":  strParam("归因证据：哪条响应证明因果关系"),
				"blast_radius": map[string]any{"type": "string", "enum": []string{"", "single_record", "any_record", "cross_tenant"}},
			}, "conclusion", "summary", "evidence"), func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
				var a struct {
					Conclusion  string `json:"conclusion"`
					Severity    string `json:"severity"`
					Summary     string `json:"summary"`
					Evidence    string `json:"evidence"`
					Attribution string `json:"attribution"`
					BlastRadius string `json:"blast_radius"`
				}
				if err := json.Unmarshal(in, &a); err != nil {
					return actool.Errorf(err.Error()), nil
				}
				if err := s.m.pg.RecordFindingVerifyResult(ctx, intercept.ConvIDFromContext(ctx), a.Conclusion, a.Severity, a.Summary, a.Evidence, a.Attribution, a.BlastRadius); err != nil {
					return actool.Errorf(err.Error()), nil
				}
				return jsonResult(map[string]any{"saved": true, "conclusion": a.Conclusion})
			}),
	}
}

func (s *Server) seedFindingVerifier() error {
	for _, t := range s.findingVerifyTools() {
		schema, _ := json.Marshal(t.InputSchema())
		bindings, _ := json.Marshal([]string{db.FindingVerifierAgentKey})
		if err := s.m.pg.SeedTool(t.Name(), t.Description(), schema, bindings); err != nil {
			return err
		}
	}
	const flag = "finding_verifier_seed_v1"
	tx, err := s.m.pg.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`SELECT pg_advisory_xact_lock(7337741011)`); err != nil {
		return err
	}
	var done string
	err = tx.QueryRow(`SELECT value FROM settings WHERE key=$1`, flag).Scan(&done)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if done == "true" {
		return nil
	}
	var id int64
	err = tx.QueryRow(`INSERT INTO agents(key,name,description,role,builtin,enabled)
	VALUES ($1,'漏洞验证','对新建漏洞做独立复现与归因，写出 verified/false_positive/downgraded/needs_human。','assistant',false,true)
	ON CONFLICT (key) DO NOTHING RETURNING id`, db.FindingVerifierAgentKey).Scan(&id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if id > 0 {
		var pid int64
		if err = tx.QueryRow(`INSERT INTO agent_prompts(agent_id,version,template_text,note,updated_by)
		VALUES ($1,1,$2,'内置默认','system') RETURNING id`, id, agent.VerifierDefaultPrompt).Scan(&pid); err != nil {
			return err
		}
		if _, err = tx.Exec(`UPDATE agents SET current_prompt_id=$1 WHERE id=$2`, pid, id); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`INSERT INTO settings(key,value) VALUES ($1,'true') ON CONFLICT(key) DO UPDATE SET value='true'`, flag); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Server) finishVerification(id int64, status, reason string) {
	if err := s.m.pg.FinishFindingVerification(id, status, reason); err != nil {
		log.Printf("[verify %d] finish: %v", id, err)
	}
}

// registerVerificationRoutes adds verifier HTTP endpoints (called from server setup).
func (s *Server) registerVerificationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/findings/{id}/verifications", s.listFindingVerifications)
	mux.HandleFunc("POST /api/findings/{id}/verify", s.startFindingVerification)
}

// unused import guard helpers
var _ = strconv.Itoa
