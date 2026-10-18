package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// FindingVerifierAgentKey is the seeded conversation agent for independent verification.
const FindingVerifierAgentKey = "verifier"

// Verification status on findings.verification_status (independent of triage status).
const (
	VerificationUnverified    = "unverified"
	VerificationVerifying     = "verifying"
	VerificationVerified      = "verified"
	VerificationFalsePositive = "false_positive"
	VerificationDowngraded    = "downgraded"
	VerificationNeedsHuman    = "needs_human"
)

// ValidVerificationStatus reports whether s is a known verification state.
func ValidVerificationStatus(s string) bool {
	switch s {
	case VerificationUnverified, VerificationVerifying, VerificationVerified,
		VerificationFalsePositive, VerificationDowngraded, VerificationNeedsHuman:
		return true
	}
	return false
}

// ValidVerifyConclusion is the terminal conclusion set the verifier agent may record.
func ValidVerifyConclusion(s string) bool {
	switch s {
	case VerificationVerified, VerificationFalsePositive, VerificationDowngraded, VerificationNeedsHuman:
		return true
	}
	return false
}

// Blast radius values for logic findings.
const (
	BlastSingleRecord = "single_record"
	BlastAnyRecord    = "any_record"
	BlastCrossTenant  = "cross_tenant"
)

func ValidBlastRadius(s string) bool {
	switch s {
	case "", BlastSingleRecord, BlastAnyRecord, BlastCrossTenant:
		return true
	}
	return false
}

var ErrVerifyNotRunning = errors.New("本次验证已结束或尚未开始")

