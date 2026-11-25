package server

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Autumn-27/cautiousmemory/selfupdate"
)

// releaseCache æ˜¯ä¿æŠ¤ GitHub é…é¢çš„é‚£ä¸€å±‚ï¼šæœªè®¤è¯çš„ API åªæœ‰ 60 æ¬¡/å°æ—¶/IPï¼Œ
// è€Œé¡¶æ çš„"æœ‰æ–°ç‰ˆæœ¬"æç¤ºæ¯æ¬¡æ•´é¡µåŠ è½½éƒ½ä¼šæŸ¥ä¸€æ¬¡ã€‚ç¼“å­˜ä¸€æ—¦å¤±æ•ˆï¼Œç”¨æˆ·å¤šå¼€å‡ ä¸ª
// æ ‡ç­¾é¡µå°±ä¼šæŠŠé…é¢è€—å…‰ï¼Œä¹‹åŽçœŸæƒ³æ›´æ–°åè€ŒæŸ¥ä¸åŠ¨ã€‚

func newTestCache(fetch func(context.Context, *http.Client) (*selfupdate.Release, error)) *releaseCache {
	return &releaseCache{fetch: fetch}
}

func TestReleaseCacheServesFromCache(t *testing.T) {
	calls := 0
	c := newTestCache(func(context.Context, *http.Client) (*selfupdate.Release, error) {
		calls++
		return &selfupdate.Release{TagName: "v0.3.8"}, nil
	})

	for range 5 {
		rel, err := c.get(t.Context(), nil, false, "")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if rel.TagName != "v0.3.8" {
			t.Fatalf("TagName = %q", rel.TagName)
		}
	}
	if calls != 1 {
		t.Errorf("5 æ¬¡æŸ¥è¯¢åªåº”å›žæº 1 æ¬¡ï¼Œå®žé™… %d æ¬¡", calls)
	}
}

func TestReleaseCacheForceBypasses(t *testing.T) {
	calls := 0
	c := newTestCache(func(context.Context, *http.Client) (*selfupdate.Release, error) {
		calls++
		return &selfupdate.Release{TagName: "v0.3.8"}, nil
	})

	if _, err := c.get(t.Context(), nil, false, ""); err != nil {
		t.Fatal(err)
	}
	// ç”¨æˆ·ç‚¹ã€Œæ£€æŸ¥æ›´æ–°ã€å¿…é¡»æ‹¿åˆ°å®žæ—¶ç»“æžœï¼Œå¦åˆ™åˆšå‘å¸ƒçš„ç‰ˆæœ¬è¦ç­‰ç¼“å­˜è¿‡æœŸæ‰çœ‹å¾—è§ã€‚
	if _, err := c.get(t.Context(), nil, true, ""); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("force åº”ç»•è¿‡ç¼“å­˜ï¼ŒæœŸæœ›å›žæº 2 æ¬¡ï¼Œå®žé™… %d æ¬¡", calls)
	}
}

func TestReleaseCacheExpiresAfterTTL(t *testing.T) {
	calls := 0
	c := newTestCache(func(context.Context, *http.Client) (*selfupdate.Release, error) {
		calls++
		return &selfupdate.Release{TagName: "v0.3.8"}, nil
	})

	if _, err := c.get(t.Context(), nil, false, ""); err != nil {
		t.Fatal(err)
	}
	// æŠŠè½åº“æ—¶é—´å¾€å‰æ‹¨åˆ°åˆšè¿‡æœŸï¼Œæ¨¡æ‹Ÿ TTL åˆ°ç‚¹ã€‚
	c.at = time.Now().Add(-releaseTTL - time.Second)
	if _, err := c.get(t.Context(), nil, false, ""); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("TTL è¿‡æœŸåŽåº”é‡æ–°å›žæºï¼ŒæœŸæœ› 2 æ¬¡ï¼Œå®žé™… %d æ¬¡", calls)
	}
}

func TestReleaseCacheUsesShorterTTLForErrors(t *testing.T) {
	calls := 0
	c := newTestCache(func(context.Context, *http.Client) (*selfupdate.Release, error) {
		calls++
		return nil, errors.New("github unreachable")
	})

	if _, err := c.get(t.Context(), nil, false, ""); err == nil {
		t.Fatal("æœŸæœ›è¿”å›žé”™è¯¯")
	}
	// å¤±è´¥ç»“æžœä¹Ÿè¦ç¼“å­˜ä¸€ä¼šå„¿ï¼Œå¦åˆ™ GitHub ä¸å¯è¾¾æ—¶æ¯æ¬¡é¡µé¢åŠ è½½éƒ½ç™½ç­‰ä¸€æ¬¡è¶…æ—¶ã€‚
	if _, err := c.get(t.Context(), nil, false, ""); err == nil {
		t.Fatal("æœŸæœ›è¿”å›žé”™è¯¯")
	}
	if calls != 1 {
		t.Errorf("é”™è¯¯åº”çŸ­æ—¶ç¼“å­˜ï¼ŒæœŸæœ›å›žæº 1 æ¬¡ï¼Œå®žé™… %d æ¬¡", calls)
	}

	// ä½†é”™è¯¯çš„ TTL å¿…é¡»æ˜Žæ˜¾çŸ­äºŽæˆåŠŸçš„ï¼Œç½‘ç»œæ¢å¤åŽè¦èƒ½å¾ˆå¿«è‡ªæ„ˆã€‚
	if releaseErrTTL >= releaseTTL {
		t.Fatalf("é”™è¯¯ TTL(%v) å¿…é¡»çŸ­äºŽæˆåŠŸ TTL(%v)", releaseErrTTL, releaseTTL)
	}
	c.at = time.Now().Add(-releaseErrTTL - time.Second)
	if _, err := c.get(t.Context(), nil, false, ""); err == nil {
		t.Fatal("æœŸæœ›è¿”å›žé”™è¯¯")
	}
	if calls != 2 {
		t.Errorf("é”™è¯¯ TTL è¿‡æœŸåŽåº”é‡è¯•ï¼ŒæœŸæœ› 2 æ¬¡ï¼Œå®žé™… %d æ¬¡", calls)
	}
}

