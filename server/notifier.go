package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/notify"
)

// Global setting keys (stored in the settings key-value table; no table creation needed).
const (
	// settingNotifyEnabled is the global push toggle. Enabled by default: it is used to quickly stop the bleeding during maintenance,
	// not as a condition for enabling the feature—the actual enabling condition is 「whether a channel is configured」.
	settingNotifyEnabled = "notify_enabled"
	// settingNotifyPublicBaseURL is the externally accessible address used to generate links back to vulnerability details
	// (e.g. https://artex.example.com). If empty, messages will not include a link button.
	// There is no reusable external address configuration in the project, so a new setting is added here.
	settingNotifyPublicBaseURL = "notify_public_base_url"
	// settingNotifyDigestMinutes is the interval (in minutes) for digest mode.
	settingNotifyDigestMinutes = "notify_digest_interval_min"
)

const (
	// notifyTick is the polling interval of the delivery engine. 3 seconds is the upper limit for the engine's real-time performance,
	// and is also the primary source of delay between «vulnerability persisted» and «message arriving in IM».
	notifyTick = 3 * time.Second
	// notifyLease is the lease duration when claiming a delivery. It must be significantly longer than the worst-case duration of a single delivery
	// (the notify package's HTTP client timeout is 15 seconds); otherwise, the same row may be delivered by two
	// dispatchers simultaneously.
	notifyLease = 3 * time.Minute
	// notifyFanOutPerTick limits the number of events dispatched per tick, preventing all historical backlog from being expanded into delivery tasks
	// at once when a channel is first enabled.
	notifyFanOutPerTick = 200
	// notifyDefaultDigestMinutes is the default value for the digest interval.
	notifyDefaultDigestMinutes = 30
	// notifyUnlimitedBurstPerTick is the per-tick delivery limit when a channel has no rate limit.
	// It exists to prevent “one channel configured with no rate limit + thousands of vulnerabilities found in one scan” from
	// making a single loop iteration block for a long time.
	notifyUnlimitedBurstPerTick = 50
	// notifyMaxSendsPerChannelPerTick is the maximum number of items to deliver per channel per tick.
	//
	// This limit is derived from the **lease duration**: rows are assigned a lease (notifyLease = 3 minutes) when claimed,
	// and if too many items are delivered serially in one tick, the worst-case duration may exceed the lease, causing it to expire before the later items are sent.
	// This is not an issue within a single process (Run runs serially in one goroutine, so ticks cannot re-enter), but when **two
	// processes share the same database**, the other process will reclaim expired rows and send them again, and will also
	// attempts double-increments, marking it failed while the original process is still delivering.
	//
	// Value: 3-minute lease / 30-second per-attempt timeout = 6 would **use up the lease exactly**,
	// leaving no margin, so it cannot be used; choose 5 to leave a 30-second margin after the worst-case 150 seconds. This relationship is
	// locked down by TestNotifyTickBudgetFitsWithinLease—changing notifyLease,
	// notifySendTimeout, or this value will cause that assertion to fail.
	notifyMaxSendsPerChannelPerTick = 5
	// notifySendTimeout is the timeout for a single delivery. It also determines the value of the previous constant,
	// and their product must not exceed notifyLease; see TestNotifyTickBudgetFitsWithinLease.
	notifySendTimeout = 30 * time.Second
)

// notifyBackoff is the backoff sequence for failed retries, indexed by the number of attempts made.
// 3 attempts (including the first) correspond to db.MaxNotifyAttempts; both must be changed together.
var notifyBackoff = []time.Duration{
	time.Second,
	5 * time.Second,
	30 * time.Second,
}

// Notifier is the delivery engine for vulnerability notifications.
//
// It runs as an independent goroutine alongside Scheduler (see server.New). It deliberately does not reuse
// Scheduler's tick: the real-time requirement for notifications (3 seconds) differs from the trigger's business cadence,
// and failures in either do not affect the other—stalled notifications should not affect agent triggers.
type Notifier struct {
	s  *Server
	pg *db.DB

	// mu protects buckets. There are few channels and little contention, so one mutex is enough,
	// and introducing a finer-grained structure for it is not worthwhile.
	mu      sync.Mutex
	buckets map[int64]*notifyBucket
}

