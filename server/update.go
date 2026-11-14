package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/Autumn-27/artex/selfupdate"
)

// HTTP handler for one-click update. The actual download/verify/swap logic
// lives in selfupdate; this handles auth boundaries, concurrency control,
// progress broadcasting, and telling main "time to exit".
//
// Restart is not done by this process: after staging the new version the
// process exits with selfupdate.ExitRestart, and the guardian script
// (start.sh / start.bat, ENTRYPOINT in Docker) relaunches it.

// restartCh closes when an upgrade is ready or rollback completes; main
// receives it and exits with ExitRestart.
var (
	restartOnce sync.Once
	restartCh   = make(chan struct{})
)

// bootState holds the result of selfupdate.Bootstrap at this boot (upgrade
// succeeded / just rolled back / staged artifact discarded), injected by main
// so /api/update/check can tell the frontend what happened last time.
var (
	bootStateMu sync.Mutex
	bootState   selfupdate.State
)

// SetBootUpdateState is called once by main at startup.
func SetBootUpdateState(st selfupdate.State) {
	bootStateMu.Lock()
	defer bootStateMu.Unlock()
	bootState = st
}

func bootUpdateState() selfupdate.State {
	bootStateMu.Lock()
	defer bootStateMu.Unlock()
	return bootState
}

// RestartRequested returns a channel that closes when "please exit and let
// the guardian process relaunch me".
func RestartRequested() <-chan struct{} { return restartCh }

func requestRestart() { restartOnce.Do(func() { close(restartCh) }) }

// releaseCache caches GitHub latest-release query results.
//
// The "update available" top-bar hint queries on every full page load, while
// unauthenticated GitHub API allows 60 req/hr/IP — without caching, opening a
// few tabs or refreshing a few times would exhaust the quota, making actual
// updates fail to query. Explicit "check update" clicks can force bypass the cache.
type releaseCache struct {
	mu      sync.Mutex
	rel     *selfupdate.Release
	err     error
	at      time.Time
	network string
	// fetch is the data-fetch function, kept for test injection; nil = real GitHub query.
	fetch func(context.Context, *http.Client) (*selfupdate.Release, error)
}

const (
	releaseTTL = 30 * time.Minute
	// Failed results are also cached briefly, otherwise GitHub unreachable means
	// every page load waits for a timeout; but TTL must be short so recovery is
	// quick once the network returns.
	releaseErrTTL = 2 * time.Minute
	// Query timeout. NewClient's 30-minute timeout is for full package download;
	// version check cannot wait that long.
	releaseTimeout = 20 * time.Second
)

var relCache = &releaseCache{}

// get returns the latest Release, hitting cache if fresh.
//
//
// The lock is held for the entire fetch: concurrent requests queue for the same
// query result instead of each hitting GitHub (multiple tabs loading at once is
// exactly when rate limits bite).
func (c *releaseCache) get(ctx context.Context, client *http.Client, force bool, network string) (*selfupdate.Release, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !force && c.network == network {
		ttl := releaseTTL
		if c.err != nil {
			ttl = releaseErrTTL
		}
		if !c.at.IsZero() && time.Since(c.at) < ttl {
			return c.rel, c.err
		}
	}

	fetch := c.fetch
	if fetch == nil {
		fetch = selfupdate.FetchLatest
	}
	ctx, cancel := context.WithTimeout(ctx, releaseTimeout)
	defer cancel()
	rel, err := fetch(ctx, client)
	// Request cancelled (user closed tab) doesn't mean GitHub is broken; don't
	// cache it, or the next visitor gets a spurious "canceled" error.
	if err != nil && ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled) {
		return c.rel, err
	}
	c.rel, c.err, c.at, c.network = rel, err, time.Now(), network
	return rel, err
}

