package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

// smokeEnv tells a subprocess launched by the smoke test to skip Bootstrap.
//
// Strictly speaking this is not required: the subprocess executable is CAUTIOUSMEMORY.new, so all
// paths derived from os.Executable() carry the .new suffix and will not collide with the
// real upgrade files. Relying on that coincidence is fragile, though; an explicit short
// circuit is clearer and avoids a pointless disk probe in the subprocess.
const smokeEnv = "CAUTIOUSMEMORY_SELFUPDATE_SMOKE"

// Action is the instruction Bootstrap returns to main.
type Action int

const (
	// Continue: start the server normally.
	Continue Action = iota
	// Restart: exit immediately with ExitRestart so the supervisor script relaunches.
	Restart
)

// State describes the upgrade state at startup so /api/update/check can report to the
// frontend whether the previous upgrade succeeded or was rolled back.
type State struct {
	Pending     bool   // swapped in but not yet confirmed stable
	RolledBack  bool   // an automatic rollback was performed during this startup
	FailedStage bool   // staged artifact validation/smoke test failed and was discarded
	Detail      string // a user-facing one-line explanation
}

// Bootstrap runs at the very beginning of main, before any port is listened on or any
// database is opened.
//
// Three situations:
//
//	â‘  staged artifact CAUTIOUSMEMORY.new exists -> validate + smoke test; on success swap and request restart, on failure discard and keep running the old version
//	â‘¡ only a marker file remains       -> swap already happened; count one attempt, roll back after too many consecutive failures
//	â‘¢ nothing present                  -> normal startup
func Bootstrap() (Action, State) {
	if os.Getenv(smokeEnv) != "" {
		return Continue, State{}
	}
	p, err := ResolvePaths()
	if err != nil {
		log.Printf("[update] skipping bootstrap: %v", err)
		return Continue, State{}
	}

	if _, err := os.Stat(p.New); err == nil {
		return applyStaged(p)
	}

	m, ok := readMarker(p.Marker)
	if !ok {
		return Continue, State{}
	}
	return confirmOrRollback(p, m)
}

// applyStaged handles the "staged artifact exists" case: swap on successful validation,
// discard on failure.
//
// This is the only place in the entire upgrade flow that overwrites the executable, and
// the final gate: the smoke test catches corrupted downloads, wrong architectures, and
// missing dynamic dependencies. If a binary that cannot run is allowed through, the
// supervisor script will relaunch it endlessly while Go code never gets a chance to run,
// making automatic rollback impossible.
func applyStaged(p Paths) (Action, State) {
	m, _ := readMarker(p.Marker)

	if err := verifyStaged(p); err != nil {
		log.Printf("[update] staged new version failed validation, discarded; continuing with current version: %v", err)
		cleanStaged(p)
		_ = os.Remove(p.Marker)
		return Continue, State{FailedStage: true, Detail: "New version validation failed and was discarded: " + err.Error()}
	}

	if err := swap(p); err != nil {
		log.Printf("[update] swap failed, continuing with current version: %v", err)
		cleanStaged(p)
		_ = os.Remove(p.Marker)
		return Continue, State{FailedStage: true, Detail: "Swap failed: " + err.Error()}
	}

	// Swap succeeded. Keep the marker so the next startup (running the new version)
	// can confirm stability.
	m.Attempts = 0
	if m.StagedAt == 0 {
		m.StagedAt = time.Now().Unix()
	}
	if err := writeMarker(p.Marker, m); err != nil {
		log.Printf("[update] failed to write upgrade marker (automatic rollback will be unavailable): %v", err)
	}
	log.Printf("[update] swapped to %s, exiting to restart (exit %d)", orUnknown(m.To), ExitRestart)
	return Restart, State{Pending: true}
}

// confirmOrRollback handles "startup after swap": count attempts and restore the old
// version when the limit is exceeded.
//
// The counter is incremented only after Go code is running, so it covers failures like
// "starts but crashes during initialization" (incompatible config, port in use, DB
// migration failure). "Cannot exec at all" is caught by the pre-swap smoke test; together
// they provide complete coverage.
func confirmOrRollback(p Paths, m marker) (Action, State) {
	m.Attempts++
	if m.Attempts > maxAttempts {
		if err := rollback(p); err != nil {
			// If rollback itself fails, do not restart again or we would loop forever.
			// Clear the marker and let the process start in its current state; if it
			// cannot start, the logs will at least show why.
			log.Printf("[update] new version failed to start %d times in a row and rollback failed: %v", maxAttempts, err)
			_ = os.Remove(p.Marker)
			return Continue, State{Detail: "New version failed to start and rollback failed: " + err.Error()}
		}
		log.Printf("[update] new version failed to start %d times in a row, rolled back to %s, exiting to restart (exit %d)",
			maxAttempts, orUnknown(m.From), ExitRestart)
		_ = os.Remove(p.Marker)
		return Restart, State{RolledBack: true, Detail: fmt.Sprintf("New version failed to start; rolled back to %s", orUnknown(m.From))}
	}
	if err := writeMarker(p.Marker, m); err != nil {
		log.Printf("[update] failed to update upgrade marker: %v", err)
	}
	log.Printf("[update] new version starting (attempt %d/%d); upgrade will be confirmed after stable operation",
		m.Attempts, maxAttempts)
	return Continue, State{Pending: true}
}

