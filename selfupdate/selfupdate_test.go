package selfupdate

import (
	"archive/zip"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// testPaths é€ ä¸€ä¸ªéš”ç¦»çš„å‡çº§ç›®å½•ã€‚ä¸èƒ½ç›´æŽ¥ç”¨ ResolvePaths()â€”â€”é‚£ä¼šæŒ‡å‘æµ‹è¯•
// äºŒè¿›åˆ¶æœ¬èº«ï¼Œä¸€è·‘å°±æŠŠ go test çš„å¯æ‰§è¡Œæ–‡ä»¶æ”¹åäº†ã€‚
func testPaths(t *testing.T) Paths {
	t.Helper()
	dir := t.TempDir()
	return Paths{
		Dir:     dir,
		Current: filepath.Join(dir, "CAUTIOUSMEMORY"),
		New:     filepath.Join(dir, "CAUTIOUSMEMORY.new"),
		Sum:     filepath.Join(dir, "CAUTIOUSMEMORY.new.sha256"),
		Old:     filepath.Join(dir, "CAUTIOUSMEMORY.old"),
		Marker:  filepath.Join(dir, "CAUTIOUSMEMORY.upgrade.json"),
	}
}

// fakeBin å†™ä¸€ä¸ªå¯æ‰§è¡Œçš„å£³è„šæœ¬å†’å…… CAUTIOUSMEMORYã€‚smokeTest åªæ˜¯ç”¨ -h æ‹‰èµ·å®ƒçœ‹é€€å‡ºç ï¼Œ
// è„šæœ¬å®Œå…¨å¤Ÿç”¨ï¼Œè€Œä¸”æ¯”ç¼–è¯‘ä¸€ä¸ªçœŸäºŒè¿›åˆ¶å¿«å¾—å¤šã€‚
func fakeBin(t *testing.T, path, marker string, exitCode int) {
	t.Helper()
	script := "#!/bin/sh\necho " + marker + "\nexit " + itoa(exitCode) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("å†™å…¥å‡äºŒè¿›åˆ¶ %s: %v", path, err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	return string(rune('0' + n))
}

// stage æŠŠ bin å¸ƒç½®æˆ"å·²æš‚å­˜å¾…æ¢è£…"çš„æ ·å­ï¼šå†™å¥½ CAUTIOUSMEMORY.new å’Œå®ƒçš„æ ¡éªŒå’Œã€‚
func stage(t *testing.T, p Paths, marker string, exitCode int) {
	t.Helper()
	fakeBin(t, p.New, marker, exitCode)
	sum, err := fileSHA256(p.New)
	if err != nil {
		t.Fatalf("è®¡ç®—æ ¡éªŒå’Œ: %v", err)
	}
	if err := os.WriteFile(p.Sum, []byte(sum), 0o644); err != nil {
		t.Fatalf("å†™å…¥æ ¡éªŒå’Œ: %v", err)
	}
}

func readAll(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("è¯»å– %s: %v", path, err)
	}
	return string(b)
}

func requireUnix(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("å‡äºŒè¿›åˆ¶ç”¨çš„æ˜¯ sh è„šæœ¬ï¼ŒWindows ä¸Šè·‘ä¸äº†")
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b       string
		want       int
		comparable bool
	}{
		{"0.3.7", "0.3.8", -1, true},
		{"0.3.8", "0.3.7", 1, true},
		{"0.3.7", "0.3.7", 0, true},
		{"v0.3.7", "0.3.8", -1, true}, // build.sh åŽ»æŽ‰ vï¼Œtag å¸¦ vï¼Œä¸¤è¾¹éƒ½è¦è®¤
		{"0.3.7", "v0.3.7", 0, true},
		{"0.9.0", "0.10.0", -1, true}, // æŒ‰æ•°å­—æ¯”è€Œä¸æ˜¯å­—å…¸åº
		{"1.0.0", "0.99.99", 1, true},
		// å¼€å‘æž„å»ºå¿…é¡»åˆ¤ä¸ºä¸å¯æ¯”è¾ƒï¼Œå¦åˆ™ä¼šè¢«æ­£å¼ç‰ˆè¦†ç›–æŽ‰æœªæäº¤çš„æ”¹åŠ¨ã€‚
		{"dev", "0.3.8", 0, false},
		{"0.3.7-2-gabc1234", "0.3.8", 0, false},
		{"0.3.7-dirty", "0.3.8", 0, false},
		{"0.3", "0.3.8", 0, false},
		{"", "0.3.8", 0, false},
	}
	for _, c := range cases {
		got, ok := CompareVersions(c.a, c.b)
		if ok != c.comparable {
			t.Errorf("CompareVersions(%q,%q) comparable=%v, æœŸæœ› %v", c.a, c.b, ok, c.comparable)
			continue
		}
		if ok && got != c.want {
			t.Errorf("CompareVersions(%q,%q)=%d, æœŸæœ› %d", c.a, c.b, got, c.want)
		}
	}
}