// updateProgress is a progress update pushed to the frontend.
type updateProgress struct {
	Phase   selfupdate.Phase `json:"phase"`
	Percent int              `json:"percent"` // only meaningful for download phase; -1 otherwise
	Message string           `json:"message"`
	Version string           `json:"version,omitempty"`
	Error   string           `json:"error,omitempty"`
}

// updateHub holds one upgrade's progress and broadcasts to SSE subscribers.
//
// running also acts as a mutex: re-POSTing /api/update/apply during an upgrade
// returns 409 directly, preventing two goroutines from writing to the same
// artex.new simultaneously.
type updateHub struct {
	mu      sync.Mutex
	running bool
	cur     updateProgress
	subs    map[chan updateProgress]struct{}
}

var updHub = &updateHub{
	cur:  updateProgress{Phase: selfupdate.PhaseIdle, Percent: -1},
	subs: map[chan updateProgress]struct{}{},
}

// begin acquires upgrade permission; returns false if already in progress.
func (h *updateHub) begin(version string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.running {
		return false
	}
	h.running = true
	h.cur = updateProgress{Phase: selfupdate.PhaseDownload, Percent: 0, Message: "preparing...", Version: version}
	h.fanout(h.cur)
	return true
}

// finish completes an upgrade. err == nil means staged successfully, awaiting restart.
func (h *updateHub) finish(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.running = false
	if err != nil {
		h.cur = updateProgress{Phase: selfupdate.PhaseFailed, Percent: -1, Message: "update failed", Error: err.Error(), Version: h.cur.Version}
	} else {
		h.cur = updateProgress{Phase: selfupdate.PhaseStaged, Percent: 100, Message: "new version staged, restarting...", Version: h.cur.Version}
	}
	h.fanout(h.cur)
}

func (h *updateHub) publish(ph selfupdate.Phase, pct int, msg string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cur = updateProgress{Phase: ph, Percent: pct, Message: msg, Version: h.cur.Version}
	h.fanout(h.cur)
}

// fanout must be called with h.mu held. Subscriber channels are buffered;
// full = drop — progress is disposable ephemeral data, never let a stuck SSE
// connection block the upgrade itself.
func (h *updateHub) fanout(p updateProgress) {
	for ch := range h.subs {
		select {
		case ch <- p:
		default:
		}
	}
}

func (h *updateHub) snapshot() (updateProgress, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cur, h.running
}

func (h *updateHub) subscribe() (<-chan updateProgress, func()) {
	ch := make(chan updateProgress, 64)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs, ch)
			h.mu.Unlock()
			close(ch)
		})
	}
}

// updateCheck queries GitHub for the latest release and compares with current.
//
// The frontend also hits api.github.com directly (GitHub CORS is *), but **this
// endpoint is authoritative**: download is done by the backend, so only backend
// GitHub reachability matters. Browser can connect but server cannot (common
// when server is internal or proxy only configured in browser) — then clicking
// update would definitely fail, better to error honestly at check time.
func (s *Server) updateCheck(w http.ResponseWriter, r *http.Request) {
	current := BuildVersion
	mode := "binary"
	if selfupdate.InDocker() {
		mode = "docker"
	}
	boot := bootUpdateState()
	out := map[string]any{
		"current":     current,
		"mode":        mode,
		"os":          runtime.GOOS,
		"arch":        runtime.GOARCH,
		"has_backup":  selfupdate.HasBackup(),
		"repo":        selfupdate.Repo,
		"boot_notice": boot.Detail,
		"rolled_back": boot.RolledBack,
}

// Top-bar hint uses cache (default); explicit "check update" passes force=1 to bypass cache.
	force := r.URL.Query().Get("force") != ""
	client, network, err := s.updateClient()
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	defer client.CloseIdleConnections()
	rel, err := relCache.get(r.Context(), client, force, network)
	if err != nil {
		out["error"] = err.Error()
		writeJSON(w, 200, out)
		return
	}

	latest := rel.TagName
	out["latest"] = latest
	out["notes"] = rel.Body
	out["html_url"] = rel.HTMLURL
	if !rel.PublishedAt.IsZero() {
		out["published_at"] = rel.PublishedAt.Format(time.RFC3339)
	}

	asset := selfupdate.AssetName(latest, runtime.GOOS, runtime.GOARCH)
	out["asset"] = asset
	if a, ok := rel.FindAsset(asset); ok {
		out["asset_available"] = true
		out["size"] = a.Size
	} else {
		out["asset_available"] = false
	}

	cmp, comparable := selfupdate.CompareVersions(current, latest)
	out["comparable"] = comparable
	out["has_update"] = comparable && cmp < 0
	if !comparable {
		// Dev builds (dev / git describe with suffix) have no comparable version.
		// Allowing update would overwrite a locally-debugged binary with a release,
		// so just disable one-click update.
		out["reason"] = fmt.Sprintf("current version %q is not a formal release, one-click update disabled", current)
	}
	writeJSON(w, 200, out)
}

