// Package db is the PostgreSQL data source for CAUTIOUSMEMORY (replacing the old single-file graph SQLite store).
// It opens connections, applies the schema, and seeds built-in agents and the variable catalog.
package db

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Autumn-27/cautiousmemory/config"
	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib" // pgx database/sql driver ("pgx")
)

//go:embed schema.sql
var schemaSQL string

const schemaMigrationLockKey int64 = 7337741001

// maxOpenConns is the connection-pool limit; see Open below. It is far below PostgreSQL's default
// max_connections=100, yet well above the application's nested connection depth (during startup the schema
// advisory lock holds one connection while seedBuiltins obtains another), avoiding self-deadlock.
const maxOpenConns = 32

var schemaDeadlockRetryDelays = [...]time.Duration{
	100 * time.Millisecond,
	250 * time.Millisecond,
	500 * time.Millisecond,
	time.Second,
}

type schemaExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func isPostgresDeadlock(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "40P01"
}

func applySchemaWithRetry(ctx context.Context, execer schemaExecer, sleep func(time.Duration)) error {
	for attempt := 0; ; attempt++ {
		if _, err := execer.ExecContext(ctx, schemaSQL); err != nil {
			if !isPostgresDeadlock(err) || attempt >= len(schemaDeadlockRetryDelays) {
				return err
			}
			sleep(schemaDeadlockRetryDelays[attempt])
			continue
		}
		return nil
	}
}

// withSchemaMigrationLock pins the session-level lock to one checked-out
// connection. Running pg_advisory_lock through *sql.DB is incorrect because a
// later schema or unlock call may use a different pooled PostgreSQL session.
func withSchemaMigrationLock(ctx context.Context, sqlDB *sql.DB, action func(*sql.Conn) error) (err error) {
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, schemaMigrationLockKey); err != nil {
		return fmt.Errorf("advisory lock: %w", err)
	}
	defer func() {
		if _, unlockErr := conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, schemaMigrationLockKey); unlockErr != nil && err == nil {
			err = fmt.Errorf("advisory unlock: %w", unlockErr)
		}
	}()
	return action(conn)
}