// notifyBucket is a token bucket for a single channel.
//
// A token bucket is used instead of a sliding window that resets its count every minute because the latter has severe boundary effects:
// sending 20 messages at the end of a window and another 20 the next instant means 40 messages in one second from the platform's perspective,
// which triggers rate limiting; a token bucket replenishes at a constant rate and naturally avoids this burst.
type notifyBucket struct {
	tokens   float64
	lastFill time.Time
}

func newNotifier(s *Server) *Notifier {
	return &Notifier{s: s, pg: s.m.pg, buckets: map[int64]*notifyBucket{}}
}

// Run loops until ctx ends. Started once by server.New.
func (n *Notifier) Run(ctx context.Context) {
	if n.pg == nil {
		return
	}
	t := time.NewTicker(notifyTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n.step(ctx)
		}
	}
}

// step runs one round: dispatch new events first, then deliver due tasks.
//
// Any failure only gets logged and does not interrupt the loop—notification system failures must never become process-level problems.
// Each tick is independent, and the next round will retry naturally.
func (n *Notifier) step(ctx context.Context) {
	if !n.enabled() {
		return
	}
	if _, _, err := n.pg.FanOutPendingEvents(ctx, notifyFanOutPerTick); err != nil {
		log.Printf("[notify] failed to dispatch events: %v", err)
		return
	}
	channels, err := n.pg.ListNotificationChannels(ctx)
	if err != nil {
		log.Printf("[notify] failed to read channels: %v", err)
		return
	}
	baseURL := n.publicBaseURL()
	for _, ch := range channels {
		if !ch.IsEnabled() {
			continue
		}
		// The token bucket's unit is the **number of messages** (equivalent to the number of HTTP requests), not the number of vulnerabilities.
		// They are the same in real-time mode (one message per vulnerability); in digest mode, a whole batch of vulnerabilities is combined into
		// one message, so only one token is consumed.
		//
		// Both modes check the token bucket first, then claim work up to the quota—the order must not be reversed, or deliveries blocked by rate limiting will have
		// already consumed retry attempts.
		now := time.Now()
		if ch.Mode == db.NotifyModeDigest {
			tokens, claimLimit := digestTickPlan()
			if n.takeTokens(ch.ID, ch.RatePerMin, tokens, now) <= 0 {
				continue
			}
			n.stepDigest(ctx, ch, claimLimit, baseURL)
			continue
		}
		allow := n.takeTokens(ch.ID, ch.RatePerMin, notifyMaxSendsPerChannelPerTick, now)
		if allow <= 0 {
			continue
		}
		n.stepRealtime(ctx, ch, allow, baseURL)
	}
}

// digestTickPlan returns the token consumption and maximum batch size for this digest-channel tick.
//
// The two return values have **different units**, which is why this is a separate function:
//
//   - tokens is the number of messages. A batch of vulnerabilities is combined into one message and sent in one HTTP request, so it is always 1.
//     rate_per_min therefore still applies to digest (at most this many digest messages per minute).
//   - claimLimit is the maximum number of vulnerabilities to include in this batch. It is constrained only by the memory limit and is unrelated to the request budget.
//
// Previously, to make rate_per_min apply to digest, the per-round request budget
// (notifyMaxSendsPerChannelPerTick, derived backward from the lease) was passed directly as the batch size.
// As a result, a channel with rate_per_min=20 replenishes only 1 token during a 3-second tick, so each digest
// message contains only 1 vulnerability—digest degrades into “real-time push with summary text,” and readers receive a string of
// “1 new vulnerability in the last 30 minutes,” while db.MaxDigestBatchSize is never reached.
//
// This symptom is hard to catch in end-to-end tests (the existing cases all manually pass a sufficiently large limit to
// stepDigest, bypassing the quota calculation in step), so keep the decision here and let
// TestDigestTickPlanDecouplesBatchSizeFromSendBudget directly pins this down.
func digestTickPlan() (tokens, claimLimit int) {
	return 1, db.MaxDigestBatchSize
}