// Settle confirms that the new version has been running stably and clears the upgrade marker.
//
// Called by main with a delay after HTTP starts listening: only surviving that period
// counts. Otherwise the marker remains and the next startup continues counting attempts
// until rollback is triggered.
func Settle() {
	p, err := ResolvePaths()
	if err != nil {
		return
	}
	settle(p)
}

func settle(p Paths) {
	if _, ok := readMarker(p.Marker); !ok {
		return // not a startup after an upgrade
	}
	if err := os.Remove(p.Marker); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("[update] failed to clear upgrade marker: %v", err)
		return
	}
	log.Printf("[update] new version is stable, upgrade complete (previous version kept as %s)", p.Old)
}

// SettleDelay is the runtime duration required before the new version is considered alive.
const SettleDelay = 30 * time.Second

// verifyStaged validates the staged artifact: compare SHA256 first, then actually run it.
func verifyStaged(p Paths) error {
	want, err := os.ReadFile(p.Sum)
	if err != nil {
		return fmt.Errorf("read checksum: %w", err)
	}
	got, err := fileSHA256(p.New)
	if err != nil {
		return fmt.Errorf("compute checksum: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(string(want)), got) {
		return errors.New("SHA256 mismatch (download corrupted or tampered)")
	}
	return smokeTest(p.New)
}

// smokeTest runs the new binary with -h to confirm it can actually execute on this system.
// This catches truncated downloads, wrong architecture (exec format error), missing
// dependencies, and similar issues.
func smokeTest(bin string) error {
	if err := os.Chmod(bin, 0o755); err != nil {
		return fmt.Errorf("set executable permission: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, "-h")
	cmd.Env = append(os.Environ(), smokeEnv+"=1")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return errors.New("smoke test timed out (new binary did not respond)")
	}
	if err != nil {
		snippet := strings.TrimSpace(string(out))
		if len(snippet) > 300 {
			snippet = snippet[:300] + "â€¦"
		}
		return fmt.Errorf("smoke test failed: %v: %s", err, snippet)
	}
	return nil
}

// swap replaces the current binary with the staged new version.
//
// Both Unix and Windows allow renaming a running executable (Windows forbids deleting or
// overwriting it, but rename is allowed), so no platform-specific handling or prior
// shutdown is needed here.
func swap(p Paths) error {
	// On Windows rename does not overwrite an existing target, so a .old left from the
	// previous upgrade must be removed first.
	if err := os.Remove(p.Old); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove old backup %s: %w", p.Old, err)
	}
	if err := os.Rename(p.Current, p.Old); err != nil {
		return fmt.Errorf("back up current version: %w", err)
	}
	if err := os.Rename(p.New, p.Current); err != nil {
		// The swap failed but the current version has already been moved away; it must
		// be restored, otherwise there will be no executable on the next startup.
		if rerr := os.Rename(p.Old, p.Current); rerr != nil {
			return fmt.Errorf("install new version failed (%v), and restoring current version failed: %w", err, rerr)
		}
		return fmt.Errorf("install new version: %w", err)
	}
	_ = os.Remove(p.Sum)
	return nil
}

// rollback restores the old version backed up by swap.
func rollback(p Paths) error {
	if _, err := os.Stat(p.Old); err != nil {
		return fmt.Errorf("no backup available for rollback %s: %w", p.Old, err)
	}
	// Move the failing new version to .failed for inspection instead of deleting it.
	failed := p.Current + ".failed"
	_ = os.Remove(failed)
	if err := os.Rename(p.Current, failed); err != nil {
		return fmt.Errorf("move failed version aside: %w", err)
	}
	if err := os.Rename(p.Old, p.Current); err != nil {
		return fmt.Errorf("restore old version: %w", err)
	}
	return nil
}

// Rollback is the implementation of /api/update/rollback: actively revert to the previous
// version. It only performs the swap; restart is still handled by the supervisor script
// (the caller exits with ExitRestart afterward).
func Rollback() error {
	p, err := ResolvePaths()
	if err != nil {
		return err
	}
	if _, err := os.Stat(p.Old); err != nil {
		return errors.New("no previous version available for rollback (" + p.Old + " does not exist)")
	}
	cleanStaged(p)
	if err := smokeTest(p.Old); err != nil {
		return fmt.Errorf("previous version cannot run, refusing rollback: %w", err)
	}
	// Swap current and backup so rollback can be reversed again.
	tmp := p.Current + ".swap"
	_ = os.Remove(tmp)
	if err := os.Rename(p.Current, tmp); err != nil {
		return fmt.Errorf("move current version aside: %w", err)
	}
	if err := os.Rename(p.Old, p.Current); err != nil {
		_ = os.Rename(tmp, p.Current)
		return fmt.Errorf("install previous version: %w", err)
	}
	if err := os.Rename(tmp, p.Old); err != nil {
		log.Printf("[update] failed to tidy backup after rollback (does not affect runtime): %v", err)
	}
	_ = os.Remove(p.Marker)
	return nil
}

// HasBackup reports whether a previous version is available for rollback, so the frontend
// can decide whether to show the rollback button.
func HasBackup() bool {
	p, err := ResolvePaths()
	if err != nil {
		return false
	}
	_, err = os.Stat(p.Old)
	return err == nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unknown version"
	}
	return s
}

