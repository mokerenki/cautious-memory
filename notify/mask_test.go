package notify

import (
	"errors"
	"strings"
	"testing"
)

func TestMaskedValueHidesBodyButKeepsTailHint(t *testing.T) {
	const secret = "https://oapi.dingtalk.com/robot/send?access_token=abcdef123456"
	got := MaskedValue(secret)
	if strings.Contains(got, "abcdef123456") {
		t.Fatalf("masked value leaked the full credential: %q", got)
	}
	if strings.Contains(got, "oapi.dingtalk.com") {
		t.Fatalf("masked value must not expose the address body: %q", got)
	}
	// The last 6 digits are kept so the user can still tell which bot this is.
	if !strings.HasSuffix(got, "123456") {
		t.Fatalf("should keep the last 6 digits as an identification hint: %q", got)
	}
	if !IsMasked(got) {
		t.Fatalf("masked value must be recognizable by IsMasked: %q", got)
	}
}

func TestMaskedValueShortSecretGivesNoHint(t *testing.T) {
	// A short credential that also exposed its last 6 digits would expose the whole thing.
	for _, s := range []string{"abc", "abcdef", ""} {
		got := MaskedValue(s)
		if got != MaskedPrefix {
			t.Fatalf("a credential of length %d must not give a tail hint, got %q", len(s), got)
		}
		if s != "" && strings.Contains(got, s) {
			t.Fatalf("masked value contained the original value: %q", got)
		}
	}
}

func TestMaskConfigMasksOnlySecrets(t *testing.T) {
	cfg := map[string]any{
		"webhook": "https://example.com/hook?token=SECRETVALUE",
		"secret":  "SECtest123456",
		"port":    float64(587),
		"host":    "smtp.example.com",
	}
	masked := MaskConfig(KindDingTalk, cfg)
	for _, k := range []string{"webhook", "secret"} {
		s, _ := masked[k].(string)
		if !IsMasked(s) {
			t.Errorf("%s should be masked, got %q", k, s)
		}
	}
	// Non-credential fields must be preserved as-is, otherwise the UI cannot display them.
	if masked["port"] != float64(587) {
		t.Errorf("non-credential field port must not be modified: %v", masked["port"])
	}
}

func TestMaskConfigUnknownKindReturnsEmpty(t *testing.T) {
	// When the channel type is unrecognized, it is safer to show an empty config
// than to hand back raw content that might contain credentials.
	got := MaskConfig("nope", map[string]any{"webhook": "https://x/y?token=LEAK"})
	if len(got) != 0 {
		t.Fatalf("an unrecognized channel type should return an empty config, got %v", got)
	}
}

func TestMaskConfigDoesNotMutateInput(t *testing.T) {
	// Masking is a presentation-layer behavior; it must not overwrite the real values in the store.
	cfg := map[string]any{"webhook": "https://example.com/hook", "secret": "SECtest123456"}
	_ = MaskConfig(KindDingTalk, cfg)
	if IsMasked(cfg["secret"].(string)) {
		t.Fatal("MaskConfig mutated its input, which would overwrite real credentials with a masked value")
	}
}

func TestMergeConfigKeepsStoredOnMaskedIncoming(t *testing.T) {
	stored := map[string]any{"webhook": "https://real/hook", "secret": "REALSECRET", "method": "POST"}
	// The user only changed method; the browser submits a masked value plus the new method.
	incoming := map[string]any{
		"webhook": MaskedValue("https://real/hook"),
		"secret":  MaskedValue("REALSECRET"),
		"method":  "PUT",
	}
	got := MergeConfig(stored, incoming)
	if got["webhook"] != "https://real/hook" || got["secret"] != "REALSECRET" {
		t.Fatalf("masked fields should keep the stored original value, got %v", got)
	}
	if got["method"] != "PUT" {
		t.Fatalf("the modified field should take effect, got %v", got["method"])
	}
}

func TestMergeConfigEmptyStringClears(t *testing.T) {
	stored := map[string]any{"webhook": "https://real/hook", "secret": "REALSECRET"}
	got := MergeConfig(stored, map[string]any{"secret": ""})
	if _, ok := got["secret"]; ok {
		t.Fatalf("an empty string should clear the field, got %v", got)
	}
	// Unmentioned fields are kept (partial-update semantics).
	if got["webhook"] != "https://real/hook" {
		t.Fatalf("unmentioned fields should be preserved, got %v", got)
	}
}

func TestMergeConfigKeepsUnmentionedStoredKeys(t *testing.T) {
	stored := map[string]any{"host": "smtp.example.com", "port": float64(587), "password": "pw"}
	got := MergeConfig(stored, map[string]any{"port": float64(465)})
	if got["host"] != "smtp.example.com" || got["password"] != "pw" {
		t.Fatalf("unmentioned fields should be preserved, got %v", got)
	}
	if got["port"] != float64(465) {
		t.Fatalf("an explicitly mentioned field should be updated, got %v", got["port"])
	}
}