// stepRealtime claims and delivers real-time tasks for a channel, one message per vulnerability.
func (n *Notifier) stepRealtime(ctx context.Context, ch *db.NotificationChannel, allow int, baseURL string) {
	deliveries, err := n.pg.ClaimRealtimeDeliveries(ctx, ch.ID, allow, notifyLease)
	if err != nil {
		log.Printf("[notify] failed to claim realtime deliveries channel=%d: %v", ch.ID, err)
		return
	}
	if len(deliveries) == 0 {
		return
	}
	channel, cfg, ok := n.adapt(ch)
	if !ok {
		_ = n.pg.FailDeliveries(ctx, deliveryIDs(deliveries), fmt.Sprintf("channel type %q not registered", ch.Kind))
		return
	}
	for _, dl := range deliveries {
		msg, err := n.renderSingle(ctx, dl, baseURL)
		if err != nil {
			// A render failure is a local data issue; retrying won't help.
			_ = n.pg.FailDeliveries(ctx, []int64{dl.ID}, err.Error())
			continue
		}
		n.send(ctx, channel, cfg, msg, []*db.NotificationDelivery{dl})
	}
}

// stepDigest sends pending deliveries for a channel as one aggregated message when the batch expires.
func (n *Notifier) stepDigest(ctx context.Context, ch *db.NotificationChannel, allow int, baseURL string) {
	window := n.digestInterval()
	due, err := n.pg.DigestBatchDue(ctx, ch.ID, window)
	if err != nil {
		log.Printf("[notify] failed to determine digest batch channel=%d: %v", ch.ID, err)
		return
	}
	if !due {
		return
	}
	deliveries, err := n.pg.ClaimDigestBatch(ctx, ch.ID, allow, notifyLease)
	if err != nil {
		log.Printf("[notify] failed to claim digest batch channel=%d: %v", ch.ID, err)
		return
	}
	if len(deliveries) == 0 {
		return
	}
	channel, cfg, ok := n.adapt(ch)
	if !ok {
		_ = n.pg.FailDeliveries(ctx, deliveryIDs(deliveries), fmt.Sprintf("channel type %q not registered", ch.Kind))
		return
	}
	msg, included, err := n.renderBatch(ctx, deliveries, baseURL, int(window.Minutes()))
	if err != nil {
		_ = n.pg.FailDeliveries(ctx, deliveryIDs(deliveries), err.Error())
		return
	}
	// Deliveries omitted from the message because the snapshot is invalid must be explicitly marked as failed. Otherwise, they remain
	// outside included, neither in the message nor the failure list—and when sending succeeds, their status will be
	// missed by the subsequent bulk update, leaving them stuck in sending until the lease expires and they are claimed repeatedly.
	if skipped := excludeDeliveries(deliveries, included); len(skipped) > 0 {
		reason := "事件快照无法解析，本条漏洞无法渲染成消息"
		if fErr := n.pg.FailDeliveries(ctx, deliveryIDs(skipped), reason); fErr != nil {
			log.Printf("[notify] failed to mark invalid-snapshot deliveries channel=%s ids=%v: %v", ch.Kind, deliveryIDs(skipped), fErr)
		}
		log.Printf("[notify] skipped %d deliveries with unparseable snapshots channel=%d", len(skipped), ch.ID)
	}
	// Pass only those included in the message to send: included[i] corresponds exactly to msg.Items[i],
	// send relies on this mapping to apply “the channel response fit the first K items” to the correct delivery rows.
	n.send(ctx, channel, cfg, msg, included)
}

