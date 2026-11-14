package notify

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// emailDialTimeout / emailSessionTimeout constrain connection establishment and the whole SMTP session respectively.
// net/smtp has no timeout mechanism of its own; without these, a stuck peer can leave the delivery goroutine
// hanging forever—and the dispatcher processes serially in a single goroutine, effectively stalling the whole notification system.
const (
	emailDialTimeout    = 10 * time.Second
	emailSessionTimeout = 45 * time.Second
)

// emailChannel implements SMTP email delivery.
type emailChannel struct{}

func (emailChannel) Kind() string { return KindEmail }

// Email has no platform rate limit, but it should not be used to spam; use a generous default.
func (emailChannel) DefaultRatePerMin() int { return 60 }

// Only mask the password. SMTP host, account, and recipients are not secrets; masking them would only make editing harder.
func (emailChannel) SecretKeys() []string { return []string{"password"} }

// host/port determine which server receives the password; tls determines whether transport is encrypted.
// Any change to these requires re-entering the password—this also forces "turning off TLS" to be explicit rather than accidental.
func (emailChannel) DestinationKeys() []string { return []string{"host", "port", "tls"} }

func (emailChannel) Validate(cfg map[string]any) error {
	if cfgString(cfg, "host") == "" {
		return errors.New("missing SMTP server host")
	}
	port := cfgInt(cfg, "port")
	if port <= 0 || port > 65535 {
		return errors.New("invalid SMTP port (must be 1-65535)")
	}
	if cfgString(cfg, "from") == "" {
		return errors.New("missing sender address")
	}
	if len(cfgStrings(cfg, "to")) == 0 {
		return errors.New("at least one recipient address is required")
	}
	return nil
}

func (c emailChannel) Send(ctx context.Context, cfg map[string]any, m Message) (int, error) {
	if err := c.Validate(cfg); err != nil {
		return 0, Permanent(err)
	}
	host := cfgString(cfg, "host")
	port := cfgInt(cfg, "port")
	from := cfgString(cfg, "from")
	to := cfgStrings(cfg, "to")
	username := cfgString(cfg, "username")
	password := cfgString(cfg, "password")
	implicitTLS := cfgBool(cfg, "tls")

	msg, err := buildEmailMessage(from, to, m)
	if err != nil {
		return 0, Permanent(err)
	}

	addr := net.JoinHostPort(host, strconv.Itoa(port))
	client, err := emailDial(ctx, addr, host, implicitTLS)
	if err != nil {
		return 0, err
	}
	defer client.Close()

	// STARTTLS: upgrade if the peer supports it. Credentials must not be sent over a plaintext session (see auth handling below).
	if !implicitTLS {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
				return 0, fmt.Errorf("failed to start STARTTLS: %w", err)
			}
		}
	}
	if username != "" {
		if err := client.Auth(smtp.PlainAuth("", username, password, host)); err != nil {
			// smtp.PlainAuth refuses to send credentials over an unencrypted connection (unless the target is localhost).
			// This is the **correct** security behavior and must not be bypassed, but the reason needs to be clear—
			// otherwise users only see "unencrypted connection" without knowing what to do.
			if strings.Contains(err.Error(), "unencrypted connection") {
				return 0, Permanent(fmt.Errorf("refusing to send credentials over an unencrypted connection: enable TLS, use port 465 (implicit TLS), or turn on STARTTLS (%w)", err))
			}
			return 0, Permanent(fmt.Errorf("SMTP authentication failed: %w", err))
		}
	}
	if err := client.Mail(from); err != nil {
		return 0, smtpStageError(fmt.Sprintf("sender %s rejected", from), err)
	}
	for _, rcpt := range to {
		if err := client.Rcpt(rcpt); err != nil {
			return 0, smtpStageError(fmt.Sprintf("recipient %s rejected", rcpt), err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return 0, fmt.Errorf("SMTP DATA failed: %w", err)
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return 0, fmt.Errorf("failed to write email body: %w", err)
	}
	if err := w.Close(); err != nil {
		return 0, fmt.Errorf("failed to submit email: %w", err)
	}
	// Quit failure does not change the fact that the server has accepted the message, so its error is ignored.
	_ = client.Quit()
	// Email has no body truncation (the full HTML body is sent), so the whole batch counts as delivered.
	return len(m.Items), nil
}

// emailDial establishes an SMTP connection.
//
// implicitTLS=true uses "TLS on connect" (e.g. port 465); false uses plaintext connect on 25/587 followed by
// STARTTLS. The two must not be mixed: sending a plaintext greeting to port 465 will be dropped immediately.
//
// The session deadline is set **at connection time** (not retroactively), because net/smtp hides the underlying
// connection in an unexported field and the caller cannot access it later; once handed off, only a pre-set deadline
// can act as a backstop. This also covers blocking during the handshake.
// Control attaches blockInternalDial, sharing the same guard as HTTP channels. Without it SMTP would be the
// gap in the SSRF protection: a host of 169.254.169.254 or 127.0.0.1 could be connected to directly, and a
// handshake failure would wrap the peer's response line into the error, echo it via last_error in delivery
// history (a semi-blind read primitive), while timing differences ("connection refused" vs "timeout") could probe ports.
// The dial stage is the final enforcement point and also covers DNS rebinding.
func emailDial(ctx context.Context, addr, host string, implicitTLS bool) (*smtp.Client, error) {
	dialer := &net.Dialer{Timeout: emailDialTimeout, Control: blockInternalDial}
	var (
		conn net.Conn
		err  error
	)
	if implicitTLS {
		tlsDialer := &tls.Dialer{NetDialer: dialer, Config: &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}}
		conn, err = tlsDialer.DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to connect to SMTP server %s: %w", addr, err)
	}
	// Cover the entire session including handshake and data transfer.
	_ = conn.SetDeadline(time.Now().Add(emailSessionTimeout))
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("SMTP handshake failed: %w", err)
	}
	return client, nil
}

