package notify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// This file is an invariant test: **no** error text emitted from the channel
// implementations may carry credentials.
//
// Why a separate file: the original channel cases only covered the success path and
// platform business errors -- they never looked at transport-layer failures. And it is
// precisely transport-layer failures (connection refused / DNS failure / timeout) that
// are most dangerous: http.Client.Do returns a *url.Error that embeds the **full URL** in
// its error text, and the credentials for these providers live in that URL. The credential
// string flows to four exits:
//
//	notification_deliveries.last_error        -> written to the DB in plaintext
//	GET /api/notify/deliveries response      -> echoed to the browser, bypassing the
//	                                            channel config's redaction mask
//	server-side logs                          -> commonly shipped off-site
//	502 response from the test-send endpoint  -> pops up directly in the UI
//
// So this file does not test one function in isolation -- it sends a request that is
// guaranteed to fail through each real channel and asserts the credential is absent from
// the error text.

// credentialCases covers every channel shape whose credentials live in the URL:
// DingTalk / WeCom put them in the query string, Feishu at the end of the path,
// Telegram in the middle of the path.
var credentialCases = []struct {
	name   string
	ch     Channel
	cfg    map[string]any
	secret string
}{
	{
		name:   "DingTalk access_token in query",
		ch:     dingTalkChannel{},
		cfg:    map[string]any{"webhook": "http://127.0.0.1:1/robot/send?access_token=" + leakProbeToken},
		secret: leakProbeToken,
	},
	{
		name:   "WeCom key in query",
		ch:     weComChannel{},
		cfg:    map[string]any{"webhook": "http://127.0.0.1:1/cgi-bin/webhook/send?key=" + leakProbeToken},
		secret: leakProbeToken,
	},
	{
		name:   "Feishu hook id at end of path",
		ch:     feishuChannel{},
		cfg:    map[string]any{"webhook": "http://127.0.0.1:1/open-apis/bot/v2/hook/" + leakProbeToken},
		secret: leakProbeToken,
	},
	{
		name:   "Telegram bot token in middle of path",
		ch:     telegramChannel{},
		cfg:    map[string]any{"bot_token": leakProbeToken, "chat_id": "1", "base_url": "http://127.0.0.1:1"},
		secret: leakProbeToken,
	},
	{
		name:   "DingTalk signing secret",
		ch:     dingTalkChannel{},
		cfg:    map[string]any{"webhook": "http://127.0.0.1:1/robot/send", "secret": leakProbeToken},
		secret: leakProbeToken,
	},
}

// leakProbeToken is a sentinel value that could never be a real credential, used to
// search for it in error text.
const leakProbeToken = "LEAKPROBE0123456789abcdef"

// TestChannelErrorsNeverLeakCredentials is the core invariant.
func TestChannelErrorsNeverLeakCredentials(t *testing.T) {
	for _, tc := range credentialCases {
		t.Run(tc.name, func(t *testing.T) {
			// Guaranteed-failing peer: nothing listens on 127.0.0.1:1, so we exercise the
			// connection-refused path.
			_, err := tc.ch.Send(context.Background(), tc.cfg, Message{
				Items: []Item{{FindingID: 1, Severity: "high", Name: "leak probe"}},
			})
			if err == nil {
				t.Fatal("an unreachable address should produce an error")
			}
			assertNoSecret(t, err.Error(), tc.secret)
		})
	}
}

// TestChannelErrorsNeverLeakCredentialsInPermanentPath covers the permanent-failure branch:
// URL validation failures and platform business errors also surface error text, and must
// not carry credentials either.
func TestChannelErrorsNeverLeakCredentialsInPermanentPath(t *testing.T) {
	cases := []struct {
		name string
		ch   Channel
		cfg  map[string]any
	}{
		// Addresses that carry credentials but are malformed -> exercise the validateHTTPURL
		// / url.Parse branch.
		{"DingTalk address malformed", dingTalkChannel{}, map[string]any{"webhook": "file:///" + leakProbeToken}},
		{"WeCom address malformed", weComChannel{}, map[string]any{"webhook": "gopher://" + leakProbeToken}},
		{"Feishu address malformed", feishuChannel{}, map[string]any{"webhook": "ftp://" + leakProbeToken + "/hook"}},
		{"Telegram API address malformed", telegramChannel{}, map[string]any{"bot_token": "tok", "chat_id": "1", "base_url": "file://" + leakProbeToken}},
		{"Generic Webhook address malformed", webhookChannel{}, map[string]any{"url": "javascript:" + leakProbeToken}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.ch.Send(context.Background(), tc.cfg, Message{Items: []Item{{Severity: "high"}}})
			if err == nil {
				t.Fatal("a malformed config should produce an error")
			}
			assertNoSecret(t, err.Error(), leakProbeToken)
		})
	}
}

func assertNoSecret(t *testing.T, text, secret string) {
	t.Helper()
	if strings.Contains(text, secret) {
		t.Fatalf("error text leaked credential %q:\n    %s", secret, text)
	}
}