// send delivers and updates status based on the result.
//
// All deliveries in the same batch (possibly dozens in summary mode) share one send result: either delivered or the entire batch is retried.
// Do not retry individual items—the summary is a single message, and resending only part of it would break batch semantics.
//
// The only exception is **segmentation due to the channel's length limit**: the channel response actually fit only the first K items,
// so items starting from K+1 must be left for the next batch instead of being marked successful along with the rest. Otherwise, the omitted
// items are neither in the message nor in the failure list, and disappear entirely.
func (n *Notifier) send(ctx context.Context, channel notify.Channel, cfg map[string]any, msg notify.Message, deliveries []*db.NotificationDelivery) {
	// Set a limit on each delivery so a stalled channel does not hold up all remaining channels in this round.
	sendCtx, cancel := context.WithTimeout(ctx, notifySendTimeout)
	defer cancel()
	delivered, err := channel.Send(sendCtx, cfg, msg)
	if err == nil && delivered > 0 {
		if delivered > len(deliveries) {
			// The number of channel-reported messages cannot exceed the number delivered; if it does, the renderer miscalculated,
			// so treat them all as delivered and record the problem—better than corrupting the records.
			log.Printf("[notify] Channel delivery report count %d exceeds delivery count %d channel=%s; treating all as delivered",
				delivered, len(deliveries), channel.Kind())
			delivered = len(deliveries)
		}
		sent, rest := deliveries[:delivered], deliveries[delivered:]
		if err := n.pg.MarkDeliveriesSent(ctx, deliveryIDs(sent)); err != nil {
			log.Printf("[notify] failed to mark deliveries as sent channel=%s ids=%v: %v", channel.Kind(), deliveryIDs(sent), err)
		}
		if len(rest) > 0 {
			// This message has reached the channel's length limit: immediately return the rest to the queue for the next tick.
			// Use DeferDeliveries rather than RescheduleDeliveries—this isn't a failure,
			// so it shouldn't consume the retry budget (claiming already optimistically added +1; it will be decremented there).
			if err := n.pg.DeferDeliveries(ctx, deliveryIDs(rest),
				fmt.Sprintf("This message has reached the channel length limit; only the first %d messages were delivered, the rest are left for the next batch", delivered)); err != nil {
				log.Printf("[notify] failed to queue remaining delivery segments channel=%s ids=%v: %v", channel.Kind(), deliveryIDs(rest), err)
			}
		}
		return
	}
	if err == nil {
		// The channel reported neither an error nor how many messages were delivered. Treat this as a failure (with backoff),
		// to avoid repeatedly claiming this delivery without ever marking it.
		err = fmt.Errorf("Channel did not report delivered count (delivered=%d)", delivered)
	}

	// Failure handling is decided **per item**, not by checking the batch's maximum attempt count.
	//
	// Previously, `if maxAttempts(deliveries) >= MaxNotifyAttempts` marked the entire batch as failed, but
	// attempts can differ across deliveries: an older delivery that has been retried twice (attempts=2) would drag
	// a brand-new delivery (attempts=1) in the same batch into failed too—the new webhook would be permanently lost without a single retry,
	// exactly the opposite of the goal of “not letting old rows drag new rows down.”
	permanent := notify.IsPermanent(err)
	var failIDs, exhaustedIDs []int64
	byDelay := map[time.Duration][]int64{}
	for _, dl := range deliveries {
		switch {
		case permanent:
			failIDs = append(failIDs, dl.ID)
		case dl.Attempts >= db.MaxNotifyAttempts:
			exhaustedIDs = append(exhaustedIDs, dl.ID)
		default:
			delay := notifyBackoff[min(dl.Attempts, len(notifyBackoff)-1)]
			byDelay[delay] = append(byDelay[delay], dl.ID)
		}
	}

	if len(failIDs) > 0 {
		if fErr := n.pg.FailDeliveries(ctx, failIDs, err.Error()); fErr != nil {
			log.Printf("[notify] failed to mark failed status channel=%s ids=%v: %v", channel.Kind(), failIDs, fErr)
		}
	}
	if len(exhaustedIDs) > 0 {
		reason := fmt.Sprintf("Still failed after %d retries: %s", db.MaxNotifyAttempts, err)
		if fErr := n.pg.FailDeliveries(ctx, exhaustedIDs, reason); fErr != nil {
			log.Printf("[notify] failed to mark failed status channel=%s ids=%v: %v", channel.Kind(), exhaustedIDs, fErr)
		}
	}
	// Reorder by delay group: there are only 3 backoff tiers, so the number of groups is naturally small; no need to issue a separate
	// UPDATE for each delivery (which would result in 500 round trips for a batch of 500).
	for delay, group := range byDelay {
		if rErr := n.pg.RescheduleDeliveries(ctx, group, delay, err.Error()); rErr != nil {
			log.Printf("[notify] failed to reschedule deliveries channel=%s ids=%v: %v", channel.Kind(), group, rErr)
		}
	}
	if len(failIDs)+len(exhaustedIDs) > 0 {
		log.Printf("[notify] delivery failed channel=%d kind=%s permanent_failures=%d retries_exhausted=%d pending_retries=%d: %s",
			deliveries[0].ChannelID, channel.Kind(), len(failIDs), len(exhaustedIDs), len(byDelay), err)
	}
}

