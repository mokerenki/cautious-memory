package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"
)

// allowLocalTargets determines whether delivering messages to loopback / link-local addresses is allowed.
//
// Denied by default. These address ranges are not where IM bots or public mail servers appear, and what they can reach
// is sensitive: management ports of other services on the same host, and cloud metadata endpoints (169.254.169.254,
// which can expose instance credentials). Delivery targets are configured by admins, but an admin session hijacked via
// XSS/CSRF, or a second person sharing the same JWT, can change the config and read response content back—doJSON writes
// the first 200 bytes of 4xx/5xx response bodies into last_error and the delivery history API echoes it, forming a
// semi-blind read primitive.
//
// However, "local SMTP relay" (postfix on 127.0.0.1:25) is a common setup for self-hosted email; blocking it outright
// would break legitimate deployments. So we provide an explicit escape hatch instead of a hardcoded allowlist:
// set CAUTIOUSMEMORY_NOTIFY_ALLOW_LOCAL=1 to allow it.
//
// Exported as AllowLocalTargetsEnv so tests can explicitly enable it—this package and server tests use httptest receivers
// on 127.0.0.1 extensively, and without it they would all be blocked by the guard.
const AllowLocalTargetsEnv = "CAUTIOUSMEMORY_NOTIFY_ALLOW_LOCAL"

func allowLocalTargets() bool {
	v := strings.TrimSpace(os.Getenv(AllowLocalTargetsEnv))
	return v == "1" || strings.EqualFold(v, "true")
}

// isBlockedDialIP reports whether a target IP belongs to an address range that is "not allowed by default for delivery".
//
// Only loopback, link-local (including cloud metadata 169.254.169.254), unspecified, and multicast are rejected.
// RFC1918 private networks are **not** rejected: internal Mattermost / SMTP relays are common legitimate uses, and
// blocking them would make the feature unusable in real environments. This tradeoff is deliberate—protection must block
// truly sensitive targets without breaking normal deployments.
func isBlockedDialIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	// IPv4-mapped IPv6 (::ffff:127.0.0.1) must be converted to IPv4 before checking, otherwise the check can be bypassed.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	return ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

// blockInternalDial is the Control hook for http.Transport's dialer, checking the target address **at connection time**.
//
// Why enforce at dial time rather than only validating when saving config: this is the final enforcement point.
// It also covers two ways to bypass config validation—DNS rebinding (resolves to a public IP during validation but to an
// internal IP at connect time) and redirects (although we reject cross-host redirects, same-host redirects can still point
// to different paths).
func blockInternalDial(_, address string, _ syscall.RawConn) error {
	if allowLocalTargets() {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("unable to resolve target address %q", host)
	}
	if isBlockedDialIP(ip) {
		return fmt.Errorf("delivery to loopback/link-local address %s is refused (set %s=1 if delivery to a local service is required)", ip, AllowLocalTargetsEnv)
	}
	return nil
}

// notifyTransport adds only a dial guard on top of the default Transport.
// Clone preserves all default tuning (connection pool, HTTP/2, timeouts, proxy, etc.) to avoid changing other behavior.
var notifyTransport = func() *http.Transport {
	t, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Transport{}
	}
	clone := t.Clone()
	clone.DialContext = (&net.Dialer{Timeout: 10 * time.Second, Control: blockInternalDial}).DialContext
	return clone
}()

// httpClient is the shared client used by all channel deliveries.
//
// It deliberately does **not** reuse the project's global egress proxy (server-side GlobalProxy): that proxy is for
// penetration-test target traffic and is often an unstable tunnel, while notification availability should not be tied to
// target-network jitter. IM delivery can connect directly. Timeout is set to 15 seconds—peers slower than this are
// effectively in a fault state.
//
// Cross-host redirects are rejected: delivery targets for this feature are all "fixed endpoint" style and normally do not
// redirect to other hosts; credentials for these providers (DingTalk access_token, WeCom key, Telegram bot token) are
// **in the URL**, so following a cross-host redirect would hand credentials to the redirect target. Same-host redirects
// (e.g. appending a trailing slash) are still allowed.
var httpClient = &http.Client{
	Timeout:   15 * time.Second,
	Transport: notifyTransport,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if len(via) > 0 && req.URL.Host != via[0].URL.Host {
			return fmt.Errorf("cross-host redirect refused (%s → %s)", via[0].URL.Host, req.URL.Host)
		}
		return nil
	},
}

// respBodyLimit limits how much of the response body is read. A misbehaving peer may return very large content,
// while we only need the error code and a short description for display in delivery history.
const respBodyLimit = 8 << 10

