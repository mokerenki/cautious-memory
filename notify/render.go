package notify

import (
	"strings"
	"unicode/utf8"
)

const ellipsis = "…"

// TruncateBytes truncates s to at most max bytes, ensuring the result is valid
// UTF-8 and does not split a character.
//
// Why character-boundary cutting is required: WeCom group bot markdown has a
// hard 4096-byte limit (not character count), and a Chinese character is 3
// bytes. Slicing by bytes directly can split a character in half and produce
// invalid UTF-8, which the platform rejects or renders as garbled blocks. The
// approach is to step back from the budget position to the nearest rune start
// byte (utf8.RuneStart detects continuation bytes 0b10xxxxxx).
//
// max<=0 means unlimited. Appends an ellipsis after truncation unless max is
// too small to fit it.
func TruncateBytes(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	budget := max - len(ellipsis)
	suffix := ellipsis
	if budget < 0 {
		budget = max
		suffix = ""
	}
	cut := budget
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + suffix
}

// OneLine collapses multi-line text into a single line by folding whitespace,
// then truncates by rune count. Used for IM title lines where summaries often
// contain newlines that would break table/title layout.
// max<=0 means unlimited.
func OneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	return TruncateRunes(s, max)
}

// TruncateRunes truncates s to at most max runes (not bytes), appending an
// ellipsis when truncated. max<=0 means unlimited.
//
// Unlike TruncateBytes, this uses a character-count limit: WeCom limits by
// bytes, Telegram limits by character count. Using the wrong unit does not
// error, but it can cut messages far shorter than intended (1 Chinese char = 3
// bytes, so a 4096-byte cut leaves ~1365 chars), so both functions must be
// kept and chosen per channel.
func TruncateRunes(s string, max int) string {
	if max <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 1 {
		return string(runes[:max])
	}
	return string(runes[:max-1]) + ellipsis
}

// TruncateHTML truncates an HTML fragment by rune count and ensures no partial
// tag is produced.
//
// Truncating HTML by characters can produce a broken tag like `<a href="htt`;
// platform parsers may reject the whole message or treat following text as an
// attribute value. The approach: truncate by characters, then check whether the
// tail contains an unclosed `<` and cut back before it if so.
//
// Tag balancing (e.g. closing </b>) is intentionally not done: Telegram's HTML
// parser auto-closes unclosed tags, and implementing balancing would require
// handling quotes inside attributes, comments, and self-closing tags with poor
// cost/benefit.
func TruncateHTML(s string, max int) string {
	if max <= 0 || len([]rune(s)) <= max {
		return s
	}
	cut := TruncateRunes(s, max)
	if lt := strings.LastIndex(cut, "<"); lt >= 0 && !strings.Contains(cut[lt:], ">") {
		cut = cut[:lt]
	}
	if amp := strings.LastIndex(cut, "&"); amp >= 0 && !strings.Contains(cut[amp:], ";") {
		cut = cut[:amp]
	}
	return cut
}

// packItemCount computes how many items can fit completely within the budget,
// used to pack digest messages item by item.
//
// Why pack whole items instead of rendering the full text and truncating:
// truncation makes trailing items disappear while their delivery records may
// still be marked delivered, so they vanish from the message and from delivery
// history. By packing whole items, items that do not fit remain for the next
// batch, and the returned kept count is what was actually delivered.
//
// Parameters: maxSize<=0 means unlimited; reserve is space reserved for
// header/footer; size measures length (units differ by platform: WeCom/DingTalk
// use bytes, Telegram uses rune count; using the wrong unit does not error but
// can compress Chinese messages far below the limit); render renders item idx
// to its actual text, whose length varies by content and cannot be estimated.
//
// Returns at least 1 when there are items. If a single item is extremely long,
// it is still emitted and the caller's final truncation acts as a backstop;
// otherwise one overlong finding would permanently block the whole batch.
func packItemCount(items []Item, maxSize, reserve int, footer string, size func(string) int, render func(Item, int) string) int {
	if maxSize <= 0 {
		return len(items)
	}
	budget := maxSize - reserve - size(footer)
	if budget < 0 {
		budget = 0
	}
	used := 0
	for i, it := range items {
		used += size(render(it, i))
		if used > budget && i > 0 {
			return i
		}
	}
	return len(items)
}

// byteSize / runeSize are the two measurement units for packItemCount, named
// to avoid bare func(s string) int closures at call sites where the unit would
// be hard to read.
func byteSize(s string) int { return len(s) }
func runeSize(s string) int { return utf8.RuneCountInString(s) }

// assetLine renders an asset list as one display line, omitting extras beyond
// limit and annotating the total count. A finding may be linked to dozens of
// assets; listing them all would bloat the message.
func assetLine(assets []string, limit int) string {
	if len(assets) == 0 {
		return ""
	}
	if limit <= 0 || len(assets) <= limit {
		return strings.Join(assets, ", ")
	}
	return strings.Join(assets[:limit], ", ") + " and " + itoa(len(assets)-limit) + " more"
}

// itoa is a short alias of strconv.Itoa used only for display text assembly,
// avoiding repeated strconv imports.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
