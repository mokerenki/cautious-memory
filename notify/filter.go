package notify

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Filter is the contract for the notification_channels.filter JSONB column: per-channel-instance filter conditions.
// All fields are optional; the default means "no filtering"—this is the fallback semantics for malformed config (see ParseFilter).
type Filter struct {
	// MinSeverity is the minimum severity threshold (low/medium/high/critical); empty means no threshold.
	MinSeverity string `json:"min_severity"`
	// TaskIDs / AssetIDs empty means no restriction; non-empty requires the event to intersect with them.
	TaskIDs  []int64 `json:"task_ids"`
	AssetIDs []int64 `json:"asset_ids"`
	// VulnClassInclude empty means accept all; non-empty requires vulnclass to match any keyword.
	// VulnClassExclude excludes on any match (exclusion takes precedence over inclusion).
	// Matching is a case-insensitive substring—safer than regex: a bad user regex cannot silently disable a channel.
	VulnClassInclude []string `json:"vulnclass_include"`
	VulnClassExclude []string `json:"vulnclass_exclude"`
	// OnStatusChange controls whether this channel receives finding status change events (meaningful only in realtime mode).
	OnStatusChange bool `json:"on_status_change"`
}

// ParseFilter parses channel filter configuration.
//
// **Never returns an error.** This is a deliberate design choice: malformed filter config always degrades to a
// zero-value Filter (= no filtering = match all), because for a vulnerability notification system it is far better
// to send one extra message than to silently drop a high-severity one. Turning a parse failure into "do not notify"
// would give the user a channel that looks configured but actually pushes nothing—the worst failure mode.
func ParseFilter(raw []byte) Filter {
	var f Filter
	if len(raw) == 0 {
		return f
	}
	// On parse failure f stays zero-valued, i.e. no filtering.
	_ = json.Unmarshal(raw, &f)
	return f
}

// ValidMinSeverity reports whether s is a valid severity threshold (empty string means no threshold).
func ValidMinSeverity(s string) bool {
	if s == "" {
		return true
	}
	_, ok := severityRank[s]
	return ok
}

// Validate checks fields in the filter config whose values are constrained, for use when saving a channel.
//
// Why it must be enforced on write: Match treats an unknown threshold as `rank >= 0`, which is always true—
// so a typo in min_severity (e.g. "hgih") would **silently disable** the filter and become "push all".
// This is consistent with the package's "prefer extra notifications over missed ones" tradeoff (nothing is missed),
// but the result is that users think they configured severity-based delivery while actually flooding the channel with
// every finding, with no indication that the config is wrong. This kind of silent downgrade must be caught at input.
//
// Note: Validate is used only on **write** paths. Read paths still use ParseFilter's permissive semantics so that
// bad values already stored in historical data do not make the whole channel unreadable.
func (f Filter) Validate() error {
	if !ValidMinSeverity(f.MinSeverity) {
		return fmt.Errorf("minimum severity %q is invalid; allowed: low / medium / high / critical, or leave empty for no limit", f.MinSeverity)
	}
	return nil
}

// Match determines whether an event should be delivered to a channel with this filter.
//
// **Never returns an error**, for the same reason as ParseFilter: any internal anomaly is treated as a match.
// Evaluation order: event type → severity threshold → task/asset scope → vulnerability class keywords.
func Match(f Filter, s Snapshot) bool {
	// Status change events are received only by channels that explicitly opt in. Off by default, because most users
	// expect "push" to mean "a new finding was discovered", not a running log of every status transition.
	if s.Kind == EventFindingStatusChanged && !f.OnStatusChange {
		return false
	}
	if !AtLeast(s.Severity, f.MinSeverity) {
		return false
	}
	if len(f.TaskIDs) > 0 && !slices.Contains(f.TaskIDs, s.TaskID) {
		return false
	}
	if len(f.AssetIDs) > 0 && !intersectsInt(f.AssetIDs, s.AssetIDs) {
		return false
	}
	// Exclusion takes precedence: a match on any exclusion keyword rejects, even if an inclusion keyword also matches.
	if len(f.VulnClassExclude) > 0 && containsAnyFold(s.VulnClass, f.VulnClassExclude) {
		return false
	}
	if len(f.VulnClassInclude) > 0 && !containsAnyFold(s.VulnClass, f.VulnClassInclude) {
		return false
	}
	return true
}

func intersectsInt(a, b []int64) bool {
	// Linear scan is sufficient for small sets; both sides are on the order of "a few dozen manually selected items",
	// so building a map costs more than it saves.
	for _, v := range b {
		if slices.Contains(a, v) {
			return true
		}
	}
	return false
}

// containsAnyFold reports whether s contains any keyword in keywords (case-insensitively).
func containsAnyFold(s string, keywords []string) bool {
	lower := strings.ToLower(s)
	for _, kw := range keywords {
		kw = strings.ToLower(strings.TrimSpace(kw))
		if kw != "" && strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}