package notify

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

// This file covers two related hardening measures:
//   (1) Delivery addresses must not be used to reach internal networks / cloud metadata (SSRF)
//   (2) Address validation error messages must not leak credentials from the address
//
// About the test environment: many cases in this package use httptest servers on
// 127.0.0.1, which the guard rejects by default. TestMain therefore opens
// AllowLocalTargetsEnv globally, and each SSRF case below clears it again to assert
// the **default-deny** behavior.

func TestMain(m *testing.M) {
	// Allow ordinary cases to reach local fake receivers; SSRF cases clear this themselves.
	_ = os.Setenv(AllowLocalTargetsEnv, "1")
	os.Exit(m.Run())
}

// TestDialGuardRejectsLoopbackByDefault is the core SSRF assertion:
// by default, delivery to a loopback address must be rejected at the **connection layer**.
func TestDialGuardRejectsLoopbackByDefault(t *testing.T) {
	var hit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		_, _ = io.WriteString(w, `{"errcode":0}`)
	}))
	defer srv.Close()

	t.Setenv(AllowLocalTargetsEnv, "") // opt-out escape hatch = default behavior
	_, err := (dingTalkChannel{}).Send(context.Background(),
		map[string]any{"webhook": srv.URL + "/robot/send"}, Message{Items: []Item{{Severity: "high"}}})
	if err == nil {
		t.Fatal("delivery to a loopback address should be rejected by default")
	}
	if hit {
		t.Fatal("request reached a local service -- guard did not take effect")
	}
	// The error message must tell the user how to opt in (a local SMTP relay is a legitimate config).
	if !strings.Contains(err.Error(), AllowLocalTargetsEnv) {
		t.Errorf("rejection message should explain how to explicitly allow: %v", err)
	}
}

// TestDialGuardAllowsLoopbackWhenOptedIn is the inverse case: once explicitly opted in,
// delivery must work -- otherwise legitimate local postfix / internal relays would be
// broken wholesale.
func TestDialGuardAllowsLoopbackWhenOptedIn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	defer srv.Close()

	t.Setenv(AllowLocalTargetsEnv, "1")
	if _, err := (dingTalkChannel{}).Send(context.Background(),
		map[string]any{"webhook": srv.URL + "/robot/send"}, Message{Items: []Item{{Severity: "high"}}}); err != nil {
		t.Fatalf("delivery should succeed once explicitly allowed: %v", err)
	}
}

func TestIsBlockedDialIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "127.1.2.3", "::1",
		"169.254.169.254", // cloud metadata endpoint -- the main reason this function exists
		"169.254.1.1", "fe80::1",
		"0.0.0.0", "::",
		"224.0.0.1", "ff02::1",
		"::ffff:127.0.0.1", // IPv4-mapped form must be normalized first, else it bypasses the check
		"",
	}
	for _, s := range blocked {
		if !isBlockedDialIP(net.ParseIP(s)) {
			t.Errorf("%s should be rejected", s)
		}
	}
	// RFC1918 private ranges are **intentionally allowed**: self-hosted Mattermost /
	// SMTP relays on the internal network are common legitimate delivery targets.
	// This assertion pins that trade-off -- if someone later adds private-range checks
	// here, this test fails and forces a conscious decision instead of silently
	// breaking a batch of deployments.
	allowed := []string{"10.0.0.5", "172.16.3.4", "192.168.1.10", "8.8.8.8", "2606:4700::1111"}
	for _, s := range allowed {
		if isBlockedDialIP(net.ParseIP(s)) {
			t.Errorf("%s should be allowed (private ranges are common legitimate targets)", s)
		}
	}
}

// TestValidateHTTPURLRejectsLiteralPrivateTargets covers the config-stage guard:
// literal IP addresses should be rejected at save time, not on first delivery failure.
func TestValidateHTTPURLRejectsLiteralPrivateTargets(t *testing.T) {
	t.Setenv(AllowLocalTargetsEnv, "")
	for _, raw := range []string{
		"http://127.0.0.1:8080/hook",
		"http://169.254.169.254/latest/meta-data/",
		"http://[::1]:8080/hook",
	} {
		if err := validateHTTPURL(raw); err == nil {
			t.Errorf("%s should be rejected at config time", raw)
		}
	}
	// Public and private addresses still pass (private ranges are left for the dial stage).
	for _, raw := range []string{"https://oapi.dingtalk.com/robot/send", "http://10.0.0.9/hook"} {
		if err := validateHTTPURL(raw); err != nil {
			t.Errorf("%s should pass validation: %v", raw, err)
		}
	}
}

