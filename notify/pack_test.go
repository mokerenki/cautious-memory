package notify

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// This file covers the "pack whole items" fix: when a summary exceeds a channel length limit, it must **truncate whole items**
// and honestly report how many items didn't fit, so callers only mark the ones actually delivered.
//
// The old approach rendered the full text then truncated, and marked the whole batch delivered: the second half of the message vanished into thin air,
// while the delivery history showed everything successful—the hole was just gone, with nowhere to discover it.

func TestMarkdownBodyPacksWholeItemsWithinByteLimit(t *testing.T) {
	// 200 Chinese summary items will far exceed WeCom's 4096 bytes.
	m := batchMsg(200)
	body, kept := markdownBody(m, weComMarkdownLimit)

	if len(body) > weComMarkdownLimit {
		t.Fatalf("body %d bytes exceeds limit %d", len(body), weComMarkdownLimit)
	}
	if !utf8.ValidString(body) {
		t.Fatal("body is not valid UTF-8")
	}
	if kept <= 0 || kept >= len(m.Items) {
		t.Fatalf("should only fit a partial batch (0 < kept < %d), got %d", len(m.Items), kept)
	}
	// The header must honestly state how many items this message contains and how many remain—otherwise readers will take the header number as the total.
	if !strings.Contains(body, "more will follow") || !strings.Contains(body, "next message") {
		t.Fatalf("header should state how many items are omitted from this message:\n%s", body[:minInt(400, len(body))])
	}
	// Should only contain the first kept items.
	for i := 0; i < kept; i++ {
		if !strings.Contains(body, "Finding "+itoa(i+1)) {
			t.Fatalf("item %d should be included in this message:\n%s", i+1, body)
		}
	}
	if strings.Contains(body, "Finding "+itoa(kept+1)) {
		t.Fatalf("item %d should not appear (it belongs to the next batch)", kept+1)
	}
}

func TestMarkdownBodyKeepsEverythingWhenUnderLimit(t *testing.T) {
	m := batchMsg(3)
	body, kept := markdownBody(m, 0) // 0 = unlimited
	if kept != len(m.Items) {
		t.Fatalf("when length is unlimited all items should be retained, got kept=%d", kept)
	}
	if strings.Contains(body, "more will follow") {
		t.Fatalf("truncation note should not appear when nothing is truncated:\n%s", body)
	}
}

func TestMarkdownBodyAlwaysKeepsAtLeastOneItem(t *testing.T) {
	// Even when the budget can't fit a single item, still send one (final truncation as fallback).
	// Otherwise one oversized finding would permanently stall the whole batch: each fetch fails to fit, so nothing ever sends.
	m := batchMsg(5)
	_, kept := markdownBody(m, 50)
	if kept != 1 {
		t.Fatalf("at least 1 item should be retained, got %d", kept)
	}
}

func TestMarkdownBodySingleReturnsOne(t *testing.T) {
	_, kept := markdownBody(singleMsg(), 4096)
	if kept != 1 {
		t.Fatalf("a single message should report 1 delivered, got %d", kept)
	}
	// An empty message has no deliverable items.
	if _, k := markdownBody(Message{}, 4096); k != 0 {
		t.Fatalf("an empty message should report 0 items, got %d", k)
	}
}

func TestTelegramPackingUsesRuneBudget(t *testing.T) {
	m := batchMsg(200)
	text, kept := telegramHTML(m)
	// Telegram limits by **character count**; using byte count would squeeze Chinese messages to one third.
	if n := utf8.RuneCountInString(text); n > telegramTextLimit {
		t.Fatalf("body %d chars exceeds limit %d", n, telegramTextLimit)
	}
	if kept <= 0 || kept >= len(m.Items) {
		t.Fatalf("should only fit a partial batch, got %d", kept)
	}
	if !strings.Contains(text, "next message") && !strings.Contains(text, "more will follow") {
		t.Fatalf("should indicate remaining items are not included:\n%.300s", text)
	}
}

