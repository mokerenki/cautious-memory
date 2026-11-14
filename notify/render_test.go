package notify

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateBytesKeepsValidUTF8(t *testing.T) {
	// This is the package's most important invariant. WeCom limits by **bytes**, and Chinese is 3 bytes/char,
	// so any byte-hard-cut implementation will slice a Han character in half, producing invalid UTF-8 that the platform rejects.
	// Use several Chinese/English mixed inputs of coprime lengths to hit every possible cut point.
	inputs := []string{
		"中文测试内容",
		"混合 mixed 内容 content",
		"a中b文c测d试e",
		"🔴🟠🟡🔵", // 4-byte emoji, a wrong cut is more obvious
		strings.Repeat("漏洞", 100),
	}
	for _, in := range inputs {
		for max := 1; max <= len(in)+2; max++ {
			got := TruncateBytes(in, max)
			if !utf8.ValidString(got) {
				t.Fatalf("input %q max=%d: produced invalid UTF-8 %q", in, max, got)
			}
			if len(got) > max {
				t.Fatalf("input %q max=%d: result %d bytes exceeds limit", in, max, len(got))
			}
			// Content must not be modified when not truncated.
			if len(in) <= max && got != in {
				t.Fatalf("input %q max=%d: under limit but content changed -> %q", in, max, got)
			}
		}
	}
}

func TestTruncateBytesZeroMeansUnlimited(t *testing.T) {
	long := strings.Repeat("x", 10000)
	if got := TruncateBytes(long, 0); got != long {
		t.Fatal("max=0 means unlimited")
	}
	if got := TruncateBytes(long, -5); got != long {
		t.Fatal("max<0 means unlimited")
	}
}

func TestTruncateBytesEllipsisBudget(t *testing.T) {
	// When max is smaller than the ellipsis itself, appending the ellipsis must not push back over the limit.
	got := TruncateBytes("abcdefgh", 1)
	if len(got) > 1 {
		t.Fatalf("max=1: result %q length %d exceeds limit", got, len(got))
	}
	// Normally it should carry the ellipsis.
	if got := TruncateBytes("abcdefgh", 5); !strings.HasSuffix(got, ellipsis) {
		t.Fatalf("expected an ellipsis, got %q", got)
	}
}

func TestTruncateRunesCountsCharactersNotBytes(t *testing.T) {
	// The distinction from TruncateBytes must be preserved: Telegram limits by characters,
	// using byte count would cut Chinese messages down to one third.
	s := "一二三四五六七八九十"
	got := TruncateRunes(s, 5)
	if n := utf8.RuneCountInString(got); n != 5 {
		t.Fatalf("expected 5 characters, got %d (%q)", n, got)
	}
	// The same string by byte count should clearly be shorter.
	if utf8.RuneCountInString(TruncateBytes(s, 5)) >= 5 {
		t.Fatal("byte count must not produce the same character count as character count")
	}
}

func TestOneLineCollapsesWhitespace(t *testing.T) {
	got := OneLine("第一行\n\n第二行\t带制表   多空格", 0)
	if strings.ContainsAny(got, "\n\t") {
		t.Fatalf("all whitespace should be collapsed, got %q", got)
	}
	if strings.Contains(got, "  ") {
		t.Fatalf("consecutive spaces must not be retained, got %q", got)
	}
	// After truncation it must still be readable and valid.
	got = OneLine("一二三四五六七八九十", 4)
	if n := utf8.RuneCountInString(got); n != 4 {
		t.Fatalf("expected 4 characters, got %d (%q)", n, got)
	}
}

func TestTruncateHTMLNeverCutsTagInHalf(t *testing.T) {
	// Cutting HTML directly produces fragments like `<a href="htt`, and the platform rejects the whole message.
	s := `<b>标题</b>正文正文正文<a href="https://example.com/very/long/path">查看详情</a>`
	for max := 1; max <= utf8.RuneCountInString(s)+2; max++ {
		got := TruncateHTML(s, max)
		if n := utf8.RuneCountInString(got); max > 0 && n > max {
			t.Fatalf("max=%d: result %d chars exceeds limit", max, n)
		}
		// The tail must not have an unclosed `<` (a `<` in the last segment without a matching `>`).
		if lt := strings.LastIndex(got, "<"); lt >= 0 && !strings.Contains(got[lt:], ">") {
			t.Fatalf("max=%d: trailing tag was cut -> %q", max, got)
		}
	}
}

func TestAssetLineOmitsExcess(t *testing.T) {
	if got := assetLine(nil, 3); got != "" {
		t.Fatalf("expected empty string for no assets, got %q", got)
	}
	if got := assetLine([]string{"a", "b"}, 3); got != "a, b" {
		t.Fatalf("expected all assets when under limit, got %q", got)
	}
	// When over the limit the total must be noted, otherwise the reader won't know how many assets weren't listed.
	got := assetLine([]string{"a", "b", "c", "d", "e"}, 2)
	if !strings.Contains(got, "and 3 more") {
		t.Fatalf("expected overflow count, got %q", got)
	}
}

func TestSeverityAndStatusLabels(t *testing.T) {
	if AtLeast("", "low") {
		t.Fatal("empty level's order is 0, should be blocked by any threshold")
	}
	if !AtLeast("critical", "") {
		t.Fatal("empty threshold should pass")
	}
	if got := StatusLabel("fixed"); got != "Fixed" {
		t.Fatalf("unknown status mapped, got %q", got)
	}
	// Unknown status echoed as-is, no fabricated labels.
	if got := StatusLabel("weird_status"); got != "weird_status" {
		t.Fatalf("unknown status should be echoed as-is, got %q", got)
	}
}

// TestTruncateHTMLNeverCutsEntity covers a gap flagged by audit: truncation must avoid
// half-cut HTML entities, not just half-cut tags.
//
// If `&amp;` is cut into `&amp`, an entity-only parser may reject the **whole** message—
// and oversized summary messages are common, so the cost is too high.
func TestTruncateHTMLNeverCutsEntity(t *testing.T) {
	s := "aaaa&amp;bbbb&lt;cccc&quot;dddd"
	for max := 1; max <= utf8.RuneCountInString(s)+2; max++ {
		got := TruncateHTML(s, max)
		// The tail must not leave an entity fragment: a `&` without a matching `;`.
		if amp := strings.LastIndex(got, "&"); amp >= 0 && !strings.Contains(got[amp:], ";") {
			t.Fatalf("max=%d: trailing entity fragment left %q", max, got[amp:])
		}
		if strings.Contains(got, "&amp\x00") {
			t.Fatalf("max=%d: malformed entity appeared", max)
		}
	}
}
