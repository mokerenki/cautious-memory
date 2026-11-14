package server

// egress_probe.go — global egress proxy connectivity probe.
//
// settings.global_proxy only validates format on save, sends zero bytes, so a dead
// proxy can still be saved. Here we actually send probe requests: ① direct egress IP,
// ② egress IP via this proxy. ②'s target is decided by egressTarget — the only
// place in this file that needs judgment.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/Autumn-27/artex/traffic"
)

const settingEgressProbeURL = "egress_probe_url"

// defaultEgressProbes tries in order, stops at first success; used when custom address is empty.
var defaultEgressProbes = []string{
	"https://api.ipify.org?format=json", // returns {"ip":"1.2.3.4"}
	"https://ifconfig.me/ip",            // returns plain text 1.2.3.4
}

func (m *Manager) EgressProbeURL() string {
	if m == nil || m.pg == nil {
		return ""
	}
	raw, _, _ := m.pg.GetSetting(settingEgressProbeURL)
	return strings.TrimSpace(raw)
}

// egressProbes reads settings for every probe so saves take effect without a restart.
func (m *Manager) egressProbes() []string {
	if raw := m.EgressProbeURL(); raw != "" {
		return []string{raw}
	}
	return defaultEgressProbes
}

// validateProbeURL only allows absolute http(s) URLs targeting "non-localhost, non-link-local" addresses.
//
// This endpoint requires auth but still blocks: agents could be tricked by a target page into
// calling it, and "localhost" in a container IS ARTEX itself (admin port, cloud metadata
// 169.254.169.254). **RFC1918 addresses are deliberately allowed** — custom probe services'
// main use case is intranet echo; blocking 192.168/10/172.16 would break that function.
func validateProbeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil {
		return "", errors.New("probe_url is not a valid URL")
	}
	if !strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https") {
		return "", errors.New("probe_url only supports http / https")
	}
	if u.Host == "" || u.Hostname() == "" {
		return "", errors.New("probe_url missing hostname")
	}
	if err := probeCheckURL(u); err != nil {
		return "", err
	}
	return raw, nil
}

// probeCheckRedirect validates hop-by-hop, blocking "public address 302 to 127.0.0.1" bypasses.
func probeCheckRedirect(next *http.Request, via []*http.Request) error {
	if len(via) >= 3 {
		return errors.New("too many redirects")
	}
	if len(via) > 0 && next.URL.Host != via[0].URL.Host {
		return fmt.Errorf("Rejected cross-host redirect (%s → %s)", via[0].URL.Host, next.URL.Host)
	}
	return probeCheckURL(next.URL)
}

// probeCheckURL rejects only loopback, link-local, unspecified, and multicast
// destinations. RFC1918 addresses remain valid for intranet echo services.
func probeCheckURL(u *url.URL) error {
	host := strings.TrimSuffix(strings.TrimSpace(u.Hostname()), ".")
	if zone := strings.IndexByte(host, '%'); zone >= 0 {
		host = host[:zone]
	}
	ip := net.ParseIP(host)
	if ip == nil && repeaterLooksNumericHost(host) {
		var ok bool
		ip, ok = repeaterParseLegacyIPv4(host)
		if !ok {
			return fmt.Errorf("Rejected unresolvable numeric host address %q", host)
		}
	}
	if strings.EqualFold(host, "localhost") || (ip != nil && probeIsBlockedDialIP(ip)) {
		return errors.New("Rejected access to local/link-local address")
	}
	return nil
}

func probeIsBlockedDialIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	return ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

// probeBlockSensitiveDial is always enabled for direct probes. Unlike the
// repeater, probe_url has no local-target escape hatch.
func probeBlockSensitiveDial(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("Unable to resolve target address %q", host)
	}
	if probeIsBlockedDialIP(ip) {
		return fmt.Errorf("Rejected access to local/link-local address %s", ip)
	}
	return nil
}

// userinfoRe matches user:pass@ in URLs.
var userinfoRe = regexp.MustCompile(`://[^/@\s]+@`)

