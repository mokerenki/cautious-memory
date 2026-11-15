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

// smokeEnv makes a subprocess launched by the smoke test skip Bootstrap.
//
// Strictly speaking, this is not necessary: os.Executable() in the subprocess is artex.new, so the derived
// paths all have the .new prefix and cannot touch the real update files. But relying on this coincidence is fragile,
// so an explicit short-circuit is clearer and avoids an unnecessary disk probe in the subprocess.
const smokeEnv = "ARTEX_SELFUPDATE_SMOKE"

// Action is the instruction Bootstrap gives to main.
type Action int

const (
	// Continue: start the server normally.
	Continue Action = iota
	// Restart: exit immediately with ExitRestart so the supervisor restarts the process.
	Restart
)

// State describes the update status at startup so /api/update/check can accurately tell the frontend
// whether the previous update succeeded or was rolled back.
type State struct {
	Pending     bool   // Not yet confirmed stable after swapping
	RolledBack  bool   // Automatic rollback was just performed at startup
	FailedStage bool   // Staged file failed verification/smoke test and was discarded
	Detail      string // One-sentence explanation for the user
}

// Bootstrap runs at the very start of main and must be called before listening on ports or opening the database.
//
// Three possible states:
//
//	① Staged file artex.new exists → verify and smoke-test; swap and restart on success, otherwise discard and keep running the old version
//	② Only the marker remains → the swap just completed; count one attempt and roll back after enough consecutive failures
//	③ Nothing remains → normal startup
func Bootstrap() (Action, State) {
	if os.Getenv(smokeEnv) != "" {
		return Continue, State{}
	}
	p, err := ResolvePaths()
	if err != nil {
		log.Printf("[update] Skipping bootstrap: %v", err)
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

// applyStaged handles the "staged file exists" state: swap after successful verification, otherwise discard it.
//
// This is the only place in the update flow that overwrites the executable, and the final gate: the smoke test catches
// corrupted downloads, wrong architectures, missing dynamic libraries, and similar problems. If an unusable binary gets through,
// the supervisor will keep restarting it, Go code will never run, and automatic rollback will be impossible.
func applyStaged(p Paths) (Action, State) {
	m, _ := readMarker(p.Marker)

	if err := verifyStaged(p); err != nil {
		log.Printf("[update] Staged version failed verification and was discarded; continuing with the current version: %v", err)
		cleanStaged(p)
		_ = os.Remove(p.Marker)
		return Continue, State{FailedStage: true, Detail: "New version verification failed and was discarded: " + err.Error()}
	}

	if err := swap(p); err != nil {
		log.Printf("[update] Swap failed; continuing with the current version: %v", err)
		cleanStaged(p)
		_ = os.Remove(p.Marker)
		return Continue, State{FailedStage: true, Detail: "Swap failed: " + err.Error()}
	}

	// Swap succeeded. Keep the marker so the next startup (running the new version) can confirm stability.
	m.Attempts = 0
	if m.StagedAt == 0 {
		m.StagedAt = time.Now().Unix()
	}
	if err := writeMarker(p.Marker, m); err != nil {
		log.Printf("[update] Failed to write update marker (automatic rollback is unavailable): %v", err)
	}
	log.Printf("[update] Swapped to %s; exiting to restart (exit %d)", orUnknown(m.To), ExitRestart)
	return Restart, State{Pending: true}
}

// confirmOrRollback handles startup after the swap: count attempts and restore the old version if the limit is exceeded.
//
// The count increments only after Go code starts, so this covers failures where the binary runs but initialization crashes
// (incompatible config, occupied port, failed DB migration); a binary that cannot exec at all is caught by the
// pre-swap smoke test. Together, these checks cover both cases.
func confirmOrRollback(p Paths, m marker) (Action, State) {
	m.Attempts++
	if m.Attempts > maxAttempts {
		if err := rollback(p); err != nil {
			// Do not restart again if rollback also fails, or the process may loop forever. Clear the marker
			// and start in the current state; if it cannot start, the user can at least see why in the logs.
			log.Printf("[update] New version failed to start %d consecutive times and rollback also failed: %v", maxAttempts, err)
			_ = os.Remove(p.Marker)
			return Continue, State{Detail: "New version failed to start and rollback also failed: " + err.Error()}
		}
		log.Printf("[update] New version failed to start %d consecutive times; rolled back to %s and exiting to restart (exit %d)",
			maxAttempts, orUnknown(m.From), ExitRestart)
		_ = os.Remove(p.Marker)
		return Restart, State{RolledBack: true, Detail: fmt.Sprintf("New version failed to start; rolled back to %s", orUnknown(m.From))}
	}
	if err := writeMarker(p.Marker, m); err != nil {
		log.Printf("[update] Failed to update the upgrade marker: %v", err)
	}
	log.Printf("[update] New version starting (attempt %d/%d); update will be confirmed after it runs stably",
		m.Attempts, maxAttempts)
	return Continue, State{Pending: true}
}

// Settle confirms that the new version is running stably and clears the update marker.
//
// Called by main after HTTP starts listening: the new version must stay up for this period; otherwise the marker remains,
// and the next startup increments the attempt count until rollback is triggered.
func Settle() {
	p, err := ResolvePaths()
	if err != nil {
		return
	}
	settle(p)
}

func settle(p Paths) {
	if _, ok := readMarker(p.Marker); !ok {
		return // Not a post-update startup; nothing to do
	}
	if err := os.Remove(p.Marker); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("[update] Failed to clear update marker: %v", err)
		return
	}
	log.Printf("[update] New version is stable; update complete (previous version retained as %s)", p.Old)
}

// SettleDelay is how long the new version must run to be considered stable.
const SettleDelay = 30 * time.Second

// verifyStaged checks the staged file: compare SHA256, then launch it once.
func verifyStaged(p Paths) error {
	want, err := os.ReadFile(p.Sum)
	if err != nil {
		return fmt.Errorf("read checksum: %w", err)
	}
	got, err := fileSHA256(p.New)
	if err != nil {
		return fmt.Errorf("calculate checksum: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(string(want)), got) {
		return errors.New("SHA256 mismatch (download is corrupted or has been tampered with)")
	}
	return smokeTest(p.New)
}

// smokeTest launches the new binary with -h to confirm that it can run on the current system.
// This catches truncated downloads, wrong architectures (exec format error), missing dependencies, and many similar problems.
func smokeTest(bin string) error {
	if err := os.Chmod(bin, 0o755); err != nil {
		return fmt.Errorf("set executable permissions: %w", err)
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
			snippet = snippet[:300] + "…"
		}
		return fmt.Errorf("smoke test failed: %v: %s", err, snippet)
	}
	return nil
}

// swap replaces the current binary with the staged version.
//
// Both Unix and Windows allow renaming a running executable (Windows prohibits deleting and
// overwriting it, not renaming it), so no platform-specific handling or self-shutdown is needed.
func swap(p Paths) error {
	// Windows rename does not overwrite an existing target, so remove the .old file left by the previous update first.
	if err := os.Remove(p.Old); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove old backup %s: %w", p.Old, err)
	}
	if err := os.Rename(p.Current, p.Old); err != nil {
		return fmt.Errorf("back up current version: %w", err)
	}
	if err := os.Rename(p.New, p.Current); err != nil {
		// If the swap fails after moving the current version, restore it exactly as it was; otherwise there will be no executable on next startup.
		if rerr := os.Rename(p.Old, p.Current); rerr != nil {
			return fmt.Errorf("failed to install new version (%v) and restore current version: %w", err, rerr)
		}
		return fmt.Errorf("install new version: %w", err)
	}
	_ = os.Remove(p.Sum)
	return nil
}