// doJSON sends one request and returns the (length-limited) response body.
//
// A nil payload sends an empty body (for GET or scenarios where the platform does not require a body).
// Header key/value pairs are attached as-is for custom headers in generic Webhooks.
//
// Error classification is the core responsibility of this function: network failures and 5xx/408/429 are treated as
// "retryable"; other 4xx are treated as "permanent"—retrying a 403 only repeats the same error in logs.
func doJSON(ctx context.Context, method, rawURL string, headers map[string]string, payload any) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			// Serialization failure is a local bug (wrong config field type); retrying will not help.
			return nil, Permanent(fmt.Errorf("failed to build request body: %w", err))
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		// Invalid URL—usually a user input error, so treat as permanent.
		// Do not pass err through: url.Parse error text includes the full URL.
		return nil, Permanent(fmt.Errorf("invalid request URL: %s", redactRequestTarget(rawURL)))
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		// Connection refused, DNS failure, timeout—often transient, so let backoff retry handle it.
		//
		// The error text must be redacted before being returned. Reason: http.Client.Do returns *url.Error whose
		// Error() is `Op "fullURL": underlying error`, and credentials for these providers are **in the URL**
		// (DingTalk access_token, WeCom key, Feishu hook id, Telegram /bot<token>/). Without redaction, credentials
		// would flow into four places: last_error in notification_deliveries (stored in plaintext), delivery history
		// API responses (**bypassing channel config masking**), server logs, and the 502 text returned to the frontend
		// by the test-send endpoint.
		return nil, fmt.Errorf("request failed: %s", redactTransportError(err))
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, respBodyLimit))
	if readErr != nil {
		return nil, fmt.Errorf("failed to read response: %w", readErr)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return raw, nil
	}
	// 429 (rate limit) and 408 (timeout) are worth retrying; other 4xx indicate config or permission issues and retries are pointless.
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusRequestTimeout {
		return nil, fmt.Errorf("peer rate limited or timed out (HTTP %d): %s", resp.StatusCode, snippet(raw))
	}
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("peer service error (HTTP %d): %s", resp.StatusCode, snippet(raw))
	}
	return nil, Permanent(fmt.Errorf("peer rejected request (HTTP %d): %s", resp.StatusCode, snippet(raw)))
}

// snippet compresses a response body into a short single-line string for error messages. Responses may contain
// newlines and large amounts of whitespace; inserting them directly into last_error would break delivery history layout.
func snippet(raw []byte) string {
	return OneLine(string(raw), 200)
}

// redactRequestTarget compresses a delivery address to "scheme://host/…" for use in error messages.
//
// This is the package's only address-redaction policy and is intentionally **coarse**: everything except scheme and host
// is discarded. There is no "generic and safe" way to decide which part of a URL is a credential:
//
//	DingTalk  credential in query  /robot/send?access_token=xxx
//	WeCom     credential in query  /cgi-bin/webhook/send?key=xxx
//	Feishu    credential at **path end**  /open-apis/bot/v2/hook/<hook_id>
//	Telegram  credential in **path middle** /bot<token>/sendMessage
//
// Trying to "keep useful parts" would require per-channel patching, and missing any one would leak credentials.
// Keeping host is enough for diagnosis (DNS resolution, connectivity, certificate issues), while identifying which bot
// is handled by the masked tail hint in channel configuration.
//
// On parse failure a fixed placeholder is returned—the original string is never echoed.
func redactRequestTarget(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "(unparseable address)"
	}
	return u.Scheme + "://" + u.Host + "/…"
}

// redactTransportError strips the address from a transport error, keeping only the underlying cause.
//
// *url.Error is {Op, URL, Err} and its Error() prints the URL. We explicitly take the Err field to bypass its Error()
// method—more reliable than string replacement after the fact, which would need to handle URL-encoded/escaped variants correctly.
func redactTransportError(err error) string {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		host := ""
		if u, parseErr := url.Parse(uerr.URL); parseErr == nil {
			host = u.Host
		}
		if uerr.Err != nil {
			return fmt.Sprintf("%s %s: %s", uerr.Op, host, uerr.Err)
		}
		return fmt.Sprintf("%s %s: unknown error", uerr.Op, host)
	}
	// Non-*url.Error (e.g. redirect policy errors) may also contain URLs; apply redaction as a fallback.
	return redactURLsInText(err.Error())
}

// redactURLsInText replaces http(s) URLs appearing in a text string with redacted forms.
//
// Used as a fallback for errors without structured fields (redirect policy errors, custom errors from third-party libraries).
// Only recognizes http/https prefixes and splits on whitespace and quotes—URLs do not contain those characters.
func redactURLsInText(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		rest := s[i:]
		if strings.HasPrefix(rest, "http://") || strings.HasPrefix(rest, "https://") {
			end := len(rest)
			if j := strings.IndexAny(rest, " \t\n\"'"); j >= 0 {
				end = j
			}
			b.WriteString(redactRequestTarget(rest[:end]))
			i += end
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
