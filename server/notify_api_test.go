package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Autumn-27/cautiousmemory/db"
	"github.com/Autumn-27/cautiousmemory/notify"
)

// æœ¬æ–‡ä»¶è¦†ç›–æŽ¨é€åŠŸèƒ½çš„ç«¯åˆ°ç«¯è¡Œä¸ºï¼šæ¼æ´žè½åº“ â†’ äº‹ä»¶ â†’ åˆ†æ´¾ â†’ çœŸå‘ HTTPã€‚
//
// ä¸€ä¸ªå®‰å…¨ä¸Šçš„æ³¨æ„ç‚¹ï¼šè¿™äº›ç”¨ä¾‹**ä¸è°ƒç”¨å…¨å±€çš„ Notifier.step()**ï¼Œåªå¯¹è‡ªå·±åˆ›å»ºçš„
// æ¸ é“è°ƒç”¨ stepRealtime/stepDigestã€‚åŽŸå› æ˜¯ step() ä¼šéåŽ†åº“é‡Œæ‰€æœ‰å¯ç”¨æ¸ é“â€”â€”
// åœ¨ä¸€ä¸ªå·²é…å¥½çœŸå®žé’‰é’‰/ä¼å¾®æœºå™¨äººçš„å¼€å‘åº“ä¸Šè·‘æµ‹è¯•ï¼Œå…¨å±€ step ä¼šæŠŠæµ‹è¯•æœŸé—´
// äº§ç”Ÿçš„æ¼æ´žçœŸæŽ¨åˆ°é‚£äº›ç¾¤é‡Œã€‚é€æ¸ é“è°ƒç”¨æŠŠå½±å“é¢ä¸¥æ ¼é™åˆ¶åœ¨æµ‹è¯•è‡ªé€ çš„å‡æŽ¥æ”¶ç«¯ä¸Šã€‚
//
// æ¸…ç†ï¼šç”¨ä¾‹ç»“æŸæ—¶åˆ æŽ‰æœ¬ç”¨ä¾‹äº§ç”Ÿçš„äº‹ä»¶ï¼ˆçº§è”åˆ æŠ•é€’ï¼‰ä¸Žæ¸ é“ï¼Œä¸ç»™çœŸå®žæ¸ é“ç•™ç§¯åŽ‹ã€‚
//
// æ–­è¨€å£å¾„ï¼šstepRealtime/stepDigest ä¸è¿”å›žå€¼ã€å†…éƒ¨è®°æ—¥å¿—ï¼Œå› æ­¤è¿™é‡Œæ–­è¨€çš„æ˜¯
// **å¯è§‚æµ‹çš„å¤–éƒ¨è¡Œä¸º**ï¼ˆå‡æŽ¥æ”¶ç«¯æ”¶åˆ°äº†ä»€ä¹ˆã€æŠ•é€’è¡Œè½åˆ°ä»€ä¹ˆçŠ¶æ€ï¼‰ï¼Œè€Œä¸æ˜¯å‡½æ•°çš„
// è¿”å›žå€¼â€”â€”è¿™æ¯”å¯¹è¿”å›žå€¼æ‰“æ¡©æ›´æŽ¥è¿‘çœŸå®žè°ƒç”¨è·¯å¾„ã€‚

// notifyFixture æ˜¯æœ¬æ–‡ä»¶ç”¨ä¾‹çš„å…¬å…±è£…ç½®ã€‚
type notifyFixture struct {
	s       *Server
	pg      *db.DB
	request func(method, path, body string) *httptest.ResponseRecorder
	n       *Notifier
	// è‡ªå»ºçš„ task/explorationï¼šç”¨ä¾‹æŠŠæ¼æ´žè®°åˆ°è¿™é‡Œï¼Œä¸Žå…¶å®ƒç”¨ä¾‹çš„æ•°æ®éš”ç¦»ã€‚
	taskID int64
	expID  int64
	// cleanupMark ä¹‹åŽäº§ç”Ÿçš„äº‹ä»¶åœ¨æ¸…ç†æ—¶ä¸€å¹¶åˆ é™¤ã€‚
	cleanupMark int64
}

func newNotifyFixture(t *testing.T) *notifyFixture {
	t.Helper()
	// æœ¬æ–‡ä»¶çš„æ‰€æœ‰å‡æŽ¥æ”¶ç«¯éƒ½è·‘åœ¨ 127.0.0.1 ä¸Šï¼Œè€ŒæŠ•é€’é»˜è®¤æ‹’ç»çŽ¯å›žåœ°å€
	// ï¼ˆé˜² SSRF æ‰“åˆ°åŒæœºæœåŠ¡ä¸Žäº‘å…ƒæ•°æ®ï¼‰ã€‚æµ‹è¯•æ˜¾å¼æ‰“å¼€è¿™ä¸ªå¼€å…³ï¼›
	// å®ˆå«ã€Œé»˜è®¤æ‹’ç»ã€çš„è¡Œä¸ºç”± notify åŒ…çš„ ssrf_test.go è¦†ç›–ã€‚
	t.Setenv(notify.AllowLocalTargetsEnv, "1")
	s, _, request := trafficEvidenceServer(t)
	pg := s.m.pg

	// è‡ªå»ºä¸€ä¸ª taskï¼šå…±äº«è£…ç½® trafficEvidenceServer é€ çš„ task æ‹¿ä¸åˆ° exploration idï¼Œ
	// è€Œè®°å½•æ¼æ´žå¿…é¡»æä¾›å®ƒã€‚
	task, err := s.m.CreateTask("é€šçŸ¥æŽ¨é€æµ‹è¯•", "æŽ¨é€è¡Œä¸ºéªŒè¯", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := strconv.ParseInt(task.ID, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pg.Exec(`DELETE FROM tasks WHERE id=$1`, taskID) })

	var mark int64
	if err := pg.QueryRow(`SELECT COALESCE(max(id),0) FROM notification_events`).Scan(&mark); err != nil {
		t.Fatal(err)
	}
	// è®©è£…ç½®è‡ªæˆé—­çŽ¯ï¼šæŠŠå»º fixture ä¹‹å‰å°±å­˜åœ¨çš„äº‹ä»¶ä¸€æ¬¡æ€§æ ‡è®°ä¸ºå·²åˆ†æ´¾ã€‚
	//
	// ä¸ºä»€ä¹ˆå¿…é¡»åšï¼šFanOutPendingEvents æ˜¯**å…¨å±€**çš„ï¼Œä¼šæŠŠåº“é‡Œæ‰€æœ‰æœªåˆ†æ´¾äº‹ä»¶
	// å±•å¼€åˆ°æ‰€æœ‰åŒ¹é…æ¸ é“ä¸Šã€‚è€Œå…±äº«è£…ç½® trafficEvidenceServer è‡ªå·±å°±ä¼šè®°ä¸€æ¡æ¼æ´ž
	// ï¼ˆæ­£æ˜¯å®ƒè¿”å›žçš„é‚£ä¸ªåˆå§‹ findingï¼‰ï¼Œå…¶å®ƒç”¨ä¾‹ä¹Ÿå¯èƒ½æœ‰æ®‹ç•™ã€‚ä¸éš”ç¦»çš„è¯ï¼Œ
	// è¿™äº›æ‚æ•£äº‹ä»¶ä¼šè¢«åˆ†æ´¾åˆ°æœ¬ç”¨ä¾‹çš„æ¸ é“ä¸Šï¼Œè®©ã€Œåº”æœ‰ N æ¡æŠ•é€’ã€è¿™ç±»æ–­è¨€
	// æ—¶å¯¹æ—¶é”™â€”â€”è€Œä¸”é”™æ³•å–å†³äºŽç”¨ä¾‹æ‰§è¡Œé¡ºåºï¼Œæ¯”ç›´æŽ¥å¤±è´¥æ›´éš¾æŸ¥ã€‚
	if _, err := pg.Exec(`UPDATE notification_events SET fanned_out = true WHERE id <= $1 AND NOT fanned_out`, mark); err != nil {
		t.Fatal(err)
	}

	f := &notifyFixture{s: s, pg: pg, request: request, n: newNotifier(s), taskID: taskID, expID: task.ExpID, cleanupMark: mark}
	t.Cleanup(func() {
		if _, err := pg.Exec(`DELETE FROM notification_events WHERE id > $1`, f.cleanupMark); err != nil {
			t.Logf("æ¸…ç†é€šçŸ¥äº‹ä»¶å¤±è´¥: %v", err)
		}
	})
	// æ€»å¼€å…³å¿…é¡»æ˜¯å¼€çš„ï¼ˆå…¶å®ƒç”¨ä¾‹å¯èƒ½å…³è¿‡å®ƒï¼‰ã€‚
	if err := pg.SetBool(settingNotifyEnabled, true); err != nil {
		t.Fatal(err)
	}
	return f
}