// updateApply downloads and stages a new version, then exits for the guardian
// script to restart.
//
// Returns 202 immediately; actual work runs in background goroutine (full package
// download may take minutes; hanging on the request would hit reverse-proxy
// timeouts). Progress streams via /api/update/stream.
func (s *Server) updateApply(w http.ResponseWriter, r *http.Request) {
	current := BuildVersion

	// Use cache: ensure we install exactly the version the user saw and confirmed.
	client, network, err := s.updateClient()
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	defer client.CloseIdleConnections()
	rel, err := relCache.get(r.Context(), client, false, network)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	cmp, comparable := selfupdate.CompareVersions(current, rel.TagName)
	if !comparable {
		writeErr(w, 400, fmt.Sprintf("Current version %q is not a formal release, one-click update disabled", current))
		return
	}
	if cmp >= 0 {
		writeErr(w, 400, fmt.Sprintf("Already on latest version %s", current))
		return
	}
	if !updHub.begin(rel.TagName) {
		writeErr(w, 409, "An update is already in progress")
		return
	}

	go func() {
		// Deliberately use s.ctx not request ctx: HTTP response returns, request
		// ends, download attached to it would be cancelled immediately.
		defer client.CloseIdleConnections()
		err := selfupdate.Stage(s.ctx, client, rel, current, func(ph selfupdate.Phase, pct int, msg string) {
			updHub.publish(ph, pct, msg)
		})
		updHub.finish(err)
		if err != nil {
			log.Printf("[update] update failed: %v", err)
			return
		}
		log.Printf("[update] %s -> %s staged, exiting to complete swap", current, rel.TagName)
		// Allow time for final progress to reach frontend, then trigger exit.
		time.Sleep(1500 * time.Millisecond)
		requestRestart()
	}()

	writeJSON(w, 202, map[string]any{"ok": true, "target": rel.TagName})
}

// updateRollback actively rolls back to the previous version (the artex.old
// backup from before the swap).
func (s *Server) updateRollback(w http.ResponseWriter, r *http.Request) {
	if _, running := updHub.snapshot(); running {
		writeErr(w, 409, "Update in progress, cannot roll back")
		return
	}
	if err := selfupdate.Rollback(); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	log.Printf("[update] manually rolled back to previous version, exiting to complete switch")
	writeJSON(w, 202, map[string]any{"ok": true})
	go func() {
		time.Sleep(500 * time.Millisecond)
		requestRestart()
	}()
}

// updateStream pushes update progress via SSE.
func (s *Server) updateStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, unsub := updHub.subscribe()
	defer unsub()

	send := func(p updateProgress) {
		b, _ := json.Marshal(p)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}
	// Send current state first so page refresh sees in-progress upgrade immediately.
	cur, _ := updHub.snapshot()
	send(cur)

	ctx := r.Context()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case p, ok := <-ch:
			if !ok {
				return
			}
			send(p)
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}