// maskSecrets strips username:password from URLs. They appear in Configured / Path / probes[].error,
// all three of which are rendered in UI or logged.
func maskSecrets(s string) string { return userinfoRe.ReplaceAllString(s, "://***@") }

// egressAttempt keeps each URL's failure reason to distinguish "can't reach proxy" from "proxy works but no egress".
type egressAttempt struct {
	URL       string `json:"url"`
	OK        bool   `json:"ok"`
	IP        string `json:"ip,omitempty"`
	LatencyMS int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
}

// egressProbeResult is a probe summary for one path topology.
type egressProbeResult struct {
	OK        bool            `json:"ok"`
	ExitIP    string          `json:"exit_ip,omitempty"` // external egress IP
	Via       string          `json:"via,omitempty"`     // direct / http / https / socks5
	ViaLabel  string          `json:"via_label,omitempty"`
	LatencyMS int64           `json:"latency_ms"`
	Error     string          `json:"error,omitempty"`
	Probes    []egressAttempt `json:"probes"` // per-URL detail for troubleshooting
}

// egressCheck is one row in the report, corresponding to one path topology.
type egressCheck struct {
	Key        string            `json:"key"`   // direct | global
	Label      string            `json:"label"` // Chinese label, displayed directly in frontend
	Path       string            `json:"path"`  // actual proxy address used (empty = direct)
	Skipped    bool              `json:"skipped,omitempty"`
	SkipReason string            `json:"skip_reason,omitempty"`
	Result     egressProbeResult `json:"result"`
}

// egressReport is the response body for POST /api/settings/proxy/test.
type egressReport struct {
	OK         bool          `json:"ok"`          // based on "② via global proxy"; if no proxy, use "① direct"
	Capture    bool          `json:"capture"`     // whether traffic capture is currently enabled
	Configured string        `json:"configured"`  // global_proxy from form
	AgentProxy string        `json:"agent_proxy"` // actual ProxyAddr() return value
	Checks     []egressCheck `json:"checks"`
	Warnings   []string      `json:"warnings,omitempty"`
}

// probeEgress touches no DB, no Server, so tests can call it directly, fully offline.
func probeEgress(ctx context.Context, proxyRaw, caPEMPath string, probes []string, timeout time.Duration) egressProbeResult {
	start := time.Now()

	// Reuse traffic package validation so "what counts as a valid proxy" is defined in one place.
	var proxyURL *url.URL
	if strings.TrimSpace(proxyRaw) != "" {
		u, err := traffic.ValidateProxyURL(proxyRaw)
		if err != nil {
			return egressProbeResult{
				OK:        false,
				Error:     maskSecrets(err.Error()),
				LatencyMS: time.Since(start).Milliseconds(),
				// Empty slice not nil: nil serializes to JSON null, frontend expects array.
				Probes: []egressAttempt{},
			}
		}
		proxyURL = u
	}

	tr, caErr := buildProbeTransport(proxyURL, caPEMPath)

	client := &http.Client{
		Transport: tr,
		// 2s wider than ctx so ctx expires first, error shows "context deadline exceeded".
		Timeout:       timeout + 2*time.Second,
		CheckRedirect: probeCheckRedirect,
	}

	attempts := make([]egressAttempt, 0, len(probes))
	for _, target := range probes {
		reqCtx, cancel := context.WithTimeout(ctx, timeout)
		t0 := time.Now()
		ip, err := probeOne(reqCtx, client, target)
		cancel()

		a := egressAttempt{URL: maskSecrets(target), LatencyMS: time.Since(t0).Milliseconds()}
		if err != nil {
			a.Error = maskSecrets(err.Error())
			// Only annotate cert errors; otherwise "can't reach proxy" gets irrelevant CA note.
			// Must go in probes[].error — UI renders that, not result.error.
			if caErr != nil && strings.Contains(a.Error, "certificate") {
				a.Error = "Traffic capture CA unavailable (" + caErr.Error() + "): certificate errors may result from this." + a.Error
			}
		} else {
			a.OK, a.IP = true, ip
		}
		attempts = append(attempts, a)

		if a.OK {
			// via describes the proxy protocol, not the egress network type.
			via, viaLabel := "direct", "Direct"
			if proxyURL != nil {
				via, viaLabel = proxyURL.Scheme, schemeLabel(proxyURL.Scheme)
			}
			return egressProbeResult{
				OK:        true,
				ExitIP:    ip,
				Via:       via,
				ViaLabel:  viaLabel,
				LatencyMS: time.Since(start).Milliseconds(),
				Probes:    attempts,
			}
		}
	}

	return egressProbeResult{
		OK:        false,
		Error:     "All probe targets failed",
		LatencyMS: time.Since(start).Milliseconds(),
		Probes:    attempts,
	}
}