// record èµ°çœŸå®žçš„è¯æ®å†™å…¥è·¯å¾„è½ä¸€æ¡æ¼æ´žï¼Œè¿”å›ž finding idã€‚
// è¿™æ¡è·¯ä¼šåœ¨**åŒä¸€äº‹åŠ¡**é‡Œç™»è®°æŽ¨é€äº‹ä»¶â€”â€”æ­£æ˜¯æœ¬åŠŸèƒ½çš„æŒ‚ç‚¹ã€‚
func (f *notifyFixture) record(t *testing.T, vulnclass, severity string) int64 {
	t.Helper()
	out, err := f.s.evidenceStore().Record(context.Background(), db.RecordFindingInput{
		TaskID:        f.taskID,
		ExplorationID: f.expID,
		Worker:        "test",
		VulnClass:     vulnclass,
		Name:          vulnclass,
		Severity:      severity,
		Summary:       vulnclass + " çš„æ‘˜è¦",
		Evidence:      "poc",
	}, nil)
	if err != nil {
		t.Fatalf("è®°å½•æ¼æ´žå¤±è´¥: %v", err)
	}
	return out.FindingID
}

// channel è¯»å›žæ¸ é“é…ç½®ï¼ˆä¾›é€æ¸ é“è°ƒç”¨ stepX ä½¿ç”¨ï¼‰ã€‚
func (f *notifyFixture) channel(t *testing.T, id int64) *db.NotificationChannel {
	t.Helper()
	ch, err := f.pg.NotificationChannelByID(context.Background(), id)
	if err != nil {
		t.Fatalf("è¯»æ¸ é“å¤±è´¥: %v", err)
	}
	return ch
}

// deliver åˆ†æ´¾äº‹ä»¶å¹¶åªå¯¹æŒ‡å®šæ¸ é“è·‘ä¸€è½®æŠ•é€’ã€‚
func (f *notifyFixture) deliver(t *testing.T, chID int64, baseURL string) {
	t.Helper()
	ctx := context.Background()
	if _, _, err := f.pg.FanOutPendingEvents(ctx, 500); err != nil {
		t.Fatalf("åˆ†æ´¾å¤±è´¥: %v", err)
	}
	f.n.stepRealtime(ctx, f.channel(t, chID), 50, baseURL)
}

// createChannel é€šè¿‡ HTTP æŽ¥å£å»ºæ¸ é“ï¼Œé¡ºå¸¦è¦†ç›–æŽ¥å£è‡ªèº«çš„æ ¡éªŒè·¯å¾„ã€‚
func (f *notifyFixture) createChannel(t *testing.T, payload map[string]any) int64 {
	t.Helper()
	raw, _ := json.Marshal(payload)
	r := f.request("POST", "/api/notify/channels", string(raw))
	if r.Code != 200 {
		t.Fatalf("å»ºæ¸ é“å¤±è´¥ %d: %s", r.Code, r.Body)
	}
	var res struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &res); err != nil || res.ID == 0 {
		t.Fatalf("å»ºæ¸ é“è¿”å›žå¼‚å¸¸: %s (%v)", r.Body, err)
	}
	t.Cleanup(func() { f.pg.Exec(`DELETE FROM notification_channels WHERE id=$1`, res.ID) })
	return res.ID
}

// fakeWebhook æ˜¯è®°å½•æ”¶åˆ°çš„è¯·æ±‚ä½“çš„å‡æŽ¥æ”¶ç«¯ã€‚
type fakeWebhook struct {
	*httptest.Server
	mu     sync.Mutex
	bodies []map[string]any
}

func newFakeWebhook(t *testing.T) *fakeWebhook {
	t.Helper()
	f := &fakeWebhook{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.bodies = append(f.bodies, body)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeWebhook) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.bodies)
}

func (f *fakeWebhook) body(t *testing.T, i int) map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= len(f.bodies) {
		t.Fatalf("å‡æŽ¥æ”¶ç«¯åªæ”¶åˆ° %d æ¡è¯·æ±‚ï¼Œå–ä¸åˆ°ç¬¬ %d æ¡", len(f.bodies), i)
	}
	return f.bodies[i]
}

func (f *fakeWebhook) last(t *testing.T) map[string]any {
	t.Helper()
	if f.count() == 0 {
		t.Fatal("å‡æŽ¥æ”¶ç«¯æ²¡æœ‰æ”¶åˆ°ä»»ä½•è¯·æ±‚")
	}
	return f.body(t, f.count()-1)
}

// markdownText ä»Žè¯·æ±‚ä½“é‡Œå–å‡ºæ­£æ–‡ï¼Œå…¼å®¹å„å®¶çš„å­—æ®µåå·®å¼‚ï¼š
// é’‰é’‰ markdown ç”¨ `text`ã€ActionCard ç”¨ `text`ã€ä¼ä¸šå¾®ä¿¡ markdown ç”¨ `content`ã€‚
func markdownText(t *testing.T, body map[string]any) string {
	t.Helper()
	for _, key := range []string{"markdown", "actionCard"} {
		section, ok := body[key].(map[string]any)
		if !ok {
			continue
		}
		for _, field := range []string{"text", "content"} {
			if s, ok := section[field].(string); ok && s != "" {
				return s
			}
		}
	}
	t.Fatalf("è¯·æ±‚ä½“é‡Œæ²¡æœ‰å¯è¯†åˆ«çš„æ­£æ–‡: %v", body)
	return ""
}

// agePendingBatch æŠŠè¯¥æ¸ é“çš„å¾…å‘æŠ•é€’å‚¬è€ï¼Œç”¨äºŽæµ‹è¯•æ±‡æ€»æ‰¹æ¬¡åˆ°æœŸã€‚
func (f *notifyFixture) agePendingBatch(t *testing.T, chID int64) {
	t.Helper()
	if _, err := f.pg.Exec(`UPDATE notification_deliveries SET created_at = now() - interval '2 hours'
WHERE channel_id=$1 AND state=$2`, chID, db.NotifyStatePending); err != nil {
		t.Fatal(err)
	}
}

func TestNotifyEndToEndRealtimeDelivery(t *testing.T) {
	f := newNotifyFixture(t)
	hook := newFakeWebhook(t)
	chID := f.createChannel(t, map[string]any{
		"name":   "å®žæ—¶æŽ¨é€",
		"kind":   notify.KindDingTalk,
		"config": map[string]any{"webhook": hook.URL},
	})
	f.record(t, "SQLæ³¨å…¥", "high")
	f.deliver(t, chID, "")

	if hook.count() != 1 {
		t.Fatalf("åº”å‘å‡º 1 æ¡æ¶ˆæ¯ï¼Œå®žé™… %d", hook.count())
	}
	text := markdownText(t, hook.last(t))
	for _, want := range []string{"SQLæ³¨å…¥", "é«˜å±", "æ‘˜è¦"} {
		if !strings.Contains(text, want) {
			t.Fatalf("æ¶ˆæ¯æ­£æ–‡ç¼ºå°‘ %q:\n%s", want, text)
		}
	}
	// æŠ•é€’åº”æµè½¬ä¸º sentã€‚
	var pending int
	if err := f.pg.QueryRow(`SELECT count(*) FROM notification_deliveries WHERE channel_id=$1 AND state <> $2`,
		chID, db.NotifyStateSent).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 0 {
		t.Fatalf("æŠ•é€’åŽä»æœ‰ %d æ¡æœªæ ‡è®° sent", pending)
	}
}