func TestResolvePathsNaming(t *testing.T) {
	p, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	// å…³é”®ä¸å˜é‡ï¼šæ‰€æœ‰å‡çº§æ–‡ä»¶éƒ½å’Œå¯æ‰§è¡Œæ–‡ä»¶åŒç›®å½•ã€‚è½åˆ° CWD ä¼šè®©æœåŠ¡åŒ–è¿è¡Œ
	// ï¼ˆå·¥ä½œç›®å½•å¯èƒ½æ˜¯ /ï¼‰æ—¶çš„æ¢è£…å½»åº•å¤±æ•ˆã€‚
	for name, path := range map[string]string{"New": p.New, "Sum": p.Sum, "Old": p.Old, "Marker": p.Marker} {
		if filepath.Dir(path) != p.Dir {
			t.Errorf("%s ä¸åœ¨å¯æ‰§è¡Œæ–‡ä»¶ç›®å½•ä¸‹: %s (æœŸæœ› %s)", name, path, p.Dir)
		}
	}
	// Windows ä¸Š .new/.old å¿…é¡»ä¿ç•™ .exeï¼Œå¦åˆ™å†’çƒŸæµ‹è¯•å’Œæ¢è£…åŽçš„æ‰§è¡Œéƒ½ä¼šå¤±è´¥ã€‚
	if runtime.GOOS == "windows" {
		if !strings.HasSuffix(p.New, ".exe") || !strings.HasSuffix(p.Old, ".exe") {
			t.Errorf("Windows ä¸Š .new/.old å¿…é¡»ä»¥ .exe ç»“å°¾: new=%s old=%s", p.New, p.Old)
		}
	}
}

func TestVerifyStagedRejectsTamperedBinary(t *testing.T) {
	requireUnix(t)
	p := testPaths(t)
	stage(t, p, "new", 0)

	// æ ¡éªŒå’Œå†™å¥½ä¹‹åŽå†æ”¹åŠ¨æ–‡ä»¶ï¼Œæ¨¡æ‹Ÿä¸‹è½½æŸå / è¢«æŽ‰åŒ…ã€‚
	fakeBin(t, p.New, "tampered", 0)
	if err := verifyStaged(p); err == nil {
		t.Fatal("æœŸæœ› SHA256 ä¸åŒ¹é…è¢«æ‹’ç»ï¼Œå´é€šè¿‡äº†")
	}
}

func TestVerifyStagedRejectsUnrunnableBinary(t *testing.T) {
	requireUnix(t)
	p := testPaths(t)
	stage(t, p, "broken", 1) // èƒ½æ‰§è¡Œä½†é€€å‡ºç éž 0

	if err := verifyStaged(p); err == nil {
		t.Fatal("æœŸæœ›å†’çƒŸæµ‹è¯•å¤±è´¥è¢«æ‹’ç»ï¼Œå´é€šè¿‡äº†")
	}
}