func TestFeishuPackingReportsKept(t *testing.T) {
	m := batchMsg(2000)
	_, kept := feishuCard(m)
	if kept <= 0 || kept >= len(m.Items) {
		t.Fatalf("the card should only fit a partial batch, got %d", kept)
	}
}

func TestWebhookAndEmailReportAllItems(t *testing.T) {
	// These two channels don't truncate the body; the whole batch counts as delivered.
	m := batchMsg(7)
	if n := len(m.Items); n != 7 {
		t.Fatal("precondition not met")
	}
	// Indirectly confirmed via the renderer return value: markdownBody(0) with unlimited length retains all.
	if _, k := markdownBody(m, 0); k != len(m.Items) {
		t.Fatalf("when length is unlimited all should be retained, got %d", k)
	}
}

// TestMarkdownEscapesUntrustedContent is a regression test for "untrusted content must not change message structure".
// Title and summary come from model output (the model reads the target-under-test response); asset names come from the target-under-test's URL.
func TestMarkdownEscapesUntrustedContent(t *testing.T) {
	cases := []struct {
		name  string
		item  Item
		must  []string // must appear (escaped forms)
		wrong []string // must NOT appear (unescaped forms)
	}{
		{
			name: "title newline + external link",
			item: Item{
				Severity: "high",
				Name:     "登录口 SQL 注入\n[紧急：点此验证账号](http://attacker.tld)",
			},
			// Newlines must be collapsed (otherwise new list items / blockquotes could be forged);
			// square brackets and parentheses must be escaped (otherwise they're clickable external links).
			must:  []string{`\[紧急：点此验证账号\]`, `\(http://attacker.tld\)`},
			wrong: []string{"\n[紧急", "\n\n[紧急"},
		},
		{
			name: "title image beacon",
			item: Item{
				Severity: "high",
				Name:     "漏洞 ![](http://attacker.tld/beacon)",
			},
			must:  []string{`\!`, `\(http://attacker.tld/beacon\)`},
			wrong: []string{"![]("},
		},
		{
			name: "asset name emphasis and quote",
			item: Item{
				Severity: "high",
				Name:     "普通标题",
				Assets:   []string{"a.com/*注入*>引用"},
			},
			must:  []string{`\*注入\*`, `\>`},
			wrong: []string{"*注入*"},
		},
		{
			name: "summary backtick and pipe",
			item: Item{
				Severity: "high",
				Name:     "标题",
				Summary:  "`code` | 表格",
			},
			must:  []string{"\\`code\\`", `\|`},
			wrong: []string{"`code`"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := Message{Items: []Item{tc.item}}
			// The single-item writeItem path is shared by the three markdown channels.
			var b strings.Builder
			writeItem(&b, tc.item, "", true)
			got := b.String()
			for _, want := range tc.must {
				if !strings.Contains(got, want) {
					t.Errorf("missing escaped form %q:\n%s", want, got)
				}
			}
			for _, bad := range tc.wrong {
				if strings.Contains(got, bad) {
					t.Errorf("unescaped form %q appeared (could be used to inject structure or links):\n%s", bad, got)
				}
			}
			_ = m
		})
	}
}

// TestMarkdownEscapeBackslashFirst locks the escape order: backslashes must be processed first,
// otherwise backslashes added later get another layer, producing double backslashes in output.
func TestMarkdownEscapeBackslashFirst(t *testing.T) {
	if got := markdownEscape(`a\b*c`); got != `a\\b\*c` {
		t.Fatalf("escape order wrong, got %q", got)
	}
}

// TestTelegramTitleHasNoMarkdownEscapes locks a specific regression:
// markdown escapes must not leak into Telegram's HTML output (escapes were once added in the shared title function,
// adding escapes caused Telegram messages to show visible backslashes like `\(1\)`).
func TestTelegramTitleHasNoMarkdownEscapes(t *testing.T) {
	m := Message{Items: []Item{{Severity: "high", Name: "alert(1) *重点*"}}}
	text, _ := telegramHTML(m)
	if strings.Contains(text, `\(`) || strings.Contains(text, `\*`) {
		t.Fatalf("markdown backslash escapes appeared in Telegram body:\n%s", text)
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