func TestReleaseCacheDoesNotPoisonOnCallerCancel(t *testing.T) {
	good := &selfupdate.Release{TagName: "v0.3.8"}
	c := newTestCache(func(ctx context.Context, _ *http.Client) (*selfupdate.Release, error) {
		return good, nil
	})
	if _, err := c.get(t.Context(), nil, false, ""); err != nil {
		t.Fatal(err)
	}

	// è®¿å®¢å…³æŽ‰æ ‡ç­¾é¡µä¼šå–æ¶ˆè¯·æ±‚ã€‚é‚£ä¸ä»£è¡¨ GitHub æœ‰é—®é¢˜ï¼Œç»ä¸èƒ½æŠŠ"å·²å–æ¶ˆ"
	// å†™è¿›ç¼“å­˜â€”â€”å¦åˆ™æŽ¥ä¸‹æ¥ 30 åˆ†é’Ÿå†…æ¯ä¸ªè®¿å®¢éƒ½ä¼šæ”¶åˆ°ä¸€æ¡èŽ«åå…¶å¦™çš„é”™è¯¯ã€‚
	c.fetch = func(ctx context.Context, _ *http.Client) (*selfupdate.Release, error) {
		return nil, ctx.Err()
	}
	c.at = time.Now().Add(-releaseTTL - time.Second) // è®©ç¼“å­˜è¿‡æœŸï¼Œé€¼å®ƒå›žæº

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.get(ctx, nil, false, ""); err == nil {
		t.Fatal("è°ƒç”¨æ–¹å·²å–æ¶ˆæ—¶åº”æŠŠé”™è¯¯é€ä¼ ç»™å®ƒ")
	}

	// å…³é”®ä¸å˜é‡ï¼šè¢«å–æ¶ˆçš„é‚£ä¸€æ¬¡ä¸ç•™ä¸‹ä»»ä½•ç—•è¿¹â€”â€”ç¼“å­˜é‡Œæ—¢æ²¡æœ‰"å·²å–æ¶ˆ"è¿™ä¸ªé”™è¯¯ï¼Œ
	// ä¹Ÿè¿˜ä¿ç€ä¸Šä¸€æ¬¡çš„å¥½ç»“æžœã€‚
	if c.err != nil {
		t.Fatalf("å–æ¶ˆé”™è¯¯ä¸åº”å†™è¿›ç¼“å­˜ï¼Œå¾—åˆ° %v", c.err)
	}
	if c.rel == nil || c.rel.TagName != "v0.3.8" {
		t.Fatalf("ç¼“å­˜åº”ä¿ç•™ä¸Šä¸€æ¬¡çš„å¥½ç»“æžœï¼Œå¾—åˆ° %+v", c.rel)
	}

	// é‚£æ¬¡å–æ¶ˆæ²¡æ¢æ¥ä»»ä½•æ–°æ•°æ®ï¼Œæ‰€ä»¥ä¸‹ä¸€ä¸ªè®¿å®¢ç†åº”é‡æ–°å›žæºâ€”â€”è€Œä¸”èƒ½æ­£å¸¸æ‹¿åˆ°ç»“æžœï¼Œ
	// ä¸ä¼šè¢«ä¸Šä¸€æ¬¡çš„å–æ¶ˆè¿žç´¯ã€‚
	c.fetch = func(context.Context, *http.Client) (*selfupdate.Release, error) {
		return good, nil
	}
	rel, err := c.get(t.Context(), nil, false, "")
	if err != nil {
		t.Fatalf("å–æ¶ˆä¹‹åŽçš„æ­£å¸¸è¯·æ±‚ä¸åº”æŠ¥é”™: %v", err)
	}
	if rel == nil || rel.TagName != "v0.3.8" {
		t.Fatalf("åº”æ‹¿åˆ°æ­£å¸¸ç»“æžœï¼Œå¾—åˆ° %+v", rel)
	}
}

func TestReleaseCacheChangesWithNetwork(t *testing.T) {
	calls := 0
	c := newTestCache(func(context.Context, *http.Client) (*selfupdate.Release, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("direct unavailable")
		}
		return &selfupdate.Release{TagName: "v0.3.21"}, nil
	})
	if _, err := c.get(t.Context(), nil, false, ""); err == nil {
		t.Fatal("expected direct error")
	}
	if _, err := c.get(t.Context(), nil, false, "http://localhost:7897"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.get(t.Context(), nil, false, "http://localhost:7897"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("same proxy should reuse cache; calls=%d", calls)
	}
	if _, err := c.get(t.Context(), nil, false, ""); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("switching back to direct must refetch; calls=%d", calls)
	}
}

func TestUpdateSettingsValidation(t *testing.T) {
	for _, tc := range []struct {
		proxy string
		valid bool
	}{
		{"", true}, {" http://127.0.0.1:7897 ", true}, {"localhost:7897", false},
	} {
		cfg := updateSettings{Proxy: tc.proxy}
		if err := cfg.validate(); (err == nil) != tc.valid {
			t.Errorf("validation: %v", err)
		}
	}
}

