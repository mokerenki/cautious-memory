package db

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Autumn-27/cautiousmemory/notify"
)

// æœ¬æ–‡ä»¶çš„ç”¨ä¾‹éƒ½ä¼šçœŸè¿ž PostgreSQLï¼ˆæ— åº“æ—¶è·³è¿‡ï¼‰ã€‚è¿™äº› SQL ç”¨åˆ°äº†
// FOR UPDATE SKIP LOCKEDã€make_intervalã€JSONBã€å¤šè¡Œ IN(...) å ä½ç¬¦æ‹¼æŽ¥ï¼Œ
// éƒ½æ˜¯ã€Œç¼–è¯‘é€šè¿‡ä½†å¯èƒ½è¿è¡Œæ—¶æŠ¥é”™ã€çš„å†™æ³•ï¼Œå¿…é¡»å®žè·‘æ‰ç®—éªŒè¯è¿‡ã€‚

func notifyTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) â€” skipping", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// newTestChannel å»ºä¸€ä¸ªæ¸ é“ï¼Œæµ‹è¯•ç»“æŸè‡ªåŠ¨åˆ é™¤ã€‚
func newTestChannel(t *testing.T, d *DB, kind, mode string, filter string) *NotificationChannel {
	t.Helper()
	if filter == "" {
		filter = `{}`
	}
	ch := &NotificationChannel{
		Name:       "æµ‹è¯•æ¸ é“-" + t.Name(),
		Kind:       kind,
		Mode:       mode,
		Config:     json.RawMessage(`{"webhook":"https://example.com/hook"}`),
		Filter:     json.RawMessage(filter),
		RatePerMin: 100,
	}
	id, err := d.SaveNotificationChannel(context.Background(), ch)
	if err != nil {
		t.Fatalf("å»ºæ¸ é“å¤±è´¥: %v", err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM notification_channels WHERE id=$1`, id) })
	ch.ID = id
	return ch
}

// addTestEvent ç›´æŽ¥å†™ä¸€æ¡äº‹ä»¶ï¼ˆä¸ç» findingï¼‰ï¼Œç”¨äºŽæµ‹è¯•åˆ†æ´¾ä¸ŽæŠ•é€’ã€‚
func addTestEvent(t *testing.T, d *DB, kind string, findingID int64, snap notify.Snapshot) int64 {
	t.Helper()
	snap.Kind = kind
	snap.FindingID = findingID
	id, err := d.AddNotificationEvent(context.Background(), kind, findingID, snap)
	if err != nil {
		t.Fatalf("å†™äº‹ä»¶å¤±è´¥: %v", err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM notification_events WHERE id=$1`, id) })
	return id
}

