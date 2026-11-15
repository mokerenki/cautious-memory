package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// This file handles delivery-job claiming and state transitions.
//
// Claiming uses a lease rather than a long transaction: set the row to sending and move next_attempt_at into the future as
// the lease expiry time, then commit before making the network request. This avoids holding database locks during delivery—
// network requests can take seconds (15-second client timeout), and holding row locks would hinder other writes to the database.
//
// If a process crashes during delivery, the row remains sending. This is **self-healing**: after the lease expires,
// next_attempt_at is in the past and the next claim picks up the same row again (see the claim condition
// state IN ('pending','sending')). Attempts are incremented when claimed, so crashes cannot cause
// infinite retries: after MaxNotifyAttempts, the row enters failed for manual handling.

// MaxNotifyAttempts is the maximum number of attempts for a delivery, including the first.
// Defined here rather than in the delivery engine because it is state-machine policy; the engine only executes it.
const MaxNotifyAttempts = 3

// MaxDigestBatchSize is the maximum number of deliveries combined into one digest batch.
//
// This limit protects resources: a digest interval may collect tens of thousands of findings (a full scan can easily do so);
// without a cap, claiming loads every row into memory and renders one extremely long message,
// which the channel length limit then truncates, wasting memory and **silently losing** the omitted findings.
// With a cap, excess rows stay in the database for the next batch and are sent in the next interval.
//
// The value 500 keeps a rendered message meaningfully readable under WeCom’s 4,096-byte limit;
// a larger value would only move the truncation point.
const MaxDigestBatchSize = 500