func TestApplyStagedHappyPath(t *testing.T) {
	requireUnix(t)
	p := testPaths(t)
	fakeBin(t, p.Current, "old", 0)
	stage(t, p, "new", 0)
	if err := writeMarker(p.Marker, marker{From: "0.3.7", To: "0.3.8"}); err != nil {
		t.Fatalf("å†™æ ‡è®°: %v", err)
	}

	action, st := applyStaged(p)
	if action != Restart {
		t.Fatalf("æœŸæœ› Restartï¼Œå¾—åˆ° %v", action)
	}
	if !st.Pending {
		t.Error("æ¢è£…åŽçŠ¶æ€åº”ä¸º Pending")
	}
	if !strings.Contains(readAll(t, p.Current), "new") {
		t.Error("CAUTIOUSMEMORY åº”å·²è¢«æ›¿æ¢ä¸ºæ–°ç‰ˆæœ¬")
	}
	if !strings.Contains(readAll(t, p.Old), "old") {
		t.Error("æ—§ç‰ˆæœ¬åº”å¤‡ä»½åˆ° CAUTIOUSMEMORY.old")
	}
	if _, err := os.Stat(p.New); !os.IsNotExist(err) {
		t.Error("æ¢è£…åŽ CAUTIOUSMEMORY.new åº”å·²æ¶ˆå¤±")
	}
	if _, err := os.Stat(p.Sum); !os.IsNotExist(err) {
		t.Error("æ¢è£…åŽæ ¡éªŒå’Œæ–‡ä»¶åº”å·²æ¸…ç†")
	}
	// æ ‡è®°å¿…é¡»ç•™ç€ï¼Œä¸‹ä¸€æ¬¡å¯åŠ¨ï¼ˆè·‘çš„æ˜¯æ–°ç‰ˆï¼‰é å®ƒè®¡æ•°ã€å¿…è¦æ—¶å›žæ»šã€‚
	if _, ok := readMarker(p.Marker); !ok {
		t.Error("æ¢è£…åŽå‡çº§æ ‡è®°åº”ä¿ç•™")
	}
}

func TestApplyStagedKeepsCurrentWhenVerifyFails(t *testing.T) {
	requireUnix(t)
	p := testPaths(t)
	fakeBin(t, p.Current, "old", 0)
	stage(t, p, "new", 0)
	fakeBin(t, p.New, "tampered", 0) // ç ´åæ ¡éªŒå’Œ

	action, st := applyStaged(p)
	if action != Continue {
		t.Fatalf("æ ¡éªŒå¤±è´¥æ—¶æœŸæœ› Continueï¼Œå¾—åˆ° %v", action)
	}
	if !st.FailedStage {
		t.Error("çŠ¶æ€åº”æ ‡è®°ä¸º FailedStage")
	}
	if !strings.Contains(readAll(t, p.Current), "old") {
		t.Fatal("æ ¡éªŒå¤±è´¥æ—¶ç»ä¸èƒ½åŠ¨å½“å‰ç‰ˆæœ¬")
	}
	if _, err := os.Stat(p.New); !os.IsNotExist(err) {
		t.Error("æ ¡éªŒå¤±è´¥çš„æš‚å­˜ä»¶åº”è¢«æ¸…ç†ï¼Œå¦åˆ™ä¸‹æ¬¡å¯åŠ¨ä¼šå†è¯•ä¸€é")
	}
}

func TestSwapOverwritesPreviousBackup(t *testing.T) {
	requireUnix(t)
	p := testPaths(t)
	fakeBin(t, p.Current, "v2", 0)
	fakeBin(t, p.Old, "v1", 0) // ä¸Šä¸€è½®å‡çº§ç•™ä¸‹çš„å¤‡ä»½
	stage(t, p, "v3", 0)

	if err := swap(p); err != nil {
		t.Fatalf("swap: %v", err)
	}
	if !strings.Contains(readAll(t, p.Current), "v3") {
		t.Error("åº”æ¢è£…åˆ° v3")
	}
	if !strings.Contains(readAll(t, p.Old), "v2") {
		t.Error("å¤‡ä»½åº”æ›´æ–°ä¸ºåˆšè¢«æ¢ä¸‹çš„ v2")
	}
}

