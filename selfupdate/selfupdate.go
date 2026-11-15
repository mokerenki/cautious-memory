// Package selfupdate implements ARTEX’s one-click update from the UI: fetch the new version from a GitHub Release
// binary, verify and stage it, then atomically swap it on next startup.
//
// Responsibilities (see start.sh / start.bat):
//
//	Startup script = simple supervisor loop; only decides whether to restart the process based on its exit code
//	This package = all error-prone logic (download / SHA256 verification / smoke test / swap / rollback on failure)
//
// The swap logic is in Go rather than the scripts because checksum verification and smoke tests in sh and bat
// would require two implementations (sha256sum / shasum / certutil), and this is precisely the step that must not fail: swapping in a
// binary that cannot run makes the supervisor faithfully restart it repeatedly, leaving the user to log in and recover manually.
//
// A complete update involves three process starts:
//
//	① Old server receives /api/update/apply → download and verify → stage artex.new → exit 75
//	② Script restarts the old version → Bootstrap finds artex.new → verify and smoke-test → swap → exit 75
//	③ Script restarts the process, now running the new version → Bootstrap records an attempt → clear the marker after successful startup
//
// Any failure returns to the old version: if verification fails at ②, discard the staged file and keep running the old version; at ③, if the new version fails to survive three consecutive starts before
// the marker is cleared (crashing if it cannot start), automatically restore artex.old.
package selfupdate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ExitRestart is the exit code meaning "please have the supervisor restart me" (EX_TEMPFAIL). When the startup script sees it,
// it restarts immediately without crash backoff. 0 means the user stopped normally (exit the loop); all other codes count as crashes.
const ExitRestart = 75

// maxAttempts is the number of startup attempts allowed after a swap. Each new-version startup increments the counter; surviving
// settleDelay clears the marker. Crashing maxAttempts times means the new version cannot start, so roll back automatically.
const maxAttempts = 3

// Paths contains all files involved in an update, all located next to the **executable**.
// CWD is intentionally not used: a service may run with / or any path as its working directory, so using CWD could place staged files
// elsewhere and break the swap logic.
type Paths struct {
	Dir     string // Directory containing the executable
	Current string // Current binary              artex      / artex.exe
	New     string // Staged new version          artex.new  / artex.new.exe
	Sum     string // SHA256 of new version (hex) artex.new.sha256 / artex.new.exe.sha256
	Old     string // Previous version backup     artex.old  / artex.old.exe
	Marker  string // Update status marker         artex.upgrade.json
}

// ResolvePaths derives all update paths from the current executable.
//
// On Windows, .new/.old must also have the .exe suffix; otherwise the smoke test and execution after the swap will fail,
// so strip the suffix first, then append it to keep names symmetric across platforms.
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
	ext := filepath.Ext(name) // ".exe" on Windows; usually empty on Unix
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

// marker records swap progress and triggers automatic rollback if the new version cannot start.
type marker struct {
	From     string `json:"from"`     // Version before update
	To       string `json:"to"`       // Target version
	Attempts int    `json:"attempts"` // Number of startup attempts after swap
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

// cleanStaged removes staged files. Successful swaps, verification failures, and user cancellations all use it to prevent a leftover
// artex.new from being retried on the next startup.
func cleanStaged(p Paths) {
	_ = os.Remove(p.New)
	_ = os.Remove(p.Sum)
}

// CompareVersions compares two version numbers and returns -1/0/1 (a<b / a==b / a>b).
// ok=false means at least one version cannot be compared (for example, a local development build "dev" or
// "0.3.7-2-gabc1234-dirty" from git describe); in that case, the caller should disable one-click updates,
// or a development build could be "upgraded" to a stable release, overwriting uncommitted changes.
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

// parseVersion parses a version in "v0.3.7" / "0.3.7" form into [3]int.
//
// Accept only clean three-part versions: for non-tag builds, build.sh uses git describe to produce
// versions with suffixes such as "0.3.7-2-gabc1234"; these must be treated as incomparable rather than as
// 0.3.7, or a development build could be wrongly considered "up to date" or overwritten by a stable release.
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

// InDocker reports whether the process is running in a container. In Docker, the swap writes to the container’s writable layer,
// and `docker compose up -d` recreates the container with the image’s version — this is expected
// (the user is pulling a new image anyway), but the frontend needs this information to explain it clearly.
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