// TestPrepareConfigUpdateBlocksDestinationSwap is the most important safety invariant
// in this package:
// **Changing the destination address must not carry the old credentials over.**
//
// These cases use exactly attack-shaped input (change only the address, say nothing
// about the credentials), rather than "correct input for the defense logic" — if you
// only test the latter, the defense can be a no-op and everything still passes green.
func TestPrepareConfigUpdateBlocksDestinationSwap(t *testing.T) {
	cases := []struct {
		name     string
		kind     string
		stored   map[string]any
		incoming map[string]any
		// wantMissing is the credential key expected to be flagged as missing.
		wantMissing string
	}{
		{
			name: "generic webhook changes destination and tries to reuse the Authorization header",
			kind: KindWebhook,
			stored: map[string]any{
				"url":     "https://legit.example.com/hook",
				"headers": map[string]any{"Authorization": "Bearer REAL-TOKEN"},
			},
			incoming:    map[string]any{"url": "https://attacker.tld/c"},
			wantMissing: "headers",
		},
		{
			name:        "Telegram changes base_url and tries to send the Bot Token to its own endpoint",
			kind:        KindTelegram,
			stored:      map[string]any{"bot_token": "123456:REAL", "chat_id": "1", "base_url": "https://api.telegram.org"},
			incoming:    map[string]any{"base_url": "https://attacker.tld"},
			wantMissing: "bot_token",
		},
		{
			name:        "email changes SMTP host and tries to hand over the password",
			kind:        KindEmail,
			stored:      map[string]any{"host": "smtp.corp.com", "port": 587, "password": "REALPW", "from": "a@b.c", "to": []any{"d@e.f"}},
			incoming:    map[string]any{"host": "smtp.attacker.tld"},
			wantMissing: "password",
		},
		{
			name:        "email disables TLS and still must re-confirm the password",
			kind:        KindEmail,
			stored:      map[string]any{"host": "smtp.corp.com", "port": 587, "tls": false, "password": "REALPW", "from": "a@b.c", "to": []any{"d@e.f"}},
			incoming:    map[string]any{"tls": true},
			wantMissing: "password",
		},
		{
// A masked value means "keep the old credentials"; in a destination-change
		// context that must also be rejected.
		name:        "submit masked credentials plus a new address",
			kind:        KindTelegram,
			stored:      map[string]any{"bot_token": "123456:REAL", "chat_id": "1", "base_url": "https://api.telegram.org"},
			incoming:    map[string]any{"base_url": "https://attacker.tld", "bot_token": MaskedValue("123456:REAL")},
			wantMissing: "bot_token",
		},
		{
			name:        "DingTalk webhook change tries to reuse the signing secret",
			kind:        KindDingTalk,
			stored:      map[string]any{"webhook": "https://oapi.dingtalk.com/robot/send?access_token=OLD", "secret": "REALSEC"},
			incoming:    map[string]any{"webhook": "https://attacker.tld/hook"},
			wantMissing: "secret",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			merged, err := PrepareConfigUpdate(tc.kind, tc.stored, tc.incoming)
			if err == nil {
				t.Fatalf("changing the destination without re-confirming credentials must be rejected; got config %v", merged)
			}
			var target *ErrDestinationChangedWithoutCredentials
			if !errors.As(err, &target) {
				t.Fatalf("should return a dedicated error type so the interface can give an actionable hint, got %T: %v", err, err)
			}
			found := false
			for _, m := range target.Missing {
				if m == tc.wantMissing {
					found = true
				}
			}
			if !found {
				t.Fatalf("should name the missing credential key %q, got %v", tc.wantMissing, target.Missing)
			}
// The error message should guide the operator on how to fix it.
		if !strings.Contains(err.Error(), tc.wantMissing) {
			t.Errorf("error message should mention %q: %v", tc.wantMissing, err)
		}
		})
	}
}

