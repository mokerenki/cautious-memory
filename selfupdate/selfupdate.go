// Package selfupdate implements in-app one-click updates for CAUTIOUSMEMORY: it fetches a new
// binary from a GitHub Release, verifies and stages it, then atomically swaps it in on
// the next startup.
//
// Division of responsibilities (see start.sh / start.bat):
//
//	Startup script = a simple supervisor loop that only decides whether to relaunch
//	                 based on the process exit code
//	This package   = all error-prone logic (download / SHA256 verification / smoke
//	                 test / swap / rollback on failure)
//
// The swap lives in Go rather than the scripts because SHA256 verification and smoke
// testing would need two implementations in sh and bat (sha256sum / shasum / certutil),
// and this is exactly the part that must not fail: if a binary that cannot run is
// installed, the supervisor will faithfully relaunch it forever and the user can only
// recover by manually fixing it on the machine.
//
// A complete upgrade spans three process starts:
//
//	â‘  old server receives /api/update/apply -> download/verify -> stage CAUTIOUSMEMORY.new -> exit 75
//	â‘¡ script relaunches the old version -> Bootstrap finds CAUTIOUSMEMORY.new -> verify/smoke -> swap -> exit 75
//	â‘¢ script relaunches, now running the new version -> Bootstrap records one attempt -> clears the marker after a stable startup
//
// Any step that fails falls back to the old version: if â‘¡ fails validation the staged
// artifact is deleted and the old version keeps running; if â‘¢ never reaches marker
// clearing (it crashes before then) for 3 consecutive attempts, CAUTIOUSMEMORY.old is swapped back
// automatically.
package selfupdate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ExitRestart is the exit code meaning "please relaunch me" (EX_TEMPFAIL). When the
// startup script sees it, it restarts immediately without counting toward crash backoff.
// 0 means the user stopped normally (the script exits the loop); all other codes are
// treated as crashes.
const ExitRestart = 75

// maxAttempts is the number of startup attempts allowed after a swap. Each startup of the
// new version increments the counter, and surviving settleDelay clears the marker; if it
// crashes maxAttempts times in a row the new version clearly cannot run and is rolled back.
const maxAttempts = 3

// Paths holds all files involved in one upgrade, all rooted in the directory containing
// the executable.
// CWD is intentionally not used: in service deployments the working directory may be / or
// any other path, which would place staged artifacts elsewhere and break the swap logic.
type Paths struct {
	Dir     string // directory containing the executable
	Current string // currently running binary        CAUTIOUSMEMORY      / CAUTIOUSMEMORY.exe
	New     string // staged new version              CAUTIOUSMEMORY.new  / CAUTIOUSMEMORY.new.exe
	Sum     string // sha256 (hex) of the new version CAUTIOUSMEMORY.new.sha256 / CAUTIOUSMEMORY.new.exe.sha256
	Old     string // backup of the old version       CAUTIOUSMEMORY.old  / CAUTIOUSMEMORY.old.exe
	Marker  string // upgrade state marker            CAUTIOUSMEMORY.upgrade.json
}

// ResolvePaths derives all upgrade paths from the current executable.
//
// On Windows, .new/.old must also carry the .exe suffix, otherwise the smoke test and
// post-swap execution will fail. The suffix is stripped and reattached so naming is
// symmetric across platforms.
func ResolvePaths() (Paths, error) {
	exe, err := os.Executable()
	if err != nil {
		return Paths{}, fmt.Errorf("locate executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	dir := filepath.Dir(exe)
	name := filepath.Base(exe)
	ext := filepath.Ext(name) // ".exe" on Windows, usually empty on Unix
	stem := strings.TrimSuffix(name, ext)

	join := func(suffix string) string { return filepath.Join(dir, stem+suffix+ext) }
	return Paths{
		Dir:     dir,
		Current: exe,
		New:     join(".new"),
		Sum:     join(".new") + ".sha256",
		Old:     join(".old"),
		Marker:  filepath.Join(dir, stem+".upgrade.json"),
	}, nil
}

// marker records the progress of one swap so an automatic rollback can be triggered if
// the new version cannot start.
type marker struct {
	From     string `json:"from"`     // version before the upgrade
	To       string `json:"to"`       // target version
	Attempts int    `json:"attempts"` // startup attempts after the swap
	StagedAt int64  `json:"staged_at"`
}

func readMarker(path string) (marker, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return marker{}, false
	}
	var m marker
	if json.Unmarshal(b, &m) != nil {
		return marker{}, false
	}
	return m, true
}

func writeMarker(path string, m marker) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// cleanStaged removes staged artifacts. Used after a successful swap, failed validation,
// or user cancellation to prevent a leftover CAUTIOUSMEMORY.new from being retried on the next start.
func cleanStaged(p Paths) {
	_ = os.Remove(p.New)
	_ = os.Remove(p.Sum)
}

// CompareVersions compares two version strings and returns -1/0/1 (a<b / a==b / a>b).
// ok=false means at least one side is not a comparable version (for example a local dev
// build like "dev" or a git-describe string such as "0.3.7-2-gabc1234-dirty"). In that
// case callers should disable one-click updates, otherwise a development build could be
// "upgraded" to a release and overwrite uncommitted changes.
func CompareVersions(a, b string) (int, bool) {
	av, aok := parseVersion(a)
	bv, bok := parseVersion(b)
	if !aok || !bok {
		return 0, false
	}
	for i := range 3 {
		if av[i] != bv[i] {
			if av[i] < bv[i] {
				return -1, true
			}
			return 1, true
		}
	}
	return 0, true
}

// parseVersion parses a version string of the form "v0.3.7" / "0.3.7" into [3]int.
//
// Only a clean three-part form is accepted: build.sh produces suffixed versions such as
// "0.3.7-2-gabc1234" for non-tag builds via git describe, and those must be treated as
// incomparable rather than as 0.3.7. Otherwise a development build could be mistaken for
// the latest release or overwritten by one.
func parseVersion(s string) ([3]int, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if s == "" {
		return [3]int{}, false
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

// InDocker reports whether the process is running inside a container. In Docker a swap is
// written to the container's writable layer, so `docker compose up -d` recreating the
// container will revert to the image's bundled version. That is expected behavior (the
// user is pulling a new image at that point), but the frontend needs to explain it clearly.
func InDocker() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	b, err := os.ReadFile("/proc/1/cgroup")
	if err != nil {
		return false
	}
	s := string(b)
	return strings.Contains(s, "docker") || strings.Contains(s, "containerd")
}