func TestRedactRequestTargetKeepsOnlySchemeAndHost(t *testing.T) {
	cases := map[string]string{
		"https://oapi.dingtalk.com/robot/send?access_token=S1":    "https://oapi.dingtalk.com/…",
		"https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=S2": "https://qyapi.weixin.qq.com/…",
		"https://open.feishu.cn/open-apis/bot/v2/hook/S3":         "https://open.feishu.cn/…",
		"https://api.telegram.org/botS4/sendMessage":              "https://api.telegram.org/…",
		"http://10.0.0.5:8080/hook":                               "http://10.0.0.5:8080/…",
	}
	for in, want := range cases {
		got := redactRequestTarget(in)
		if got != want {
			t.Errorf("redactRequestTarget(%q) = %q, want %q", in, got, want)
		}
		// The redacted result must not retain any path/query fragment of the original address.
		if parts := strings.SplitN(in, "://", 2); len(parts) == 2 {
			if hostAndRest := strings.SplitN(parts[1], "/", 2); len(hostAndRest) == 2 && hostAndRest[1] != "" {
				if strings.Contains(got, hostAndRest[1]) {
					t.Errorf("redacted result still contains path/query fragment %q: %q", hostAndRest[1], got)
				}
			}
		}
	}
	// Unparseable input must never echo the original string back.
	for _, bad := range []string{"", "://", "not a url", "http://"} {
		if got := redactRequestTarget(bad); strings.Contains(got, bad) && bad != "" {
			t.Errorf("unparseable input %q was echoed back as %q", bad, got)
		}
	}
}

// TestRedactTransportErrorStripsURL stares directly at *url.Error -- the concrete type returned
// by http.Client.Do and the first place a leak happens.
func TestRedactTransportErrorStripsURL(t *testing.T) {
	inner := errors.New("dial tcp 127.0.0.1:1: connect: connection refused")
	uerr := &url.Error{
		Op:  "Post",
		URL: "https://api.telegram.org/bot" + leakProbeToken + "/sendMessage",
		Err: inner,
	}
	got := redactTransportError(uerr)
	assertNoSecret(t, got, leakProbeToken)
	if !strings.Contains(got, "api.telegram.org") {
		t.Errorf("should keep the host for troubleshooting, got %q", got)
	}
	if !strings.Contains(got, "connection refused") {
		t.Errorf("should keep the underlying cause for troubleshooting, got %q", got)
	}
	// Op is also preserved (POST vs GET matters for troubleshooting).
	if !strings.Contains(got, "Post") {
		t.Errorf("should keep the operation name, got %q", got)
	}
}

// TestRedactURLsInTextHandlesFallback covers the fallback path: custom errors that are
// not *url.Error (e.g. returned by redirect policy) must also have addresses stripped.
func TestRedactURLsInTextHandlesFallback(t *testing.T) {
	in := fmt.Sprintf("refused cross-host redirect (a.example -> http://b.example/bot%s/send)", leakProbeToken)
	got := redactURLsInText(in)
	assertNoSecret(t, got, leakProbeToken)
	if !strings.Contains(got, "http://b.example/…") {
		t.Errorf("should replace the address with its redacted form, got %q", got)
	}
	// Text with no address is left untouched.
	if plain := "dial tcp: connection refused"; redactURLsInText(plain) != plain {
		t.Error("text without an address should not be modified")
	}
}

// TestCrossHostRedirectRefused covers "credentials in the URL + following a cross-host redirect
// = handing over the credentials". The two httptest servers listen on 127.0.0.1 on different
// ports, and a different port means a different Host -- a genuine cross-host redirect.
func TestCrossHostRedirectRefused(t *testing.T) {
	var hit bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/robot/send?access_token="+leakProbeToken, http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()

	_, err := (dingTalkChannel{}).Send(context.Background(),
		map[string]any{"webhook": redirector.URL + "/robot/send?access_token=" + leakProbeToken},
		Message{Items: []Item{{Severity: "high"}}})
	if err == nil {
		t.Fatal("a cross-host redirect should be refused")
	}
	if hit {
		t.Fatal("the redirect target was reached -- credentials leaked with the redirect")
	}
	assertNoSecret(t, err.Error(), leakProbeToken)
}

// TestSameHostRedirectAllowed is the inverse case: same-host redirects (e.g. a trailing-slash
// append) must still work, or legitimate flows get blocked along with the bad ones.
func TestSameHostRedirectAllowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robot/send" {
			// Same host, same port -- a normal redirect.
			http.Redirect(w, r, "/robot/send/", http.StatusTemporaryRedirect)
			return
		}
		_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	defer srv.Close()

	if _, err := (dingTalkChannel{}).Send(context.Background(),
		map[string]any{"webhook": srv.URL + "/robot/send"},
		Message{Items: []Item{{Severity: "high"}}}); err != nil {
		t.Fatalf("a same-host redirect should not be refused: %v", err)
	}
}