func TestConfirmCountsAttemptsThenRollsBack(t *testing.T) {
	requireUnix(t)
	p := testPaths(t)
	fakeBin(t, p.Current, "broken-new", 0)
	fakeBin(t, p.Old, "good-old", 0)
	m := marker{From: "0.3.7", To: "0.3.8"}

	// å‰ maxAttempts æ¬¡å¯åŠ¨åªç´¯è®¡è®¡æ•°ï¼Œè®©æ–°ç‰ˆæœ‰æœºä¼šè‡ªå·±ç«™ç¨³ã€‚
	for i := 1; i <= maxAttempts; i++ {
		action, st := confirmOrRollback(p, m)
		if action != Continue {
			t.Fatalf("ç¬¬ %d æ¬¡å°è¯•æœŸæœ› Continueï¼Œå¾—åˆ° %v", i, action)
		}
		if !st.Pending {
			t.Errorf("ç¬¬ %d æ¬¡å°è¯•çŠ¶æ€åº”ä¸º Pending", i)
		}
		got, ok := readMarker(p.Marker)
		if !ok || got.Attempts != i {
			t.Fatalf("ç¬¬ %d æ¬¡å°è¯•åŽ attempts=%dï¼ˆok=%vï¼‰ï¼ŒæœŸæœ› %d", i, got.Attempts, ok, i)
		}
		m = got
	}

	// å†å´©ä¸€æ¬¡å°±è¶…é™ï¼Œè‡ªåŠ¨æŠŠæ—§ç‰ˆæ¢å›žæ¥ã€‚
	action, st := confirmOrRollback(p, m)
	if action != Restart {
		t.Fatalf("è¶…è¿‡å°è¯•ä¸Šé™æ—¶æœŸæœ› Restartï¼Œå¾—åˆ° %v", action)
	}
	if !st.RolledBack {
		t.Error("çŠ¶æ€åº”æ ‡è®°ä¸º RolledBack")
	}
	if !strings.Contains(readAll(t, p.Current), "good-old") {
		t.Fatal("åº”å·²å›žæ»šåˆ°æ—§ç‰ˆæœ¬")
	}
	if _, err := os.Stat(p.Marker); !os.IsNotExist(err) {
		t.Error("å›žæ»šåŽæ ‡è®°åº”æ¸…é™¤ï¼Œå¦åˆ™ä¼šæ— é™å›žæ»š")
	}
	// èµ·ä¸æ¥çš„é‚£ä¸ªç‰ˆæœ¬ç•™ä½œæŽ’æŸ¥ï¼Œä¸ç›´æŽ¥åˆ ã€‚
	if _, err := os.Stat(p.Current + ".failed"); err != nil {
		t.Error("å¤±è´¥çš„ç‰ˆæœ¬åº”ä¿ç•™ä¸º .failed ä¾›æŽ’æŸ¥")
	}
}

func TestManualRollbackIsReversible(t *testing.T) {
	requireUnix(t)
	p := testPaths(t)
	fakeBin(t, p.Current, "v2", 0)
	fakeBin(t, p.Old, "v1", 0)

	// Rollback() èµ° ResolvePaths()ï¼Œè¿™é‡Œç›´æŽ¥æµ‹åº•å±‚çš„äº¤æ¢è¯­ä¹‰ã€‚
	tmp := p.Current + ".swap"
	if err := os.Rename(p.Current, tmp); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(p.Old, p.Current); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, p.Old); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readAll(t, p.Current), "v1") {
		t.Error("å›žæ»šåŽå½“å‰ç‰ˆæœ¬åº”æ˜¯ v1")
	}
	if !strings.Contains(readAll(t, p.Old), "v2") {
		t.Error("å›žæ»šåŽå¤‡ä»½åº”å˜æˆ v2ï¼Œè¿™æ ·è¿˜èƒ½å†æ»šå›žåŽ»")
	}
}