// rollback restores the old version backed up by swap.
func rollback(p Paths) error {
	if _, err := os.Stat(p.Old); err != nil {
		return fmt.Errorf("no rollback backup at %s: %w", p.Old, err)
	}
	// Move the non-starting new version to .failed for troubleshooting instead of deleting it.
	failed := p.Current + ".failed"
	_ = os.Remove(failed)
	if err := os.Rename(p.Current, failed); err != nil {
		return fmt.Errorf("move failed version: %w", err)
	}
	if err := os.Rename(p.Old, p.Current); err != nil {
		return fmt.Errorf("restore old version: %w", err)
	}
	return nil
}

// Rollback implements /api/update/rollback: manually return to the previous version.
// It only swaps the binaries; the supervisor handles the restart (the caller then exits with ExitRestart).
func Rollback() error {
	p, err := ResolvePaths()
	if err != nil {
		return err
	}
	if _, err := os.Stat(p.Old); err != nil {
		return errors.New("no previous version to roll back to (" + p.Old + " does not exist)")
	}
	cleanStaged(p)
	if err := smokeTest(p.Old); err != nil {
		return fmt.Errorf("previous version cannot run; refusing rollback: %w", err)
	}
	// Swap the current and backup versions so the rollback can itself be reversed.
	tmp := p.Current + ".swap"
	_ = os.Remove(tmp)
	if err := os.Rename(p.Current, tmp); err != nil {
		return fmt.Errorf("move current version: %w", err)
	}
	if err := os.Rename(p.Old, p.Current); err != nil {
		_ = os.Rename(tmp, p.Current)
		return fmt.Errorf("install previous version: %w", err)
	}
	if err := os.Rename(tmp, p.Old); err != nil {
		log.Printf("[update] Failed to tidy the backup after rollback (does not affect operation): %v", err)
	}
	_ = os.Remove(p.Marker)
	return nil
}

// HasBackup reports whether a previous version is available for rollback, so the frontend can decide whether to show the rollback button.
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