// FindingVerification is one independent verifier run for a finding.
type FindingVerification struct {
	ID             int64           `json:"id"`
	FindingID      int64           `json:"finding_id"`
	ConversationID *int64          `json:"conversation_id"`
	Status         string          `json:"status"`
	Conclusion     string          `json:"conclusion"`
	Severity       string          `json:"severity"`
	Summary        string          `json:"summary"`
	Evidence       string          `json:"evidence"`
	Attribution    string          `json:"attribution"`
	BlastRadius    string          `json:"blast_radius"`
	Error          string          `json:"error"`
	Snapshot       json.RawMessage `json:"snapshot,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	StartedAt      *time.Time      `json:"started_at"`
	FinishedAt     *time.Time      `json:"finished_at"`
}

const verifyCols = `id, finding_id, conversation_id, status, conclusion, severity, summary, evidence, attribution, blast_radius, error, created_at, started_at, finished_at`

const verificationSchema = `
ALTER TABLE findings ADD COLUMN IF NOT EXISTS verification_status TEXT NOT NULL DEFAULT 'verified';
ALTER TABLE findings ADD COLUMN IF NOT EXISTS verification_severity TEXT NOT NULL DEFAULT '';
ALTER TABLE findings ADD COLUMN IF NOT EXISTS verification_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE findings ADD COLUMN IF NOT EXISTS verifier_conversation_id BIGINT;
ALTER TABLE findings ADD COLUMN IF NOT EXISTS invariant_id BIGINT;
ALTER TABLE findings ADD COLUMN IF NOT EXISTS violation_demo TEXT NOT NULL DEFAULT '';
ALTER TABLE findings ADD COLUMN IF NOT EXISTS blast_radius TEXT NOT NULL DEFAULT '';
ALTER TABLE findings ALTER COLUMN verification_status SET DEFAULT 'unverified';
CREATE INDEX IF NOT EXISTS idx_findings_verification ON findings(verification_status, created_at DESC);

CREATE TABLE IF NOT EXISTS finding_verifications (
    id BIGSERIAL PRIMARY KEY,
    finding_id BIGINT NOT NULL REFERENCES findings(id) ON DELETE CASCADE,
    conversation_id BIGINT UNIQUE REFERENCES conversations(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    conclusion TEXT NOT NULL DEFAULT '',
    severity TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    evidence TEXT NOT NULL DEFAULT '',
    attribution TEXT NOT NULL DEFAULT '',
    blast_radius TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    snapshot JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_finding_verifications_finding ON finding_verifications(finding_id, id DESC);
CREATE INDEX IF NOT EXISTS idx_finding_verifications_active ON finding_verifications(status) WHERE status IN ('pending','running');

ALTER TABLE tasks ADD COLUMN IF NOT EXISTS invariants TEXT NOT NULL DEFAULT '';
`

// EnsureVerificationSchema applies verification + invariant columns idempotently.
func (d *DB) EnsureVerificationSchema() error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if err := coordinateWithSchemaMigration(tx); err != nil {
		return err
	}
	if _, err := tx.Exec(verificationSchema); err != nil {
		return err
	}
	// Extend node kind/state/edge CHECKs for invariant (best-effort).
	_, _ = tx.Exec(`
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid='exploration_nodes'::regclass
          AND conname='ck_node_kind'
          AND pg_get_constraintdef(oid) NOT LIKE '%invariant%'
    ) THEN
        ALTER TABLE exploration_nodes DROP CONSTRAINT ck_node_kind;
        ALTER TABLE exploration_nodes ADD CONSTRAINT ck_node_kind
            CHECK (kind IN ('begin','goal','intent','fact','finding','hint','digest','invariant'));
    END IF;
END $$;`)
	_, _ = tx.Exec(`
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid='exploration_nodes'::regclass
          AND conname='ck_node_state'
          AND pg_get_constraintdef(oid) NOT LIKE '%invariant%'
    ) THEN
        ALTER TABLE exploration_nodes DROP CONSTRAINT ck_node_state;
        ALTER TABLE exploration_nodes ADD CONSTRAINT ck_node_state CHECK (
            (kind='begin'   AND state IN ('open')) OR
            (kind='intent'  AND state IN ('open','running','paused','done','blocked','exhausted','stopped','deleted')) OR
            (kind='goal'    AND state IN ('open','met','abandoned')) OR
            (kind='fact'    AND state IN ('confirmed','dismissed','origin')) OR
            (kind='finding' AND state IN ('confirmed','dismissed')) OR
            (kind='hint'    AND state IN ('active','consumed')) OR
            (kind='digest'  AND state IN ('active','superseded')) OR
            (kind='invariant' AND state IN ('proposed','confirmed','rejected'))
        );
    END IF;
END $$;`)
	_, _ = tx.Exec(`
DO $$
DECLARE cname text;
BEGIN
    SELECT conname INTO cname FROM pg_constraint
     WHERE conrelid='exploration_edges'::regclass AND contype='c'
       AND pg_get_constraintdef(oid) LIKE '%rel%'
       AND pg_get_constraintdef(oid) NOT LIKE '%protects%';
    IF cname IS NOT NULL THEN
        EXECUTE 'ALTER TABLE exploration_edges DROP CONSTRAINT '||quote_ident(cname);
        ALTER TABLE exploration_edges ADD CONSTRAINT ck_edge_rel
            CHECK (rel IN ('spawns','derived_from','yields','proves','covers','protects'));
    END IF;
END $$;`)
	return tx.Commit()
}

func scanVerification(row interface{ Scan(...any) error }) (*FindingVerification, error) {
	r := &FindingVerification{}
	err := row.Scan(&r.ID, &r.FindingID, &r.ConversationID, &r.Status, &r.Conclusion, &r.Severity,
		&r.Summary, &r.Evidence, &r.Attribution, &r.BlastRadius, &r.Error, &r.CreatedAt, &r.StartedAt, &r.FinishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return r, err
}

func (r *FindingVerification) InitialMessage() string {
	return fmt.Sprintf("请独立验证漏洞 #%d。先调用 get_finding_verify_context 读取证据与流量，完成最小复现与归因后调用 record_finding_verify_result 保存结论。无绑定 HTTP 流量时不得给出 verified。", r.FindingID)
}

// CreateFindingVerification starts an independent verifier conversation for findingID.
func (d *DB) CreateFindingVerification(ctx context.Context, findingID int64) (*FindingVerification, *Conversation, bool, error) {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, false, err
	}
	defer tx.Rollback()
	var title string
	var snapshot []byte
	var trafficCount int
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(NULLIF(f.name,''), NULLIF(f.vulnclass,''), '未分类'),
	jsonb_build_object(
	 'finding', to_jsonb(f),
	 'traffic_count', (SELECT count(*) FROM finding_traffic_bindings b WHERE b.finding_id=f.id),
	 'assets', COALESCE((SELECT jsonb_agg(to_jsonb(a)) FROM assets a WHERE f.asset_ids @> to_jsonb(ARRAY[a.id])), '[]'::jsonb)
	),
	(SELECT count(*) FROM finding_traffic_bindings b WHERE b.finding_id=f.id)
	FROM findings f WHERE f.id=$1 FOR UPDATE OF f`, findingID).Scan(&title, &snapshot, &trafficCount)
	if err != nil {
		return nil, nil, false, err
	}
	r, err := scanVerification(tx.QueryRowContext(ctx, `SELECT `+verifyCols+` FROM finding_verifications WHERE finding_id=$1 AND status IN ('pending','running')`, findingID))
	if err != nil {
		return nil, nil, false, err
	}
	if r != nil {
		return r, nil, false, nil
	}
	if runes := []rune(title); len(runes) > 100 {
		title = string(runes[:100])
	}
	c, err := scanConv(tx.QueryRowContext(ctx, `INSERT INTO conversations(agent_key,title) VALUES ($1,$2) RETURNING `+convCols,
		FindingVerifierAgentKey, fmt.Sprintf("验证 #%d · %s", findingID, title)))
	if err != nil {
		return nil, nil, false, err
	}
	r, err = scanVerification(tx.QueryRowContext(ctx, `INSERT INTO finding_verifications(finding_id,conversation_id,snapshot) VALUES ($1,$2,$3) RETURNING `+verifyCols,
		findingID, c.ID, snapshot))
	if err != nil {
		return nil, nil, false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE findings SET verification_status=$2, verifier_conversation_id=$3 WHERE id=$1`,
		findingID, VerificationVerifying, c.ID); err != nil {
		return nil, nil, false, err
	}
	msg := r.InitialMessage()
	_, err = tx.ExecContext(ctx, `INSERT INTO conversation_activities(conversation_id,worker,kind,summary,detail) VALUES ($1,$2,'user',$3,$4)`,
		c.ID, FindingVerifierAgentKey, fmt.Sprintf("请验证漏洞 #%d", findingID), msg)
	if err != nil {
		return nil, nil, false, err
	}
	if err = tx.Commit(); err != nil {
		return nil, nil, false, err
	}
	_ = trafficCount
	return r, &c, true, nil
}

func (d *DB) StartFindingVerification(ctx context.Context, id int64) (bool, error) {
	res, err := d.ExecContext(ctx, `UPDATE finding_verifications SET status='running', started_at=now() WHERE id=$1 AND status='pending'`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (d *DB) FindingVerificationForConversation(ctx context.Context, conversationID int64) (*FindingVerification, error) {
	r, err := scanVerification(d.QueryRowContext(ctx, `SELECT `+verifyCols+` FROM finding_verifications WHERE conversation_id=$1`, conversationID))
	if err != nil || r == nil {
		return r, err
	}
	err = d.QueryRowContext(ctx, `SELECT snapshot FROM finding_verifications WHERE id=$1`, r.ID).Scan(&r.Snapshot)
	return r, err
}

func (d *DB) ListFindingVerifications(findingID int64) ([]*FindingVerification, error) {
	rows, err := d.Query(`SELECT `+verifyCols+` FROM finding_verifications WHERE finding_id=$1 ORDER BY id DESC`, findingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*FindingVerification{}
	for rows.Next() {
		r, err := scanVerification(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountFindingTrafficBindings returns how many traffic rows are bound to the finding.
func (d *DB) CountFindingTrafficBindings(ctx context.Context, findingID int64) (int, error) {
	var n int
	err := d.QueryRowContext(ctx, `SELECT count(*) FROM finding_traffic_bindings WHERE finding_id=$1`, findingID).Scan(&n)
	return n, err
}

// RecordFindingVerifyResult stores the verifier conclusion. Traffic hard-gate:
// conclusion=verified with zero bound traffic is coerced to needs_human.
func (d *DB) RecordFindingVerifyResult(ctx context.Context, conversationID int64, conclusion, severity, summary, evidence, attribution, blastRadius string) error {
	if !ValidVerifyConclusion(conclusion) {
		return errors.New("conclusion 必须为 verified / false_positive / downgraded / needs_human")
	}
	summary, evidence = strings.TrimSpace(summary), strings.TrimSpace(evidence)
	if summary == "" || evidence == "" {
		return errors.New("summary 与 evidence 不能为空")
	}
	if !ValidBlastRadius(blastRadius) {
		return errors.New("blast_radius 必须为 single_record / any_record / cross_tenant 或空")
	}
	if conclusion == VerificationVerified {
		var findingID int64
		if err := d.QueryRowContext(ctx, `SELECT finding_id FROM finding_verifications WHERE conversation_id=$1 AND status='running'`, conversationID).Scan(&findingID); err != nil {
			return ErrVerifyNotRunning
		}
		n, err := d.CountFindingTrafficBindings(ctx, findingID)
		if err != nil {
			return err
		}
		if n == 0 {
			conclusion = VerificationNeedsHuman
			if attribution == "" {
				attribution = "HTTP 断言缺少绑定流量；按硬门禁降为 needs_human"
			} else {
				attribution += "；[traffic-gate] 无绑定流量，不得 verified"
			}
		}
	}
	res, err := d.ExecContext(ctx, `UPDATE finding_verifications SET conclusion=$2,severity=$3,summary=$4,evidence=$5,attribution=$6,blast_radius=$7
	WHERE conversation_id=$1 AND status='running' AND (conclusion='' OR (conclusion=$2 AND summary=$4 AND evidence=$5))`,
		conversationID, conclusion, severity, summary, evidence, attribution, blastRadius)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrVerifyNotRunning
	}
	return nil
}

// FinishFindingVerification seals the run and writes terminal verification_status on the finding.
func (d *DB) FinishFindingVerification(id int64, status, reason string) error {
	if status != "completed" && status != "failed" && status != "stopped" {
		return errors.New("invalid terminal verification status")
	}
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var findingID int64
	err = tx.QueryRow(`SELECT f.id FROM findings f WHERE f.id=(SELECT finding_id FROM finding_verifications WHERE id=$1) FOR UPDATE OF f`, id).Scan(&findingID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var finalStatus, conclusion, severity, summary, attribution, blast string
	err = tx.QueryRow(`UPDATE finding_verifications SET
	status=CASE WHEN $2='completed' AND conclusion='' THEN 'failed' ELSE $2 END,
	error=CASE WHEN $2='completed' AND conclusion='' THEN 'Agent 未保存验证结论' ELSE $3 END,
	finished_at=now() WHERE id=$1 AND status IN ('pending','running')
	RETURNING status,conclusion,severity,summary,attribution,blast_radius`, id, status, reason).
		Scan(&finalStatus, &conclusion, &severity, &summary, &attribution, &blast)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	vStatus := VerificationNeedsHuman
	if finalStatus == "completed" && ValidVerifyConclusion(conclusion) {
		vStatus = conclusion
	} else if finalStatus != "completed" {
		vStatus = VerificationNeedsHuman
	}
	reasonText := summary
	if attribution != "" {
		reasonText = attribution
	}
	if reason != "" && finalStatus != "completed" {
		reasonText = reason
	}
	if _, err = tx.Exec(`UPDATE findings SET verification_status=$2, verification_severity=$3, verification_reason=$4, blast_radius=CASE WHEN $5<>'' THEN $5 ELSE blast_radius END WHERE id=$1`,
		findingID, vStatus, severity, reasonText, blast); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) RecoverFindingVerifications() error {
	_, err := d.Exec(`UPDATE finding_verifications SET status='stopped', error='服务重启，验证已中断', finished_at=now() WHERE status IN ('pending','running')`)
	if err != nil {
		return err
	}
	_, err = d.Exec(`UPDATE findings SET verification_status=$1, verification_reason='服务重启，验证中断' WHERE verification_status=$2`,
		VerificationNeedsHuman, VerificationVerifying)
	return err
}

// ApplyTrafficGate maps a desired conclusion through the hard gate.
// Pure function for unit tests.
func ApplyTrafficGate(conclusion string, trafficCount int) string {
	if conclusion == VerificationVerified && trafficCount <= 0 {
		return VerificationNeedsHuman
	}
	return conclusion
}

// CanTransitionVerification rejects illegal verification_status transitions.
func CanTransitionVerification(from, to string) bool {
	if !ValidVerificationStatus(from) || !ValidVerificationStatus(to) {
		return false
	}
	switch from {
	case VerificationUnverified:
		return to == VerificationVerifying || to == VerificationNeedsHuman
	case VerificationVerifying:
		return to == VerificationVerified || to == VerificationFalsePositive ||
			to == VerificationDowngraded || to == VerificationNeedsHuman
	case VerificationVerified, VerificationFalsePositive, VerificationDowngraded, VerificationNeedsHuman:
		// Terminal: only re-queue to verifying allowed.
		return to == VerificationVerifying
	}
	return false
}