// NotificationDelivery is a delivery job containing the channel configuration and event snapshot needed for rendering.
type NotificationDelivery struct {
	ID            int64           `json:"id"`
	EventID       int64           `json:"event_id"`
	ChannelID     int64           `json:"channel_id"`
	State         string          `json:"state"`
	Attempts      int             `json:"attempts"`
	NextAttemptAt time.Time       `json:"next_attempt_at"`
	LastError     string          `json:"last_error"`
	BatchID       *int64          `json:"batch_id,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	SentAt        *time.Time      `json:"sent_at,omitempty"`
	Snapshot      json.RawMessage `json:"snapshot,omitempty"`
	// Joined rendering context, excluded from JSON (the server layer builds the DTO).
	Channel *NotificationChannel `json:"-"`
	// FindingID/EventKind are carried over from the event so the history list can link directly to finding details.
	FindingID int64  `json:"finding_id,string"`
	EventKind string `json:"event_kind"`
	// ChannelName/ChannelKind are denormalized display fields that avoid a second frontend query.
	ChannelName string `json:"channel_name"`
	ChannelKind string `json:"channel_kind"`
}

const notificationDeliveryCols = `d.id, d.event_id, d.channel_id, d.state, d.attempts, d.next_attempt_at,
       d.last_error, d.batch_id, d.created_at, d.sent_at`

// joinedDeliveryQuery is the common read shape for delivery row + event snapshot + channel configuration.
// All three are required to render a message; querying separately would require three round trips.
const joinedDeliveryQuery = `SELECT ` + notificationDeliveryCols + `,
       e.snapshot, e.kind, e.finding_id,
       c.id, c.name, c.kind, c.enabled, c.config, c.mode, c.filter, c.rate_per_min
FROM notification_deliveries d
JOIN notification_events e ON e.id = d.event_id
JOIN notification_channels c ON c.id = d.channel_id`

func scanNotificationDelivery(sc interface{ Scan(...any) error }) (*NotificationDelivery, error) {
	var (
		dl        NotificationDelivery
		lastErr   sql.NullString
		batchID   sql.NullInt64
		sentAt    sql.NullTime
		snapshot  []byte
		eventKind string
		channel   NotificationChannel
		chEnabled bool
	)
	if err := sc.Scan(&dl.ID, &dl.EventID, &dl.ChannelID, &dl.State, &dl.Attempts, &dl.NextAttemptAt,
		&lastErr, &batchID, &dl.CreatedAt, &sentAt,
		&snapshot, &eventKind, &dl.FindingID,
		&channel.ID, &channel.Name, &channel.Kind, &chEnabled, &channel.Config, &channel.Mode, &channel.Filter, &channel.RatePerMin); err != nil {
		return nil, err
	}
	dl.LastError = lastErr.String
	if batchID.Valid {
		dl.BatchID = &batchID.Int64
	}
	if sentAt.Valid {
		dl.SentAt = &sentAt.Time
	}
	dl.Snapshot = json.RawMessage(snapshot)
	dl.EventKind = eventKind
	dl.ChannelName = channel.Name
	dl.ChannelKind = channel.Kind
	channel.Enabled = &chEnabled
	dl.Channel = &channel
	return &dl, nil
}

// claimQuery describes one claim: select and lock candidates using sel, then set them to sending and
// extend the lease. The caller supplies the $n placeholder and argument for the lease position in sel.
type claimQuery struct {
	sql  string
	args []any
}

// ClaimRealtimeDeliveries claims up to limit due realtime deliveries for one channel.
//
// Claim **per channel** rather than globally and then selecting: the delivery engine rate-limits per channel,
// so it must know how many deliveries remain for this channel before claiming that many rows; otherwise rate limits consume
// retry attempts. If rows are claimed then discarded, rate-limited rows have already used one attempt,
// and the three-attempt budget can be exhausted just by waiting, leaving them failed.
//
// The condition includes sending rows whose lease expired, enabling crash recovery. The lease must be much longer than one
// delivery’s worst-case duration (the channel HTTP client timeout is 15 seconds), or two dispatchers could deliver the same row
// concurrently. Disabled channels are also blocked: disabling already marks existing deliveries skipped,
// and this second check closes the race between disable and claim.
func (d *DB) ClaimRealtimeDeliveries(ctx context.Context, channelID int64, limit int, lease time.Duration) ([]*NotificationDelivery, error) {
	if limit <= 0 {
		return nil, nil
	}
	return d.claimDeliveries(ctx, lease, claimQuery{
		sql: `SELECT dd.id FROM notification_deliveries dd
JOIN notification_channels c ON c.id = dd.channel_id
WHERE dd.channel_id = $1 AND dd.state IN ($2,$3) AND dd.next_attempt_at <= now()
  AND c.enabled AND c.mode = $4
ORDER BY dd.next_attempt_at, dd.id
FOR UPDATE OF dd SKIP LOCKED
LIMIT $5`,
		args: []any{channelID, NotifyStatePending, NotifyStateSending, NotifyModeRealtime, limit},
	}, nil)
}

// DigestBatchDue reports whether a channel has accumulated a due batch: pending deliveries exist and the **oldest**
// has reached the digest interval.
//
// The oldest delivery’s age, not wall-clock alignment, determines due status. A newly created channel therefore will not
// immediately emit a one-item digest, while a long-standing backlog will not wait through another interval.
//
// Kept separate from ClaimDigestBatch because the semantics differ: this function only answers whether it is time to send,
// while claiming takes **all** pending deliveries for the channel (including younger ones); otherwise one interval
// would be split across multiple messages and the digest would lose its meaning.
func (d *DB) DigestBatchDue(ctx context.Context, channelID int64, minAge time.Duration) (bool, error) {
	var due bool
	err := d.QueryRowContext(ctx, `SELECT EXISTS (
  SELECT 1 FROM notification_deliveries d
  JOIN notification_channels c ON c.id = d.channel_id
  WHERE d.channel_id = $1 AND d.state IN ($2,$3) AND c.enabled
  GROUP BY d.channel_id
  HAVING min(d.created_at) <= now() - make_interval(secs => $4)
)`, channelID, NotifyStatePending, NotifyStateSending, int64(minAge.Seconds())).Scan(&due)
	return due, err
}

// ClaimDigestBatch claims currently due pending deliveries for a channel as one digest batch,
// with at most MaxDigestBatchSize items per batch.
//
// Every delivery in a batch shares batch_id, using the smallest ID as a stable, readable batch identifier
// without an extra sequence. COALESCE preserves the original batch ID on retry, keeping the “these N items were sent together”
// relationship across retries.
//
// Select the first N rows by ascending ID rather than randomly, so older deliveries go first and backlogs cannot
// starve older findings while newer ones are sent.
func (d *DB) ClaimDigestBatch(ctx context.Context, channelID int64, limit int, lease time.Duration) ([]*NotificationDelivery, error) {
	if limit <= 0 {
		return nil, nil
	}
	// limit is a **memory bound** supplied as MaxDigestBatchSize by the caller; clamp it here as well
	// in case the caller passes a larger value.
	//
	// Deliberately do not use the rate-limit allowance as the batch size: rate limits count messages, and one batch sends
	// one message and consumes one token via server-side takeTokens—this differs from how many findings are in a batch.
	// These are different units. Previously, to apply rate_per_min to digests, the per-cycle
	// request budget was used as the batch size, so a rate=20/min channel included only one finding per batch,
	// degrading the digest into a realtime notification with digest text. To change rate limiting, adjust takeTokens’ want,
	// not this code.
	if limit > MaxDigestBatchSize {
		limit = MaxDigestBatchSize
	}
	out, err := d.claimDeliveries(ctx, lease, claimQuery{
		sql: `SELECT dd.id FROM notification_deliveries dd
JOIN notification_channels c ON c.id = dd.channel_id
WHERE dd.channel_id = $1 AND dd.state IN ($2,$3) AND dd.next_attempt_at <= now() AND c.enabled
ORDER BY dd.id
FOR UPDATE OF dd SKIP LOCKED
LIMIT $4`,
		args: []any{channelID, NotifyStatePending, NotifyStateSending, limit},
	}, func(tx *sql.Tx, ids []int64) error {
		batchID := ids[0]
		for _, id := range ids {
			if id < batchID {
				batchID = id
			}
		}
		ph, idArgs := placeholders(2, ids)
		_, err := tx.ExecContext(ctx, `UPDATE notification_deliveries SET batch_id = COALESCE(batch_id, $1)
WHERE id IN (`+ph+`)`, append([]any{batchID}, idArgs...)...)
		return err
	})
	return out, err
}

// claimDeliveries selects rows, sets sending and extends the lease, then reads full rows, all in one transaction.
// postClaim is an optional follow-up step (used to write batch_id for digest batches).
func (d *DB) claimDeliveries(ctx context.Context, lease time.Duration, cq claimQuery, postClaim func(*sql.Tx, []int64) error) ([]*NotificationDelivery, error) {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck // 提交成功后是 no-op

	ids, err := selectForClaim(ctx, tx, cq.sql, cq.args...)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, tx.Commit()
	}
	// Set sending and move next_attempt_at into the future; that future time is the lease expiry.
	// Thus “lease not expired” and “retry time not reached” share one condition without adding a column.
	ph, idArgs := placeholders(3, ids)
	if _, err := tx.ExecContext(ctx, `UPDATE notification_deliveries
SET state=$1, attempts=attempts+1, next_attempt_at=now()+make_interval(secs => $2)
WHERE id IN (`+ph+`)`,
		append([]any{NotifyStateSending, lease.Seconds()}, idArgs...)...); err != nil {
		return nil, err
	}
	if postClaim != nil {
		if err := postClaim(tx, ids); err != nil {
			return nil, err
		}
	}
	out, err := loadDeliveriesTx(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

func selectForClaim(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func loadDeliveriesTx(ctx context.Context, tx *sql.Tx, ids []int64) ([]*NotificationDelivery, error) {
	ph, args := placeholders(1, ids)
	rows, err := tx.QueryContext(ctx, joinedDeliveryQuery+` WHERE d.id IN (`+ph+`) ORDER BY d.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*NotificationDelivery{}
	for rows.Next() {
		dl, err := scanNotificationDelivery(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, dl)
	}
	return out, rows.Err()
}

