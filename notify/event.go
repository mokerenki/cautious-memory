package notify

// Snapshot is the contract for the notification_events.snapshot JSONB column.
// Writers are the db layer's finding-persisting transaction; readers are the
// server-side delivery engine and filter matching. It lives in this package
// because it is notification-domain payload: db serializes it without
// understanding its fields.
//
// Why store finding fields redundantly instead of looking them up at render
// time: a finding can later be renamed, re-severed, or re-triaged, and pushed
// content should reflect the conclusion as it stood **at the time of the
// event** -- re-querying would surface "later changed to low", which is
// dangerously misleading. Fan-out and rendering therefore need not JOIN the
// findings/tasks/assets tables.
type Snapshot struct {
	// Event type: finding_created / finding_status_changed
	Kind      string  `json:"kind"`
	FindingID int64   `json:"finding_id"`
	TaskID    int64   `json:"task_id"`
	VulnClass string  `json:"vulnclass"`
	Name      string  `json:"name"`
	Severity  string  `json:"severity"`
	Summary   string  `json:"summary"`
	AssetIDs  []int64 `json:"asset_ids"`
	// Non-empty only when kind=finding_status_changed.
	FromStatus string `json:"from_status,omitempty"`
	ToStatus   string `json:"to_status,omitempty"`
}

// Item is one finding queued for delivery, rendered by channels.
type Item struct {
	FindingID int64
	Name      string
	VulnClass string
	Severity  string
	Summary   string
	// Assets are the resolved asset display names (e.g. domain/IP). Filled in
	// by the server layer -- this package does not touch the database and
	// cannot obtain names.
	Assets []string
	// DetailURL is the link back to the finding detail page; empty means no
	// public_base_url is configured, so it is omitted at render time.
	DetailURL string
	// Status-change event fields; when both are non-empty they render as
	// "Pending -> Fixed".
	FromStatus string
	ToStatus   string
}

// IsStatusChange reports whether this item is a status-change event.
func (i Item) IsStatusChange() bool { return i.FromStatus != "" || i.ToStatus != "" }

// Title returns the display title for the item: the human-assigned name takes
// precedence, falling back to the vulnclass, and finally a placeholder so a
// title is never empty.
func (i Item) Title() string {
	if i.Name != "" {
		return i.Name
	}
	if i.VulnClass != "" {
		return i.VulnClass
	}
	return "(Untitled finding)"
}

// Message is the complete content of one channel send.
type Message struct {
	// Length 1 for a single push; an entire batch for a digest. An empty slice
	// is illegal -- callers must guarantee at least one item.
	Items []Item
	// Batch=true renders as a digest: different title, with the time window and
	// item count.
	Batch bool
	// WindowMinutes is the digest period in minutes, used only when Batch=true
	// so the copy can read "last N minutes". It is explicitly passed in rather
	// than computed at render time via time.Since so rendering stays
	// deterministic and therefore testable.
	WindowMinutes int
	// HomeURL is the platform dashboard address (global public_base_url); empty
	// means no dashboard entry.
	HomeURL string
}