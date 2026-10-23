package approval

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// escapeApprovalText makes backend-supplied text inert before layout, so
// terminal controls display as visible escapes instead of being executed.
// Unicode format characters (category Cf) are escaped too, so bidi overrides
// and zero-width format characters cannot reorder or hide approval text.
// LF and TAB are kept for the existing multiline and tab-stop layout.
func escapeApprovalText(text string) string {
	var out strings.Builder
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		if r == utf8.RuneError && size == 1 {
			// Escape each invalid byte so none is silently lost.
			fmt.Fprintf(&out, `\x%02x`, text[i])
		} else {
			writeEscapedRune(&out, r)
		}
		i += size
	}
	return out.String()
}

func writeEscapedRune(out *strings.Builder, r rune) {
	switch {
	case r == '\n' || r == '\t':
		out.WriteRune(r)
	case r == '\r':
		out.WriteString(`\r`)
	case r == '\b':
		out.WriteString(`\b`)
	case r < 0x80 && unicode.IsControl(r): // other C0 controls and DEL
		fmt.Fprintf(out, `\x%02x`, r)
	case unicode.IsControl(r) || unicode.Is(unicode.Cf, r): // C1 and format characters
		writeUnicodeEscape(out, r)
	default:
		out.WriteRune(r)
	}
}

// writeUnicodeEscape uses Go's \u or \U form, keeping escapes unambiguous
// for characters beyond U+FFFF such as tag characters.
func writeUnicodeEscape(out *strings.Builder, r rune) {
	if r > 0xffff {
		fmt.Fprintf(out, `\U%08x`, r)
		return
	}
	fmt.Fprintf(out, `\u%04x`, r)
}