func TestNotifyChannelAPIMasksSecretsAndPreservesOnUpdate(t *testing.T) {
	f := newNotifyFixture(t)
	chID := f.createChannel(t, map[string]any{
		"name":   "æŽ©ç ç”¨ä¾‹",
		"kind":   notify.KindDingTalk,
		"config": map[string]any{"webhook": "https://oapi.dingtalk.com/robot/send?access_token=abc123456", "secret": "SECabcdef123456"},
	})

	r := f.request("GET", "/api/notify/channels", "")
	if r.Code != 200 {
		t.Fatalf("åˆ—æ¸ é“å¤±è´¥ %d: %s", r.Code, r.Body)
	}
	if strings.Contains(r.Body.String(), "abc123456") || strings.Contains(r.Body.String(), "SECabcdef123456") {
		t.Fatalf("æŽ¥å£å›žæ˜¾æ³„éœ²äº†å‡­æ®: %s", r.Body)
	}
	var listed struct {
		Channels []struct {
			ID         int64          `json:"id"`
			Config     map[string]any `json:"config"`
			SecretKeys []string       `json:"secret_keys"`
		} `json:"channels"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	var mine *struct {
		ID         int64          `json:"id"`
		Config     map[string]any `json:"config"`
		SecretKeys []string       `json:"secret_keys"`
	}
	for i := range listed.Channels {
		if listed.Channels[i].ID == chID {
			mine = &listed.Channels[i]
		}
	}
	if mine == nil {
		t.Fatal("æ–°å»ºçš„æ¸ é“æœªå‡ºçŽ°åœ¨åˆ—è¡¨é‡Œ")
	}
	if !notify.IsMasked(fmt.Sprint(mine.Config["webhook"])) || !notify.IsMasked(fmt.Sprint(mine.Config["secret"])) {
		t.Fatalf("å‡­æ®å­—æ®µåº”ä¸ºæŽ©ç å€¼: %v", mine.Config)
	}
	if len(mine.SecretKeys) == 0 {
		t.Fatal("æŽ¥å£åº”å‘ŠçŸ¥å‰ç«¯å“ªäº›å­—æ®µæ˜¯å‡­æ®")
	}

	// PATCH åªæ”¹å + å›žä¼ æŽ©ç å‡­æ®ï¼šçœŸå‡­æ®å¿…é¡»åŽŸæ ·ä¿ç•™ã€‚
	body, _ := json.Marshal(map[string]any{
		"name":   "æ”¹ååŽ",
		"config": map[string]any{"webhook": fmt.Sprint(mine.Config["webhook"]), "secret": fmt.Sprint(mine.Config["secret"])},
	})
	if r := f.request("PATCH", fmt.Sprintf("/api/notify/channels/%d", chID), string(body)); r.Code != 200 {
		t.Fatalf("æ›´æ–°å¤±è´¥ %d: %s", r.Code, r.Body)
	}
	cfg := f.channelConfig(t, chID)
	if cfg["webhook"] != "https://oapi.dingtalk.com/robot/send?access_token=abc123456" {
		t.Fatalf("æŽ©ç å›žä¼ æŠŠçœŸå‡­æ®è¦†ç›–äº†: %v", cfg["webhook"])
	}
	if cfg["secret"] != "SECabcdef123456" {
		t.Fatalf("æŽ©ç å›žä¼ æŠŠ secret è¦†ç›–äº†: %v", cfg["secret"])
	}
	if f.channel(t, chID).Name != "æ”¹ååŽ" {
		t.Fatal("åå­—æœªæ›´æ–°")
	}

	// æ˜¾å¼æ¸…ç©º secret åº”ç”Ÿæ•ˆï¼ˆåŒºåˆ«äºŽã€Œå›žä¼ æŽ©ç =ä¿æŒä¸å˜ã€ï¼‰ã€‚
	body, _ = json.Marshal(map[string]any{"config": map[string]any{"secret": ""}})
	if r := f.request("PATCH", fmt.Sprintf("/api/notify/channels/%d", chID), string(body)); r.Code != 200 {
		t.Fatalf("æ¸…ç©º secret å¤±è´¥ %d: %s", r.Code, r.Body)
	}
	if _, still := f.channelConfig(t, chID)["secret"]; still {
		t.Fatal("ç©ºä¸²åº”æ¸…ç©º secret")
	}
}

func (f *notifyFixture) channelConfig(t *testing.T, id int64) map[string]any {
	t.Helper()
	var cfg map[string]any
	if err := json.Unmarshal(f.channel(t, id).Config, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestNotifyChannelAPICreateValidation(t *testing.T) {
	f := newNotifyFixture(t)
	cases := []struct {
		name    string
		payload map[string]any
		wantSub string
	}{
		{"ç±»åž‹éžæ³•", map[string]any{"name": "x", "kind": "nope", "config": map[string]any{}}, "æ¸ é“ç±»åž‹æ— æ•ˆ"},
		{"ç¼ºåç§°", map[string]any{"kind": notify.KindDingTalk, "config": map[string]any{"webhook": "https://e.com/h"}}, "ç¼ºå°‘æ¸ é“åç§°"},
		{"ç¼º webhook", map[string]any{"name": "x", "kind": notify.KindDingTalk, "config": map[string]any{}}, "Webhook"},
		{"webhook åè®®éžæ³•", map[string]any{"name": "x", "kind": notify.KindDingTalk, "config": map[string]any{"webhook": "file:///etc/passwd"}}, "Webhook åœ°å€æ— æ•ˆ"},
		{"æ¨¡å¼éžæ³•", map[string]any{"name": "x", "kind": notify.KindDingTalk, "mode": "sometimes", "config": map[string]any{"webhook": "https://e.com/h"}}, "æŽ¨é€æ¨¡å¼æ— æ•ˆ"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(tc.payload)
			r := f.request("POST", "/api/notify/channels", string(raw))
			if r.Code != 400 {
				t.Fatalf("åº”è¿”å›ž 400ï¼Œå¾—åˆ° %d: %s", r.Code, r.Body)
			}
			if !strings.Contains(r.Body.String(), tc.wantSub) {
				t.Fatalf("é”™è¯¯ä¿¡æ¯åº”æåˆ° %qï¼Œå¾—åˆ° %s", tc.wantSub, r.Body)
			}
		})
	}
	if r := f.request("DELETE", "/api/notify/channels/99999999", ""); r.Code != 404 {
		t.Fatalf("åˆ é™¤ä¸å­˜åœ¨çš„æ¸ é“åº” 404ï¼Œå¾—åˆ° %d", r.Code)
	}
}

func TestNotifyFilterBlocksBelowThreshold(t *testing.T) {
	f := newNotifyFixture(t)
	hook := newFakeWebhook(t)
	chID := f.createChannel(t, map[string]any{
		"name":   "ä»…ä¸¥é‡",
		"kind":   notify.KindDingTalk,
		"config": map[string]any{"webhook": hook.URL},
		"filter": map[string]any{"min_severity": "critical"},
	})
	f.record(t, "ä½Žå±é—®é¢˜", "low")
	if _, _, err := f.pg.FanOutPendingEvents(context.Background(), 500); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.pg.QueryRow(`SELECT count(*) FROM notification_deliveries WHERE channel_id=$1`, chID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("ä½ŽäºŽé˜ˆå€¼çš„æ¼æ´žä¸è¯¥äº§ç”ŸæŠ•é€’ï¼Œå¾—åˆ° %d æ¡", n)
	}
	f.n.stepRealtime(context.Background(), f.channel(t, chID), 50, "")
	if hook.count() != 0 {
		t.Fatal("è¢«è¿‡æ»¤çš„æ¼æ´žä¸åº”å‘å‡ºæ¶ˆæ¯")
	}
}

func TestNotifyDigestBatchesMultipleFindingsIntoOneMessage(t *testing.T) {
	f := newNotifyFixture(t)
	hook := newFakeWebhook(t)
	chID := f.createChannel(t, map[string]any{
		"name":   "æ±‡æ€»æŽ¨é€",
		"kind":   notify.KindDingTalk,
		"mode":   db.NotifyModeDigest,
		"config": map[string]any{"webhook": hook.URL},
	})
	for i := 0; i < 3; i++ {
		f.record(t, fmt.Sprintf("æ±‡æ€»æ¼æ´ž%d", i+1), "high")
	}
	ctx := context.Background()
	if _, _, err := f.pg.FanOutPendingEvents(ctx, 500); err != nil {
		t.Fatal(err)
	}
	ch := f.channel(t, chID)

	// æœªåˆ°æœŸï¼šä¸å‘ã€‚
	f.n.stepDigest(ctx, ch, 50, "")
	if hook.count() != 0 {
		t.Fatal("æ±‡æ€»æ‰¹æ¬¡æœªåˆ°æœŸå°±å‘äº†")
	}

	// å‚¬è€æ‰¹æ¬¡åŽï¼šä¸‰æ¡åˆæˆä¸€æ¡æ¶ˆæ¯ã€‚
	f.agePendingBatch(t, chID)
	f.n.stepDigest(ctx, ch, 50, "")
	if got := hook.count(); got != 1 {
		t.Fatalf("ä¸‰æ¡åº”æ±‡æ€»æˆä¸€æ¡æ¶ˆæ¯ï¼Œå®žé™…å‘äº† %d æ¡", got)
	}
	text := markdownText(t, hook.last(t))
	if !strings.Contains(text, "è¿‘") || !strings.Contains(text, "3 ä¸ªæ¼æ´ž") {
		t.Fatalf("æ±‡æ€»æ¶ˆæ¯ç¼ºå°‘æ¡æ•°/æ—¶é—´çª—æ–‡æ¡ˆ:\n%s", text)
	}
	for i := 1; i <= 3; i++ {
		if !strings.Contains(text, fmt.Sprintf("æ±‡æ€»æ¼æ´ž%d", i)) {
			t.Fatalf("æ±‡æ€»æ¶ˆæ¯ç¼ºå°‘ç¬¬ %d æ¡:\n%s", i, text)
		}
	}
	// åŒä¸€æ‰¹æ¬¡åº”å…±äº« batch_idã€‚
	var distinct, total int
	if err := f.pg.QueryRow(`SELECT count(DISTINCT batch_id), count(*) FROM notification_deliveries WHERE channel_id=$1`, chID).Scan(&distinct, &total); err != nil {
		t.Fatal(err)
	}
	if total != 3 || distinct != 1 {
		t.Fatalf("ä¸‰æ¡æŠ•é€’åº”å…±äº«ä¸€ä¸ª batch_idï¼Œå¾—åˆ° distinct=%d total=%d", distinct, total)
	}
}

func TestNotifyDisabledChannelDoesNotSend(t *testing.T) {
	f := newNotifyFixture(t)
	hook := newFakeWebhook(t)
	chID := f.createChannel(t, map[string]any{
		"name":    "åœç”¨æ¸ é“",
		"kind":    notify.KindDingTalk,
		"enabled": false,
		"config":  map[string]any{"webhook": hook.URL},
	})
	f.record(t, "åœç”¨æœŸé—´çš„æ¼æ´ž", "critical")
	if _, _, err := f.pg.FanOutPendingEvents(context.Background(), 500); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.pg.QueryRow(`SELECT count(*) FROM notification_deliveries WHERE channel_id=$1`, chID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("åœç”¨æ¸ é“ä¸è¯¥äº§ç”ŸæŠ•é€’ï¼Œå¾—åˆ° %d æ¡", n)
	}
}

func TestNotifyStatusChangeDelivery(t *testing.T) {
	f := newNotifyFixture(t)
	hook := newFakeWebhook(t)
	chID := f.createChannel(t, map[string]any{
		"name":   "çŠ¶æ€å˜æ›´è®¢é˜…",
		"kind":   notify.KindDingTalk,
		"config": map[string]any{"webhook": hook.URL},
		"filter": map[string]any{"on_status_change": true},
	})
	finding := f.record(t, "çŠ¶æ€å˜æ›´ç”¨ä¾‹", "high")
	r := f.request("PATCH", fmt.Sprintf("/api/exploration/findings/%d", finding), `{"status":"fixed"}`)
	if r.Code != 200 {
		t.Fatalf("æ”¹çŠ¶æ€å¤±è´¥ %d: %s", r.Code, r.Body)
	}
	f.deliver(t, chID, "")

	// åº”æœ‰ä¸¤æ¡ï¼šfixed é‚£ä¸€æ¡æ˜¯çŠ¶æ€å˜æ›´ï¼›finding_created é‚£æ¡ä¹Ÿå¯èƒ½åœ¨åŒä¸€è½®å‘å‡ºã€‚
	// çŠ¶æ€å˜æ›´çš„å®žé™…ä¸Šæ›´æ™šåˆ›å»ºï¼Œä½†ä¸ä¾èµ–é¡ºåºï¼Œå…¨é‡æ‰¾ã€‚
	found := false
	for i := 0; i < hook.count(); i++ {
		text := markdownText(t, hook.body(t, i))
		if strings.Contains(text, "çŠ¶æ€å˜æ›´") && strings.Contains(text, "å·²ä¿®å¤") {
			found = true
		}
	}
	if !found {
		t.Fatalf("æ²¡æœ‰æ”¶åˆ°å«ã€ŒçŠ¶æ€å˜æ›´ â†’ å·²ä¿®å¤ã€çš„æ¶ˆæ¯ï¼ˆå…± %d æ¡ï¼‰", hook.count())
	}
}

func TestNotifyStatusChangeSuppressedByDefault(t *testing.T) {
	f := newNotifyFixture(t)
	hook := newFakeWebhook(t)
	chID := f.createChannel(t, map[string]any{
		"name":   "ä¸è®¢é˜…çŠ¶æ€å˜æ›´",
		"kind":   notify.KindDingTalk,
		"config": map[string]any{"webhook": hook.URL},
	})
	finding := f.record(t, "ä¸è®¢é˜…å˜æ›´", "high")
	if r := f.request("PATCH", fmt.Sprintf("/api/exploration/findings/%d", finding), `{"status":"false_positive"}`); r.Code != 200 {
		t.Fatalf("æ”¹çŠ¶æ€å¤±è´¥ %d: %s", r.Code, r.Body)
	}
	if _, _, err := f.pg.FanOutPendingEvents(context.Background(), 500); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.pg.QueryRow(`SELECT count(*) FROM notification_deliveries d
JOIN notification_events e ON e.id = d.event_id
WHERE d.channel_id=$1 AND e.kind=$2`, chID, notify.EventFindingStatusChanged).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("æœªè®¢é˜…çŠ¶æ€å˜æ›´çš„æ¸ é“ä¸è¯¥æ”¶åˆ°çŠ¶æ€å˜æ›´æŠ•é€’ï¼Œå¾—åˆ° %d æ¡", n)
	}
}

func TestNotifyTestMessageEndpoint(t *testing.T) {
	f := newNotifyFixture(t)
	hook := newFakeWebhook(t)
	chID := f.createChannel(t, map[string]any{
		"name":   "æµ‹è¯•å‘é€",
		"kind":   notify.KindDingTalk,
		"config": map[string]any{"webhook": hook.URL},
	})
	if r := f.request("POST", fmt.Sprintf("/api/notify/channels/%d/test", chID), ""); r.Code != 200 {
		t.Fatalf("æµ‹è¯•å‘é€å¤±è´¥ %d: %s", r.Code, r.Body)
	}
	if hook.count() != 1 {
		t.Fatalf("å‡æŽ¥æ”¶ç«¯åº”æ”¶åˆ° 1 æ¡æµ‹è¯•æ¶ˆæ¯ï¼Œå¾—åˆ° %d", hook.count())
	}
	// æµ‹è¯•æ¶ˆæ¯å¿…é¡»ä¸€çœ¼èƒ½çœ‹å‡ºæ˜¯æµ‹è¯•ï¼Œä¸èƒ½è¢«è¯¯å½“æˆçœŸå®žæ¼æ´žã€‚
	if text := markdownText(t, hook.last(t)); !strings.Contains(text, "æµ‹è¯•") {
		t.Fatalf("æµ‹è¯•æ¶ˆæ¯åº”æ ‡æ˜Žæ˜¯æµ‹è¯•: %s", text)
	}
	// é…ç½®åæŽ‰æ—¶åº”æŠŠæ¸ é“çš„åŽŸå§‹é”™è¯¯å¦‚å®žå›žç»™ç”¨æˆ·ã€‚
	badID := f.createChannel(t, map[string]any{
		"name":   "ååœ°å€",
		"kind":   notify.KindDingTalk,
		"config": map[string]any{"webhook": "http://127.0.0.1:1/hook"},
	})
	if r := f.request("POST", fmt.Sprintf("/api/notify/channels/%d/test", badID), ""); r.Code != 502 {
		t.Fatalf("æŠ•é€’å¤±è´¥åº”å›ž 502ï¼Œå¾—åˆ° %d: %s", r.Code, r.Body)
	}
}

func TestNotifyDeliveriesHistoryAndRetry(t *testing.T) {
	f := newNotifyFixture(t)
	// æŒ‡å‘å¿…ç„¶å¤±è´¥çš„åœ°å€ï¼Œåˆ¶é€  failed æŠ•é€’ã€‚
	chID := f.createChannel(t, map[string]any{
		"name":   "å¤±è´¥é‡è¯•",
		"kind":   notify.KindDingTalk,
		"config": map[string]any{"webhook": "http://127.0.0.1:1/hook"},
	})
	f.record(t, "ä¼šå¤±è´¥çš„æŽ¨é€", "high")
	ctx := context.Background()
	if _, _, err := f.pg.FanOutPendingEvents(ctx, 500); err != nil {
		t.Fatal(err)
	}
	ch := f.channel(t, chID)
	// è¿žæŠ•åˆ°è€—å°½é‡è¯•é¢„ç®—ã€‚
	for i := 0; i < db.MaxNotifyAttempts; i++ {
		f.n.stepRealtime(ctx, ch, 50, "")
		if _, err := f.pg.Exec(`UPDATE notification_deliveries SET next_attempt_at = now() - interval '1 minute' WHERE channel_id=$1`, chID); err != nil {
			t.Fatal(err)
		}
	}
	var state string
	if err := f.pg.QueryRow(`SELECT state FROM notification_deliveries WHERE channel_id=$1`, chID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != db.NotifyStateFailed {
		t.Fatalf("é‡è¯•è€—å°½åŽåº”ä¸º failedï¼Œå¾—åˆ° %s", state)
	}

	r := f.request("GET", fmt.Sprintf("/api/notify/deliveries?channel_id=%d&state=failed", chID), "")
	if r.Code != 200 {
		t.Fatalf("æŸ¥åŽ†å²å¤±è´¥ %d: %s", r.Code, r.Body)
	}
	var hist struct {
		Deliveries []struct {
			ID        int64  `json:"id"`
			State     string `json:"state"`
			LastError string `json:"last_error"`
			Attempts  int    `json:"attempts"`
			Title     string `json:"title"`
		} `json:"deliveries"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &hist); err != nil {
		t.Fatal(err)
	}
	if hist.Total != 1 || len(hist.Deliveries) != 1 {
		t.Fatalf("åº”æŸ¥åˆ° 1 æ¡å¤±è´¥æŠ•é€’ï¼Œå¾—åˆ° total=%d len=%d", hist.Total, len(hist.Deliveries))
	}
	if hist.Deliveries[0].LastError == "" {
		t.Fatal("åŽ†å²é‡Œåº”å¸¦ä¸Šå¤±è´¥åŽŸå› ï¼Œå¦åˆ™ç”¨æˆ·æ— æ³•æŽ’æŸ¥")
	}
	if hist.Deliveries[0].Attempts < db.MaxNotifyAttempts {
		t.Fatalf("å°è¯•æ¬¡æ•°åº”è¢«è®°å½•ï¼Œå¾—åˆ° %d", hist.Deliveries[0].Attempts)
	}
	if hist.Deliveries[0].Title != "ä¼šå¤±è´¥çš„æŽ¨é€" {
		t.Fatalf("åŽ†å²åº”å¸¦å‡ºæ¼æ´žæ ‡é¢˜ï¼Œå¾—åˆ° %q", hist.Deliveries[0].Title)
	}

	// æ‰‹åŠ¨é‡å‘ï¼šåº”å›žåˆ° pending ä¸”æ¸…é›¶è®¡æ•°ã€‚
	if r := f.request("POST", fmt.Sprintf("/api/notify/deliveries/%d/retry", hist.Deliveries[0].ID), ""); r.Code != 200 {
		t.Fatalf("é‡å‘å¤±è´¥ %d: %s", r.Code, r.Body)
	}
	var attempts int
	if err := f.pg.QueryRow(`SELECT state, attempts FROM notification_deliveries WHERE id=$1`, hist.Deliveries[0].ID).Scan(&state, &attempts); err != nil {
		t.Fatal(err)
	}
	if state != db.NotifyStatePending || attempts != 0 {
		t.Fatalf("é‡å‘åŽåº”ä¸º pending ä¸” attempts=0ï¼Œå¾—åˆ° %s/%d", state, attempts)
	}
}

