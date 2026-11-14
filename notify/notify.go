// Package notify implements IM/email notification channel adapters for finding
// delivery.
//
// Layering: this is a leaf package that depends only on the standard library. It
// knows nothing about the database or server. Channel config arrives as
// map[string]any (the notification_channels.config JSONB column) and content to
// deliver arrives as Message. Splitting it this way lets the error-prone parts
// -- signature computation, UTF-8 truncation, filter matching -- be unit-tested
// without PostgreSQL; the host only orchestrates on the server side.
//
// Concurrency contract: Channel implementations must be stateless. One Channel
// instance is reused concurrently by multiple channel configs (even multiple
// robot instances of the same channel), so all credentials come from the cfg
// parameter; implementations must not cache webhook URLs or similar in their
// own fields.
package notify

// Channel kind identifiers. These values are also the allowed set for
// notification_channels.kind, validated on the server side (same approach as
// findings.status: no DB CHECK, so new kinds can be added later).
const (
	KindDingTalk = "dingtalk" // DingTalk custom robot
	KindFeishu   = "feishu"   // Feishu (incl. Lark) custom robot
	KindWeCom    = "wecom"    // WeCom group robot
	KindWebhook  = "webhook"  // Generic webhook: custom method/headers/JSON template
	KindTelegram = "telegram" // Telegram Bot API
	KindEmail    = "email"    // SMTP email
)

// Event types, mirrored in notification_events.kind.
const (
	EventFindingCreated       = "finding_created"
	EventFindingStatusChanged = "finding_status_changed"
)

// InitKind is the fallback kind used when config kind is empty.
const InitKind = KindDingTalk

// severityRank maps severity levels to comparable ranks. Unknown levels return
// 0, so any min_severity threshold excludes them: when in doubt, do not push,
// to avoid alert spam.
var severityRank = map[string]int{
	"low":      1,
	"medium":   2,
	"high":     3,
	"critical": 4,
}

// SeverityRank returns the severity rank; unknown levels return 0.
func SeverityRank(severity string) int { return severityRank[severity] }

// SeverityLabel returns an emoji-prefixed severity label for message titles and
// card coloring. Unknown levels are echoed back as-is, never invented.
func SeverityLabel(severity string) string {
	switch severity {
	case "critical":
		return "🔴 Critical"
	case "high":
		return "🟠 High"
	case "medium":
		return "🟡 Medium"
	case "low":
		return "🔵 Low"
	default:
		return severity
	}
}

// StatusLabel renders a triage status for status-change messages.
func StatusLabel(status string) string {
	switch status {
	case "pending":
		return "Pending"
	case "in_progress":
		return "In Progress"
	case "confirmed":
		return "Confirmed"
	case "resolved":
		return "Resolved"
	case "fixed":
		return "Fixed"
	case "false_positive":
		return "False Positive"
	case "ignored":
		return "Ignored"
	case "duplicate":
		return "Duplicate"
	case "risk_accepted":
		return "Risk Accepted"
	default:
		return status
	}
}

// AtLeast reports whether severity meets the min threshold. An empty min means
// no threshold, so everything passes. Note that unknown severities rank 0 and
// are rejected by any non-empty min (see severityRank).
func AtLeast(severity, min string) bool {
	if min == "" {
		return true
	}
	return SeverityRank(severity) >= SeverityRank(min)
}