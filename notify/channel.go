package notify

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
)

// Channel is a notification channel adapter. Implementations must be
// **stateless**: the same instance is reused concurrently by multiple channel
// configurations, so all credentials must come from the cfg parameter.
type Channel interface {
	// Kind returns the channel type identifier and must match the registry key.
	Kind() string
	// Validate is called when saving config to check required fields and format.
	// Returned errors are shown directly to the operator, so messages must explain
	// which field is missing rather than saying "invalid config" generically.
	Validate(cfg map[string]any) error
	// Send delivers one message and returns the **number of items actually
	// delivered** and an error.
	//
	// Why return the count: platforms have message length limits, and a digest may
	// be truncated when it cannot fit the whole batch. If the caller marked the
	// whole batch as delivered unconditionally, truncated items would disappear --
	// missing from the message and shown as successful in delivery history. By
	// returning kept, the caller marks only the first kept items and leaves the
	// rest for the next batch.
	//
	// A returned error means delivery failed; *PermanentError indicates it should
	// not be retried. When delivery fails, kept is meaningless and callers should
	// ignore it.
	Send(ctx context.Context, cfg map[string]any, m Message) (int, error)
	// DefaultRatePerMin returns the provider-recommended per-minute delivery
	// limit, used as the default rate for new channel instances. Returning 0 means
	// no known limit.
	DefaultRatePerMin() int
	// SecretKeys returns config key names that are credentials for this channel.
	// API responses mask these values; updates that receive masked values keep the
	// original stored value. Only the implementation knows which fields are
	// credentials (for WeCom the entire Webhook URL is a credential, while for
	// DingTalk only the secret is), so this knowledge must come from the channel,
	// not be guessed by upper layers.
	SecretKeys() []string
	// DestinationKeys returns config key names that determine where messages are
	// sent.
	//
	// Like SecretKeys, this is security-sensitive: destination and credentials are
	// separate fields. If changing only the destination while keeping masked
	// credentials were allowed, anyone able to edit channel config could send the
	// stored real credentials to a server they control, defeating masking. See
	// PrepareConfigUpdate.
	DestinationKeys() []string
}

// registry is the channel registry. An explicit literal is used instead of
// init() self-registration so all available channels are visible in one place
// and omissions in new channels surface at compile time rather than via runtime
// side effects.
var registry = map[string]Channel{
	KindDingTalk: dingTalkChannel{},
	KindWeCom:    weComChannel{},
	KindFeishu:   feishuChannel{},
	KindTelegram: telegramChannel{},
	KindWebhook:  webhookChannel{},
	KindEmail:    emailChannel{},
}

// Get returns the channel implementation for the given kind.
func Get(kind string) (Channel, bool) {
	ch, ok := registry[kind]
	return ch, ok
}

// ValidKind reports whether kind is a supported channel type.
func ValidKind(kind string) bool {
	_, ok := registry[kind]
	return ok
}

// Kinds returns all supported channel types in lexicographic order for stable UI display.
func Kinds() []string {
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// PermanentError marks a delivery failure that should not be retried: bad
// credentials, target rejection, invalid request body, etc. Retries are only
// meaningful for transient failures (network blips, rate limiting, upstream
// 5xx); retrying permanent failures will not succeed and can bury the real
// error in retry logs.
type PermanentError struct{ Err error }

func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

// Permanent marks err as a permanent failure. Returns nil when err is nil,
// allowing `return Permanent(someCheck())`.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &PermanentError{Err: err}
}

// IsPermanent reports whether err or any wrapped error is marked permanent.
func IsPermanent(err error) bool {
	var p *PermanentError
	return errors.As(err, &p)
}

// ---- config read helpers ----
//
// Channel config comes from a database JSONB column; after encoding/json
// unmarshaling it is map[string]any, with numbers as float64 and arrays as
// []any. These helpers normalize that layer and tolerate type drift from UI
// input (for example, a port entered as a string).

// cfgString reads a string config value and trims surrounding whitespace.
func cfgString(cfg map[string]any, key string) string {
	v, ok := cfg[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

// cfgInt reads an integer config value, accepting float64 (JSON default) and strings.
func cfgInt(cfg map[string]any, key string) int {
	v, ok := cfg[key]
	if !ok || v == nil {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(n))
		return i
	}
	return 0
}

// cfgBool reads a boolean config value, also accepting string "true"/"1".
func cfgBool(cfg map[string]any, key string) bool {
	v, ok := cfg[key]
	if !ok || v == nil {
		return false
	}
	switch b := v.(type) {
	case bool:
		return b
	case string:
		b = strings.TrimSpace(strings.ToLower(b))
		return b == "true" || b == "1"
	}
	return false
}

// cfgStrings reads a string-array config value, trimming whitespace and
// dropping empty strings. A single string is also accepted for convenience.
func cfgStrings(cfg map[string]any, key string) []string {
	v, ok := cfg[key]
	if !ok || v == nil {
		return nil
	}
	// Also accept a single string for convenience.
	if s, ok := v.(string); ok {
		s = strings.TrimSpace(s)
		if s == "" {
			return nil
		}
		return []string{s}
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		s, ok := item.(string)
		if !ok {
			continue
		}
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// cfgMap reads a string map config value (for example custom HTTP headers),
// trimming keys/values and dropping empty keys.
func cfgMap(cfg map[string]any, key string) map[string]string {
	v, ok := cfg[key]
	if !ok || v == nil {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, raw := range m {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		val, ok := raw.(string)
		if !ok {
			continue
		}
		out[k] = strings.TrimSpace(val)
	}
	return out
}