// smtpStageError classifies a stage failure by SMTP reply code into "retryable" or "permanent".
//
// Why this distinction is required: SMTP 4xx and 5xx have completely different semantics—
//   - 4xx (450 greylisting, 451 local error, 452 storage full) are **temporary** rejections;
//     the correct behavior is to retry later; greylisting in particular almost always hits on the first attempt.
//   - 5xx (550 mailbox not found, 553 invalid address, 554 policy rejection) are **permanent** rejections;
//     retrying just keeps sending messages the peer will never accept.
func smtpStageError(stage string, err error) error {
	if isSMTPTemp(err) {
		return fmt.Errorf("%s (temporary): %w", stage, err)
	}
	if isSMTPPerm(err) {
		return Permanent(fmt.Errorf("%s (permanent): %w", stage, err))
	}
	// Unknown error type (e.g. broken connection): prefer retry over dropping.
	return fmt.Errorf("%s: %w", stage, err)
}

func isSMTPTemp(err error) bool {
	return smtpCode(err) >= 400 && smtpCode(err) < 500
}

func isSMTPPerm(err error) bool {
	c := smtpCode(err)
	return c >= 500 && c < 600
}

func smtpCode(err error) int {
	// net/smtp surfaces negative responses as *textproto.Error carrying the numeric code.
	type coder interface{ Error() string }
	// Avoid importing net/textproto for a single field read: extract the leading 3 digits.
	msg := err.Error()
	if len(msg) < 3 {
		return 0
	}
	code, convErr := strconv.Atoi(msg[:3])
	if convErr != nil {
		return 0
	}
	return code
}

// buildEmailMessage assembles a complete RFC 5322 email.
//
// The body is base64-encoded for two reasons: SMTP limits lines to 1000 bytes and HTML bodies
// (especially digest emails) can easily exceed that; base64 also never produces a line starting
// with ".", avoiding SMTP dot-stuffing concerns.
func buildEmailMessage(from string, to []string, m Message) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(to, ", "))
	// Subject must be RFC 2047 encoded; Q-encoding is used for compatibility with clients.
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", htmlTitle(m)))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/html; charset=\"UTF-8\"\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n")
	// Email has no hard length limit, so the body is not truncated.
	b.WriteString("\r\n")
	encoded := base64.StdEncoding.EncodeToString([]byte(htmlBody(m, 0)))
	// Wrap base64 at 76 characters per RFC 2045.
	for len(encoded) > 76 {
		b.WriteString(encoded[:76] + "\r\n")
		encoded = encoded[76:]
	}
	b.WriteString(encoded + "\r\n")
	return b.String(), nil
}