// MarkDeliveriesSent marks a batch of deliveries as sent.
func (d *DB) MarkDeliveriesSent(ctx context.Context, ids []int64) error {
	ph, args := placeholders(2, ids)
	if len(args) == 0 {
		return nil
	}
	_, err := d.ExecContext(ctx, `UPDATE notification_deliveries
SET state=$1, sent_at=now(), last_error='' WHERE id IN (`+ph+`)`, append([]any{NotifyStateSent}, args...)...)
	return err
}

// RescheduleDeliveries returns a batch of deliveries to pending and postpones their retry time.
//
// Return to pending rather than adding an intermediate state, so the number of remaining attempts is represented in one place
// (MaxNotifyAttempts), avoiding state-machine branches multiplying with retry policy.
func (d *DB) RescheduleDeliveries(ctx context.Context, ids []int64, delay time.Duration, errMsg string) error {
	ph, args := placeholders(4, ids)
	if len(args) == 0 {
		return nil
	}
	_, err := d.ExecContext(ctx, `UPDATE notification_deliveries
SET state=$1, next_attempt_at=now()+make_interval(secs => $2), last_error=$3
WHERE id IN (`+ph+`)`,
		append([]any{NotifyStatePending, delay.Seconds(), truncateNotifyError(errMsg)}, args...)...)
	return err
}