func TestParseSums(t *testing.T) {
	const (
		linuxSum = "1111111111111111111111111111111111111111111111111111111111111111"
		winSum   = "ABCDEF0000000000000000000000000000000000000000000000000000000000"
	)
	// sha256sum è¾“å‡ºæ˜¯åŒç©ºæ ¼åˆ†éš”ï¼›shasum -a 256 åœ¨äºŒè¿›åˆ¶æ¨¡å¼ä¸‹ä¼šç»™æ–‡ä»¶ååŠ  *ã€‚
	raw := linuxSum + "  CAUTIOUSMEMORY-0.3.8-linux-amd64.zip\n" +
		winSum + " *CAUTIOUSMEMORY-0.3.8-windows-amd64.zip\n" +
		"\n" +
		"garbage line\n" + // æ°å¥½ä¸¤ä¸ªå­—æ®µï¼Œä½†ç¬¬ä¸€ä¸ªä¸æ˜¯æ‘˜è¦
		"deadbeef  CAUTIOUSMEMORY-0.3.8-darwin-arm64.zip\n" // æ‘˜è¦é•¿åº¦ä¸å¯¹

	out := parseSums(raw)
	if out["CAUTIOUSMEMORY-0.3.8-linux-amd64.zip"] != linuxSum {
		t.Errorf("linux æ¡ç›®è§£æžé”™è¯¯: %v", out)
	}
	// æ‘˜è¦ç»Ÿä¸€å°å†™ï¼Œæ¯”å¯¹æ—¶æ‰ä¸ä¼šå› å¤§å°å†™è¯¯åˆ¤ä¸ºä¸åŒ¹é…ã€‚
	if got := out["CAUTIOUSMEMORY-0.3.8-windows-amd64.zip"]; got != strings.ToLower(winSum) {
		t.Errorf("windows æ¡ç›®é”™è¯¯ï¼ˆ* å‰ç¼€åº”å‰¥ç¦»ã€æ‘˜è¦åº”è½¬å°å†™ï¼‰: %q", got)
	}
	if len(out) != 2 {
		t.Errorf("åº”å¿½ç•¥ç©ºè¡Œã€éžæ‘˜è¦è¡Œå’Œé•¿åº¦ä¸å¯¹çš„è¡Œï¼Œå¾—åˆ° %v", out)
	}
}

func TestExtractBinaryFindsNestedEntry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("åŒ…å†…åŸºååœ¨ Windows ä¸Šæ˜¯ CAUTIOUSMEMORY.exeï¼Œæ­¤ç”¨ä¾‹æŒ‰ Unix å‘½åæž„é€ ")
	}
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "release.zip")

	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	// çœŸå®žå‘å¸ƒåŒ…çš„ç»“æž„ï¼šCAUTIOUSMEMORY-<ç‰ˆæœ¬>-<os>-<arch>/CAUTIOUSMEMORYï¼Œå¤–åŠ è‹¥å¹²å¹²æ‰°æ–‡ä»¶ã€‚
	for name, body := range map[string]string{
		"CAUTIOUSMEMORY-0.3.8-linux-amd64/README.md":           "readme",
		"CAUTIOUSMEMORY-0.3.8-linux-amd64/skills/a.md":         "skill",
		"CAUTIOUSMEMORY-0.3.8-linux-amd64/CAUTIOUSMEMORY":               "#!/bin/sh\nexit 0\n",
		"CAUTIOUSMEMORY-0.3.8-linux-amd64/config.example.json": "{}",
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()

	dst := filepath.Join(dir, "out")
	if err := extractBinary(zipPath, dst); err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	if got := readAll(t, dst); !strings.Contains(got, "exit 0") {
		t.Errorf("è§£åŽ‹å‡ºæ¥çš„ä¸æ˜¯ CAUTIOUSMEMORY å¯æ‰§è¡Œæ–‡ä»¶: %q", got)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Error("è§£åŽ‹å‡ºçš„äºŒè¿›åˆ¶å¿…é¡»å¸¦æ‰§è¡Œä½")
	}
}

func TestExtractBinaryMissingEntry(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "release.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("CAUTIOUSMEMORY-0.3.8-linux-amd64/README.md")
	_, _ = w.Write([]byte("readme"))
	_ = zw.Close()
	f.Close()

	if err := extractBinary(zipPath, filepath.Join(dir, "out")); err == nil {
		t.Fatal("åŒ…å†…æ²¡æœ‰å¯æ‰§è¡Œæ–‡ä»¶æ—¶åº”æŠ¥é”™")
	}
}