// excludeDeliveries returns those in all that are not in keep (compared by pointer identity).
// Used to find deliveries that didn't make it into a message—they must be explicitly handled, not left in limbo.
func excludeDeliveries(all, keep []*db.NotificationDelivery) []*db.NotificationDelivery {
	inKeep := make(map[*db.NotificationDelivery]bool, len(keep))
	for _, dl := range keep {
		inKeep[dl] = true
	}
	var out []*db.NotificationDelivery
	for _, dl := range all {
		if !inKeep[dl] {
			out = append(out, dl)
		}
	}
	return out
}

// adapt retrieves the channel implementation and parses its configuration.
// Returning ok=false means the type is unregistered; delivery should fail immediately rather than retry indefinitely.
func (n *Notifier) adapt(ch *db.NotificationChannel) (notify.Channel, map[string]any, bool) {
	channel, ok := notify.Get(ch.Kind)
	if !ok {
		return nil, nil, false
	}
	var cfg map[string]any
	if len(ch.Config) > 0 {
		// Use an empty map if configuration parsing fails: the channel's own Validate will report "which field is missing",
		// which is more helpful to users fixing the issue than a JSON parsing error.
		_ = json.Unmarshal(ch.Config, &cfg)
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	return channel, cfg, true
}

// renderSingle renders a single vulnerability message.
func (n *Notifier) renderSingle(ctx context.Context, dl *db.NotificationDelivery, baseURL string) (notify.Message, error) {
	snap, err := parseSnapshot(dl)
	if err != nil {
		return notify.Message{}, err
	}
	item, err := n.itemFor(ctx, snap, baseURL)
	if err != nil {
		return notify.Message{}, err
	}
	return notify.Message{Items: []notify.Item{item}, HomeURL: baseURL}, nil
}

// renderBatch renders a summary message. Parse snapshots individually—if one is invalid, skip only that one,
// rather than losing the entire batch summary.
//
// The return value included corresponds **exactly one-to-one** with msg.Items (the i-th delivery ↔ the i-th item).
// This correspondence is a hard requirement: callers use “the channel reports that the first K items fit” to decide which first K deliveries
// to mark as delivered. If we skip a bad snapshot here but don't remove the skipped delivery from included,
// the indices will shift—the bad item that should have failed will be marked as delivered, and a good item will be mistakenly treated as undelivered.
// The caller explicitly marks bad items as failed; see stepDigest.
func (n *Notifier) renderBatch(ctx context.Context, deliveries []*db.NotificationDelivery, baseURL string, windowMinutes int) (notify.Message, []*db.NotificationDelivery, error) {
	items := make([]notify.Item, 0, len(deliveries))
	included := make([]*db.NotificationDelivery, 0, len(deliveries))
	for _, dl := range deliveries {
		snap, err := parseSnapshot(dl)
		if err != nil {
			// A bad snapshot goes neither into the message nor into included—handling it is the caller's responsibility
			// (explicitly mark it as failed, rather than letting it slip through as “delivered”).
			log.Printf("[notify] Omitted unparseable snapshot from summary batch delivery=%d: %v", dl.ID, err)
			continue
		}
		item, err := n.itemFor(ctx, snap, baseURL)
		if err != nil {
			return notify.Message{}, nil, err
		}
		items = append(items, item)
		included = append(included, dl)
	}
	if len(items) == 0 {
		return notify.Message{}, nil, fmt.Errorf("Digest batch %d deliveries all failed to parse", len(deliveries))
	}
	return notify.Message{
		Items:         items,
		Batch:         true,
		WindowMinutes: windowMinutes,
		HomeURL:       baseURL,
	}, included, nil
}

// itemFor renders an event snapshot as an item to be pushed, and also resolves the asset name and details backlink.
func (n *Notifier) itemFor(ctx context.Context, snap notify.Snapshot, baseURL string) (notify.Item, error) {
	assets, err := n.pg.NotificationAssetNames(ctx, snap.AssetIDs)
	if err != nil {
		// Failure to resolve the asset name should not block the push: not being able to read the name is far less serious than not receiving the notification,
		// It's only one missing asset line in the message.
		log.Printf("[notify] failed to resolve asset name finding=%d: %v", snap.FindingID, err)
	}
	item := notify.Item{
		FindingID:  snap.FindingID,
		Name:       snap.Name,
		VulnClass:  snap.VulnClass,
		Severity:   snap.Severity,
		Summary:    snap.Summary,
		Assets:     assets,
		FromStatus: snap.FromStatus,
		ToStatus:   snap.ToStatus,
	}
	if baseURL != "" {
		// The detail page route is web/src/app/(main)/function/findings/detail/page.tsx,
		// which reads the vulnerability id from the query parameter id.
		item.DetailURL = fmt.Sprintf("%s/function/findings/detail?id=%d", baseURL, snap.FindingID)
	}
	return item, nil
}

// takeTokens takes up to **want tokens** from the channel's token bucket and returns the actual number taken.
//
// One token = one message (one HTTP request). In real-time mode, the caller passes the number of messages it needs;
// in summary mode, an entire batch of vulnerabilities is sent in one message, so pass 1.
//
// The bucket capacity is the channel's per-minute limit, replenished at a constant rate. ratePerMin<=0 means no rate limit;
// return a finite but sufficiently large value to prevent a single loop from being held up by an unlimited backlog.
//
// This cap is necessary: without it, we can only drain the entire bucket, and the caller itself has a per-round limit,
// so the extra tokens are unusable and disappear before the next refill—the accumulated burst capacity is forever unreachable,
// and even “no pending deliveries this round” still incurs a charge.
func (n *Notifier) takeTokens(channelID int64, ratePerMin, want int, now time.Time) int {
	if want <= 0 {
		return 0
	}
	if ratePerMin <= 0 {
		return min(want, notifyUnlimitedBurstPerTick)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	b := n.buckets[channelID]
	if b == nil {
		b = &notifyBucket{tokens: float64(ratePerMin), lastFill: now}
		n.buckets[channelID] = b
	}
	// Refill based on elapsed real time, at a rate of ratePerMin/60 per second.
	if elapsed := now.Sub(b.lastFill).Seconds(); elapsed > 0 {
		b.tokens = minF(float64(ratePerMin), b.tokens+elapsed*float64(ratePerMin)/60)
		b.lastFill = now
	}
	// Add a tiny epsilon before rounding: the token count is accumulated as a float, and when refilling in two steps,
	// 0.5 + 0.5 might yield 0.9999999999, which int() truncates to 0 directly—
	// the bucket is mathematically full, yet no token can be taken. 1e-9 is far less than one token and won't forgive a real deficit.
	take := min(int(b.tokens+1e-9), want)
	if take <= 0 {
		return 0
	}
	b.tokens -= float64(take)
	return take
}

// Read the global enable switch.
func (n *Notifier) enabled() bool {
	return n.pg.GetBool(settingNotifyEnabled, true)
}

// publicBaseURL returns the external URL for backlinks, with trailing slashes removed.
func (n *Notifier) publicBaseURL() string {
	v, ok, err := n.pg.GetSetting(settingNotifyPublicBaseURL)
	if err != nil || !ok {
		return ""
	}
	return trimTrailingSlash(v)
}

// digestInterval returns the digest interval, falling back to the default if invalid or unset.
func (n *Notifier) digestInterval() time.Duration {
	v, ok, err := n.pg.GetSetting(settingNotifyDigestMinutes)
	if err != nil || !ok {
		return time.Duration(notifyDefaultDigestMinutes) * time.Minute
	}
	m := 0
	if _, err := fmt.Sscanf(v, "%d", &m); err != nil || m <= 0 {
		return time.Duration(notifyDefaultDigestMinutes) * time.Minute
	}
	return time.Duration(m) * time.Minute
}

// parseSnapshot parses the snapshot for the delivered event.
func parseSnapshot(dl *db.NotificationDelivery) (notify.Snapshot, error) {
	var snap notify.Snapshot
	if len(dl.Snapshot) == 0 {
		return snap, fmt.Errorf("Delivery %d event snapshot is empty", dl.ID)
	}
	if err := json.Unmarshal(dl.Snapshot, &snap); err != nil {
		return snap, fmt.Errorf("Failed to parse delivery %d event snapshot: %w", dl.ID, err)
	}
	if snap.Kind == "" {
		// The event type is determined by the event itself; the one in the snapshot may have been written by an older version.
		snap.Kind = dl.EventKind
	}
	return snap, nil
}

func deliveryIDs(deliveries []*db.NotificationDelivery) []int64 {
	out := make([]int64, 0, len(deliveries))
	for _, dl := range deliveries {
		out = append(out, dl.ID)
	}
	return out
}

func trimTrailingSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
