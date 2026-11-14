package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// weComMarkdownLimit is the hard byte limit for WeCom group bot markdown
// content (bytes, not characters). This is the tightest limit among all six
// channels and the main reason TruncateBytes exists.
const weComMarkdownLimit = 4096

// weComChannel implements the WeCom group robot.
//
// Platform characteristics:
//   - Authenticated solely by the key in the URL, with no signing support --
//     the webhook URL itself is the entire credential.
//   - markdown content limit is 4096 **bytes**; oversized messages are
//     rejected (not truncated). With 3 bytes per Chinese character, only
//     ~1k characters fit, so client-side truncation is required.
//   - Rate limit is 20 messages/minute, enforced client-side.
type weComChannel struct{}

func (weComChannel) Kind() string { return KindWeCom }

func (weComChannel) DefaultRatePerMin() int { return 20 }

// WeCom has only the webhook URL as a credential (key in URL) and does not
// support signing; the entire URL is the credential.
func (weComChannel) SecretKeys() []string { return []string{"webhook"} }

// WeCom has only the webhook field, which is both destination and credential,
// so there is no residual credential after a destination change.
func (weComChannel) DestinationKeys() []string { return []string{"webhook"} }

func (weComChannel) Validate(cfg map[string]any) error {
	hook := cfgString(cfg, "webhook")
	if hook == "" {
		return errors.New("missing Webhook URL")
	}
	if err := validateHTTPURL(hook); err != nil {
		return fmt.Errorf("invalid Webhook URL: %w", err)
	}
	return nil
}

func (c weComChannel) Send(ctx context.Context, cfg map[string]any, m Message) (int, error) {
	if err := c.Validate(cfg); err != nil {
		return 0, Permanent(err)
	}
	// Digest batches can be long (50 items x one line each + header); 4096
	// bytes is easy to exceed. Truncate here instead of relying on platform
	// rejection: rejection drops the whole batch, while truncation delivers at
	// least the first items.
	content, kept := markdownBody(m, weComMarkdownLimit)
	payload := map[string]any{
		"msgtype":  "markdown",
		"markdown": map[string]any{"content": content},
	}
	raw, err := doJSON(ctx, "POST", cfgString(cfg, "webhook"), nil, payload)
	if err != nil {
		return 0, err
	}
	var res struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return 0, fmt.Errorf("failed to parse WeCom response: %w (%s)", err, snippet(raw))
	}
	if res.ErrCode != 0 {
		// 45009 is API rate limit exceeded; the platform window rolls over, so
		// retrying after backoff is effective. This means client rate_per_min
		// is too aggressive; retry is a fallback, the real fix is lowering it.
		if res.ErrCode == 45009 {
			return 0, fmt.Errorf("WeCom rate limited %d: %s", res.ErrCode, res.ErrMsg)
		}
		// 93000 is invalid webhook key -- permanent failure, retry will not help.
		return 0, Permanent(fmt.Errorf("WeCom returned error %d: %s", res.ErrCode, res.ErrMsg))
	}
	return kept, nil
}
