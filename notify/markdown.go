package notify

import (
	"fmt"
	"strings"
)

// This file renders messages shared by "Markdown-family" channels (DingTalk,
// WeCom). Feishu uses card JSON, Telegram uses HTML, and email uses HTML;
// each is rendered in its own adapter.

// maxAssetsShown is the maximum number of assets listed in a message. A
// finding may be linked to dozens of assets; listing all of them bloats the
// message with little value.
const maxAssetsShown = 3

// maxSummaryRunes is the character limit for summaries. IM messages are prompts
// to view details, not the report itself; full content lives in the platform.
const maxSummaryRunes = 120

// markdownReservedBytes reserves space for the message header (digest line +
// severity distribution + possible truncation note) and footer (platform link).
// It is subtracted from the budget when packing whole items so header/footer
// are not cut off.
const markdownReservedBytes = 320

// markdownEscape escapes markdown metacharacters.
//
// Why this is required: finding title, summary, type, and asset display names
// all come from untrusted sources. Titles and summaries come from model
// output (the model reads target responses), and asset URLs are full scanned
// URLs (including target-controlled query strings). Without escaping, a title
// like:
//
//	login SQL injection\n[Urgent: verify account](http://attacker.tld)
//
// would render as a clickable external link in DingTalk/Feishu for a security
// engineer; and `![](http://attacker.tld/beacon)` would be fetched by the
// client on render, leaking that the finding was viewed and the reader's IP.
// Even benign injected bold/blockquote content can push severe findings out of
// view.
//
// The escaped set covers characters that change structure or create clickable
// elements (headings/links/emphasis/lists/quotes/strikethrough). `\` must be
// handled first, otherwise backslashes added later would be escaped again.
func markdownEscape(s string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`,
		"`", "\\`",
		"*", `\*`,
		"_", `\_`,
		"[", `\[`,
		"]", `\]`,
		"(", `\(`,
		")", `\)`,
		"!", `\!`,
		"#", `\#`,
		">", `\>`,
		"|", `\|`,
		"~", `\~`,
	)
	return replacer.Replace(s)
}

// markdownText normalizes untrusted text to a single line and escapes it for
// markdown body use. Single-line normalization complements escaping: newlines
// themselves can forge list items or blockquotes, which escaping cannot block.
func markdownText(s string, maxRunes int) string {
	return markdownEscape(OneLine(s, maxRunes))
}

// markdownTitle returns the message title (IM title bar/card title) as raw,
// unescaped text.
//
// Escaping is intentionally omitted here: this title is shared by renderers in
// four contexts -- markdown body, Telegram HTML, Feishu plain_text card
// header, and generic webhook JSON/email subject. Each context has different
// escaping rules (markdown escapes in HTML leave visible backslashes; in JSON
// they corrupt data), so escaping must be done by each output path (see
// writeItem / feishuItemLines / telegramEscape).
func markdownTitle(m Message) string {
	if m.Batch {
		return fmt.Sprintf("Finding digest · %d items", len(m.Items))
	}
	if len(m.Items) == 0 {
		return "Finding notification"
	}
	it := m.Items[0]
	return fmt.Sprintf("[%s] %s", SeverityLabel(it.Severity), OneLine(it.Title(), 0))
}

// markdownBody renders the message body and returns the body plus the number of
// items actually written.
//
// The returned kept count is what was truly delivered; callers must mark only
// the first kept items as delivered. Items excluded by channel length limits
// must remain for the next batch rather than being marked successful.
//
// maxBytes<=0 means unlimited.
func markdownBody(m Message, maxBytes int) (string, int) {
	if !m.Batch {
		if len(m.Items) == 0 {
			return "", 0
		}
		var b strings.Builder
		writeItem(&b, m.Items[0], "", true)
		return TruncateBytes(b.String(), maxBytes), 1
	}

	footer := ""
	if m.HomeURL != "" {
		footer = fmt.Sprintf("\n[View all in platform](%s)\n", m.HomeURL)
	}
	kept := packItemCount(m.Items, maxBytes, markdownReservedBytes, footer, byteSize, func(it Item, idx int) string {
		var b strings.Builder
		writeItem(&b, it, fmt.Sprintf("%d. ", idx+1), false)
		return b.String()
	})

	items := m.Items[:kept]
	var b strings.Builder
	b.WriteString(markdownBatchIntro(m, items, len(m.Items)))
	for i, it := range items {
		writeItem(&b, it, fmt.Sprintf("%d. ", i+1), false)
	}
	b.WriteString(footer)
	return TruncateBytes(b.String(), maxBytes), kept
}

// markdownBatchIntro renders the digest header: time window, count, and
// severity distribution. items are the items actually included; total is the
// full batch size. When they differ, the header must state how many remain for
// the next message.
func markdownBatchIntro(m Message, items []Item, total int) string {
	var b strings.Builder
	if m.WindowMinutes > 0 {
		fmt.Fprintf(&b, "**%d new findings in the last %d minutes**", total, m.WindowMinutes)
	} else {
		fmt.Fprintf(&b, "**%d new findings**", total)
	}
	if extra := total - len(items); extra > 0 {
		fmt.Fprintf(&b, " (showing first %d, %d more will follow in the next message)", len(items), extra)
	}
	counts := map[string]int{}
	for _, it := range items {
		counts[it.Severity]++
	}
	var parts []string
	for _, sev := range []string{"critical", "high", "medium", "low"} {
		if n := counts[sev]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", SeverityLabel(sev), n))
		}
	}
	if len(parts) > 0 {
		b.WriteString("\n" + strings.Join(parts, " · "))
	}
	b.WriteString("\n\n")
	return b.String()
}

// writeItem renders a single finding item.
//
// prefix is used for list numbering in digests; single=true renders the full
// form (with summary and link), while digest mode renders a compact line.
// All untrusted content (title/type/assets/summary) is passed through
// markdownText. The detail link comes from configured public_base_url and must
// remain clickable, so it is emitted as-is.
func writeItem(b *strings.Builder, it Item, prefix string, single bool) {
	line := fmt.Sprintf("%s**%s · %s**", prefix, SeverityLabel(it.Severity), markdownText(it.Title(), 0))
	if !single {
		var extras []string
		if a := assetLine(it.Assets, maxAssetsShown); a != "" {
			extras = append(extras, markdownText(a, 0))
		}
		if it.Summary != "" {
			extras = append(extras, markdownText(it.Summary, 60))
		}
		if len(extras) > 0 {
			line += " — " + strings.Join(extras, " · ")
		}
		b.WriteString(line + "\n")
		return
	}
	b.WriteString(line + "\n")
	if it.IsStatusChange() {
		fmt.Fprintf(b, "**Status change**: %s → %s\n",
			markdownText(StatusLabel(it.FromStatus), 0), markdownText(StatusLabel(it.ToStatus), 0))
	}
	if it.VulnClass != "" && it.VulnClass != it.Title() {
		fmt.Fprintf(b, "**Type**: %s\n", markdownText(it.VulnClass, 0))
	}
	if a := assetLine(it.Assets, maxAssetsShown); a != "" {
		fmt.Fprintf(b, "**Assets**: %s\n", markdownText(a, 0))
	}
	if it.Summary != "" {
		if s := markdownText(it.Summary, maxSummaryRunes); s != "" {
			fmt.Fprintf(b, "**Summary**: %s\n", s)
		}
	}
	if it.DetailURL != "" {
		fmt.Fprintf(b, "[View details](%s)\n", it.DetailURL)
	}
}