// TestPrepareConfigUpdateAllowsLegitimateEdits is the inverse case: normal edits
// must not be blocked, otherwise this guard will be bypassed or removed as "too annoying".
func TestPrepareConfigUpdateAllowsLegitimateEdits(t *testing.T) {
	cases := []struct {
		name     string
		kind     string
		stored   map[string]any
		incoming map[string]any
	}{
		{
			name:     "only the name changes (config echoed back as-is)",
			kind:     KindWebhook,
			stored:   map[string]any{"url": "https://legit.example.com/hook", "headers": map[string]any{"Authorization": "Bearer REAL"}},
			incoming: map[string]any{"url": MaskedValue("https://legit.example.com/hook")},
		},
		{
			name:     "only the request method changes; address and credentials stay untouched",
			kind:     KindWebhook,
			stored:   map[string]any{"url": "https://legit.example.com/hook", "method": "POST"},
			incoming: map[string]any{"method": "PUT"},
		},
		{
			name:     "change the address **and** supply new credentials at the same time",
			kind:     KindWebhook,
			stored:   map[string]any{"url": "https://old.example.com/hook", "headers": map[string]any{"Authorization": "Bearer OLD"}},
			incoming: map[string]any{"url": "https://new.example.com/hook", "headers": map[string]any{"Authorization": "Bearer NEW"}},
		},
		{
			name:     "change the address and explicitly declare that credentials are no longer needed",
			kind:     KindWebhook,
			stored:   map[string]any{"url": "https://old.example.com/hook", "headers": map[string]any{"Authorization": "Bearer OLD"}},
			incoming: map[string]any{"url": "https://new.example.com/hook", "headers": ""},
		},
		{
			name:     "Telegram changes chat_id (not the destination)",
			kind:     KindTelegram,
			stored:   map[string]any{"bot_token": "t", "chat_id": "1", "base_url": "https://api.telegram.org"},
			incoming: map[string]any{"chat_id": "-100200"},
		},
		{
			name:     "email changes recipients (not the destination)",
			kind:     KindEmail,
			stored:   map[string]any{"host": "smtp.corp.com", "port": 587, "password": "PW", "from": "a@b.c", "to": []any{"x@y.z"}},
			incoming: map[string]any{"to": []any{"new@y.z"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			merged, err := PrepareConfigUpdate(tc.kind, tc.stored, tc.incoming)
			if err != nil {
				t.Fatalf("a legitimate edit was wrongly blocked: %v", err)
			}
			if merged == nil {
				t.Fatal("should return a merged result")
			}
		})
	}
}

// TestPrepareConfigUpdatePortTypeTolerance covers an easy-to-misjudge detail:
// The port submitted by the frontend is a JSON number (float64), and the value
// read back from the store is also float64, but the two may have different Go
// types (e.g. int vs float64). Comparing with == would treat "no change" as
// "changed", popping a "please re-enter your password" prompt for users who
// only changed the name — a false alarm that erodes trust in this guard.
func TestPrepareConfigUpdatePortTypeTolerance(t *testing.T) {
	stored := map[string]any{"host": "smtp.corp.com", "port": float64(587), "password": "PW"}
	// The same port submitted as an int.
	if _, err := PrepareConfigUpdate(KindEmail, stored, map[string]any{"port": 587}); err != nil {
		t.Fatalf("an identical port value (different type only) must not be treated as a destination change: %v", err)
	}
	// A real port change must be blocked.
	if _, err := PrepareConfigUpdate(KindEmail, stored, map[string]any{"port": 25}); err == nil {
		t.Fatal("a port change should be blocked")
	}
}

// TestPrepareConfigUpdateSurvivesRepeatedSaveWithBlankDestination covers the
// "destination field may be left blank" path: leaving Telegram's base_url empty
// means "use the official API address".
//
// This used to make the channel permanently fail to save starting from the second save:
//
//	Initial create stores base_url:"" (the create path saves the frontend config
//	directly, bypassing MergeConfig)
//	→ first save, MergeConfig treats the empty string as an explicit clear and
//	  deletes the key
//	→ second save, incoming is still "" but the key is gone from stored, so it is
//	  judged "address changed"
//	→ bot_token is a masked echo value → 400 "destination address changed, please
//	  also re-enter the credential fields"
//
// The user changed nothing, yet could never save again without re-pasting the Bot Token.
func TestPrepareConfigUpdateSurvivesRepeatedSaveWithBlankDestination(t *testing.T) {
	stored := map[string]any{"bot_token": "123:ABC", "chat_id": "-100", "base_url": ""}

// The frontend's buildConfig() submits a value for every field defined for this
// channel: credentials are echoed back masked, empty text boxes submit an empty
// string. Reproduce its full output here rather than submitting only "the changed keys".
	submit := func() map[string]any {
		return map[string]any{
			"bot_token": MaskedValue("123:ABC"),
			"chat_id":   "-100",
			"base_url":  "",
		}
	}

	// First save: only the channel name changed, config is echoed back as-is.
	merged, err := PrepareConfigUpdate(KindTelegram, stored, submit())
	if err != nil {
		t.Fatalf("first save was wrongly blocked: %v", err)
	}
	if _, ok := merged["base_url"]; ok {
		t.Fatal("premise changed: an empty string should be deleted by MergeConfig — the step this case is built to cover is the one after the key disappears")
	}

	// Second save: submission is identical to the previous one; the user changed nothing.
	merged2, err := PrepareConfigUpdate(KindTelegram, merged, submit())
	if err != nil {
		t.Fatalf("second save was wrongly blocked (user changed nothing): %v", err)
	}
	// Third save, to confirm this is not a "fails only once" situation but a stable, savable state.
	if _, err := PrepareConfigUpdate(KindTelegram, merged2, submit()); err != nil {
		t.Fatalf("third save was wrongly blocked: %v", err)
	}
	// Credentials must be preserved all the way through, not cleared along with the empty-string logic.
	if got := merged2["bot_token"]; got != "123:ABC" {
		t.Fatalf("Bot Token should retain its original value, got %v", got)
	}
}

// TestPrepareConfigUpdateStillGuardsBlankDestinationChanges is the pair to the previous
// case: treating an empty string as equivalent to "key absent" **must not** also
// let a real destination change through.
// Both directions are real credential-exfiltration paths — Telegram's Bot Token
// lives in the URL path, so changing base_url means handing the Token to the new address.
func TestPrepareConfigUpdateStillGuardsBlankDestinationChanges(t *testing.T) {
	// Direction one: moving from "empty" (official address) to a self-hosted address.
	official := map[string]any{"bot_token": "123:ABC", "chat_id": "-100"}
	if _, err := PrepareConfigUpdate(KindTelegram, official, map[string]any{
		"bot_token": MaskedValue("123:ABC"),
		"base_url":  "https://tg-proxy.attacker.tld",
	}); err == nil {
		t.Fatal("moving from the official address to a self-hosted one must require re-entering the Token")
	}

	// Direction two: clearing a self-hosted address (= switching back to the official API) is also a destination change.
	proxied := map[string]any{"bot_token": "123:ABC", "base_url": "https://proxy.internal/bot"}
	if _, err := PrepareConfigUpdate(KindTelegram, proxied, map[string]any{
		"bot_token": MaskedValue("123:ABC"),
		"base_url":  "",
	}); err == nil {
		t.Fatal("clearing a self-hosted address (switching back to the official API) is also a destination change and must require re-entering the Token")
	}
}

func TestDestinationKeysDeclaredForEveryKind(t *testing.T) {
	// Same reasoning as SecretKeys: if a channel forgets to declare its destination keys,
	// PrepareConfigUpdate cannot protect it.
	for kind, ch := range registry {
		if len(ch.DestinationKeys()) == 0 {
			t.Errorf("channel %s declared no destination keys; the change-destination-leaks-credentials guard does not apply to it", kind)
		}
		if len(ch.SecretKeys()) == 0 {
			t.Errorf("channel %s declared no credential keys", kind)
		}
	}
}

func TestSecretKeysDeclaredForEveryKind(t *testing.T) {
	// The compiler already forces every channel to implement SecretKeys; here we
	// confirm once more that "no channel hands in a blank mask" — a channel returning
	// an empty slice means its credentials are echoed back to the browser in plaintext.
	expect := map[string]bool{
		KindDingTalk: true, KindFeishu: true, KindWeCom: true,
		KindWebhook: true, KindTelegram: true, KindEmail: true,
	}
	for kind, ch := range registry {
		if !expect[kind] {
			t.Errorf("channel %s is not registered with a masking expectation in the test", kind)
			continue
		}
		if len(ch.SecretKeys()) == 0 {
			t.Errorf("channel %s declared no credential fields; its config will be echoed back in plaintext", kind)
		}
	}
}

// TestPrepareConfigUpdateRejectsMaskedInContainer covers a gap flagged in an audit:
// when a masked sentinel is stuffed into a **non-string** container (e.g. webhook.headers
// is an object), MergeConfig only treats a string with the prefix as masked, so the
// literal "__masked__" gets stored as a real header value — subsequent auth silently
// breaks, with no error reported.
func TestPrepareConfigUpdateRejectsMaskedInContainer(t *testing.T) {
	stored := map[string]any{
		"url":     "https://legit.example.com/hook",
		"headers": map[string]any{"Authorization": "Bearer REAL"},
	}
	// A masked sentinel smuggled inside an object.
	incoming := map[string]any{
		"headers": map[string]any{"Authorization": MaskedPrefix},
	}
	if _, err := PrepareConfigUpdate(KindWebhook, stored, incoming); err == nil {
		t.Fatal("a masked sentinel hidden inside a struct must be rejected (otherwise the literal gets stored)")
	}
	// Submitting the object whole (a genuine new value) is still accepted.
	ok := map[string]any{"headers": map[string]any{"Authorization": "Bearer NEW"}}
	if _, err := PrepareConfigUpdate(KindWebhook, stored, ok); err != nil {
		t.Fatalf("submitting a normal new request header should not be blocked: %v", err)
	}
}
