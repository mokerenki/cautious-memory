package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"
)

// dingTalkChannel implements the DingTalk custom robot.
//
// Platform characteristics (driving implementation choices):
//   - Single robot rate limit is 20 messages/minute; excess may be dropped
//     silently (HTTP can still return 200), so rate limiting must be done on
//     the client side (see DefaultRatePerMin).
//   - Three security modes: signature / custom keywords / IP allowlist.
//     Signature is the only option independent of message content, so only
//     signature is supported (unsigned webhooks are also supported).
//   - Both success and failure return HTTP 200; the body errcode distinguishes
//     them -- not checking errcode would record failed deliveries as success.
type dingTalkChannel struct{}

func (dingTalkChannel) Kind() string { return KindDingTalk }

func (dingTalkChannel) DefaultRatePerMin() int { return 20 }

// DingTalk webhook URLs include access_token and are credentials, so mask the
// whole URL. The signing secret is also a credential.
func (dingTalkChannel) SecretKeys() []string { return []string{"webhook", "secret"} }

// The destination is the DingTalk webhook URL itself; changing it requires
// restating the signing secret for the new URL.
func (dingTalkChannel) DestinationKeys() []string { return []string{"webhook"} }

func (dingTalkChannel) Validate(cfg map[string]any) error {
	hook := cfgString(cfg, "webhook")
	if hook == "" {
		return errors.New("missing Webhook URL")
	}
	if err := validateHTTPURL(hook); err != nil {
		return fmt.Errorf("invalid Webhook URL: %w", err)
	}
	return nil
}

// Send delivers one message. Uses ActionCard (with button) for a single item
// that has a detail link; otherwise uses markdown.
func (c dingTalkChannel) Send(ctx context.Context, cfg map[string]any, m Message) (int, error) {
	hook := cfgString(cfg, "webhook")
	if err := c.Validate(cfg); err != nil {
		return 0, Permanent(err)
	}
	endpoint, err := dingTalkSignedURL(hook, cfgString(cfg, "secret"), time.Now())
	if err != nil {
		return 0, Permanent(err)
	}

	title := markdownTitle(m)
	// DingTalk markdown body has no explicit byte limit, but cap it to avoid
	// abnormal evidence-field expansion.
	text, kept := markdownBody(m, 20000)

	var payload any
	if !m.Batch && len(m.Items) == 1 && m.Items[0].DetailURL != "" {
		payload = map[string]any{
			"msgtype": "actionCard",
			"actionCard": map[string]any{
				"title":          title,
				"text":           text,
				"btnOrientation": "0",
				"singleTitle":    "View details",
				"singleURL":      m.Items[0].DetailURL,
			},
		}
	} else {
		payload = map[string]any{
			"msgtype":  "markdown",
			"markdown": map[string]any{"title": title, "text": text},
		}
	}

	raw, err := doJSON(ctx, "POST", endpoint, nil, payload)
	if err != nil {
		return 0, err
	}
	// DingTalk embeds business errors in 200 responses.
	var res struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return 0, fmt.Errorf("failed to parse DingTalk response: %w (%s)", err, snippet(raw))
	}
	if res.ErrCode != 0 {
		// 301000 is signature verification failure, 310000 is keyword mismatch --
		// both are config errors and will not self-heal on retry.
		return 0, Permanent(fmt.Errorf("DingTalk returned error %d: %s", res.ErrCode, res.ErrMsg))
	}
	return kept, nil
}

// dingTalkSignedURL appends timestamp and sign query parameters per the
// official signing rules.
//
// Rule: string to sign = timestamp + "\n" + secret; the HMAC-SHA256 key is also
// secret; result is base64-encoded then URL-encoded. timestamp is milliseconds.
// Returns the original hook when secret is empty to support unsigned robots.
func dingTalkSignedURL(hook, secret string, now time.Time) (string, error) {
	if secret == "" {
		return hook, nil
	}
	ts := strconv.FormatInt(now.UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "\n" + secret))
	sign := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	u, err := url.Parse(hook)
	if err != nil {
		// Do not pass err through: url.Parse error text contains the full URL
		// (including access_token).
		return "", fmt.Errorf("failed to parse Webhook URL: %s", redactRequestTarget(hook))
	}
	q := u.Query()
	q.Set("timestamp", ts)
	q.Set("sign", sign)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// validateHTTPURL validates that the URL is usable and uses a supported scheme,
// and rejects literal loopback/link-local IP targets.
//
// Notes:
//  1. Error messages must be redacted. url.Parse returns *url.Error whose
//     Error() includes the full original URL, and these channel URLs embed
//     credentials (DingTalk access_token, WeCom key, Telegram bot token, Feishu
//     hook id). Returning err directly would leak credentials into 400 test
//     responses, stored last_error, server logs, and delivery history APIs.
//  2. Literal IPs are checked here; domain names are checked at dial time
//     (blockInternalDial is the final enforcement point and covers DNS
//     rebinding). Checking here provides early feedback when saving config.
//  3. Scheme restriction is defensive: file://, gopher:// and similar schemes
//     can cause unexpected http.Client behavior.
func validateHTTPURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("URL cannot be parsed (%s)", redactRequestTarget(raw))
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("only http/https are supported, got %q", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("missing host")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && isBlockedDialIP(ip) && !allowLocalTargets() {
		return fmt.Errorf("refusing to deliver to loopback/link-local address %s (to allow local delivery, set %s=1)", ip, AllowLocalTargetsEnv)
	}
	return nil
}
