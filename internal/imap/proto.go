// Package imap implements a compact IMAP4rev1 server on top of the lifemail
// store, using implicit TLS with per-domain certificates.
package imap

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

func (c *conn) writeLine(format string, args ...any) error {
	if _, err := fmt.Fprintf(c.w, format+"\r\n", args...); err != nil {
		return err
	}
	return c.w.Flush()
}

func (c *conn) ok(tag, text string)  { _ = c.writeLine("%s OK %s", tag, text) }
func (c *conn) no(tag, text string)  { _ = c.writeLine("%s NO %s", tag, text) }
func (c *conn) bad(tag, text string) { _ = c.writeLine("%s BAD %s", tag, text) }

// readCommand reads one command line. A trailing IMAP literal ({n}) is
// consumed and returned separately.
func (c *conn) readCommand() (string, []byte, error) {
	raw, err := c.r.ReadString('\n')
	if err != nil {
		return "", nil, err
	}
	raw = strings.TrimRight(raw, "\r\n")
	if !strings.HasSuffix(raw, "}") {
		return raw, nil, nil
	}
	open := strings.LastIndexByte(raw, '{')
	if open < 0 {
		return raw, nil, nil
	}
	spec := raw[open+1 : len(raw)-1]
	nonSync := strings.HasSuffix(spec, "+")
	size, err := strconv.Atoi(strings.TrimSuffix(spec, "+"))
	if err != nil || size < 0 || size > 64<<20 {
		return "", nil, fmt.Errorf("invalid literal size")
	}
	if !nonSync {
		if err := c.writeLine("+ Ready for literal data"); err != nil {
			return "", nil, err
		}
	}
	lit := make([]byte, size)
	if _, err := io.ReadFull(c.r, lit); err != nil {
		return "", nil, err
	}
	rest, err := c.r.ReadString('\n')
	if err != nil {
		return "", nil, err
	}
	return strings.TrimSpace(raw[:open] + " " + rest), lit, nil
}

// splitTokens splits an IMAP argument list on spaces, keeping quoted strings
// and bracketed/parenthesised groups intact.
func splitTokens(s string) []string {
	var out []string
	var cur strings.Builder
	depth := 0
	inQuote := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inQuote {
			cur.WriteByte(ch)
			if ch == '\\' && i+1 < len(s) {
				i++
				cur.WriteByte(s[i])
				continue
			}
			if ch == '"' {
				inQuote = false
			}
			continue
		}
		switch ch {
		case '"':
			inQuote = true
			cur.WriteByte(ch)
		case '[', '(':
			depth++
			cur.WriteByte(ch)
		case ']', ')':
			if depth > 0 {
				depth--
			}
			cur.WriteByte(ch)
		case ' ', '\t':
			if depth == 0 {
				if cur.Len() > 0 {
					out = append(out, cur.String())
					cur.Reset()
				}
				continue
			}
			cur.WriteByte(ch)
		default:
			cur.WriteByte(ch)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func unquote(s string) string {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return s
	}
	inner := s[1 : len(s)-1]
	var b strings.Builder
	for i := 0; i < len(inner); i++ {
		if inner[i] == '\\' && i+1 < len(inner) {
			i++
			b.WriteByte(inner[i])
			continue
		}
		b.WriteByte(inner[i])
	}
	return b.String()
}

func quote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

type seqRange struct {
	from int64
	to   int64 // -1 means '*'
}

type seqSet []seqRange

func parseSeqSet(s string) (seqSet, error) {
	var out seqSet
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		fromStr, toStr, hasRange := strings.Cut(part, ":")
		from, err := parseSeqNum(fromStr)
		if err != nil {
			return nil, err
		}
		to := from
		if hasRange {
			to, err = parseSeqNum(toStr)
			if err != nil {
				return nil, err
			}
		}
		out = append(out, seqRange{from: from, to: to})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty sequence set")
	}
	return out, nil
}

func parseSeqNum(s string) (int64, error) {
	if s == "*" {
		return -1, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid sequence number %q", s)
	}
	return n, nil
}

func (set seqSet) contains(value, max int64) bool {
	for _, r := range set {
		lo, hi := r.from, r.to
		if lo < 0 {
			lo = max
		}
		if hi < 0 {
			hi = max
		}
		if lo > hi {
			lo, hi = hi, lo
		}
		if value >= lo && value <= hi {
			return true
		}
	}
	return false
}

func splitVerb(rest string) (verb, args string) {
	i := strings.IndexByte(rest, ' ')
	if i < 0 {
		return rest, ""
	}
	return rest[:i], strings.TrimLeft(rest[i+1:], " ")
}