func TestNotificationAssetNamesResolvesAndPreservesOrder(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()

	// ä¸‰ç±»èµ„äº§å„æœ‰å„çš„å±•ç¤ºå£å¾„ï¼šåŸŸåã€IPã€URLã€‚
	insertAsset := func(query, value string) int64 {
		t.Helper()
		var id int64
		if err := d.QueryRow(query, value).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	domID := insertAsset(`INSERT INTO assets(type, domain) VALUES('subdomain',$1) RETURNING id`, "a.example.com")
	ipID := insertAsset(`INSERT INTO assets(type, ip) VALUES('ip',$1) RETURNING id`, "10.1.2.3")
	svcID := insertAsset(`INSERT INTO assets(type, url) VALUES('service',$1) RETURNING id`, "https://a.example.com/admin")
	t.Cleanup(func() {
		d.Exec(`DELETE FROM assets WHERE id IN ($1,$2,$3)`, domID, ipID, svcID)
	})

	// ä¼ å…¥é¡ºåºåˆ»æ„ä¹±åºï¼Œä¸”å«ä¸€ä¸ªä¸å­˜åœ¨çš„ idã€‚
	got, err := d.NotificationAssetNames(ctx, []int64{svcID, 999999999, domID, ipID, svcID})
	if err != nil {
		t.Fatalf("è§£æžèµ„äº§åå¤±è´¥: %v", err)
	}
	want := []string{"https://a.example.com/admin", "a.example.com", "10.1.2.3"}
	if len(got) != len(want) {
		t.Fatalf("èµ„äº§åæ•°é‡ä¸ç¬¦ï¼ŒæœŸæœ› %v å¾—åˆ° %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("é¡ºåº/å–å€¼ä¸ç¬¦ï¼ŒæœŸæœ› %v å¾—åˆ° %v", want, got)
		}
	}
}

// TestRecordNotificationEventTxUnwindsOnFailure æ˜¯ä¿å­˜ç‚¹æœºåˆ¶çš„æ ¸å¿ƒç”¨ä¾‹ï¼š
// åœ¨äº‹åŠ¡é‡Œå…ˆè®© notification_events çš„å†™å…¥å¿…ç„¶å¤±è´¥ï¼ˆä¸´æ—¶åŠ ä¸€ä¸ªæ’ false çš„çº¦æŸï¼‰ï¼Œ
// æ–­è¨€ â‘  è¯¥å‡½æ•°æŠ¥ false â‘¡ äº‹åŠ¡æ²¡æœ‰è¿›å…¥ aborted çŠ¶æ€ï¼ŒåŽç»­è¯­å¥ä»èƒ½æ‰§è¡Œã€‚
//
// æ²¡æœ‰ä¿å­˜ç‚¹çš„è¯ï¼ŒPostgreSQL ä¼šè®©æ•´ä¸ªäº‹åŠ¡ä½œåºŸï¼ŒåŽç»­ä»»ä½•è¯­å¥éƒ½ä»¥
// "current transaction is aborted" å¤±è´¥â€”â€”é‚£æ­£æ˜¯ã€Œä¸€ä¸ªé€šçŸ¥è¡¨çš„é—®é¢˜å¯¼è‡´
// æ¼æ´žå­˜ä¸è¿›åº“ã€çš„æ•…éšœè·¯å¾„ã€‚
//
// è¿™é‡Œåˆ»æ„ç”¨ **ROLLBACK æ”¶å°¾è€Œä¸æ˜¯ COMMIT**ï¼šALTER TABLE åœ¨ PG é‡Œæ˜¯äº‹åŠ¡æ€§çš„ï¼Œ
// ä¸€æ—¦æäº¤ï¼Œé‚£ä¸ªä¸´æ—¶çº¦æŸå°±ä¼šæ°¸ä¹…ç•™åœ¨ schema é‡Œï¼ŒæŠŠåŽç»­æ‰€æœ‰ç”¨ä¾‹ä¸€èµ·æ‰“æŒ‚ã€‚
// å›žæ»šèƒ½è‡ªåŠ¨æ’¤é”€ DDLï¼Œæ— éœ€æ‰‹å·¥æ¸…ç†ã€‚æ–­è¨€åªéœ€è¦ã€Œäº‹åŠ¡è¿˜æ´»ç€ã€ï¼Œ
// ä¸éœ€è¦çœŸçš„æäº¤ã€‚
func TestRecordNotificationEventTxUnwindsOnFailure(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()

	// é˜²å¾¡æ€§æ¸…ç†ï¼šè‹¥åŽ†å²è¿è¡Œç•™ä¸‹è¿‡è¿™ä¸ªçº¦æŸï¼Œå…ˆæ‘˜æŽ‰ã€‚
	if _, err := d.Exec(`ALTER TABLE notification_events DROP CONSTRAINT IF EXISTS notify_test_never`); err != nil {
		t.Fatal(err)
	}

	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback() //nolint:errcheck // æ’¤é”€ä¸´æ—¶çº¦æŸï¼Œè§å‡½æ•°æ³¨é‡Š

	// NOT VALIDï¼šåªçº¦æŸæ­¤åŽå†™å…¥çš„è¡Œï¼Œä¸åŽ»æ ¡éªŒåº“é‡Œå·²æœ‰çš„åŽ†å²äº‹ä»¶
	// ï¼ˆå¦åˆ™å­˜é‡è¡Œè¿è§„ä¼šå¯¼è‡´çº¦æŸåŠ ä¸ä¸Šï¼‰ã€‚
	if _, err := tx.ExecContext(ctx, `ALTER TABLE notification_events ADD CONSTRAINT notify_test_never CHECK (false) NOT VALID`); err != nil {
		t.Fatalf("åŠ ä¸´æ—¶çº¦æŸå¤±è´¥: %v", err)
	}
	if RecordNotificationEventTx(ctx, tx, notify.EventFindingCreated, 1, notify.Snapshot{Severity: "high"}) {
		t.Fatal("åœ¨å¿…ç„¶å¤±è´¥çš„çº¦æŸä¸‹ä»æŠ¥å‘Šå†™å…¥æˆåŠŸ")
	}
	// å…³é”®æ–­è¨€ï¼šäº‹åŠ¡è¿˜èƒ½ç”¨ã€‚
	var one int
	if err := tx.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil {
		t.Fatalf("äº‹åŠ¡å·²è¢«æ±¡æŸ“ï¼ˆä¿å­˜ç‚¹æœªç”Ÿæ•ˆï¼‰: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("å›žæ»šå¤±è´¥: %v", err)
	}
	// ç¡®è®¤ DDL å·²éšå›žæ»šæ’¤é”€ï¼Œä¸ç»™åŽç»­ç”¨ä¾‹ç•™é›·ã€‚
	var exists bool
	if err := d.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conname='notify_test_never')`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("ä¸´æ—¶çº¦æŸæœªè¢«å›žæ»šæ’¤é”€ï¼Œä¼šæ±¡æŸ“åŽç»­ç”¨ä¾‹")
	}
}

func TestFanOutRoutesEventsByFilter(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()

	all := newTestChannel(t, d, notify.KindDingTalk, NotifyModeRealtime, `{}`)
	onlyCritical := newTestChannel(t, d, notify.KindDingTalk, NotifyModeRealtime, `{"min_severity":"critical"}`)
	sqlOnly := newTestChannel(t, d, notify.KindDingTalk, NotifyModeRealtime, `{"vulnclass_include":["SQL"]}`)

	highSQL := addTestEvent(t, d, notify.EventFindingCreated, 1001, notify.Snapshot{Severity: "high", VulnClass: "SQLæ³¨å…¥"})
	lowXSS := addTestEvent(t, d, notify.EventFindingCreated, 1002, notify.Snapshot{Severity: "low", VulnClass: "XSS"})
	criticalXSS := addTestEvent(t, d, notify.EventFindingCreated, 1003, notify.Snapshot{Severity: "critical", VulnClass: "XSS"})

	if _, _, err := d.FanOutPendingEvents(ctx, 100); err != nil {
		t.Fatalf("åˆ†æ´¾å¤±è´¥: %v", err)
	}

	cases := []struct {
		name    string
		eventID int64
		channel int64
		want    bool
	}{
		{"å…¨æ”¶æ¸ é“æ”¶åˆ° high", highSQL, all.ID, true},
		{"å…¨æ”¶æ¸ é“æ”¶åˆ° low", lowXSS, all.ID, true},
		{"ä»…ä¸¥é‡æ¸ é“è·³è¿‡ high", highSQL, onlyCritical.ID, false},
		{"ä»…ä¸¥é‡æ¸ é“æ”¶åˆ° critical", criticalXSS, onlyCritical.ID, true},
		{"ä»…SQLæ¸ é“æ”¶åˆ° SQL", highSQL, sqlOnly.ID, true},
		{"ä»…SQLæ¸ é“è·³è¿‡ XSS", lowXSS, sqlOnly.ID, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var exists bool
			if err := d.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM notification_deliveries WHERE event_id=$1 AND channel_id=$2)`,
				tc.eventID, tc.channel).Scan(&exists); err != nil {
				t.Fatal(err)
			}
			if exists != tc.want {
				t.Fatalf("æŠ•é€’æ˜¯å¦å­˜åœ¨: æœŸæœ› %v å¾—åˆ° %v", tc.want, exists)
			}
		})
	}

	// å†åˆ†æ´¾ä¸€æ¬¡ä¸åº”äº§ç”Ÿé‡å¤æŠ•é€’ï¼ˆfanned_out å¹‚ç­‰ï¼‰ã€‚
	events, deliveries, err := d.FanOutPendingEvents(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if events != 0 || deliveries != 0 {
		t.Fatalf("å·²åˆ†æ´¾çš„äº‹ä»¶ä¸åº”è¢«å†æ¬¡å¤„ç†ï¼Œå¾—åˆ° events=%d deliveries=%d", events, deliveries)
	}
}