// TestValidateHTTPURLErrorNeverLeaksCredentials covers a branch audit caught me missing.
//
// When url.Parse **fails** it returns a *url.Error whose Error() contains the full original
// address. Last round I only sanitized http.Client.Do's returned errors and missed this one;
// and the "permanent failure path" cases I added back then (file://, gopher://, ftp://) all
// actually parse fine and take the scheme branch -- so they were green without proving this
// path is safe. That was a false guarantee.
func TestValidateHTTPURLErrorNeverLeaksCredentials(t *testing.T) {
	cases := []string{
		"http://127.0.0.1/%zz?access_token=" + leakProbeToken,         // invalid percent-escape
		"https://a.example.com:port/x?access_token=" + leakProbeToken, // non-numeric port
		"http://[::1?access_token=" + leakProbeToken,                  // unbalanced brackets
	}
	for _, raw := range cases {
		// First confirm this input actually makes url.Parse fail. Without this step the case
		// could silently take a different branch -- that is how last round's false guarantee
		// came to be.
		if _, err := url.Parse(raw); err == nil {
			t.Errorf("%q should have failed to parse, otherwise this case does not cover the target branch", raw)
			continue
		}
		err := validateHTTPURL(raw)
		if err == nil {
			t.Errorf("%q should fail validation", raw)
			continue
		}
		assertNoSecret(t, err.Error(), leakProbeToken)
	}
	// Confirm the channel-level wrapper does not leak the address either.
	t.Setenv(AllowLocalTargetsEnv, "")
	err := (dingTalkChannel{}).Validate(map[string]any{"webhook": cases[0]})
	if err == nil {
		t.Fatal("an invalid address should fail validation")
	}
	assertNoSecret(t, err.Error(), leakProbeToken)
}

// TestEmailDialGuardRejectsLoopbackByDefault covers the SMTP channel's dial guard.
//
// The email channel used a bare net.Dialer -- the only gap in the whole SSRF suite: with
// host set to 169.254.169.254 or 127.0.0.1 it connected directly, and when smtp.NewClient's
// handshake failed it wrapped the peer's reply line into the error, surfaced via
// last_error through the delivery-history API -- the same half-blind-read oracle the other
// channels had already closed. The connection-vs-timeout timing difference also leaks
// port probing.
//
// This package's TestMain opens AllowLocalTargetsEnv globally (many cases use fake receivers
// on 127.0.0.1), so this case must clear it itself -- otherwise the guard passes whether or
// not it is present. That is exactly why the gap was found by no test at all.
func TestEmailDialGuardRejectsLoopbackByDefault(t *testing.T) {
	f := newFakeSMTP(t)
	cfg := emailCfg(t, f, nil)

	t.Setenv(AllowLocalTargetsEnv, "") // opt-out escape hatch = default behavior
	_, err := (emailChannel{}).Send(context.Background(), cfg, singleMsg())
	if err == nil {
		t.Fatal("email must not be deliverable to a loopback address by default")
	}
	// The connection must never be established: the guard intercepts in Control, so EHLO
	// is never sent.
	if f.sawCommand("EHLO") || f.sawCommand("HELO") {
		t.Fatal("SMTP session was established -- guard did not take effect")
	}
	// The error message must tell the user how to opt in (a local postfix relay is legitimate).
	if !strings.Contains(err.Error(), AllowLocalTargetsEnv) {
		t.Errorf("rejection message should explain how to explicitly allow: %v", err)
	}
}

// TestEmailDialGuardAllowsLoopbackWhenOptedIn is the paired inverse case: once explicitly
// opted in, delivery must work. Self-hosted SMTP / local relays are very common.
func TestEmailDialGuardAllowsLoopbackWhenOptedIn(t *testing.T) {
	f := newFakeSMTP(t)
	cfg := emailCfg(t, f, nil)

	t.Setenv(AllowLocalTargetsEnv, "1")
	if _, err := (emailChannel{}).Send(context.Background(), cfg, singleMsg()); err != nil {
		t.Fatalf("delivery to a local SMTP relay should succeed once explicitly allowed: %v", err)
	}
	if !f.sawCommand("EHLO") {
		t.Fatal("EHLO was not seen -- session was never really established")
	}
}