// schemeLabel translates protocol to human label ("http" → "HTTP proxy").
func schemeLabel(scheme string) string {
	switch strings.ToLower(scheme) {
	case "http":
		return "HTTP proxy"
	case "https":
		return "HTTPS proxy"
	case "socks5", "socks5h":
		return "SOCKS5 proxy"
	default:
		return strings.ToUpper(scheme) + " proxy"
	}
}

// buildProbeTransport can't use http.DefaultTransport: its Proxy reads container's HTTP_PROXY,
// so "test direct" would actually go through proxy. Proxy=nil here is true direct.
func buildProbeTransport(proxyURL *url.URL, caPEMPath string) (*http.Transport, error) {
	tr := &http.Transport{
		DisableKeepAlives:   true,
		TLSHandshakeTimeout: 8 * time.Second,
	}
	if proxyURL != nil {
		tr.Proxy = http.ProxyURL(proxyURL)
	} else {
		// For direct, re-validate at dial: block "domain resolves to 127.0.0.1" (DNS rebinding).
		// For proxy, skip it — we dial the proxy address, and user configuring localhost proxy is allowed.
		tr.DialContext = (&net.Dialer{Timeout: 10 * time.Second, Control: probeBlockSensitiveDial}).DialContext
	}
	// If CA unavailable, don't panic; propagate reason so failure shows real cause, not just x509.
	if path := strings.TrimSpace(caPEMPath); path != "" {
		pemBytes, err := os.ReadFile(path)
		if err != nil {
			return tr, fmt.Errorf("Unable to read %s", path)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemBytes) {
			return tr, fmt.Errorf("%s is not a valid PEM", path)
		}
		tr.TLSClientConfig = &tls.Config{RootCAs: pool}
	}
	return tr, nil
}

// probeOne accepts JSON ({"ip":"..."}) and plain text; errors if unrecognizable, never guesses an IP.
func probeOne(ctx context.Context, client *http.Client, target string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "artex-egress-probe/1.0")

	resp, err := client.Do(req)
	if err != nil {
		// Return as-is: dial refused / deadline exceeded / x509 each map to different failures.
		return "", err
	}
	defer resp.Body.Close()

body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var v struct {
		IP string `json:"ip"`
	}
	if json.Unmarshal(body, &v) == nil && v.IP != "" {
		if net.ParseIP(strings.TrimSpace(v.IP)) == nil {
			return "", errors.New("ip field in response is not a valid IP")
		}
		return strings.TrimSpace(v.IP), nil
	}

	// net.ParseIP also rejects HTML (anti-scrape pages, proxy error pages).
	if s := strings.TrimSpace(string(body)); net.ParseIP(s) != nil {
		return s, nil
	}
	return "", fmt.Errorf("Unable to parse IP from response (received %d bytes, possibly an anti-scrape page or proxy error page)", len(body))
}