func TestCheckURLRejectsNonGitHub(t *testing.T) {
	bad := []string{
		"http://github.com/x",           // éž HTTPS
		"https://evil.com/CAUTIOUSMEMORY.zip",    // åŸŸåä¸åœ¨ç™½åå•
		"https://github.com.evil.com/x", // åŽç¼€ä¼ªè£…
		"https://raw.githubusercontent.com.evil.com/x",
	}
	for _, raw := range bad {
		u := mustParse(t, raw)
		if err := checkURL(u); err == nil {
			t.Errorf("checkURL(%q) åº”å½“æ‹’ç»", raw)
		}
	}
	good := []string{
		"https://api.github.com/repos/x/releases/latest",
		"https://objects.githubusercontent.com/blah",
		"https://GitHub.com/x", // åŸŸåå¤§å°å†™ä¸æ•æ„Ÿ
	}
	for _, raw := range good {
		u := mustParse(t, raw)
		if err := checkURL(u); err != nil {
			t.Errorf("checkURL(%q) åº”å½“æ”¾è¡Œï¼Œå´æŠ¥é”™: %v", raw, err)
		}
	}
}

func TestAssetNameMatchesBuildScript(t *testing.T) {
	// build.sh çš„ package_binary ç”¨çš„æ˜¯ CAUTIOUSMEMORY-<ç‰ˆæœ¬>-<os>-<arch>.zipï¼Œä¸”ç‰ˆæœ¬å·
	// åŽ»æŽ‰äº† v å‰ç¼€ã€‚è¿™é‡Œå¯¹é”™ä¸€ä¸ªå­—ç¬¦ï¼Œæ‰€æœ‰å¹³å°çš„ä¸€é”®æ›´æ–°éƒ½ä¼šæ‰¾ä¸åˆ°èµ„äº§ã€‚
	if got := AssetName("v0.3.8", "linux", "amd64"); got != "CAUTIOUSMEMORY-0.3.8-linux-amd64.zip" {
		t.Errorf("AssetName = %q", got)
	}
	if got := AssetName("0.3.8", "windows", "amd64"); got != "CAUTIOUSMEMORY-0.3.8-windows-amd64.zip" {
		t.Errorf("AssetName = %q", got)
	}
}

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("è§£æž %q: %v", raw, err)
	}
	return u
}

func TestSettleClearsMarkerAndStopsRollback(t *testing.T) {
	requireUnix(t)
	p := testPaths(t)
	fakeBin(t, p.Current, "new", 0)
	fakeBin(t, p.Old, "old", 0)
	if err := writeMarker(p.Marker, marker{From: "0.3.7", To: "0.3.8", Attempts: 2}); err != nil {
		t.Fatal(err)
	}

	settle(p)

	if _, err := os.Stat(p.Marker); !os.IsNotExist(err) {
		t.Fatal("ç¡®è®¤ç¨³å®šåŽå‡çº§æ ‡è®°å¿…é¡»æ¸…é™¤")
	}
	// æ ‡è®°æ²¡äº†ï¼ŒåŽç»­æ­£å¸¸é‡å¯å°±ä¸ä¼šå†ç´¯è®¡æ¬¡æ•°ã€ä¹Ÿä¸ä¼šè¯¯è§¦å‘å›žæ»šã€‚
	if _, ok := readMarker(p.Marker); ok {
		t.Error("æ ‡è®°è¯»å–åº”å¤±è´¥")
	}
	// å¤‡ä»½è¦ç•™ç€ï¼Œç”¨æˆ·è¿˜èƒ½æ‰‹åŠ¨å›žæ»šã€‚
	if _, err := os.Stat(p.Old); err != nil {
		t.Error("ç¡®è®¤ç¨³å®šåŽä»åº”ä¿ç•™ä¸Šä¸€ç‰ˆæœ¬å¤‡ä»½")
	}
}

func TestSettleIsNoopWithoutMarker(t *testing.T) {
	requireUnix(t)
	p := testPaths(t)
	fakeBin(t, p.Current, "cur", 0)
	settle(p) // æ™®é€šå¯åŠ¨è·¯å¾„ï¼Œä¸è¯¥ panic ä¹Ÿä¸è¯¥åŠ¨ä»»ä½•æ–‡ä»¶
	if _, err := os.Stat(p.Current); err != nil {
		t.Error("æ— æ ‡è®°æ—¶ settle ä¸åº”å½±å“ä»»ä½•æ–‡ä»¶")
	}
}

