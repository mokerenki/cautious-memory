package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Repo is the release source. It is hardcoded rather than configurable: a configurable
// update source would give anyone who can change configuration a remote code execution
// channel, which is unacceptable for a penetration testing platform.
const Repo = "RuoJi6/ARTEX"

// latestURL is GitHub's "latest stable release" endpoint. It automatically skips
// prereleases and drafts.
const latestURL = "https://api.github.com/repos/" + Repo + "/releases/latest"

// allowedHosts restricts the domains the upgrade flow may contact. Together with
// checkRedirect below, any redirect hop to a host outside this set fails immediately.
// This is the first line of defense against DNS poisoning / MITM swapping the binary;
// the second is SHA256SUMS verification.
var allowedHosts = map[string]bool{
	"api.github.com":                       true,
	"github.com":                           true,
	"objects.githubusercontent.com":        true, // object storage where release assets are actually hosted
	"release-assets.githubusercontent.com": true,
	"raw.githubusercontent.com":            true,
}

// Release contains the GitHub Release fields we care about.
type Release struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	HTMLURL     string    `json:"html_url"`
	Assets      []Asset   `json:"assets"`
}

// Asset is a file attached to a Release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// ParseProxy validates an optional update proxy without including credentials in errors.
func ParseProxy(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("proxy address format is invalid")
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, fmt.Errorf("proxy scheme must be http, https, socks5, or socks5h")
	}
	if u.Hostname() == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("proxy address must include a host and must not include a path, query, or fragment")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("proxy port must be between 1 and 65535")
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return nil, fmt.Errorf("proxy port must not be empty")
	}
	return u, nil
}

// NewClient validates the proxy before opening any connection. Empty means direct,
// regardless of HTTP_PROXY / HTTPS_PROXY in the process environment.
func NewClient(proxy string) (*http.Client, error) {
	pu, err := ParseProxy(proxy)
	if err != nil {
		return nil, err
	}
	tr := &http.Transport{
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: 15 * time.Second,
	}
	if pu != nil {
		tr.Proxy = http.ProxyURL(pu)
	}

	return &http.Client{
		Transport: tr,
		Timeout:   30 * time.Minute, // downloading a full package must not be killed by a per-request timeout
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return checkURL(req.URL)
		},
	}, nil
}

// checkURL enforces https and the host allowlist.
func checkURL(u *url.URL) error {
	if u.Scheme != "https" {
		return fmt.Errorf("non-HTTPS URL refused: %s", u.Scheme+"://"+u.Host)
	}
	if !allowedHosts[strings.ToLower(u.Hostname())] {
		return fmt.Errorf("non-GitHub host refused: %s", u.Hostname())
	}
	return nil
}

// FetchLatest queries the latest stable release.
func FetchLatest(ctx context.Context, c *http.Client) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestURL, nil)
	if err != nil {
		return nil, err
	}
	if err := checkURL(req.URL); err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "artex-selfupdate")

	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to reach GitHub (you can configure an update proxy in Version & Update): %w", err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusForbidden, resp.StatusCode == http.StatusTooManyRequests:
		// The unauthenticated GitHub API allows 60 requests per IP per hour, which is
		// easy to hit on shared egress IPs.
		return nil, fmt.Errorf("GitHub API rate limit reached (60 requests/hour); please try again later")
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("repository %s is inaccessible or has no stable release yet", Repo)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("GitHub returned %d", resp.StatusCode)
	}

	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("failed to parse Release: %w", err)
	}
	if strings.TrimSpace(rel.TagName) == "" {
		return nil, fmt.Errorf("Release is missing a tag")
	}
	return &rel, nil
}

// AssetName returns the release package name for the current platform, consistent with
// package_binary in build.sh: artex-<version>-<os>-<arch>.zip (version without the v prefix).
func AssetName(tag, goos, goarch string) string {
	return fmt.Sprintf("artex-%s-%s-%s.zip", strings.TrimPrefix(tag, "v"), goos, goarch)
}

// FindAsset finds an asset in the Release by name.
func (r *Release) FindAsset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if strings.EqualFold(a.Name, name) {
			return a, true
		}
	}
	return Asset{}, false
}