// egressTarget decides where ② sends the request:
//
//	capture off → form value; capture on + saved → MITM; capture on + unsaved → form value (skip MITM)
//
// Third case can't use MITM: MITM upstream only updated by SetGlobalProxy, can't read request body's
// new value; using it would only test old proxy. viaMITM lets caller relax timeout.
func egressTarget(cfg string, capture, pending bool, agentProxy, agentCA string) (addr, ca string, viaMITM bool) {
	if cfg == "" {
		return "", "", false
	}
	if capture && !pending && agentProxy != "" {
		return agentProxy, agentCA, true
	}
	return cfg, "", false
}

// testEgressProxy semantics align with testWebSearch: address from request body (unsaved works),
// empty = test direct, always returns 200 with ok expressing result.
func (s *Server) testEgressProxy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GlobalProxy string  `json:"global_proxy"`
		ProbeURL    *string `json:"probe_url"` // omitted = saved address; empty string = default; non-empty = temporary override
		Resume      bool    `json:"resume"`    // manual settings probe may release paused_by_proxy tasks
	}
	// Request body has only one address; cap it to prevent large payload.
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	// Empty body equals test direct.
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, 400, err.Error())
		return
	}

	probes := s.m.egressProbes()
	if req.ProbeURL != nil {
		probes = defaultEgressProbes
		if u := strings.TrimSpace(*req.ProbeURL); u != "" {
			checked, err := validateProbeURL(u)
			if err != nil {
				writeErr(w, 400, err.Error())
				return
			}
			probes = []string{checked}
		}
	}

	// These three calls determine which path the agent actually takes.
	capture := s.m.TrafficEnabled() // traffic capture switch
	agentProxy := s.m.ProxyAddr()   // agent's actual proxy address
	agentCA := s.m.ProxyCACert()    // MITM CA path when capture on; empty when off
	savedProxy := s.m.GlobalProxy() // saved global_proxy

	// Overall cap 40s: single Timeout only covers one attempt, not total; frontend fetch has no timeout.
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	var proxyGuards map[string]proxyReconnectGuard
	if req.Resume {
		proxyGuards = s.snapshotProxyReconnectGuardsWithExhausted(false, true)
	}

	configuredProxy := strings.TrimSpace(req.GlobalProxy)
	if strings.Contains(configuredProxy, "***") || strings.Contains(configuredProxy, "******") {
		configuredProxy = s.m.GlobalProxy()
	}
// When form value != saved value, ② still tests form value but agent uses saved — must warn.
// Normalize before compare: trailing slash doesn't count as "unsaved".
	pending := normalizeProxy(configuredProxy) != normalizeProxy(savedProxy)
	rep := egressReport{
		Capture:    capture,
		Configured: maskSecrets(configuredProxy),
		AgentProxy: maskSecrets(agentProxy),
	}

	rep.Checks = append(rep.Checks, egressCheck{
		Key:    "direct",
		Label:  "① Direct IP (not via proxy)",
		Result: s.runProxyProbe(ctx, "", "", probes, 10*time.Second),
	})

	if configuredProxy != "" {
		probeAddr, probeCA, viaMITM := egressTarget(configuredProxy, capture, pending, agentProxy, agentCA)
		timeout := 10 * time.Second
		if viaMITM {
			timeout = 15 * time.Second // extra TLS decrypt + re-sign hop
		}
		rep.Checks = append(rep.Checks, egressCheck{
			Key:   "global",
			Label: "② Exit IP via global proxy",
			// Path is the actual probe address (8788 when via MITM), shown for troubleshooting.
			Path:   maskSecrets(probeAddr),
			Result: s.runProxyProbe(ctx, probeAddr, probeCA, probes, timeout),
		})
	} else {
		skipReason := "Input box is empty, no proxy configured"
		if savedProxy != "" {
			// Saying "no proxy configured" would contradict W1 ("Agent still uses xxx").
			skipReason = "Input box is empty; saved proxy is still in effect"
		}
		rep.Checks = append(rep.Checks, egressCheck{
			Key:        "global",
			Label:      "② Exit IP via global proxy",
			Skipped:    true,
			SkipReason: skipReason,
			Result:     egressProbeResult{Probes: []egressAttempt{}},
		})
	}

	rep.OK = judgeEgress(rep)
	rep.Warnings = egressWarnings(rep, pending, savedProxy)
	if req.Resume && rep.OK && !pending {
		// The navbar uses the same probe endpoint for display-only polling but
		// omits resume. Only an explicit settings-page test can release the
		// guarded paused_by_proxy tasks.
		s.proxyReconnectSuccessWithGuards(proxyGuards, nil)
	}
	writeJSON(w, 200, rep)
}