// coordinateWithSchemaMigration makes long, multi-table archive transactions
// mutually exclusive with startup DDL while allowing ordinary runtime queries
// to continue normally.
func coordinateWithSchemaMigration(tx *sql.Tx) error {
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock($1)`, schemaMigrationLockKey); err != nil {
		return fmt.Errorf("coordinate with schema migration: %w", err)
	}
	return nil
}

// DSN resolves the PostgreSQL connection string and reports where it came from.
// Precedence: env CAUTIOUSMEMORY_PG_DSN > config file (config.json). There is no
// built-in default â€” it errors if neither source is configured.
func DSN() (dsn, source string, err error) {
	return config.PostgresDSN()
}

// DB wraps the shared *sql.DB. PG handles its own connection pool + concurrency
// (MVCC), so unlike the old SQLite store there is no process-wide write mutex.
type DB struct{ *sql.DB }

// ensureDatabase connects to the postgres system database and creates the target
// database if it does not exist. dsn must be a postgres:// URL.
func ensureDatabase(dsn string) error {
	u, err := url.Parse(dsn)
	if err != nil {
		return nil // unparseable DSN â€” let the normal Open fail with a clear error
	}
	dbName := strings.TrimPrefix(u.Path, "/")
	if dbName == "" || dbName == "postgres" {
		return nil
	}
	// connect to the postgres maintenance database instead
	adminDSN := *u
	adminDSN.Path = "/postgres"
	admin, err := sql.Open("pgx", adminDSN.String())
	if err != nil {
		return nil // best-effort; let Open surface the real error
	}
	defer admin.Close()
	if err := admin.Ping(); err != nil {
		return nil
	}
	var exists bool
	_ = admin.QueryRow(`SELECT true FROM pg_database WHERE datname=$1`, dbName).Scan(&exists)
	if !exists {
		if _, err := admin.Exec(`CREATE DATABASE "` + dbName + `"`); err != nil {
			return fmt.Errorf("create database %q: %w", dbName, err)
		}
	}
	return nil
}

// Open connects, applies the schema (idempotent), and seeds builtin rows.
func Open(dsn string) (*DB, error) {
	if err := ensureDatabase(dsn); err != nil {
		return nil, err
	}
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	// database/sql does not limit connections by default: when the pool has no idle connections it creates new ones until
	// PostgreSQL max_connections (default 100) is reached and peak-time queries receive
	// errors such as `FATAL: sorry, too many clients already`. With a cap, excess queries instead
	// queue for an idle connectionâ€”under the same load they become slower rather than failing, and callers need not distinguish
	// â€œunreadableâ€ from â€œmissing.â€ maxOpenConns must leave headroom for psql / reset-password.sh and
	// other instances that may run concurrently; lower this value too if max_connections has been reduced.
	sqlDB.SetMaxOpenConns(maxOpenConns)
	sqlDB.SetMaxIdleConns(maxOpenConns)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)
	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("ping postgres (%s): %w", config.Redact(dsn), err)
	}
	d := &DB{sqlDB}
	// pgx runs multi-statement Exec via the simple protocol when there are no args.
	// Keep the dedicated lock connection checked out until both DDL and seeding
	// finish so concurrent application instances cannot initialize out of order.
	err = withSchemaMigrationLock(context.Background(), sqlDB, func(conn *sql.Conn) error {
		if err := applySchemaWithRetry(context.Background(), conn, time.Sleep); err != nil {
			return fmt.Errorf("apply schema: %w", err)
		}
		if err := d.seedBuiltins(); err != nil {
			return fmt.Errorf("seed builtins: %w", err)
		}
		return nil
	})
	if err != nil {
		sqlDB.Close()
		return nil, err
	}
	return d, nil
}

// builtinAgent describes one of the fixed agents and its prompt-variable catalog.
type builtinAgent struct {
	key, name, role, desc string
	vars                  []promptVar
	interactiveShell      bool // Default interactive-shell setting on row creation; ON CONFLICT does not override later user changes
	runSeconds            *int // Per-run wall-clock limit (seconds) at row creation; nil = seed default (1200), 0 = unlimited
}

type promptVar struct{ name, desc, example, source string }

// intp returns a pointer to v, allowing explicit values for optional builtinAgent fields (such as runSeconds).
func intp(v int) *int { return &v }

// builtinAgents mirrors docs Â§5(a). Built-in tools are not stored in the database; only agents and the variable catalog are seeded here.
// Note: planner/worker/mainagent/auto interactive-shell defaults are set by the interactive_shell_default_v1 block below
// (preserving subsequent toggles); interactiveShell here is only for new agents that should have it enabled at row creation.
var builtinAgents = []builtinAgent{
	{"goals", "ç›®æ ‡æ‹†è§£", "goals", "æŠŠæ¸—é€ä»»åŠ¡ç›®æ ‡æ‹†è§£æˆè‹¥å¹²ç‹¬ç«‹ã€å¯éªŒè¯çš„å­ç›®æ ‡ã€‚", []promptVar{
		{"EngagementDescription", "ä»»åŠ¡æè¿°ï¼ˆæµ‹è¯•å¯¹è±¡/èƒŒæ™¯ï¼‰", "æµ‹è¯• example.com ç«™ç‚¹", "exploration"},
		// Now is a global runtime variable (see server.globalPromptVars), no longer defined in each agent catalog
		// because withGlobalVars would append a duplicate name and conflict with the global entry.
	}, false, nil},
	{"planner", "è§„åˆ’", "planner", "è¯»å–æ€åŠ¿ã€åˆ¤å®šç›®æ ‡ï¼Œåªåœ¨ç¡®æœ‰æœªè¦†ç›–çš„æ–°æ–¹å‘æ—¶è¡¥å……æŽ¢ç´¢æ„å›¾ï¼ˆæ¯ä»»åŠ¡ä¸€ä¸ªè§„åˆ’å¾ªçŽ¯ï¼‰ã€‚", []promptVar{
		{"Goal", "ä»»åŠ¡æ€»ç›®æ ‡", "æ‹¿ä¸‹ example.com çš„ç®¡ç†å‘˜æƒé™", "exploration"},
		{"AssetSummary", "èµ„äº§è®¡æ•°/ç±»åž‹åˆ†å¸ƒæ‘˜è¦(å¯é€‰)", "domain:3 ip:5 site:2", "distilled"},
	}, false, nil},
	{"mainagent", "ä¸»", "main", "äººæœºæŽ¥å£ï¼šè§‚å¯Ÿè¿›å±•ï¼ŒæŠŠäººçš„æ„å›¾è½æˆ hint æˆ–é«˜ä¼˜å…ˆçº§æ„å›¾ã€‚", []promptVar{
		{"Goal", "å½“å‰ä»»åŠ¡ç›®æ ‡", "æ‹¿ä¸‹ example.com çš„ç®¡ç†å‘˜æƒé™", "exploration"},
		{"AssetSummary", "å¼€å±€æ€åŠ¿æ‘˜è¦(å¯é€‰)", "domain:3 ip:5", "distilled"},
		{"FindingsSummary", "å·²ç¡®è®¤æ¼æ´žæ‘˜è¦(å¯é€‰)", "high:1 medium:2", "distilled"},
	}, false, nil},
	{"worker", "æ‰§è¡Œ", "worker", "é¢†å–ä¸€æ¡æ„å›¾æ‰§è¡Œï¼ŒæŠŠå‘çŽ°çš„äº‹å®ž/æ¼æ´žå†™å›žçŸ¥è¯†å›¾è°±åŽåœæ­¢ã€‚", []promptVar{
		{"ProxyAddr", "è®°å½•ä»£ç†åœ°å€(é©±åŠ¨ if åŒæ–‡æ¡ˆ)", "127.0.0.1:8080", "runtime"},
		{"WorkerName", "worker è‡ªæˆ‘æ ‡è¯†(å¯é€‰)", "worker-1", "runtime"},
	}, false, nil},
	// Auto: built-in platform-operations agent. It does not participate in the penetration-testing orchestration loop; it is driven from the conversation page and operates the platform through tools.
	{"auto", "Auto", "assistant", "å¹³å°æ“ä½œåŠ©æ‰‹ï¼šç”¨å·¥å…·ç®¡ç†ä»»åŠ¡(å»º/çœ‹/æš‚åœ/ç»™æç¤º)ä¸Žèµ„äº§ï¼Œå¹¶å¯åˆ›å»º/ä¿®æ”¹ skillã€è‡ªå®šä¹‰å·¥å…·ã€MCPã€‚", nil, false, nil},
	// Asset mapper: read-only assistant used by the topology canvas and asset views.
	{"asset_mapper", "èµ„äº§æ¢³ç†", "assistant", "èµ„äº§æ¢³ç†åŠ©æ‰‹ï¼šæ±‡æ€»å…¬ç½‘ã€è¾¹ç•Œä¸Žå†…ç½‘èµ„äº§å…³ç³»ï¼ŒåŸºäºŽçœŸå®žèµ„äº§æ•°æ®è§£é‡Šå…¥ç«™è·¯å¾„ï¼Œä¸å‡­ç©ºæŽ¨æ–­è¿žæŽ¥ã€‚", nil, false, nil},
	// Penetration testing: built-in standalone pentest agent, driven from the conversation page. It handles the full chain from reconnaissance to wrap-up, planning, executing, and validating itself. Interactive shell is enabled by default.
	{"pentest", "æ¸—é€æµ‹è¯•", "assistant", "ç‹¬ç«‹æ¸—é€ agentï¼šä¸€äººä»Žä¾¦å¯Ÿâ†’æ‰¾æ”»å‡»é¢â†’æ·±å…¥åˆ©ç”¨â†’éªŒè¯â†’æ”¶å°¾èµ°å®Œæ•´æ¡é“¾ï¼Œè‡ªå·±è§„åˆ’ã€è‡ªå·±æ‰§è¡Œã€è‡ªå·±å¯¹æŠ—å¼éªŒè¯ã€‚", nil, true, intp(0)},
}

// seedBuiltins inserts the fixed built-in agents and their variable catalog (idempotent).
func (d *DB) seedBuiltins() error {
	for _, a := range builtinAgents {
		var agentID int64
		err := d.QueryRow(`
INSERT INTO agents(key, name, description, role, builtin, enabled, interactive_shell, run_seconds)
VALUES ($1, $2, NULLIF($3,''), $4, true, true, $5, COALESCE($6, 1200))
ON CONFLICT (key) DO UPDATE SET name = EXCLUDED.name, description = EXCLUDED.description
RETURNING id`, a.key, a.name, a.desc, a.role, a.interactiveShell, a.runSeconds).Scan(&agentID)
		if err != nil {
			return fmt.Errorf("agent %s: %w", a.key, err)
		}
		for _, v := range a.vars {
			if _, err := d.Exec(`
INSERT INTO agent_prompt_vars(agent_id, var_name, description, example, source)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (agent_id, var_name) DO UPDATE
  SET description = EXCLUDED.description, example = EXCLUDED.example, source = EXCLUDED.source`,
				agentID, v.name, v.desc, v.example, v.source); err != nil {
				return fmt.Errorf("agent %s var %s: %w", a.key, v.name, err)
			}
		}
	}
	// Drop catalog entries for variables that were renamed, so the white-list no
	// longer advertises a name templates can't resolve (EngagementTitleâ†’Description).
	// After 'Now' became a global runtime variable rather than a per-agent entry, older databases could retain a 'Now' entry under goals
	// that duplicates the global key in the frontend variable list; remove it as well.
	if _, err := d.Exec(`DELETE FROM agent_prompt_vars WHERE var_name IN ('EngagementTitle', 'CoverageGaps', 'Now')`); err != nil {
		return fmt.Errorf("cleanup renamed vars: %w", err)
	}
	// Default-on interactive_shell for the runtime agents (planner/worker/mainagent/auto)
	// ONCE â€” respects a later user toggle-off (guarded by a settings flag). goals(one-shot
	// decomposer) stays off. Runs after the column exists (schema applied before seed).
	if v, _, _ := d.GetSetting("interactive_shell_default_v1"); v != "true" {
		if _, err := d.Exec(`UPDATE agents SET interactive_shell=true WHERE key IN ('planner','worker','mainagent','auto')`); err != nil {
			return fmt.Errorf("seed interactive_shell defaults: %w", err)
		}
		_ = d.SetSetting("interactive_shell_default_v1", "true")
	}
	// Seed the built-in browser (Playwright) MCP once â€” DISABLED by default (users
	// enable it when needed); no proxy by default. The traffic-capture toggle injects/strips
	// the recording proxy + CA at runtime (server.Manager.syncBrowserMCPProxy).
	// Insert only if absent so we never clobber user edits (args/env/enabled/
	// visibility) on restart.
	if _, err := d.Exec(`
INSERT INTO mcp_servers(name, transport, command, args, env, enabled)
VALUES ('browser', 'stdio', 'npx', $1, '{}', false)
ON CONFLICT (name) DO NOTHING`,
		`["@playwright/mcp","--headless"]`); err != nil {
		return fmt.Errorf("seed browser mcp: %w", err)
	}
	// NOTE: the placeholder ScopeSentry data-source MCP (empty URL + empty X-API-Key,
	// disabled) is seeded directly in schema.sql Â§F so a raw `psql < schema.sql` init
	// also gets it. schema.sql is Exec'd on every startup, so it stays idempotent.
	if err := d.seedBuiltinSkillVisibility(); err != nil {
		return fmt.Errorf("seed skill visibility: %w", err)
	}
	if err := d.seedDefaultInterceptRules(); err != nil {
		return fmt.Errorf("seed intercept rules: %w", err)
	}
	if err := d.seedDefaultInterceptRulesV2(); err != nil {
		return fmt.Errorf("seed intercept rules v2: %w", err)
	}
	if err := d.seedDefaultInterceptRulesV3(); err != nil {
		return fmt.Errorf("seed intercept rules v3: %w", err)
	}
	if err := d.seedDefaultAssetInterceptRules(); err != nil {
		return fmt.Errorf("seed asset intercept rules: %w", err)
	}
	return nil
}

// seedDefaultAssetInterceptRules inserts the built-in asset blocklist (fuzzy
// domain matches for government / education sites) once on first startup. Gated
// by a settings flag so a user's later disable/delete is never resurrected on
// restart â€” same policy as the intercept-rule seed.
func (d *DB) seedDefaultAssetInterceptRules() error {
	if v, _, _ := d.GetSetting("asset_intercept_default_rules_v1"); v == "done" {
		return nil
	}
	rules := []struct {
		kind    string
		pattern string
		note    string
	}{
		{"fuzzy_domain", ".gov", "[å†…ç½®] æ”¿åºœç½‘ç«™ (.gov)"},
		{"fuzzy_domain", ".gov.cn", "[å†…ç½®] æ”¿åºœç½‘ç«™ (.gov.cn)"},
		{"fuzzy_domain", ".edu", "[å†…ç½®] æ•™è‚²ç½‘ç«™ (.edu)"},
		{"fuzzy_domain", ".edu.cn", "[å†…ç½®] æ•™è‚²ç½‘ç«™ (.edu.cn)"},
	}
	for _, r := range rules {
		if _, err := d.Exec(`
INSERT INTO asset_intercept_rules(enabled, kind, pattern, note, builtin)
VALUES (true, $1, $2, $3, true)
ON CONFLICT DO NOTHING`, r.kind, r.pattern, r.note); err != nil {
			return fmt.Errorf("asset rule %q: %w", r.pattern, err)
		}
	}
	return d.SetSetting("asset_intercept_default_rules_v1", "done")
}

// builtinSkillVisibility maps a shipped skill's directory name â†’ the built-in
// agent keys that should see it by default. The skill FILES themselves live on the
// filesystem (SkillDir, loaded by norma at runtime); DB only carries this visibility
// binding. Skills omitted here (e.g. playwright-cli, scopesentry) ship invisible by
// default â€” the user turns them on per-agent when needed. scopesentry additionally
// declares `mcps: ScopeSentry`, which only takes effect once it's made visible and
// that MCP is enabled/configured.
var builtinSkillVisibility = map[string][]string{
	"api-recon": {"auto", "pentest", "worker"},
}

// seedBuiltinSkillVisibility binds the shipped built-in skills to their default
// agents. Insert-if-absent (ON CONFLICT DO NOTHING) so a user's later toggle-off is
// never resurrected on restart â€” matches the browser-MCP / intercept-rule seed policy.
func (d *DB) seedBuiltinSkillVisibility() error {
	for skillName, agentKeys := range builtinSkillVisibility {
		for _, key := range agentKeys {
			if _, err := d.Exec(`
INSERT INTO agent_skill_visibility(agent_id, skill_name, enabled)
SELECT id, $2, true FROM agents WHERE key=$1
ON CONFLICT (agent_id, skill_name) DO NOTHING`, key, skillName); err != nil {
				return fmt.Errorf("skill %s â†’ agent %s: %w", skillName, key, err)
			}
		}
	}
	return nil
}

// seedDefaultInterceptRules inserts built-in safety intercept rules once on
// first startup. The seed is gated by a settings flag so user edits (disable,
// delete, re-order) are never overwritten on subsequent restarts.
func (d *DB) seedDefaultInterceptRules() error {
	if v, _, _ := d.GetSetting("intercept_default_rules_v1"); v == "done" {
		return nil
	}
	type rule struct {
		name     string
		target   string // tool_name | tool_input
		typ      string // string | regex
		pattern  string
		action   string
		message  string
		priority int
	}
	rules := []rule{
		// â”€â”€ Destructive system commands (priority 100) â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
		{
			name:     "[å†…ç½®] é€’å½’å¼ºåˆ¶åˆ é™¤ rm -rf",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\brm\b.{0,80}(?:-[a-z]*r[a-z]*f[a-z]*|-[a-z]*f[a-z]*r[a-z]*|--recursive|--no-preserve-root)`,
			action:   "deny",
			message:  "ç¦æ­¢æ‰§è¡Œé€’å½’å¼ºåˆ¶åˆ é™¤ï¼ˆrm -rf / rm --recursiveï¼‰ï¼Œå¯èƒ½æ°¸ä¹…æŸåç³»ç»Ÿæˆ–é¶æœºçŽ¯å¢ƒ",
			priority: 100,
		},
		{
			name:     "[å†…ç½®] åˆ é™¤ç³»ç»Ÿå…³é”®ç›®å½•",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `\brm\b[^"'\n]{0,60}["'\s](/|/etc|/bin|/usr|/boot|/var|/lib|/sys|/proc|/dev|/sbin|/root)`,
			action:   "deny",
			message:  "ç¦æ­¢åˆ é™¤ç³»ç»Ÿå…³é”®è·¯å¾„",
			priority: 100,
		},
		{
			name:     "[å†…ç½®] ç£ç›˜æ ¼å¼åŒ– mkfs",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `\bmkfs\b`,
			action:   "deny",
			message:  "ç¦æ­¢æ ¼å¼åŒ–ç£ç›˜ï¼ˆmkfsï¼‰",
			priority: 100,
		},
		{
			name:     "[å†…ç½®] è¦†å†™ç£ç›˜è®¾å¤‡ dd",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `\bdd\b[^|\n]{0,100}\bof=\s*/dev/[a-zA-Z]`,
			action:   "deny",
			message:  "ç¦æ­¢ä½¿ç”¨ dd è¦†å†™ç£ç›˜è®¾å¤‡",
			priority: 100,
		},
		{
			name:     "[å†…ç½®] Fork ç‚¸å¼¹",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `:\(\)\s*\{[^}]*:\|:`,
			action:   "deny",
			message:  "ç¦æ­¢æ‰§è¡Œ Fork ç‚¸å¼¹",
			priority: 100,
		},
		{
			name:     "[å†…ç½®] å…³æœº / é‡å¯",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `\b(?:shutdown|reboot|halt|poweroff|init\s+[06])\b`,
			action:   "deny",
			message:  "ç¦æ­¢æ‰§è¡Œå…³æœºæˆ–é‡å¯å‘½ä»¤",
			priority: 100,
		},
		{
			name:     "[å†…ç½®] æ€æ­»å…¨éƒ¨è¿›ç¨‹",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `\bkill\s+-9\s+-1\b|\bkillall\s+-9\b`,
			action:   "deny",
			message:  "ç¦æ­¢ kill -9 -1 æˆ– killall -9ï¼ˆæ€æ­»æ‰€æœ‰è¿›ç¨‹ï¼‰",
			priority: 100,
		},
		{
			name:     "[å†…ç½®] ç£ç›˜æ“¦é™¤ shred / wipe",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `\b(?:shred|wipe)\b[^|\n]{0,80}/dev/[a-zA-Z]`,
			action:   "deny",
			message:  "ç¦æ­¢å¯¹ç£ç›˜è®¾å¤‡æ‰§è¡Œ shred/wipe æ“¦é™¤",
			priority: 100,
		},
		{
			name:     "[å†…ç½®] æ¸…ç©ºé˜²ç«å¢™è§„åˆ™",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `\biptables\s+(?:-F|--flush)\b|\bnft\s+flush\s+ruleset\b`,
			action:   "deny",
			message:  "ç¦æ­¢æ¸…ç©ºé˜²ç«å¢™è§„åˆ™ï¼ˆiptables -F / nft flushï¼‰",
			priority: 100,
		},
		// â”€â”€ Destructive database operations (priority 90) â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
		{
			name:     "[å†…ç½®] SQL DROP DATABASE / TABLE / SCHEMA",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\bDROP\s+(?:DATABASE|TABLE|SCHEMA|INDEX|VIEW|TABLESPACE|USER|ROLE)\b`,
			action:   "deny",
			message:  "ç¦æ­¢æ‰§è¡Œ DROP æ“ä½œï¼Œå¯èƒ½ä¸å¯é€†åœ°é”€æ¯æ•°æ®åº“å¯¹è±¡",
			priority: 90,
		},
		{
			name:     "[å†…ç½®] SQL TRUNCATE",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\bTRUNCATE\s+(?:TABLE\s+)?\w`,
			action:   "deny",
			message:  "ç¦æ­¢æ‰§è¡Œ TRUNCATEï¼Œå¯èƒ½æ¸…ç©ºæ•°æ®è¡¨æ‰€æœ‰æ•°æ®",
			priority: 90,
		},
		{
			name:     "[å†…ç½®] MongoDB drop / dropDatabase",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\.(?:dropDatabase|dropCollection|drop)\s*\(`,
			action:   "deny",
			message:  "ç¦æ­¢æ‰§è¡Œ MongoDB drop æ“ä½œ",
			priority: 90,
		},
		{
			name:     "[å†…ç½®] Redis FLUSHALL / FLUSHDB",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\b(?:FLUSHALL|FLUSHDB)\b`,
			action:   "deny",
			message:  "ç¦æ­¢æ‰§è¡Œ Redis FLUSHALL / FLUSHDBï¼Œå¯èƒ½æ¸…ç©ºå…¨éƒ¨ç¼“å­˜æ•°æ®",
			priority: 90,
		},
		// â”€â”€ Destructive HTTP requests (priority 80) â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€
		// Three common ways an agent may send a DELETE request:
		//   1. curl -X DELETE / --request DELETE (executed directly by the Bash tool or written to a script)
		//   2. The .delete() method of a Python HTTP client
		//   3. method: 'DELETE' / method="DELETE" in JavaScript or generic scripts
		{
			name:     "[å†…ç½®] curl / wget å‘é€ DELETE è¯·æ±‚",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\bcurl\b[^|\n&;"]{0,300}(?:-X\s*DELETE|--request\s+DELETE|-XDELETE)|\bwget\b[^|\n&;"]{0,300}--method[=\s]+DELETE`,
			action:   "deny",
			message:  "ç¦æ­¢é€šè¿‡ curl/wget å‘é€ HTTP DELETE è¯·æ±‚ï¼Œå¯èƒ½åˆ é™¤ç›®æ ‡ç³»ç»Ÿæ•°æ®",
			priority: 80,
		},
		{
			name:     "[å†…ç½®] Python HTTP å®¢æˆ·ç«¯ DELETEï¼ˆrequests/httpx/aiohttpï¼‰",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\b(?:requests|httpx|aiohttp|urllib\.request)\.delete\s*\(|session\.delete\s*\(|client\.delete\s*\(`,
			action:   "deny",
			message:  "ç¦æ­¢ä½¿ç”¨ Python HTTP å®¢æˆ·ç«¯å‘é€ DELETE è¯·æ±‚",
			priority: 80,
		},
		{
			name:     "[å†…ç½®] è„šæœ¬ä¸­å£°æ˜Ž HTTP DELETE æ–¹æ³•ï¼ˆJS/é€šç”¨ï¼‰",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)axios\.delete\s*\(|method\s*[:=]\s*['"]DELETE['"]`,
			action:   "deny",
			message:  "ç¦æ­¢åœ¨è„šæœ¬ä¸­å£°æ˜Žå¹¶å‘é€ HTTP DELETE è¯·æ±‚",
			priority: 80,
		},
		{
			name:     "[å†…ç½®] æ‰¹é‡æ¸…ç©º / æ¸…é™¤æŽ¥å£è·¯å¾„",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)/(?:clear|wipe|flush|purge|truncate|drop|destroy|factory[-_]reset|reset[-_]all)(?:[/?#"'\s]|$)`,
			action:   "deny",
			message:  "ç¦æ­¢è°ƒç”¨æ‰¹é‡æ¸…ç©ºæˆ–é”€æ¯ç±»æŽ¥å£ï¼ˆ/clear /wipe /flush /purge ç­‰ï¼‰",
			priority: 80,
		},
	}
	for _, r := range rules {
		if _, err := d.Exec(`
INSERT INTO intercept_rules(name, enabled, priority, match_target, match_type, pattern, action, message, timeout_enabled, timeout_seconds, timeout_action)
VALUES ($1, true, $2, $3, $4, $5, $6, $7, false, 60, 'deny')
ON CONFLICT DO NOTHING`,
			r.name, r.priority, r.target, r.typ, r.pattern, r.action, r.message,
		); err != nil {
			return fmt.Errorf("rule %q: %w", r.name, err)
		}
	}
	return d.SetSetting("intercept_default_rules_v1", "done")
}

// seedDefaultInterceptRulesV2 migrates the two safety patterns that used to be
// hard-coded in guard.go (destructive shell + data-exfil pipe) into ordinary
// intercept rules. Gated by its own flag so it also lands on DBs that already ran
// v1. Unlike the old guard.go floor, these are plain [å†…ç½®] rules â€” the user can
// disable or delete them. The exfil rule ships DISABLED by default (its
// curl/wget/nc pipe pattern mis-fires on legitimate CTF/pentest reverse-shell and
// data-transfer pipes); enable it manually when exfil gating is actually wanted.
func (d *DB) seedDefaultInterceptRulesV2() error {
	if v, _, _ := d.GetSetting("intercept_default_rules_v2"); v == "done" {
		return nil
	}
	rules := []struct {
		name     string
		pattern  string
		action   string
		message  string
		enabled  bool
		priority int
	}{
		{
			name:     "[å†…ç½®] ç ´åæ€§ç³»ç»Ÿå‘½ä»¤",
			pattern:  `(?i)\b(rm\s+-rf\s+/|mkfs|dd\s+if=|:\(\)\s*\{|shutdown|reboot|>\s*/dev/sd)`,
			action:   "deny",
			message:  "ç ´åæ€§å‘½ä»¤è¢«æ‹’ç»ï¼ˆrm -rf / / mkfs / dd / fork bomb / å…³æœºé‡å¯ / è¦†å†™ç£ç›˜è®¾å¤‡ï¼‰",
			enabled:  true,
			priority: 100,
		},
		{
			name:     "[å†…ç½®] æ•°æ®å¤–æ³„ç®¡é“",
			pattern:  `(?i)(curl|wget|nc|ncat)\b[^|]*\b(\|\s*(curl|wget|nc))`,
			action:   "deny",
			message:  "ç–‘ä¼¼æ•°æ®å¤–æ³„ç®¡é“è¢«æ‹’ç»ï¼ˆå‘½ä»¤è¾“å‡ºç» curl/wget/nc å¤–ä¼ ï¼‰",
			enabled:  false,
			priority: 80,
		},
	}
	for _, r := range rules {
		if _, err := d.Exec(`
INSERT INTO intercept_rules(name, enabled, priority, match_target, match_type, pattern, action, message, timeout_enabled, timeout_seconds, timeout_action)
VALUES ($1, $2, $3, 'tool_input', 'regex', $4, $5, $6, false, 60, 'deny')
ON CONFLICT DO NOTHING`,
			r.name, r.enabled, r.priority, r.pattern, r.action, r.message,
		); err != nil {
			return fmt.Errorf("rule %q: %w", r.name, err)
		}
	}
	return d.SetSetting("intercept_default_rules_v2", "done")
}

// seedDefaultInterceptRulesV3 adds the delete-endpoint path rule. The v1 HTTP rules
// only catch the DELETE *method* (curl -X DELETE, requests.delete(, method:'DELETE'),
// and v1's path rule covers only /clear /wipe /flush /purge /truncate /drop /destroy
// /factory-reset /reset-all â€” so a plain `curl 'http://t/api/user/delete?id=1'` (a
// delete endpoint reached with GET/POST, which is how most web apps expose deletion)
// slipped through every built-in rule. Own flag so it also lands on DBs that already
// ran v1/v2, where editing the v1 seed would have no effect.
//
// The pattern deliberately requires a separator after the verb so /delivery,
// /details, /delta and /delegate do not match, while /deleteAll, /delete_user and
// /delete-user do. destroy is re-covered here because v1's rule does not allow a
// suffix (/destroyAll was missed).
//
// Exported as a package const only so the seeded regex is unit-testable without a DB.
const deleteEndpointPathPattern = `(?i)/(?:(?:delete|remove|unlink|erase|destroy)[-\w]*|del)(?:[/?#"'\s]|$)`

func (d *DB) seedDefaultInterceptRulesV3() error {
	if v, _, _ := d.GetSetting("intercept_default_rules_v3"); v == "done" {
		return nil
	}
	const name = "[å†…ç½®] åˆ é™¤ç±»æŽ¥å£è·¯å¾„"
	if _, err := d.Exec(`
INSERT INTO intercept_rules(name, enabled, priority, match_target, match_type, pattern, action, message, timeout_enabled, timeout_seconds, timeout_action)
SELECT $1, true, 80, 'tool_input', 'regex', $2, 'deny', $3, false, 60, 'deny'
WHERE NOT EXISTS (SELECT 1 FROM intercept_rules WHERE name = $1)`,
		name,
		deleteEndpointPathPattern,
		"ç¦æ­¢è°ƒç”¨åˆ é™¤ç±»æŽ¥å£ï¼ˆ/delete /remove /unlink /erase ç­‰ï¼‰ï¼Œä¸è®ºä½¿ç”¨å“ªç§ HTTP æ–¹æ³•â€”â€”å¤šæ•°åº”ç”¨çš„åˆ é™¤æŽ¥å£ç”¨ GET/POST å°±èƒ½è§¦å‘ï¼ŒåŒæ ·ä¼šçœŸå®žåˆ é™¤ç›®æ ‡æ•°æ®",
	); err != nil {
		return fmt.Errorf("rule %q: %w", name, err)
	}
	return d.SetSetting("intercept_default_rules_v3", "done")
}