func TestNotifyMetaAndSettingsRoundTrip(t *testing.T) {
	f := newNotifyFixture(t)
	r := f.request("GET", "/api/notify/meta", "")
	if r.Code != 200 {
		t.Fatalf("meta å¤±è´¥: %s", r.Body)
	}
	var meta struct {
		Kinds []struct {
			Kind       string   `json:"kind"`
			SecretKeys []string `json:"secret_keys"`
		} `json:"kinds"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &meta); err != nil {
		t.Fatal(err)
	}
	if len(meta.Kinds) != len(notify.Kinds()) {
		t.Fatalf("meta åº”åˆ—å‡ºå…¨éƒ¨ %d ä¸ªæ¸ é“ï¼Œå¾—åˆ° %d", len(notify.Kinds()), len(meta.Kinds))
	}
	for _, k := range meta.Kinds {
		if len(k.SecretKeys) == 0 {
			t.Errorf("æ¸ é“ %s æœªä¸ŠæŠ¥å‡­æ®å­—æ®µ", k.Kind)
		}
	}

	// ä¸‰é¡¹å…¨å±€è®¾ç½®å¾€è¿”ã€‚å°¾éƒ¨æ–œæ åº”è¢«è§„èŒƒåŒ–æŽ‰ï¼Œå¦åˆ™å›žé“¾ä¼šæ‹¼å‡º "//function/..."ã€‚
	if r := f.request("PUT", "/api/settings", `{"notify_public_base_url":"https://CAUTIOUSMEMORY.example.com/","notify_digest_interval_min":15,"notify_enabled":true}`); r.Code != 200 {
		t.Fatalf("å†™è®¾ç½®å¤±è´¥ %d: %s", r.Code, r.Body)
	}
	t.Cleanup(func() {
		f.pg.Exec(`DELETE FROM settings WHERE key IN ($1,$2)`, settingNotifyPublicBaseURL, settingNotifyDigestMinutes)
	})
	payload := f.s.settingsPayload()
	if payload["notify_public_base_url"] != "https://CAUTIOUSMEMORY.example.com" {
		t.Fatalf("å›žé“¾åœ°å€æœªè§„èŒƒåŒ–: %v", payload["notify_public_base_url"])
	}
	if payload["notify_digest_interval_min"] != 15 {
		t.Fatalf("æ±‡æ€»å‘¨æœŸæœªç”Ÿæ•ˆ: %v", payload["notify_digest_interval_min"])
	}

	// éžæ³•å€¼åº”è¢«æ‹’ã€‚
	for _, body := range []string{
		`{"notify_public_base_url":"ftp://x"}`,
		`{"notify_digest_interval_min":0}`,
		`{"notify_digest_interval_min":99999}`,
	} {
		if r := f.request("PUT", "/api/settings", body); r.Code != 400 {
			t.Errorf("%s åº”è¿”å›ž 400ï¼Œå¾—åˆ° %d", body, r.Code)
		}
	}
}

// TestNotifyDeepLinkUsesPublicBaseURL è¦†ç›–å›žé“¾æ‹¼æŽ¥ï¼šé…äº† public_base_url æ—¶
// å•æ¡æ¶ˆæ¯å¿…é¡»ç”¨å¸¦æŒ‰é’®çš„ ActionCardï¼Œä¸”é“¾æŽ¥æŒ‡å‘æ¼æ´žè¯¦æƒ…é¡µã€‚
func TestNotifyDeepLinkUsesPublicBaseURL(t *testing.T) {
	f := newNotifyFixture(t)
	hook := newFakeWebhook(t)
	chID := f.createChannel(t, map[string]any{
		"name":   "å›žé“¾",
		"kind":   notify.KindDingTalk,
		"config": map[string]any{"webhook": hook.URL},
	})
	finding := f.record(t, "å¸¦å›žé“¾çš„æ¼æ´ž", "high")
	f.deliver(t, chID, "https://CAUTIOUSMEMORY.example.com")

	body := hook.last(t)
	card, _ := body["actionCard"].(map[string]any)
	if card == nil {
		t.Fatalf("æœ‰å›žé“¾æ—¶åº”ç”¨ ActionCardï¼Œå¾—åˆ° msgtype=%v", body["msgtype"])
	}
	want := fmt.Sprintf("https://CAUTIOUSMEMORY.example.com/function/findings/detail?id=%d", finding)
	if card["singleURL"] != want {
		t.Fatalf("å›žé“¾ä¸å¯¹\næœŸæœ› %s\nå¾—åˆ° %v", want, card["singleURL"])
	}
}

// TestNotifyNoDeepLinkWithoutBaseURL åå‘è¦†ç›–ï¼šæ²¡é…å¤–éƒ¨åœ°å€æ—¶ä¸è¯¥äº§ç”Ÿåé“¾æŽ¥
// ï¼ˆæ¯”å¦‚æŒ‡å‘ localhost æˆ–ç›¸å¯¹è·¯å¾„ï¼‰ï¼Œåº”é€€å›žçº¯ markdownã€‚
func TestNotifyNoDeepLinkWithoutBaseURL(t *testing.T) {
	f := newNotifyFixture(t)
	hook := newFakeWebhook(t)
	chID := f.createChannel(t, map[string]any{
		"name":   "æ— å›žé“¾",
		"kind":   notify.KindDingTalk,
		"config": map[string]any{"webhook": hook.URL},
	})
	f.record(t, "æ— å›žé“¾çš„æ¼æ´ž", "high")
	f.deliver(t, chID, "")

	body := hook.last(t)
	if body["msgtype"] != "markdown" {
		t.Fatalf("æœªé…å¤–éƒ¨åœ°å€æ—¶åº”å‘ markdownï¼Œå¾—åˆ° %v", body["msgtype"])
	}
	if text := markdownText(t, body); strings.Contains(text, "æŸ¥çœ‹è¯¦æƒ…") {
		t.Fatalf("æœªé…å¤–éƒ¨åœ°å€æ—¶ä¸è¯¥å‡ºçŽ°è¯¦æƒ…é“¾æŽ¥:\n%s", text)
	}
}

// TestNotifyDigestSegmentsAndDefersRemainder æ˜¯ã€Œé™é»˜ä¸¢å¤±ã€ä¿®å¤çš„ç«¯åˆ°ç«¯è¯æ®ã€‚
//
// æ±‡æ€»æ¶ˆæ¯å—æ¸ é“é•¿åº¦ä¸Šé™çº¦æŸï¼ˆä¼å¾® 4096 å­—èŠ‚ï¼‰ï¼Œä¸€æ‰¹è£…ä¸ä¸‹æ—¶å¿…é¡»**æŒ‰æ•´æ¡**åˆ‡åˆ†ï¼š
// è£…è¿›æœ¬æ¡çš„é‚£äº›æ ‡è®°å·²é€è¾¾ï¼Œå…¶ä½™å›žåˆ°é˜Ÿåˆ—ç­‰ä¸‹ä¸€æ¡ã€‚æ›¾ç»çš„å®žçŽ°æ˜¯æŠŠæ•´æ‰¹æ ‡è®°
// æˆåŠŸâ€”â€”è¢«æˆªæŽ‰çš„é‚£äº›æ—¢ä¸åœ¨æ¶ˆæ¯é‡Œã€ä¹Ÿä¸åœ¨å¤±è´¥åˆ—è¡¨é‡Œï¼ŒæŠ•é€’åŽ†å²è¿˜æ˜¾ç¤ºæˆåŠŸï¼Œ
// æ¼æ´žå°±è¿™ä¹ˆæ²¡äº†ã€‚
//
// æ–­è¨€å››ä»¶äº‹ï¼šâ‘  åªæ ‡è®°äº†å®žé™…è£…ä¸‹çš„æ¡æ•° â‘¡ å…¶ä½™ä»æ˜¯å¾…å‘ â‘¢ è¢«æŽ¨è¿Ÿçš„æ¡ç›®
// **æ²¡æœ‰æ¶ˆè€—é‡è¯•æ¬¡æ•°** â‘£ å†è·‘ä¸€è½®èƒ½æŠŠå‰©ä¸‹çš„å‘å‡ºåŽ»ï¼ˆä¸ä¼šå¡æ­»ï¼‰ã€‚
func TestNotifyDigestSegmentsAndDefersRemainder(t *testing.T) {
	f := newNotifyFixture(t)
	hook := newFakeWebhook(t)
	// ç”¨ä¼ä¸šå¾®ä¿¡ï¼šmarkdown ä¸Šé™ 4096 å­—èŠ‚ï¼Œæ˜¯å…­ä¸ªæ¸ é“é‡Œæœ€ç´§çš„ã€‚
	chID := f.createChannel(t, map[string]any{
		"name":   "åˆ†æ®µæ±‡æ€»",
		"kind":   notify.KindWeCom,
		"mode":   db.NotifyModeDigest,
		"config": map[string]any{"webhook": hook.URL},
	})
	const total = 60
	// æ ‡é¢˜å–é•¿ä¸€ç‚¹ï¼Œä¿è¯ 60 æ¡è¿œè¶… 4096 å­—èŠ‚ï¼Œå¿…ç„¶åˆ†æ®µã€‚
	longName := strings.Repeat("è¶…é•¿æ¼æ´žåç§°", 6)
	for i := 0; i < total; i++ {
		f.record(t, longName+strconv.Itoa(i+1), "high")
	}
	ctx := context.Background()
	if _, _, err := f.pg.FanOutPendingEvents(ctx, 500); err != nil {
		t.Fatal(err)
	}
	f.agePendingBatch(t, chID)
	ch := f.channel(t, chID)

	f.n.stepDigest(ctx, ch, 50, "")
	if hook.count() != 1 {
		t.Fatalf("åº”åªå‘å‡ºä¸€æ¡æ¶ˆæ¯ï¼Œå¾—åˆ° %d", hook.count())
	}

	var sent, pending int
	if err := f.pg.QueryRow(`SELECT
    count(*) FILTER (WHERE state=$2),
    count(*) FILTER (WHERE state=$3)
  FROM notification_deliveries WHERE channel_id=$1`, chID, db.NotifyStateSent, db.NotifyStatePending).
		Scan(&sent, &pending); err != nil {
		t.Fatal(err)
	}
	if sent == 0 {
		t.Fatal("åº”æœ‰æ¡ç›®è¢«æ ‡è®°ä¸ºå·²é€è¾¾")
	}
	if pending == 0 {
		t.Fatalf("ä¸€æ‰¹ %d æ¡ä¸å¯èƒ½å…¨è£…è¿› 4096 å­—èŠ‚ï¼Œåº”æœ‰å‰©ä½™å¾…å‘ï¼›sent=%d", total, sent)
	}
	if sent+pending != total {
		t.Fatalf("æ¡ç›®æ•°å¯¹ä¸ä¸Šï¼šsent=%d pending=%d total=%dï¼ˆæ—¢æ²¡é€è¾¾ä¹Ÿæ²¡å¾…å‘=ä¸¢å¤±ï¼‰", sent, pending, total)
	}
	// æ¶ˆæ¯æ­£æ–‡å¿…é¡»å¦‚å®žå‘ŠçŸ¥è¿˜æœ‰å¤šå°‘æ¡æ²¡åŒ…å«åœ¨æœ¬æ¡é‡Œã€‚
	if text := markdownText(t, hook.last(t)); !strings.Contains(text, "å…¶ä½™") {
		t.Fatalf("æ¶ˆæ¯åº”è¯´æ˜Žè¿˜æœ‰æ¡ç›®æœªåŒ…å«åœ¨æœ¬æ¡:\n%.400s", text)
	}

	// è¢«æŽ¨è¿Ÿçš„æ¡ç›®ä¸å¾—æ¶ˆè€—é‡è¯•é¢„ç®—ï¼šé¢†å–æ—¶ attempts å·²ä¹è§‚ +1ï¼ŒæŽ¨è¿Ÿæ—¶è¦å‡å›žåŽ»ã€‚
	var maxAttempts int
	if err := f.pg.QueryRow(`SELECT COALESCE(max(attempts),0) FROM notification_deliveries
WHERE channel_id=$1 AND state=$2`, chID, db.NotifyStatePending).Scan(&maxAttempts); err != nil {
		t.Fatal(err)
	}
	if maxAttempts > 0 {
		t.Fatalf("è¢«æŽ¨è¿Ÿçš„æ¡ç›®ä¸è¯¥æ¶ˆè€—é‡è¯•æ¬¡æ•°ï¼ˆå¦åˆ™å‡ æ¡ä¹‹åŽå°±ä¼šè¢«åˆ¤å¤±è´¥ï¼‰ï¼Œå¾—åˆ° attempts=%d", maxAttempts)
	}

	// åå¤è·‘ç›´åˆ°æ”¶æ•›ã€‚æ–­è¨€çš„æ˜¯**æœ€ç»ˆå…¨éƒ¨é€è¾¾**ä¸”ä¸­é€”ç¡®å®žåˆ†äº†å¤šè½®â€”â€”
	// è¿™æ¯”ã€Œç¬¬äºŒè½®å‘å®Œã€æ›´å¼ºï¼šå®ƒè¯æ˜Žåˆ†æ®µä¸ä¼šå¡æ­»ã€ä¹Ÿä¸ä¼šæŠŠå‰©ä½™æ¡ç›®ä¸¢æŽ‰ã€‚
	rounds := 0
	for {
		var undelivered int
		if err := f.pg.QueryRow(`SELECT count(*) FROM notification_deliveries
WHERE channel_id=$1 AND state <> $2 AND state <> $3`, chID, db.NotifyStateSent, db.NotifyStateFailed).
			Scan(&undelivered); err != nil {
			t.Fatal(err)
		}
		if undelivered == 0 {
			break
		}
		rounds++
		if rounds > total+5 {
			t.Fatalf("åˆ†æ®µæŠ•é€’ä¸æ”¶æ•›ï¼šè·‘äº† %d è½®ä»æœ‰ %d æ¡æ‚¬è€Œæœªå†³", rounds, undelivered)
		}
		before := hook.count()
		f.n.stepDigest(ctx, ch, 50, "")
		if hook.count() == before {
			t.Fatalf("ç¬¬ %d è½®æ²¡æœ‰ä»»ä½•è¿›å±•ï¼Œå‰©ä½™ %d æ¡ä¼šæ°¸ä¹…å¡ä½", rounds, undelivered)
		}
	}
	if rounds < 2 {
		t.Fatalf("ä¸€æ¡ 4096 å­—èŠ‚çš„æ¶ˆæ¯è£…ä¸ä¸‹ %d æ¡é•¿æ ‡é¢˜æ¼æ´žï¼Œåº”åˆ†å¤šè½®å‘å‡ºï¼Œå®žé™…åªç”¨äº† %d è½®", total, rounds)
	}
	// é¦–è½®ä¹‹åŽçš„æ¯ä¸€è½®éƒ½åº”æ˜¯**çº¯ç»­å‘**ï¼Œä¸å­˜åœ¨è¢«æ¸ é“æ‹’ç»çš„æ¡ç›®ã€‚
	var failed int
	if err := f.pg.QueryRow(`SELECT count(*) FROM notification_deliveries WHERE channel_id=$1 AND state=$2`,
		chID, db.NotifyStateFailed).Scan(&failed); err != nil {
		t.Fatal(err)
	}
	if failed != 0 {
		t.Fatalf("å‡æŽ¥æ”¶ç«¯å§‹ç»ˆè¿”å›žæˆåŠŸï¼Œä¸è¯¥æœ‰å¤±è´¥æ¡ç›®ï¼Œå¾—åˆ° %d", failed)
	}
}

// TestNotifyBackoffTableMatchesAttemptBudget æ˜¯é˜²æ¼‚ç§»æ–­è¨€ã€‚
//
// é‡è¯•é¢„ç®—ï¼ˆdb.MaxNotifyAttemptsï¼‰ä¸Žé€€é¿åºåˆ—è¡¨ï¼ˆnotifyBackoffï¼‰åˆ†å±…ä¸¤ä¸ªåŒ…ï¼š
// å‰è€…æ˜¯çŠ¶æ€æœºçš„ç­–ç•¥ã€åŽè€…æ˜¯å¼•æ“Žçš„æ‰§è¡ŒèŠ‚æ‹ã€‚è‹¥åªæ”¹å…¶ä¸­ä¸€ä¸ªâ€”â€”æ¯”å¦‚æŠŠé¢„ç®—æåˆ° 5
// æ¬¡è€Œå¿˜äº†åŠ é€€é¿æ¡£ä½â€”â€”ä»£ç ä¸ä¼šæŠ¥é”™ï¼Œåªä¼šè®©ç¬¬ 4ã€5 æ¬¡é‡è¯•æ²¿ç”¨æœ€åŽä¸€æ¡£é—´éš”ï¼Œ
// è¡¨çŽ°ä¸ºã€Œé‡è¯•èŠ‚å¥èŽ«åå˜æ…¢ã€ï¼ŒæŽ’æŸ¥æ—¶å¾ˆéš¾è”æƒ³åˆ°æ˜¯è¿™é‡Œã€‚
// æ–­è¨€ä¸¤è€…é•¿åº¦ä¸€è‡´ï¼Œè®©è¿™ç§æ¼‚ç§»åœ¨ CI é‡Œå°±æš´éœ²ã€‚
func TestNotifyBackoffTableMatchesAttemptBudget(t *testing.T) {
	if len(notifyBackoff) != db.MaxNotifyAttempts {
		t.Fatalf("é€€é¿æ¡£ä½æ•°(%d)ä¸Žæœ€å¤§å°è¯•æ¬¡æ•°(%d)ä¸ä¸€è‡´â€”â€”æ”¹ä¸€ä¸ªå¿…é¡»åŒæ—¶æ”¹å¦ä¸€ä¸ª",
			len(notifyBackoff), db.MaxNotifyAttempts)
	}
	// é€€é¿é—´éš”å¿…é¡»å•è°ƒä¸å‡ï¼Œå¦åˆ™é‡è¯•ä¼šè¶Šè¯•è¶Šæ€¥ï¼Œåè€ŒåŠ å‰§é™æµã€‚
	for i := 1; i < len(notifyBackoff); i++ {
		if notifyBackoff[i] < notifyBackoff[i-1] {
			t.Fatalf("é€€é¿é—´éš”å¿…é¡»å•è°ƒä¸å‡ï¼šç¬¬ %d æ¡£ %v < ç¬¬ %d æ¡£ %v",
				i, notifyBackoff[i], i-1, notifyBackoff[i-1])
		}
	}
}

// TestNotifyRateLimitDoesNotConsumeRetryBudget é”ä½ã€Œå…ˆå–ä»¤ç‰Œå†é¢†å–ã€çš„é¡ºåºã€‚
// è‹¥åäº†ï¼ˆå…ˆé¢†åŽå¼ƒï¼‰ï¼Œè¢«é™æµæŒ¡ä¸‹çš„æŠ•é€’å·²ç»è®¡è¿‡ä¸€æ¬¡ attemptsï¼Œ
// é¢„ç®—ä¼šè¢«çº¯ç²¹çš„ç­‰å¾…è€—å…‰ï¼Œæœ€åŽè½è¿› failedã€‚
func TestNotifyRateLimitDoesNotConsumeRetryBudget(t *testing.T) {
	// åªæµ‹ä»¤ç‰Œæ¡¶æœ¬èº«ï¼Œä¸éœ€è¦ Serverï¼ˆä¹Ÿä¸è¯¥ä¸ºå®ƒé€ ä¸€ä¸ªï¼‰ã€‚
	n := &Notifier{buckets: map[int64]*notifyBucket{}}
	now := time.Now()
	// æ¯åˆ†é’Ÿ 1 æ¡ï¼šæ»¡æ¡¶æ—¶æœ€å¤š 1 æ¡ã€‚
	if got := n.takeTokens(1, 1, notifyMaxSendsPerChannelPerTick, now); got != 1 {
		t.Fatalf("æ»¡æ¡¶æ—¶æ¯åˆ†é’Ÿ 1 æ¡åº”å– 1 ä¸ªä»¤ç‰Œï¼Œå¾—åˆ° %d", got)
	}
	if got := n.takeTokens(1, 1, notifyMaxSendsPerChannelPerTick, now.Add(time.Millisecond)); got != 0 {
		t.Fatalf("ä»¤ç‰Œè€—å°½åŽåº”ç«‹å³è¿”å›ž 0ï¼Œå¾—åˆ° %d", got)
	}
	if got := n.takeTokens(1, 1, notifyMaxSendsPerChannelPerTick, now.Add(30*time.Second)); got != 0 {
		t.Fatalf("åŠç¨‹ä¸åº”è¡¥æ»¡ä¸€ä¸ªä»¤ç‰Œï¼Œå¾—åˆ° %d", got)
	}
	if got := n.takeTokens(1, 1, notifyMaxSendsPerChannelPerTick, now.Add(time.Minute)); got != 1 {
		t.Fatalf("æ»¡ä¸€ä¸ªå‘¨æœŸåº”è¡¥å›ž 1 ä¸ªä»¤ç‰Œï¼Œå¾—åˆ° %d", got)
	}
	// ä¸é™æµæ¸ é“èµ°æœ‰é™ä¸Šé™ï¼Œé¿å…å•è½®è¢«æ— é™ç§¯åŽ‹æ‹–ä½ã€‚
	if got := n.takeTokens(2, 0, notifyUnlimitedBurstPerTick+10, now); got != notifyUnlimitedBurstPerTick {
		t.Fatalf("ä¸é™æµåº”è¿”å›žæ¯è½®ä¸Šé™ %dï¼Œå¾—åˆ° %d", notifyUnlimitedBurstPerTick, got)
	}
	// æ¸ é“ä¹‹é—´çš„ä»¤ç‰Œæ¡¶äº’ç›¸ç‹¬ç«‹ã€‚
	if got := n.takeTokens(1, 1, notifyMaxSendsPerChannelPerTick, now.Add(time.Millisecond)); got != 0 {
		t.Fatalf("æ¸ é“ 1 çš„æ¡¶åº”ä»ç„¶ä¸ºç©ºï¼Œå¾—åˆ° %d", got)
	}
}

// TestNotifyTakeTokensKeepsUnusedTokens é”ä½ã€Œåªå– want ä¸ªã€çš„è¯­ä¹‰ã€‚
//
// æ›¾ç»çš„å®žçŽ°æŠŠæ¡¶æ•´ä¸ªæŠ½ç©ºåŽæ‰ç”±è°ƒç”¨æ–¹æˆªæ–­ï¼ŒäºŽæ˜¯ rate=100/min çš„æ¸ é“æ”’æ»¡æ¡¶ã€
// ä¸€è½®åªç”¨ 5 æ¡ï¼Œå‰©ä¸‹ 95 ä¸ªä»¤ç‰Œç›´æŽ¥ä¸¢å¼ƒï¼›æ¸ é“è¿™ä¸€è½®æ²¡æœ‰å¾…å‘æŠ•é€’æ—¶åŒæ ·ç…§æ‰£ã€‚
// ç»“æžœæ˜¯æ³¨é‡Šå£°ç§°çš„ã€Œç§¯åŽ‹æ—¶å¯ä»¥ä¸€æ¬¡æ€§å†² rate_per_min æ¡ã€åœ¨ä»»ä½•æƒ…å†µä¸‹éƒ½åšä¸åˆ°ã€‚
func TestNotifyTakeTokensKeepsUnusedTokens(t *testing.T) {
	n := &Notifier{buckets: map[int64]*notifyBucket{}}
	now := time.Now()
	// æ¡¶åˆå§‹ä¸ºæ»¡ï¼ˆ100ï¼‰ï¼Œæœ¬è½®åªè¦ 5 ä¸ªã€‚
	if got := n.takeTokens(1, 100, 5, now); got != 5 {
		t.Fatalf("want=5 æ—¶åº”æ°å¥½å– 5 ä¸ªä»¤ç‰Œï¼Œå¾—åˆ° %d", got)
	}
	// å…³é”®æ–­è¨€ï¼šä½™ä¸‹çš„ 95 ä¸ªå¿…é¡»è¿˜åœ¨æ¡¶é‡Œï¼Œè€Œä¸æ˜¯è¢«æŠ½ç©ºä¸¢å¼ƒã€‚
	// ä¸æŽ¨è¿›æ—¶é—´ï¼Œç¡®ä¿å–åˆ°çš„åªå¯èƒ½æ¥è‡ªå­˜é‡è€Œéžè¡¥å……ã€‚
	if got := n.takeTokens(1, 100, 95, now); got != 95 {
		t.Fatalf("å‰©ä½™ä»¤ç‰Œåº”ä»å¯å–ç”¨ï¼ˆæœŸæœ› 95ï¼‰ï¼Œå¾—åˆ° %dâ€”â€”æ¡¶è¢«æ•´è½®æŠ½ç©ºäº†", got)
	}
	if got := n.takeTokens(1, 100, 1, now); got != 0 {
		t.Fatalf("æ¡¶å·²å–å°½ï¼Œåº”è¿”å›ž 0ï¼Œå¾—åˆ° %d", got)
	}
	// want<=0 ä¸åº”æ‰£å‡ä»»ä½•ä»¤ç‰Œï¼ˆç©ºè½®ä¸æ”¶è´¹ï¼‰ã€‚
	n2 := &Notifier{buckets: map[int64]*notifyBucket{}}
	if got := n2.takeTokens(1, 20, 0, now); got != 0 {
		t.Fatalf("want=0 åº”è¿”å›ž 0ï¼Œå¾—åˆ° %d", got)
	}
	if got := n2.takeTokens(1, 20, 20, now); got != 20 {
		t.Fatalf("want=0 çš„é‚£æ¬¡ä¸è¯¥æ¶ˆè€—ä»¤ç‰Œï¼Œåº”ä»å¯å–æ»¡ 20ï¼Œå¾—åˆ° %d", got)
	}
}

// TestDigestTickPlanDecouplesBatchSizeFromSendBudget é’‰ä½æ±‡æ€»æ¨¡å¼çš„ä¸¤ä¸ªé‡çº²ã€‚
//
// æ±‡æ€»æ‰¹æ¬¡çš„å¤§å°ä¸€æ—¦è·Ÿæ¯è½®è¯·æ±‚é¢„ç®—æŒ‚ä¸Šï¼Œrate_per_min=20 çš„æ¸ é“å°±åªèƒ½åœ¨æ¯ä¸ª
// 3 ç§’ tick é‡Œè¡¥åˆ° 1 ä¸ªä»¤ç‰Œï¼ŒäºŽæ˜¯æ¯æ¡æ±‡æ€»æ¶ˆæ¯åªè£… 1 ä¸ªæ¼æ´žâ€”â€”åŠŸèƒ½ä¸Šç­‰äºŽæ²¡æœ‰
// æ±‡æ€»ï¼Œè€Œæ¶ˆæ¯å¤´éƒ¨è¿˜å†™ç€ã€Œè¿‘ 30 åˆ†é’Ÿæ–°å¢ž 1 ä¸ªæ¼æ´žã€ã€‚è¿™ä¸ªé€€åŒ–ä¸ä¼šæŠ¥é”™ï¼Œ
// çŽ°æœ‰çš„ç«¯åˆ°ç«¯ç”¨ä¾‹ä¹Ÿçœ‹ä¸å‡ºæ¥ï¼ˆå®ƒä»¬æ‰‹åŠ¨ç»™ stepDigest ä¼ ä¸€ä¸ªå¤Ÿå¤§çš„ limitï¼Œ
// ç»•è¿‡äº† step é‡Œçš„é¢åº¦è®¡ç®—ï¼‰ï¼Œæ‰€ä»¥åœ¨è¿™é‡Œç›´æŽ¥æ–­è¨€å†³ç­–æœ¬èº«ã€‚
func TestDigestTickPlanDecouplesBatchSizeFromSendBudget(t *testing.T) {
	tokens, claimLimit := digestTickPlan()
	// ä¸€æ‰¹ = ä¸€æ¡æ¶ˆæ¯ = ä¸€æ¬¡è¯·æ±‚ = ä¸€ä¸ªä»¤ç‰Œã€‚ä»¤ç‰Œçš„å•ä½æ˜¯æ¶ˆæ¯ï¼Œä¸æ˜¯æ¼æ´žã€‚
	if tokens != 1 {
		t.Fatalf("æ±‡æ€»ä¸€æ‰¹åªå‘ä¸€æ¡æ¶ˆæ¯ï¼Œåº”æ°å¥½æ¶ˆè€— 1 ä¸ªä»¤ç‰Œï¼Œå¾—åˆ° %d", tokens)
	}
	if claimLimit != db.MaxDigestBatchSize {
		t.Fatalf("æ±‡æ€»æ‰¹æ¬¡å¤§å°åº”ä¸ºå†…å­˜ä¸Šç•Œ db.MaxDigestBatchSize=%dï¼Œå¾—åˆ° %d",
			db.MaxDigestBatchSize, claimLimit)
	}
	// å…³é”®å…³ç³»ï¼šæ‰¹æ¬¡å¤§å°å¿…é¡»è¿œå¤§äºŽæ¯è½®è¯·æ±‚é¢„ç®—ã€‚ä¸¤è€…ä¸€æ—¦åŒé‡çº§ï¼Œ
	// è¯´æ˜ŽåˆæŠŠã€Œå‘å‡ æ¡æ¶ˆæ¯ã€å’Œã€Œä¸€æ‰¹è£…å‡ æ¡æ¼æ´žã€æ··æˆäº†ä¸€ä¸ªæ•°ã€‚
	if claimLimit <= notifyMaxSendsPerChannelPerTick {
		t.Fatalf("æ±‡æ€»æ‰¹æ¬¡å¤§å° %d ä¸åº”å—æ¯è½®è¯·æ±‚é¢„ç®— %d çº¦æŸâ€”â€”"+
			"è¯·æ±‚é¢„ç®—æ˜¯ç”±ç§Ÿçº¦å€’æŽ¨çš„ã€Œå‘å‡ æ¬¡è¯·æ±‚ã€ï¼Œä¸Žã€Œä¸€æ‰¹è£…å‡ æ¡æ¼æ´žã€æ˜¯ä¸¤ä¸ªé‡çº²",
			claimLimit, notifyMaxSendsPerChannelPerTick)
	}
}

// TestNotifyTickBudgetFitsWithinLease æ˜¯åˆä¸€æ¡é˜²æ¼‚ç§»æ–­è¨€ã€‚
//
// å•æ¸ é“æ¯è½®çš„æŠ•é€’æ¡æ•°ä¸Šé™ï¼ˆnotifyMaxSendsPerChannelPerTickï¼‰æ˜¯ä»Žç§Ÿçº¦æ—¶é•¿å€’æŽ¨çš„ï¼š
// ä¸€è½®é‡Œä¸²è¡ŒæŠ•é€’çš„æœ€åè€—æ—¶å¿…é¡» < ç§Ÿçº¦ï¼Œå¦åˆ™åŽå‡ æ¡è¿˜æ²¡å‘å®Œç§Ÿçº¦å°±è¿‡æœŸï¼Œ
// å¤šå®žä¾‹éƒ¨ç½²æ—¶å¯¹ç«¯ä¼šæŠŠå®ƒä»¬é‡æ–°é¢†èµ°ã€é‡å¤å‘é€ã€‚è¿™ä¸‰ä¸ªå¸¸é‡åˆ†å¤„ä¸åŒä½ç½®ï¼Œ
// æ”¹ä»»æ„ä¸€ä¸ªéƒ½å¯èƒ½æ‰“ç ´å…³ç³»è€Œä¸ä¼šæœ‰ä»»ä½•æŠ¥é”™â€”â€”æ‰€ä»¥åœ¨è¿™é‡Œé’‰æ­»ã€‚
func TestNotifyTickBudgetFitsWithinLease(t *testing.T) {
	worst := time.Duration(notifyMaxSendsPerChannelPerTick) * notifySendTimeout
	if worst >= notifyLease {
		t.Fatalf("å•æ¸ é“ä¸€è½®çš„æœ€åè€—æ—¶ %v ä¸åº”è¾¾åˆ°æˆ–è¶…è¿‡ç§Ÿçº¦ %v"+
			"ï¼ˆnotifyMaxSendsPerChannelPerTick=%d Ã— notifySendTimeout=%vï¼‰â€”â€”"+
			"æ”¹è¿™ä¸‰ä¸ªå¸¸é‡ä¸­çš„ä»»æ„ä¸€ä¸ªéƒ½è¦åŒæ­¥æ£€æŸ¥å¦å¤–ä¸¤ä¸ª",
			worst, notifyLease, notifyMaxSendsPerChannelPerTick, notifySendTimeout)
	}
}

