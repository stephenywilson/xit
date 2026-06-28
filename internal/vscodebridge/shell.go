package vscodebridge

import "strings"

func ShellFields(command string) []string {
	var fields []string
	var b strings.Builder
	inSingle := false
	inDouble := false
	escaped := false
	hadQuote := false

	flush := func() {
		if b.Len() > 0 || hadQuote {
			fields = append(fields, b.String())
		}
		b.Reset()
		hadQuote = false
	}

	for _, r := range command {
		switch {
		case escaped:
			b.WriteRune(r)
			escaped = false
		case r == '\\' && !inSingle:
			escaped = true
		case r == '\'' && !inDouble:
			inSingle = !inSingle
			hadQuote = true
		case r == '"' && !inSingle:
			inDouble = !inDouble
			hadQuote = true
		case (r == ' ' || r == '\t' || r == '\n' || r == '\r') && !inSingle && !inDouble:
			flush()
		default:
			b.WriteRune(r)
		}
	}
	if escaped {
		b.WriteRune('\\')
	}
	flush()
	return fields
}
