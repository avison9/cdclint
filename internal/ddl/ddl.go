// Package ddl has the small text tools every DDL reader needs: find the
// parenthesised body of a statement, split it on top-level commas, and
// strip identifier quoting. Each dialect's reader does the rest.
package ddl

import "strings"

// Body returns the text inside the first top-level parenthesis pair of s,
// and the text after it. ok is false when there is no balanced pair.
func Body(s string) (inner, rest string, ok bool) {
	open := -1
	depth := 0
	inQuote := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inQuote != 0 {
			if c == inQuote {
				inQuote = 0
			}
			continue
		}
		switch c {
		case '\'', '"', '`':
			inQuote = c
		case '(':
			if depth == 0 {
				open = i
			}
			depth++
		case ')':
			depth--
			if depth == 0 && open >= 0 {
				return s[open+1 : i], s[i+1:], true
			}
		}
	}
	return "", "", false
}

// SplitTop splits s on commas that are not inside parentheses or quotes,
// trimming each piece and dropping empties.
func SplitTop(s string) []string {
	var out []string
	depth := 0
	inQuote := byte(0)
	last := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inQuote != 0 {
			if c == inQuote {
				inQuote = 0
			}
			continue
		}
		switch c {
		case '\'', '"', '`':
			inQuote = c
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				if p := strings.TrimSpace(s[last:i]); p != "" {
					out = append(out, p)
				}
				last = i + 1
			}
		}
	}
	if p := strings.TrimSpace(s[last:]); p != "" {
		out = append(out, p)
	}
	return out
}

// Unquote strips one layer of "double", `back` or [bracket] quoting from an
// identifier and collapses doubled quotes.
func Unquote(id string) string {
	id = strings.TrimSpace(id)
	if len(id) >= 2 {
		switch {
		case id[0] == '"' && id[len(id)-1] == '"':
			return strings.ReplaceAll(id[1:len(id)-1], `""`, `"`)
		case id[0] == '`' && id[len(id)-1] == '`':
			return strings.ReplaceAll(id[1:len(id)-1], "``", "`")
		case id[0] == '[' && id[len(id)-1] == ']':
			return id[1 : len(id)-1]
		}
	}
	return id
}

// Quoted reports whether the identifier carried quotes, which in Snowflake
// and Postgres decides whether its case is significant.
func Quoted(id string) bool {
	id = strings.TrimSpace(id)
	return len(id) >= 2 && ((id[0] == '"' && id[len(id)-1] == '"') || (id[0] == '`' && id[len(id)-1] == '`'))
}

// Words splits s on whitespace, keeping quoted identifiers and
// parenthesised groups together as single words.
func Words(s string) []string {
	var out []string
	var cur strings.Builder
	depth := 0
	inQuote := byte(0)
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inQuote != 0 {
			cur.WriteByte(c)
			if c == inQuote {
				inQuote = 0
			}
			continue
		}
		switch c {
		case '\'', '"', '`':
			inQuote = c
			cur.WriteByte(c)
		case '(':
			depth++
			cur.WriteByte(c)
		case ')':
			depth--
			cur.WriteByte(c)
		case ' ', '\t', '\n', '\r':
			if depth > 0 {
				cur.WriteByte(' ')
			} else {
				flush()
			}
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return out
}

// HasPrefixFold reports whether words begins with the given keywords,
// case-insensitively.
func HasPrefixFold(words []string, kw ...string) bool {
	if len(words) < len(kw) {
		return false
	}
	for i, k := range kw {
		if !strings.EqualFold(words[i], k) {
			return false
		}
	}
	return true
}

// SplitName splits a possibly qualified identifier on dots outside quotes
// and unquotes each part.
func SplitName(name string) []string {
	var parts []string
	var cur strings.Builder
	inQuote := byte(0)
	for i := 0; i < len(name); i++ {
		c := name[i]
		if inQuote != 0 {
			cur.WriteByte(c)
			if c == inQuote {
				inQuote = 0
			}
			continue
		}
		switch c {
		case '"', '`':
			inQuote = c
			cur.WriteByte(c)
		case '.':
			parts = append(parts, Unquote(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	parts = append(parts, Unquote(cur.String()))
	return parts
}

// ItemLine returns the 1-based line, counted from the start of text, on
// which item begins. cursor is advanced past the item so repeated items
// resolve in order.
func ItemLine(text, item string, cursor *int) int {
	at := strings.Index(text[*cursor:], item)
	if at < 0 {
		return 1
	}
	at += *cursor
	*cursor = at + len(item)
	return 1 + strings.Count(text[:at], "\n")
}

// NameLine returns the 1-based line, counted from the start of text, of the
// first occurrence of the identifier name at or after *cursor, quoted or not,
// as a whole word; cursor is advanced past it so the next search starts
// there. It returns 0, leaving cursor alone, when name is not found. The
// readers use it to place each column an ALTER TABLE adds on its own line,
// since the actions they parse have had their whitespace folded.
func NameLine(text, name string, cursor *int) int {
	lower := strings.ToLower(text)
	target := strings.ToLower(name)
	for from := *cursor; from < len(lower); {
		at := strings.Index(lower[from:], target)
		if at < 0 {
			return 0
		}
		at += from
		end := at + len(target)
		before := at == 0 || !identChar(lower[at-1])
		after := end >= len(lower) || !identChar(lower[end])
		if before && after {
			*cursor = end
			return 1 + strings.Count(text[:at], "\n")
		}
		from = at + 1
	}
	return 0
}

func identChar(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
}
