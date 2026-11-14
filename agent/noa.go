package agent

import (
	"log"
	"path/filepath"

	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/noaadapter"
)

// noaWarn returns a diagnostics sink tagging non-fatal noa messages with the
// session, routed through the package logger (agents have no per-instance one).
func noaWarn(session string) func(string) {
	return func(msg string) { log.Printf("[noa] %s: %s", session, msg) }
}

// noa is the "model-driven context compression" mechanism introduced in norma v0.4.0, exposed as a platform experimental feature toggled by users in
// system settings. it's mutually exclusive with built-in compaction: noaadapter.Enable is the sole entry point, which attaches a context
// interceptor (Compactor), Compress tool, and three resident prompts in one call; without Enable it's off
// (built-in compaction works as normal). the switch is parsed by each agent's injected noaEnabledFn, read once per run, so toggling only affects subsequently started runs, no agent rebuild needed.
//
// enableNoa wires noa into opts when the parser reports enabled. archiveRoot is the base directory for persisting compression source text
// (uses global workDir, all agents write under <workDir>/noa, not scattered by task/intent dirs), sessionID
// names the archive subdirectory under it (globally unique, so no collisions within same base dir).
//
// noa is experimental: enable failure must not interrupt real tasks. on error, report via onWarn and fall back to built-in compression.
// on successful enable, clear opts.Compaction to avoid agentcore warning about "two context managers set simultaneously".
func enableNoa(opts *agentcore.Options, enabled func() bool, archiveRoot, sessionID string, onWarn func(string)) {
	if enabled == nil || !enabled() {
		return
	}
	if opts.OnWarn == nil {
		opts.OnWarn = onWarn
	}
	if err := noaadapter.Enable(opts, noaadapter.Options{
		ArchiveBaseDir: filepath.Join(archiveRoot, "noa"),
		SessionID:      sessionID,
		OnWarn:         onWarn,
	}); err != nil {
		if onWarn != nil {
			onWarn("noa compression failed to enable, falling back to built-in compression:" + err.Error())
		}
		return
	}
	// Compactor overrides Compaction, but when both coexist agentcore warns every time; explicitly clear it.
	opts.Compaction = nil
}