// normalizeProxy only normalizes format, no validation; empty string and parse failures returned as-is.
func normalizeProxy(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

// judgeEgress uses ② as primary; falls back to ① only if ② skipped. Not "any row passes = success".
func judgeEgress(rep egressReport) bool {
	for _, c := range rep.Checks {
		if c.Key == "global" && !c.Skipped {
			return c.Result.OK
		}
	}
	for _, c := range rep.Checks {
		if c.Key == "direct" {
			return c.Result.OK
		}
	}
	return false
}

// egressWarnings generates reminders that "numbers are easy to miss".
func egressWarnings(rep egressReport, pending bool, savedProxy string) []string {
	var out []string
	safeSavedProxy := maskSecrets(savedProxy)

	// W1: agent runs saved config; without warning "test passed = config done" is misleading.
	if pending {
		switch {
		case rep.Configured == "":
			out = append(out, "This address is not yet saved; Agent still uses "+safeSavedProxy+". After saving it will switch to direct connection.")
		case savedProxy == "":
			out = append(out, "This address is not yet saved; Agent is still using direct connection."+
				" The above results correspond to the input box value; takes effect after saving.")
		default:
			out = append(out, "This address is not yet saved; Agent still uses "+safeSavedProxy+
				". The above results correspond to the input box value; takes effect after saving.")
		}
	}

	var directIP, proxyIP string
	proxyOK := false
	for _, c := range rep.Checks {
		switch c.Key {
		case "direct":
			directIP = c.Result.ExitIP
		case "global":
			if !c.Skipped {
				proxyIP = c.Result.ExitIP
				proxyOK = c.Result.OK
			}
		}
	}

	// W2: check saved value; only warn when ② passes — if it fails, "works here ≠ works there" is moot.
	if !rep.Capture && proxyOK && strings.HasPrefix(strings.ToLower(savedProxy), "socks5") {
		out = append(out, "Currently \"capture off + socks5\": this test uses a Go client (always respects proxy),"+
			" but agent tools may not—curl works, python urllib will error directly."+
			" So this passing does not mean all tools work; enable traffic capture (MITM dials directly) for stable effect.")
	}

	// W3: both rows same IP → proxy may be ineffective, or just transparent forwarding.
	if directIP != "" && proxyIP != "" && directIP == proxyIP {
		out = append(out, fmt.Sprintf("Direct and proxy egress IP are the same (%s): proxy may not be effective, or just transparent forwarding, "+
			"or the jump host and local machine are behind the same NAT (egress IP is naturally the same). "+
			"To distinguish: fill in an internal echo service in \"Custom IP Probe Service\" and check if the source IP it sees becomes the jump host.", directIP))
	}

	// W4: same, check saved value to avoid contradicting W1.
	if rep.Capture && savedProxy == "" {
		out = append(out, "Traffic capture is enabled but global proxy is empty: capture only records, egress is still direct.")
	}

	// W5: 127.0.0.1 in container = container itself, can't reach host proxy.
	if isLoopbackProxy(rep.Configured) {
		out = append(out, "Proxy address is a loopback address (127.0.0.1 / localhost)."+
			" If ARTEX runs in a container, this 127.0.0.1 refers to the container itself, cannot reach the host proxy;"+
			" please use a reachable host IP (e.g. http://192.168.x.x:port).")
	}

	return out
}

func isLoopbackProxy(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" {
		return false
	}
	h := strings.ToLower(u.Hostname())
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}