// TestFanOutMarksEventsWithNoMatchingChannel è¦†ç›–ã€Œäº‹ä»¶æ²¡å‘½ä¸­ä»»ä½•æ¸ é“ã€çš„æƒ…å†µã€‚
// è¿™ç±»äº‹ä»¶å¿…é¡»ç…§æ ·è¢«æ ‡è®°ä¸ºå·²åˆ†æ´¾ï¼Œå¦åˆ™å®ƒä¼šæ°¸è¿œç•™åœ¨å¾…åˆ†æ´¾é›†åˆé‡Œã€æ¯ä¸ª tick é‡æ‰«ã€‚
func TestFanOutMarksEventsWithNoMatchingChannel(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()
	pick := newTestChannel(t, d, notify.KindDingTalk, NotifyModeRealtime, `{"vulnclass_include":["ç»ä¸åŒ¹é…çš„ç±»åž‹"]}`)
	_ = pick

	ev := addTestEvent(t, d, notify.EventFindingCreated, 2001, notify.Snapshot{Severity: "high", VulnClass: "XSS"})
	_, deliveries, err := d.FanOutPendingEvents(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if deliveries != 0 {
		t.Fatalf("ä¸è¯¥äº§ç”ŸæŠ•é€’ï¼Œå¾—åˆ° %d", deliveries)
	}
	var fanned bool
	if err := d.QueryRowContext(ctx, `SELECT fanned_out FROM notification_events WHERE id=$1`, ev).Scan(&fanned); err != nil {
		t.Fatal(err)
	}
	if !fanned {
		t.Fatal("æœªå‘½ä¸­æ¸ é“çš„äº‹ä»¶ä¹Ÿå¿…é¡»æ ‡è®°ä¸ºå·²åˆ†æ´¾ï¼Œå¦åˆ™ä¼šè¢«æ— é™é‡æ‰«")
	}
}

func TestClaimRealtimeDeliveriesHonorsLeaseAndMode(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()

	realtime := newTestChannel(t, d, notify.KindDingTalk, NotifyModeRealtime, `{}`)
	digest := newTestChannel(t, d, notify.KindDingTalk, NotifyModeDigest, `{}`)

	addTestEvent(t, d, notify.EventFindingCreated, 3001, notify.Snapshot{Severity: "high", VulnClass: "XSS"})
	if _, _, err := d.FanOutPendingEvents(ctx, 100); err != nil {
		t.Fatal(err)
	}

	// å®žæ—¶é¢†å–åªåº”æ‹¿åˆ° realtime æ¸ é“çš„é‚£æ¡ï¼Œä¸è¯¥åŠ¨ digest æ¸ é“çš„ã€‚
	got, err := d.ClaimRealtimeDeliveries(ctx, realtime.ID, 10, time.Minute)
	if err != nil {
		t.Fatalf("é¢†å–å¤±è´¥: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("åº”é¢†åˆ° 1 æ¡ï¼Œå¾—åˆ° %d", len(got))
	}
	if got[0].State != NotifyStateSending || got[0].Attempts != 1 {
		t.Fatalf("é¢†å–åŽåº”ä¸º sending ä¸” attempts=1ï¼Œå¾—åˆ° state=%s attempts=%d", got[0].State, got[0].Attempts)
	}
	// å…³è”åŠ è½½çš„æ¸²æŸ“ä¸Šä¸‹æ–‡å¿…é¡»é½å…¨ï¼ˆæ¸ é“é…ç½® + äº‹ä»¶å¿«ç…§ + finding idï¼‰ã€‚
	if got[0].Channel == nil || len(got[0].Channel.Config) == 0 {
		t.Fatal("é¢†å–ç»“æžœç¼ºå°‘æ¸ é“é…ç½®ï¼Œæ¸²æŸ“ä¼šå¤±è´¥")
	}
	if got[0].FindingID != 3001 {
		t.Fatalf("finding id æœªä»Žäº‹ä»¶å¸¦å‡ºï¼Œå¾—åˆ° %d", got[0].FindingID)
	}

	// ç§Ÿçº¦æœªåˆ°æœŸï¼Œç¬¬äºŒæ¬¡é¢†å–åº”ä¸ºç©ºâ€”â€”è¿™æ˜¯ã€ŒåŒä¸€è¡Œä¸ä¼šè¢«ä¸¤ä¸ª dispatcher åŒæ—¶æŠ•é€’ã€
	// çš„ä¿è¯ã€‚
	again, err := d.ClaimRealtimeDeliveries(ctx, realtime.ID, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("ç§Ÿçº¦æœŸå†…ä¸åº”é‡å¤é¢†å–ï¼Œå¾—åˆ° %d æ¡", len(again))
	}

	// digest æ¸ é“çš„æŠ•é€’ä¸åº”è¢«å®žæ—¶é¢†å–ç¢°åˆ°ã€‚
	left, err := d.ClaimRealtimeDeliveries(ctx, digest.ID, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("å®žæ—¶é¢†å–ä¸åº”æ‹¿åˆ° digest æ¸ é“çš„æŠ•é€’ï¼Œå¾—åˆ° %d æ¡", len(left))
	}
}

// TestClaimExpiredLeaseRecovers è¦†ç›–å´©æºƒè‡ªæ„ˆï¼šè¿›ç¨‹åœ¨æŠ•é€’é€”ä¸­æŒ‚æŽ‰ä¼šç•™ä¸‹ sending
// è¡Œï¼Œç§Ÿçº¦åˆ°æœŸåŽå¿…é¡»èƒ½è¢«é‡æ–°é¢†èµ·æ¥ï¼Œå¦åˆ™è¿™æ¡æŠ•é€’æ°¸è¿œå¡ä½ã€‚
func TestClaimExpiredLeaseRecovers(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()
	ch := newTestChannel(t, d, notify.KindDingTalk, NotifyModeRealtime, `{}`)
	addTestEvent(t, d, notify.EventFindingCreated, 4001, notify.Snapshot{Severity: "high"})
	if _, _, err := d.FanOutPendingEvents(ctx, 100); err != nil {
		t.Fatal(err)
	}
	first, err := d.ClaimRealtimeDeliveries(ctx, ch.ID, 10, time.Minute)
	if err != nil || len(first) != 1 {
		t.Fatalf("é¦–æ¬¡é¢†å–å¤±è´¥: %v (%d æ¡)", err, len(first))
	}
	// æŠŠç§Ÿçº¦æ‰‹åŠ¨æŽ¨åˆ°è¿‡åŽ»ï¼Œæ¨¡æ‹Ÿã€Œç§Ÿçº¦å·²è¿‡æœŸã€ã€‚
	if _, err := d.Exec(`UPDATE notification_deliveries SET next_attempt_at = now() - interval '1 minute' WHERE id=$1`, first[0].ID); err != nil {
		t.Fatal(err)
	}
	second, err := d.ClaimRealtimeDeliveries(ctx, ch.ID, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 {
		t.Fatalf("ç§Ÿçº¦è¿‡æœŸçš„ sending è¡Œåº”å¯è¢«é‡æ–°é¢†å–ï¼Œå¾—åˆ° %d æ¡", len(second))
	}
	if second[0].Attempts != 2 {
		t.Fatalf("é‡æ–°é¢†å–åº”ç´¯åŠ å°è¯•æ¬¡æ•°ï¼Œå¾—åˆ° %d", second[0].Attempts)
	}
}

func TestClaimSkipsDisabledChannel(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()
	ch := newTestChannel(t, d, notify.KindDingTalk, NotifyModeRealtime, `{}`)
	addTestEvent(t, d, notify.EventFindingCreated, 5001, notify.Snapshot{Severity: "high"})
	if _, _, err := d.FanOutPendingEvents(ctx, 100); err != nil {
		t.Fatal(err)
	}
	// åœç”¨ä¼šæŠŠå­˜é‡å¾…å‘æŠ•é€’ä¸€èµ·æ ‡è®°ä¸º skippedã€‚
	if err := d.SetNotificationChannelEnabled(ctx, ch.ID, false); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := d.QueryRow(`SELECT state FROM notification_deliveries WHERE channel_id=$1`, ch.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != NotifyStateSkipped {
		t.Fatalf("åœç”¨æ¸ é“çš„å­˜é‡å¾…å‘æŠ•é€’åº”è¢«æ ‡è®°ä¸º skippedï¼Œå¾—åˆ° %s", state)
	}
	got, err := d.ClaimRealtimeDeliveries(ctx, ch.ID, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("å·²åœç”¨æ¸ é“ä¸åº”èƒ½è¢«é¢†å–ï¼Œå¾—åˆ° %d æ¡", len(got))
	}
}

func TestDigestBatchDueAndStableBatchID(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()
	ch := newTestChannel(t, d, notify.KindDingTalk, NotifyModeDigest, `{}`)
	for i := 0; i < 3; i++ {
		addTestEvent(t, d, notify.EventFindingCreated, int64(6000+i), notify.Snapshot{Severity: "high"})
	}
	if _, _, err := d.FanOutPendingEvents(ctx, 100); err != nil {
		t.Fatal(err)
	}

	// æ‰¹æ¬¡åˆšå»ºã€å¹´é¾„ä¸º 0ï¼Œ30 åˆ†é’Ÿçš„å‘¨æœŸä¸‹ä¸è¯¥åˆ°æœŸã€‚
	due, err := d.DigestBatchDue(ctx, ch.ID, 30*time.Minute)
	if err != nil {
		t.Fatalf("åˆ¤æ–­æ‰¹æ¬¡åˆ°æœŸå¤±è´¥: %v", err)
	}
	if due {
		t.Fatal("åˆšå»ºç«‹çš„æ‰¹æ¬¡ä¸åº”ç«‹å³åˆ°æœŸ")
	}

	// æŠŠä¸‰æ¡æŠ•é€’çš„åˆ›å»ºæ—¶é—´ä¸€èµ·æŽ¨è€ï¼Œæ¨¡æ‹Ÿä¸€ä¸ªæ”’å¤Ÿå‘¨æœŸçš„æ‰¹æ¬¡ã€‚
	if _, err := d.Exec(`UPDATE notification_deliveries SET created_at = now() - interval '40 minutes' WHERE channel_id=$1`, ch.ID); err != nil {
		t.Fatal(err)
	}
	due, err = d.DigestBatchDue(ctx, ch.ID, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !due {
		t.Fatal("è¶…è¿‡å‘¨æœŸçš„æ‰¹æ¬¡åº”åˆ¤å®šä¸ºåˆ°æœŸ")
	}

	batch, err := d.ClaimDigestBatch(ctx, ch.ID, MaxDigestBatchSize, time.Minute)
	if err != nil {
		t.Fatalf("é¢†å–æ±‡æ€»æ‰¹æ¬¡å¤±è´¥: %v", err)
	}
	if len(batch) != 3 {
		t.Fatalf("æ±‡æ€»åº”ä¸€æ¬¡é¢†èµ°å…¨éƒ¨ 3 æ¡ï¼Œå¾—åˆ° %d æ¡", len(batch))
	}
	if batch[0].BatchID == nil {
		t.Fatal("æ±‡æ€»æ‰¹æ¬¡å¿…é¡»å†™ batch_idï¼Œå¦åˆ™åŽ†å²é‡Œçœ‹ä¸å‡ºè¿™å‡ æ¡æ˜¯ä¸€èµ·å‘çš„")
	}
	firstBatchID := *batch[0].BatchID
	for _, dl := range batch {
		if dl.BatchID == nil || *dl.BatchID != firstBatchID {
			t.Fatalf("åŒä¸€æ‰¹æ¬¡åº”å…±äº« batch_idï¼Œå¾—åˆ° %v vs %d", dl.BatchID, firstBatchID)
		}
	}

	// è®©è¿™æ‰¹**æ•´ä½“**å¤±è´¥é‡æŽ’åŽå†é¢†ï¼Œbatch_id å¿…é¡»ä¿æŒåŽŸå€¼ï¼ˆCOALESCE çš„ä½œç”¨ï¼‰ï¼š
	// å¦åˆ™ä¸€æ¬¡é‡è¯•å°±æŠŠã€Œè¿™æ‰¹æ˜¯ä¸€èµ·å‘çš„ã€è¿™ä¸ªäº‹å®žæŠ¹æŽ‰äº†ã€‚
	//
	// å¿…é¡»æ•´æ‰¹é‡æŽ’è€Œä¸æ˜¯åªé‡æŽ’ä¸€æ¡â€”â€”æŠ•é€’å¼•æ“Žå‘æ±‡æ€»æ¶ˆæ¯æ—¶å°±æ˜¯è¿™æ ·å¤„ç†çš„
	// ï¼ˆä¸€æ¡æ¶ˆæ¯ä»£è¡¨æ•´æ‰¹ï¼Œæˆè´¥ä¸Žå…±ï¼‰ã€‚åªé‡æŽ’ä¸€æ¡çš„è¯ï¼Œå…¶ä½™ä»åœ¨ç§Ÿçº¦æœŸå†…ï¼Œ
	// é‡é¢†è‡ªç„¶åªæ‹¿åˆ°é‚£ä¸€æ¡ã€‚
	allIDs := make([]int64, 0, len(batch))
	for _, dl := range batch {
		allIDs = append(allIDs, dl.ID)
	}
	if err := d.RescheduleDeliveries(ctx, allIDs, time.Second, "æ¨¡æ‹Ÿå¤±è´¥"); err != nil {
		t.Fatal(err)
	}
	// æŠŠç§Ÿçº¦æŽ¨åˆ°è¿‡åŽ»ï¼Œæ¨¡æ‹Ÿé€€é¿æ—¶é—´å·²åˆ°ã€‚
	if _, err := d.Exec(`UPDATE notification_deliveries SET next_attempt_at = now() - interval '1 minute' WHERE channel_id=$1`, ch.ID); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := d.ClaimDigestBatch(ctx, ch.ID, MaxDigestBatchSize, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(reclaimed) != 3 {
		t.Fatalf("é‡é¢†åº”æ‹¿åˆ°å…¨éƒ¨ 3 æ¡ï¼Œå¾—åˆ° %d", len(reclaimed))
	}
	if reclaimed[0].BatchID == nil || *reclaimed[0].BatchID != firstBatchID {
		t.Fatalf("é‡è¯•åŽ batch_id åº”ä¿æŒåŽŸå€¼ %dï¼Œå¾—åˆ° %v", firstBatchID, reclaimed[0].BatchID)
	}
}

func TestDeliveryStateTransitions(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()
	ch := newTestChannel(t, d, notify.KindDingTalk, NotifyModeRealtime, `{}`)
	addTestEvent(t, d, notify.EventFindingCreated, 7001, notify.Snapshot{Severity: "high"})
	if _, _, err := d.FanOutPendingEvents(ctx, 100); err != nil {
		t.Fatal(err)
	}
	got, err := d.ClaimRealtimeDeliveries(ctx, ch.ID, 10, time.Minute)
	if err != nil || len(got) != 1 {
		t.Fatalf("é¢†å–å¤±è´¥: %v (%d)", err, len(got))
	}
	id := got[0].ID

	if err := d.RescheduleDeliveries(ctx, []int64{id}, time.Second, "ç½‘ç»œæŠ–åŠ¨"); err != nil {
		t.Fatal(err)
	}
	var state, lastErr string
	if err := d.QueryRow(`SELECT state, last_error FROM notification_deliveries WHERE id=$1`, id).Scan(&state, &lastErr); err != nil {
		t.Fatal(err)
	}
	if state != NotifyStatePending || lastErr != "ç½‘ç»œæŠ–åŠ¨" {
		t.Fatalf("é‡æŽ’åŽåº”ä¸º pending å¹¶è®°å½•åŽŸå› ï¼Œå¾—åˆ° state=%s err=%q", state, lastErr)
	}

	if err := d.FailDeliveries(ctx, []int64{id}, "é‡è¯•è€—å°½"); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT state FROM notification_deliveries WHERE id=$1`, id).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != NotifyStateFailed {
		t.Fatalf("åº”ä¸º failedï¼Œå¾—åˆ° %s", state)
	}

	// æ‰‹åŠ¨é‡å‘è¦æ¸…é›¶é‡è¯•è®¡æ•°å¹¶ç«‹å³åˆ°æœŸï¼Œå¦åˆ™ä¼šç»§æ‰¿æ—§çš„å¤±è´¥é¢„ç®—ã€‚
	if err := d.RetryNotificationDelivery(ctx, id); err != nil {
		t.Fatalf("é‡å‘å¤±è´¥: %v", err)
	}
	var attempts int
	var next time.Time
	if err := d.QueryRow(`SELECT state, attempts, next_attempt_at FROM notification_deliveries WHERE id=$1`, id).Scan(&state, &attempts, &next); err != nil {
		t.Fatal(err)
	}
	if state != NotifyStatePending || attempts != 0 {
		t.Fatalf("é‡å‘åŽåº”ä¸º pending ä¸” attempts=0ï¼Œå¾—åˆ° state=%s attempts=%d", state, attempts)
	}
	if next.After(time.Now().Add(time.Second)) {
		t.Fatal("é‡å‘åº”ç«‹å³å¯é¢†")
	}

	// å·²é€è¾¾çš„æŠ•é€’ä¸åº”èƒ½è¢«é‡å‘ã€‚
	if err := d.MarkDeliveriesSent(ctx, []int64{id}); err != nil {
		t.Fatal(err)
	}
	if err := d.RetryNotificationDelivery(ctx, id); err == nil {
		t.Fatal("å·²é€è¾¾çš„æŠ•é€’ä¸è¯¥å…è®¸é‡å‘")
	}
}

func TestListNotificationDeliveriesPagingAndFilter(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()
	ch := newTestChannel(t, d, notify.KindDingTalk, NotifyModeRealtime, `{}`)
	for i := 0; i < 5; i++ {
		addTestEvent(t, d, notify.EventFindingCreated, int64(8000+i), notify.Snapshot{Severity: "high", Name: "åˆ†é¡µæµ‹è¯•"})
	}
	if _, _, err := d.FanOutPendingEvents(ctx, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ClaimRealtimeDeliveries(ctx, ch.ID, 10, time.Minute); err != nil {
		t.Fatal(err)
	}

	page1, total, err := d.ListNotificationDeliveries(ctx, NotificationDeliveryFilter{ChannelID: ch.ID, State: NotifyStateSending}, 1, 2)
	if err != nil {
		t.Fatalf("æŸ¥è¯¢å¤±è´¥: %v", err)
	}
	if total != 5 {
		t.Fatalf("æ€»æ•°åº”ä¸º 5ï¼Œå¾—åˆ° %d", total)
	}
	if len(page1) != 2 {
		t.Fatalf("æ¯é¡µ 2 æ¡ï¼Œå¾—åˆ° %d", len(page1))
	}
	// æ–°çš„åœ¨å‰ï¼šç¬¬ä¸€é¡µé¦–æ¡ id åº”å¤§äºŽç¬¬äºŒé¡µé¦–æ¡ã€‚
	page2, _, err := d.ListNotificationDeliveries(ctx, NotificationDeliveryFilter{ChannelID: ch.ID, State: NotifyStateSending}, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 2 || page2[0].ID >= page1[0].ID {
		t.Fatalf("åˆ†é¡µé¡ºåºåº”ä¸ºæ–°çš„åœ¨å‰ï¼Œå¾—åˆ° page1[0]=%d page2[0]=%d", page1[0].ID, page2[0].ID)
	}
	// æ¸²æŸ“ä¸Šä¸‹æ–‡å¿…é¡»éšåŽ†å²ä¸€èµ·è¿”å›žï¼Œå¦åˆ™åˆ—è¡¨æ— æ³•å±•ç¤ºã€ŒæŽ¨çš„æ˜¯ä»€ä¹ˆã€ã€‚
	if page1[0].ChannelName == "" || page1[0].FindingID == 0 {
		t.Fatalf("åŽ†å²é¡¹ç¼ºå°‘å±•ç¤ºå­—æ®µ: %+v", page1[0])
	}

	// æŒ‰çŠ¶æ€è¿‡æ»¤ï¼šæ²¡æœ‰ pending çš„ã€‚
	pending, totalPending, err := d.ListNotificationDeliveries(ctx, NotificationDeliveryFilter{ChannelID: ch.ID, State: NotifyStatePending}, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if totalPending != 0 || len(pending) != 0 {
		t.Fatalf("ä¸è¯¥æœ‰ pending æŠ•é€’ï¼Œå¾—åˆ° %d æ¡ (total=%d)", len(pending), totalPending)
	}
}

func TestSetFindingStatusWithNotifyOnlyEmitsOnRealChange(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()

	tk, err := d.CreateTask("é€šçŸ¥çŠ¶æ€å˜æ›´æµ‹è¯•", "ç›®æ ‡", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(tk.ID)
	es := d.Exploration(tk.ExplorationID)
	f, err := es.RecordFinding(ctx, RecordFindingInput{
		TaskID: tk.ID, Worker: "test", VulnClass: "SQLæ³¨å…¥", Name: "çŠ¶æ€å˜æ›´ç”¨ä¾‹",
		Severity: "high", Summary: "æ‘˜è¦",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM notification_events WHERE finding_id=$1`, f.FindingID) })

	// è½åº“æ—¶å·²ç™»è®°ä¸€æ¡ finding_created äº‹ä»¶ï¼Œå…ˆæŠŠå®ƒæ•°å‡ºæ¥ä½œä¸ºåŸºçº¿ã€‚
	var base int
	if err := d.QueryRow(`SELECT count(*) FROM notification_events WHERE finding_id=$1`, f.FindingID).Scan(&base); err != nil {
		t.Fatal(err)
	}
	if base < 1 {
		t.Fatal("æ¼æ´žè½åº“åº”åœ¨åŒä¸€äº‹åŠ¡é‡Œç™»è®°ä¸€æ¡æŽ¨é€äº‹ä»¶")
	}

	// æ”¹æˆåŒä¸€ä¸ªçŠ¶æ€ï¼šä¸è¯¥äº§ç”Ÿäº‹ä»¶ï¼ˆé¿å…é‡å¤æäº¤åˆ·å‡ºæŽ¨é€å™ªéŸ³ï¼‰ã€‚
	from, found, notified, err := d.SetFindingStatusWithNotify(ctx, f.FindingID, "pending")
	if err != nil || !found {
		t.Fatalf("çŠ¶æ€è®¾ç½®å¤±è´¥: found=%v err=%v", found, err)
	}
	if notified {
		t.Fatal("çŠ¶æ€æœªå˜åŒ–æ—¶ä¸åº”ç™»è®°æŽ¨é€äº‹ä»¶")
	}
	if from != "pending" {
		t.Fatalf("åº”è¿”å›žå˜æ›´å‰çŠ¶æ€ pendingï¼Œå¾—åˆ° %q", from)
	}

	// çœŸæ­£å˜æ›´ï¼šåº”ç™»è®°äº‹ä»¶å¹¶è®°å½• from/toã€‚
	from, found, notified, err = d.SetFindingStatusWithNotify(ctx, f.FindingID, "fixed")
	if err != nil || !found {
		t.Fatalf("çŠ¶æ€è®¾ç½®å¤±è´¥: found=%v err=%v", found, err)
	}
	if !notified {
		t.Fatal("çŠ¶æ€å®žé™…å˜æ›´æ—¶åº”ç™»è®°æŽ¨é€äº‹ä»¶")
	}
	if from != "pending" {
		t.Fatalf("from åº”ä¸º pendingï¼Œå¾—åˆ° %q", from)
	}
	var snapshot []byte
	if err := d.QueryRow(`SELECT snapshot FROM notification_events WHERE finding_id=$1 AND kind=$2`,
		f.FindingID, notify.EventFindingStatusChanged).Scan(&snapshot); err != nil {
		t.Fatalf("æœªæ‰¾åˆ°çŠ¶æ€å˜æ›´äº‹ä»¶: %v", err)
	}
	var snap notify.Snapshot
	if err := json.Unmarshal(snapshot, &snap); err != nil {
		t.Fatal(err)
	}
	if snap.FromStatus != "pending" || snap.ToStatus != "fixed" {
		t.Fatalf("å¿«ç…§é‡Œçš„çŠ¶æ€æµè½¬ä¸å¯¹: %s â†’ %s", snap.FromStatus, snap.ToStatus)
	}
	// å¿«ç…§è¦å¸¦ä¸Šæ¸²æŸ“æ‰€éœ€å­—æ®µï¼Œå¦åˆ™çŠ¶æ€å˜æ›´æ¶ˆæ¯ä¼šæ˜¯ç©ºå£³ã€‚
	if snap.VulnClass != "SQLæ³¨å…¥" || snap.Severity != "high" || snap.Name != "çŠ¶æ€å˜æ›´ç”¨ä¾‹" {
		t.Fatalf("å¿«ç…§ç¼ºå°‘æ¸²æŸ“å­—æ®µ: %+v", snap)
	}
	var status string
	if err := d.QueryRow(`SELECT status FROM findings WHERE id=$1`, f.FindingID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "fixed" {
		t.Fatalf("çŠ¶æ€åº”å·²æ›´æ–°ä¸º fixedï¼Œå¾—åˆ° %s", status)
	}

	// ä¸å­˜åœ¨çš„æ¼æ´žï¼šfound=falseï¼Œä¸æŠ¥é”™ã€‚
	if _, found, _, err := d.SetFindingStatusWithNotify(ctx, 999999999, "fixed"); err != nil || found {
		t.Fatalf("ä¸å­˜åœ¨çš„æ¼æ´žåº”è¿”å›ž found=false ä¸”æ— é”™ï¼Œå¾—åˆ° found=%v err=%v", found, err)
	}
}

func TestNotificationStatsSnapshot(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()
	ch := newTestChannel(t, d, notify.KindDingTalk, NotifyModeRealtime, `{}`)
	addTestEvent(t, d, notify.EventFindingCreated, 9001, notify.Snapshot{Severity: "high"})
	if _, _, err := d.FanOutPendingEvents(ctx, 100); err != nil {
		t.Fatal(err)
	}
	stats, err := d.NotificationStatsSnapshot(ctx)
	if err != nil {
		t.Fatalf("ç»Ÿè®¡å¤±è´¥: %v", err)
	}
	if stats.Channels < 1 || stats.ChannelsOn < 1 {
		t.Fatalf("æ¸ é“è®¡æ•°ä¸å¯¹: %+v", stats)
	}
	if stats.Pending < 1 {
		t.Fatalf("åº”ç»Ÿè®¡åˆ°å¾…å‘æŠ•é€’: %+v", stats)
	}
	// åˆšå»ºçš„æŠ•é€’ç§¯åŽ‹å¹´é¾„åº”æŽ¥è¿‘ 0ï¼Œè€Œä¸æ˜¯è´Ÿæ•°æˆ–å·¨å¤§å€¼ã€‚
	if stats.BacklogAgeMS < 0 || stats.BacklogAgeMS > int64(time.Hour/time.Millisecond) {
		t.Fatalf("ç§¯åŽ‹å¹´é¾„ä¸åˆæ³•: %d ms", stats.BacklogAgeMS)
	}
	_ = ch
}

func TestNotificationChannelCRUDRoundTrip(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()

	ch := &NotificationChannel{
		Name:       "CRUD å¾€è¿”",
		Kind:       notify.KindEmail,
		Mode:       NotifyModeDigest,
		Config:     json.RawMessage(`{"host":"smtp.example.com","port":587,"from":"a@b.c","to":["x@y.z"]}`),
		Filter:     json.RawMessage(`{"min_severity":"medium","on_status_change":true}`),
		RatePerMin: 42,
	}
	id, err := d.SaveNotificationChannel(ctx, ch)
	if err != nil {
		t.Fatalf("æ–°å»ºå¤±è´¥: %v", err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM notification_channels WHERE id=$1`, id) })

	got, err := d.NotificationChannelByID(ctx, id)
	if err != nil {
		t.Fatalf("è¯»å–å¤±è´¥: %v", err)
	}
	if got.Mode != NotifyModeDigest || got.RatePerMin != 42 || got.Name != "CRUD å¾€è¿”" {
		t.Fatalf("å¾€è¿”å­—æ®µä¸ä¸€è‡´: %+v", got)
	}
	if !got.IsEnabled() {
		t.Fatal("é»˜è®¤åº”ä¸ºå¯ç”¨")
	}
	var cfg map[string]any
	if err := json.Unmarshal(got.Config, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["host"] != "smtp.example.com" {
		t.Fatalf("é…ç½®æœªæ­£ç¡®è½åº“: %v", cfg)
	}
	var filter notify.Filter
	if err := json.Unmarshal(got.Filter, &filter); err != nil {
		t.Fatal(err)
	}
	if filter.MinSeverity != "medium" || !filter.OnStatusChange {
		t.Fatalf("è¿‡æ»¤æ¡ä»¶æœªæ­£ç¡®è½åº“: %+v", filter)
	}

	// æ›´æ–°åŽå†è¯»ã€‚
	got.Name = "æ”¹åäº†"
	off := false
	got.Enabled = &off
	if _, err := d.SaveNotificationChannel(ctx, got); err != nil {
		t.Fatal(err)
	}
	after, err := d.NotificationChannelByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if after.Name != "æ”¹åäº†" || after.IsEnabled() {
		t.Fatalf("æ›´æ–°æœªç”Ÿæ•ˆ: %+v", after)
	}

	// åˆ é™¤åŽåº”æŠ¥ã€Œä¸å­˜åœ¨ã€è€Œä¸æ˜¯é™é»˜æˆåŠŸã€‚
	if err := d.DeleteNotificationChannel(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := d.NotificationChannelByID(ctx, id); err != ErrNotificationChannelNotFound {
		t.Fatalf("æœŸæœ› ErrNotificationChannelNotFoundï¼Œå¾—åˆ° %v", err)
	}
	if err := d.DeleteNotificationChannel(ctx, id); err != ErrNotificationChannelNotFound {
		t.Fatalf("é‡å¤åˆ é™¤åº”æŠ¥ä¸å­˜åœ¨ï¼Œå¾—åˆ° %v", err)
	}
}

// TestSaveNotificationChannelKeepsExplicitZeroRate é”ä½ä¸€ä¸ªæ›¾ç»å†™é”™çš„åœ°æ–¹ï¼š
// **0 æ˜¯åˆæ³•é…ç½®ï¼Œå«ä¹‰æ˜¯ã€Œä¸é™æµã€ï¼Œä¸èƒ½è¢« db å±‚å½“æˆã€ŒæœªæŒ‡å®šã€è¦†ç›–æˆé»˜è®¤å€¼**ã€‚
//
// åŽ†å² bugï¼šSaveNotificationChannel é‡Œå†™äº† `if RatePerMin <= 0 { å–é»˜è®¤å€¼ }`ï¼Œ
// äºŽæ˜¯æ–‡æ¡£ã€UI æç¤ºã€takeTokens éƒ½æŒ‰ã€Œ0=ä¸é™æµã€è§£é‡Šï¼Œå”¯ç‹¬å†™åº“è¿™ä¸€å±‚æ‚„æ‚„æ”¹æˆ
// 20ï¼ˆé’‰é’‰/ä¼å¾®/Telegramï¼‰æˆ– 100ï¼ˆé£žä¹¦ï¼‰â€”â€”æ“ä½œè€…ä»¥ä¸ºæ”¾å¼€äº†é™æµã€å®žé™…è¢«å¡ç€ï¼Œ
// è€Œä¸”æ²¡æœ‰ä»»ä½•æç¤ºã€‚ã€ŒæœªæŒ‡å®šã€ä¸Žã€Œæ˜¾å¼ 0ã€çš„åŒºåˆ«åªæœ‰è¯·æ±‚ä½“èƒ½è¡¨è¾¾ï¼Œ
// æ‰€ä»¥é»˜è®¤å€¼åœ¨ server å±‚å¡«ï¼ˆè§ notifyCreateChannelï¼‰ï¼Œdb å±‚åªç®¡å­˜ã€‚
func TestSaveNotificationChannelKeepsExplicitZeroRate(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()

	// æ˜¾å¼ 0ï¼ˆä¸é™æµï¼‰ï¼šå¿…é¡»åŽŸæ ·å­˜ä¸‹æ¥ã€‚
	unlimited := &NotificationChannel{
		Name: "ä¸é™æµ", Kind: notify.KindDingTalk, RatePerMin: 0,
		Config: json.RawMessage(`{"webhook":"https://example.com/h"}`),
	}
	id, err := d.SaveNotificationChannel(ctx, unlimited)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM notification_channels WHERE id=$1`, id) })
	got, err := d.NotificationChannelByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.RatePerMin != 0 {
		t.Fatalf("æ˜¾å¼ 0 è¡¨ç¤ºä¸é™æµï¼Œå¿…é¡»åŽŸæ ·ä¿å­˜ï¼Œå¾—åˆ° %d", got.RatePerMin)
	}
	if got.Mode != NotifyModeRealtime {
		t.Fatalf("é»˜è®¤æ¨¡å¼åº”ä¸º realtimeï¼Œå¾—åˆ° %s", got.Mode)
	}

	// è´Ÿå€¼æ˜¯éžæ³•è¾“å…¥ï¼Œåº”è¢«æ‹’ç»è€Œä¸æ˜¯æ‚„æ‚„æ”¹æˆåˆ«çš„å€¼ã€‚
	bad := &NotificationChannel{
		Name: "è´Ÿé™æµ", Kind: notify.KindDingTalk, RatePerMin: -1,
		Config: json.RawMessage(`{"webhook":"https://example.com/h"}`),
	}
	if _, err := d.SaveNotificationChannel(ctx, bad); err == nil {
		t.Fatal("è´Ÿé™æµåº”è¢«æ‹’ç»")
	}
}

// TestDeleteChannelCascadesDeliveries é”ä½å¤–é”®è¡Œä¸ºï¼šæ¸ é“åˆ é™¤åŽå…¶æŠ•é€’åŽ†å²ä¸€å¹¶æ¶ˆå¤±
// ï¼ˆé…ç½®éƒ½æ²¡äº†ï¼ŒåŽ†å²æ— ä»Žè§£è¯»ï¼‰ï¼Œä½†äº‹ä»¶æœ¬èº«è¦ç•™ä¸‹â€”â€”å®ƒå¯èƒ½è¿˜è¢«åˆ«çš„æ¸ é“å¼•ç”¨ã€‚
func TestDeleteChannelCascadesDeliveries(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()
	ch := newTestChannel(t, d, notify.KindDingTalk, NotifyModeRealtime, `{}`)
	ev := addTestEvent(t, d, notify.EventFindingCreated, 9101, notify.Snapshot{Severity: "high"})
	if _, _, err := d.FanOutPendingEvents(ctx, 100); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := d.QueryRow(`SELECT count(*) FROM notification_deliveries WHERE channel_id=$1`, ch.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before == 0 {
		t.Fatal("å‰ç½®æ¡ä»¶ä¸æˆç«‹ï¼šæœªäº§ç”ŸæŠ•é€’")
	}
	if err := d.DeleteNotificationChannel(ctx, ch.ID); err != nil {
		t.Fatal(err)
	}
	var after int
	if err := d.QueryRow(`SELECT count(*) FROM notification_deliveries WHERE channel_id=$1`, ch.ID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != 0 {
		t.Fatalf("æ¸ é“åˆ é™¤åŽå…¶æŠ•é€’åº”çº§è”åˆ é™¤ï¼Œä»æœ‰ %d æ¡", after)
	}
	var evExists bool
	if err := d.QueryRow(`SELECT EXISTS(SELECT 1 FROM notification_events WHERE id=$1)`, ev).Scan(&evExists); err != nil {
		t.Fatal(err)
	}
	if !evExists {
		t.Fatal("åˆ æ¸ é“ä¸åº”è¿žå¸¦åˆ é™¤äº‹ä»¶æœ¬èº«")
	}
}

// TestClaimDigestBatchHonorsCallerLimit è¦†ç›–å®¡è®¡æŒ‡å‡ºçš„ä¸€å¤„å£å­ï¼š
// æ±‡æ€»æ¸ é“æ­¤å‰å®Œå…¨ç»•è¿‡ä»¤ç‰Œæ¡¶â€”â€”allow è¢« takeTokens æ‰£æŽ‰å´æ²¡äººç”¨ï¼Œ
// rate_per_min å¯¹ digest æ¨¡å¼æ¯«æ— ä½œç”¨ã€‚çŽ°åœ¨ limit ä¹Ÿå‚ä¸Žçº¦æŸã€‚
func TestClaimDigestBatchHonorsCallerLimit(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()
	ch := newTestChannel(t, d, notify.KindDingTalk, NotifyModeDigest, `{}`)
	for i := 0; i < 10; i++ {
		addTestEvent(t, d, notify.EventFindingCreated, int64(7000+i), notify.Snapshot{Severity: "high"})
	}
	if _, _, err := d.FanOutPendingEvents(ctx, 100); err != nil {
		t.Fatal(err)
	}
	// å– limit=3ï¼šåªèƒ½é¢†åˆ° 3 æ¡ï¼Œå…¶ä½™ç•™åœ¨åº“é‡Œã€‚
	got, err := d.ClaimDigestBatch(ctx, ch.ID, 3, time.Minute)
	if err != nil {
		t.Fatalf("é¢†å–å¤±è´¥: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("åº”æŒ‰è°ƒç”¨æ–¹é™æµé¢åº¦åªé¢† 3 æ¡ï¼Œå¾—åˆ° %d", len(got))
	}
	// limit=0 è¡¨ç¤ºæœ¬è½®é¢åº¦ç”¨å°½ï¼šä¸€æ¡éƒ½ä¸è¯¥é¢†ï¼Œä¹Ÿä¸è¯¥æŠ¥é”™ã€‚
	if got, err := d.ClaimDigestBatch(ctx, ch.ID, 0, time.Minute); err != nil || len(got) != 0 {
		t.Fatalf("é¢åº¦ä¸º 0 æ—¶åº”é¢† 0 æ¡ä¸”ä¸æŠ¥é”™ï¼Œå¾—åˆ° %d æ¡ err=%v", len(got), err)
	}
}

// TestFinishFindingRetestEmitsStatusChange è¦†ç›–å®¡è®¡æŒ‡å‡ºçš„ä¸€å¤„å®Œæ•´æ€§ç¼ºå£ï¼š
// å¤æµ‹ç»“è®ºä¸ºã€Œå·²ä¿®å¤ã€æ—¶ï¼ŒçŠ¶æ€ç¡®å®žå˜äº†ï¼Œä½†é‚£æ¡ UPDATE æ˜¯ç›´æŽ¥å†™åº“çš„ã€
// ç»•è¿‡äº†å¸¦é€šçŸ¥çš„ç‰ˆæœ¬â€”â€”äºŽæ˜¯é…äº† on_status_change çš„æ¸ é“å¯¹è¿™ç§çŠ¶æ€æµè½¬
// å®Œå…¨æ”¶ä¸åˆ°æŽ¨é€ï¼Œç•Œé¢ä¸ŠçŠ¶æ€æ‚„æ‚„å˜äº†ï¼Œè¿ç»´è¦æ‰“å¼€å¹³å°æ‰çŸ¥é“ã€‚
//
// è¿™æ¡ç”¨ä¾‹é”ä½ã€Œæ‰€æœ‰æ”¹çŠ¶æ€çš„è·¯å¾„éƒ½è¦ç™»è®°çŠ¶æ€å˜æ›´äº‹ä»¶ã€ã€‚
func TestFinishFindingRetestEmitsStatusChange(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()

	tk, err := d.CreateTask("å¤æµ‹æŽ¨é€æµ‹è¯•", "ç›®æ ‡", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(tk.ID)
	es := d.Exploration(tk.ExplorationID)
	f, err := es.RecordFinding(ctx, RecordFindingInput{
		TaskID: tk.ID, Worker: "test", VulnClass: "SQLæ³¨å…¥", Name: "å¤æµ‹ç›®æ ‡",
		Severity: "high", Summary: "æ‘˜è¦",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM notification_events WHERE finding_id=$1`, f.FindingID) })

	// å»ºä¸€æ¡å¤æµ‹è®°å½•å¹¶ç›´æŽ¥æŽ¨åˆ°å®Œæˆæ€ã€‚
	rt, _, _, err := d.CreateFindingRetest(ctx, f.FindingID, "å¤æ ¸")
	if err != nil {
		t.Fatal(err)
	}
	if rt.ConversationID == nil {
		t.Fatal("å¤æµ‹åº”å…³è”ä¸€ä¸ªä¼šè¯")
	}
	// å¤æµ‹å¿…é¡»å…ˆè¿›å…¥ running æ‰èƒ½è½ç»“è®ºï¼ˆä¸ŽçœŸå®žæµç¨‹ä¸€è‡´ï¼‰ã€‚
	if ok, err := d.StartFindingRetest(ctx, rt.ID); err != nil || !ok {
		t.Fatalf("å¯åŠ¨å¤æµ‹å¤±è´¥: ok=%v err=%v", ok, err)
	}
	if err := d.RecordFindingRetestResult(ctx, *rt.ConversationID, "fixed", "å·²ä¿®å¤", "è¯æ®"); err != nil {
		t.Fatal(err)
	}
	if err := d.FinishFindingRetest(rt.ID, "completed", ""); err != nil {
		t.Fatalf("ç»“æŸå¤æµ‹å¤±è´¥: %v", err)
	}

	var status string
	if err := d.QueryRow(`SELECT status FROM findings WHERE id=$1`, f.FindingID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != FindingFixed {
		t.Fatalf("å¤æµ‹åˆ¤å·²ä¿®å¤åŽçŠ¶æ€åº”ä¸º fixedï¼Œå¾—åˆ° %s", status)
	}

	// å…³é”®æ–­è¨€ï¼šå¿…é¡»æœ‰ä¸€æ¡çŠ¶æ€å˜æ›´äº‹ä»¶ï¼Œä¸” from/to æ­£ç¡®ã€‚
	var snapshot []byte
	err = d.QueryRow(`SELECT snapshot FROM notification_events WHERE finding_id=$1 AND kind=$2 ORDER BY id DESC LIMIT 1`,
		f.FindingID, notify.EventFindingStatusChanged).Scan(&snapshot)
	if err != nil {
		t.Fatalf("å¤æµ‹åˆ¤å·²ä¿®å¤åº”ç™»è®°çŠ¶æ€å˜æ›´æŽ¨é€äº‹ä»¶ï¼ˆå¦åˆ™é…äº† on_status_change çš„æ¸ é“æ”¶ä¸åˆ°ï¼‰: %v", err)
	}
	var snap notify.Snapshot
	if err := json.Unmarshal(snapshot, &snap); err != nil {
		t.Fatal(err)
	}
	if snap.FromStatus != "pending" || snap.ToStatus != FindingFixed {
		t.Fatalf("å¿«ç…§çš„çŠ¶æ€æµè½¬ä¸å¯¹: %s â†’ %s", snap.FromStatus, snap.ToStatus)
	}
	// å¿«ç…§è¦å¸¦æ¸²æŸ“æ‰€éœ€å­—æ®µï¼Œå¦åˆ™æŽ¨é€å‡ºæ¥æ˜¯ç©ºå£³ã€‚
	if snap.Name != "å¤æµ‹ç›®æ ‡" || snap.Severity != "high" {
		t.Fatalf("å¿«ç…§ç¼ºå°‘æ¸²æŸ“å­—æ®µ: %+v", snap)
	}
}