// DeferDeliveries returns a batch to pending, immediately claimable, and **undoes the attempt counted at claim time**.
//
// Used only when a digest is split at the channel length limit; items that do not fit in this message are left for the next batch.
// This is not a failure and should not consume retry budget. Attempts were optimistically incremented at claim time,
// so they must be decremented here. Otherwise a backlog of 500 split into 25 chunks of 20
// would cause tail items to hit MaxNotifyAttempts at the third chunk despite never having failed.
//
// GREATEST(...,0) handles the case where a manual retry reset attempts to zero before reaching this code,
// preventing a negative count.
func (d *DB) DeferDeliveries(ctx context.Context, ids []int64, reason string) error {
	ph, args := placeholders(3, ids)
	if len(args) == 0 {
		return nil
	}
	_, err := d.ExecContext(ctx, `UPDATE notification_deliveries
SET state=$1, attempts=GREATEST(attempts-1, 0), next_attempt_at=now(), last_error=$2
WHERE id IN (`+ph+`)`,
		append([]any{NotifyStatePending, truncateNotifyError(reason)}, args...)...)
	return err
}

// FailDeliveries marks a batch of deliveries as permanently failed, awaiting a manual retry from delivery history.
func (d *DB) FailDeliveries(ctx context.Context, ids []int64, errMsg string) error {
	// Placeholders start at $3: $1 is state and $2 is last_error.
	ph, args := placeholders(3, ids)
	if len(args) == 0 {
		return nil
	}
	_, err := d.ExecContext(ctx, `UPDATE notification_deliveries SET state=$1, last_error=$2 WHERE id IN (`+ph+`)`,
		append([]any{NotifyStateFailed, truncateNotifyError(errMsg)}, args...)...)
	return err
}

// RetryNotificationDelivery manually retries a delivery: reset to pending, clear the retry count,
// and make it due immediately. Clearing the count is intentional: clicking “retry” means the prior failure has been addressed,
// so the old count should not limit it.
func (d *DB) RetryNotificationDelivery(ctx context.Context, id int64) error {
	res, err := d.ExecContext(ctx, `UPDATE notification_deliveries
SET state=$2, attempts=0, next_attempt_at=now(), last_error=''
WHERE id=$1 AND state IN ($3,$4)`, id, NotifyStatePending, NotifyStateFailed, NotifyStateSkipped)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("Delivery %d does not exist or cannot be retried in its current state", id)
	}
	return nil
}

// NotificationDeliveryFilter contains query criteria for delivery history.
type NotificationDeliveryFilter struct {
	ChannelID int64
	State     string
	EventKind string
}

func (f NotificationDeliveryFilter) where() (string, []any) {
	var conds []string
	var args []any
	if f.ChannelID > 0 {
		args = append(args, f.ChannelID)
		conds = append(conds, fmt.Sprintf("d.channel_id=$%d", len(args)))
	}
	if f.State != "" {
		args = append(args, f.State)
		conds = append(conds, fmt.Sprintf("d.state=$%d", len(args)))
	}
	if f.EventKind != "" {
		args = append(args, f.EventKind)
		conds = append(conds, fmt.Sprintf("e.kind=$%d", len(args)))
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// ListNotificationDeliveries returns paginated delivery history, newest first.
func (d *DB) ListNotificationDeliveries(ctx context.Context, f NotificationDeliveryFilter, page, pageSize int) ([]*NotificationDelivery, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 50
	}
	where, args := f.where()

	var total int
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM notification_deliveries d
JOIN notification_events e ON e.id = d.event_id`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	q := fmt.Sprintf("%s%s ORDER BY d.id DESC LIMIT $%d OFFSET $%d",
		joinedDeliveryQuery, where, len(args)+1, len(args)+2)
	rows, err := d.QueryContext(ctx, q, append(args, pageSize, (page-1)*pageSize)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []*NotificationDelivery{}
	for rows.Next() {
		dl, err := scanNotificationDelivery(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, dl)
	}
	return out, total, rows.Err()
}

// truncateNotifyError truncates error text to the column limit. Channel response bodies can be very long
// (especially when a generic webhook calls a self-hosted service); without truncation the history payload can grow substantially.
func truncateNotifyError(msg string) string {
	const max = 500
	if len(msg) <= max {
		return msg
	}
	// Back off at a character boundary to avoid leaving a partial UTF-8 character that renders as mojibake.
	cut := max
	for cut > 0 && !isUTF8Start(msg[cut]) {
		cut--
	}
	return msg[:cut] + "…"
}

func isUTF8Start(b byte) bool { return b&0xC0 != 0x80 }

// placeholders generates $n placeholders starting at start and the matching arguments for IN (...).
// For example, start=3 and ids=[7,8] → "$3,$4", [7,8].
func placeholders(start int, ids []int64) (string, []any) {
	ph := make([]string, 0, len(ids))
	args := make([]any, 0, len(ids))
	for i, id := range ids {
		ph = append(ph, fmt.Sprintf("$%d", start+i))
		args = append(args, id)
	}
	return strings.Join(ph, ","), args
}
