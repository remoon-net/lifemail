package imap

import (
	"bytes"
	"fmt"
	"mime"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"remoon.net/lifemail/internal/store"
)

type fetchItem struct {
	name    string // canonical name: FLAGS, UID, BODY[], BODYSTRUCTURE...
	key     string // label written in the response
	section string // contents of BODY[...]
	start   int
	length  int // -1 when the fetch is not partial
}

func parseFetchItems(value string) ([]fetchItem, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "(") && strings.HasSuffix(value, ")") {
		value = value[1 : len(value)-1]
	}
	tokens := splitTokens(value)
	var out []fetchItem
	for _, token := range tokens {
		switch strings.ToUpper(token) {
		case "ALL":
			out = append(out,
				fetchItem{name: "FLAGS", key: "FLAGS", length: -1},
				fetchItem{name: "INTERNALDATE", key: "INTERNALDATE", length: -1},
				fetchItem{name: "RFC822.SIZE", key: "RFC822.SIZE", length: -1},
				fetchItem{name: "ENVELOPE", key: "ENVELOPE", length: -1})
			continue
		case "FAST":
			out = append(out,
				fetchItem{name: "FLAGS", key: "FLAGS", length: -1},
				fetchItem{name: "INTERNALDATE", key: "INTERNALDATE", length: -1},
				fetchItem{name: "RFC822.SIZE", key: "RFC822.SIZE", length: -1})
			continue
		case "FULL":
			out = append(out,
				fetchItem{name: "FLAGS", key: "FLAGS", length: -1},
				fetchItem{name: "INTERNALDATE", key: "INTERNALDATE", length: -1},
				fetchItem{name: "RFC822.SIZE", key: "RFC822.SIZE", length: -1},
				fetchItem{name: "ENVELOPE", key: "ENVELOPE", length: -1},
				fetchItem{name: "BODY", key: "BODY", length: -1})
			continue
		}
		item, err := parseFetchItem(token)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty fetch item list")
	}
	return out, nil
}

func parseFetchItem(token string) (fetchItem, error) {
	upper := strings.ToUpper(token)
	switch upper {
	case "FLAGS", "UID", "INTERNALDATE", "ENVELOPE", "RFC822.SIZE", "BODYSTRUCTURE":
		return fetchItem{name: upper, key: upper, length: -1}, nil
	case "RFC822":
		return fetchItem{name: "BODY[]", key: "RFC822", length: -1}, nil
	case "RFC822.HEADER":
		return fetchItem{name: "BODY[HEADER]", key: "RFC822.HEADER", section: "HEADER", length: -1}, nil
	case "RFC822.TEXT":
		return fetchItem{name: "BODY[TEXT]", key: "RFC822.TEXT", section: "TEXT", length: -1}, nil
	case "BODY":
		return fetchItem{name: "BODYSTRUCTURE", key: "BODY", length: -1}, nil
	}

	var start int
	switch {
	case strings.HasPrefix(upper, "BODY.PEEK["):
		start = len("BODY.PEEK[")
	case strings.HasPrefix(upper, "BODY["):
		start = len("BODY[")
	default:
		return fetchItem{}, fmt.Errorf("unsupported fetch item %q", token)
	}
	end := strings.IndexByte(token[start:], ']')
	if end < 0 {
		return fetchItem{}, fmt.Errorf("invalid BODY section")
	}
	end += start
	section := token[start:end]
	rest := token[end+1:]
	key := "BODY[" + strings.ToUpper(section) + "]"
	item := fetchItem{name: key, key: key, section: section, length: -1}
	if i := strings.IndexByte(rest, '<'); i >= 0 {
		partial := strings.TrimSuffix(rest[i+1:], ">")
		parts := strings.SplitN(partial, ".", 2)
		item.start, _ = strconv.Atoi(parts[0])
		if len(parts) == 2 {
			item.length, _ = strconv.Atoi(parts[1])
		}
	}
	return item, nil
}

func (c *conn) fetch(tag, rest string, uidMode bool) {
	if !c.requireSelected(tag) {
		return
	}
	tokens := splitTokens(rest)
	if len(tokens) < 2 {
		c.bad(tag, "FETCH expects a set and items")
		return
	}
	set, err := parseSeqSet(tokens[0])
	if err != nil {
		c.bad(tag, "invalid sequence set")
		return
	}
	items, err := parseFetchItems(strings.Join(tokens[1:], " "))
	if err != nil {
		c.bad(tag, err.Error())
		return
	}
	msgs, err := c.srv.Store.MessagesForIMAP(c.selected.ID)
	if err != nil {
		c.no(tag, "cannot read mailbox")
		return
	}
	lastUID := int64(0)
	if len(msgs) > 0 {
		lastUID = msgs[len(msgs)-1].ID
	}
	for i, m := range msgs {
		seq := int64(i + 1)
		var match bool
		if uidMode {
			match = set.contains(m.ID, lastUID)
		} else {
			match = set.contains(seq, int64(len(msgs)))
		}
		if !match {
			continue
		}
		// The MIME source may live on disk; load it only for the messages this
		// FETCH actually touches.
		if _, err := c.srv.Store.LoadRaw(m); err != nil {
			c.no(tag, "cannot read message")
			return
		}
		if _, err := c.w.Write(buildFetchResponse(seq, m, items, uidMode)); err != nil {
			return
		}
	}
	if err := c.w.Flush(); err != nil {
		return
	}
	c.ok(tag, "FETCH completed")
}

func buildFetchResponse(seq int64, m *store.Message, items []fetchItem, uidMode bool) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "* %d FETCH (", seq)
	first := true
	writeText := func(text string) {
		if !first {
			buf.WriteByte(' ')
		}
		first = false
		buf.WriteString(text)
	}
	writeLiteral := func(key string, data []byte) {
		if !first {
			buf.WriteByte(' ')
		}
		first = false
		fmt.Fprintf(&buf, "%s {%d}\r\n", key, len(data))
		buf.Write(data)
	}
	uidWritten := false
	for _, item := range items {
		switch item.name {
		case "FLAGS":
			writeText("FLAGS " + flagsString(m))
		case "UID":
			writeText("UID " + i64toa(m.ID))
			uidWritten = true
		case "RFC822.SIZE":
			writeText("RFC822.SIZE " + i64toa(int64(len(normalizeCRLF(m.Raw)))))
		case "INTERNALDATE":
			writeText("INTERNALDATE " + quote(internalDate(m)))
		case "ENVELOPE":
			writeText("ENVELOPE " + envelope(m.Raw))
		case "BODYSTRUCTURE":
			writeText("BODYSTRUCTURE " + bodyStructure(m.Raw))
		case "BODY":
			writeText("BODY " + bodyStructure(m.Raw))
		default:
			data := sectionData(m.Raw, item.section)
			if item.start > 0 || item.length >= 0 {
				if item.start > len(data) {
					data = nil
				} else {
					data = data[item.start:]
				}
				if item.length >= 0 && len(data) > item.length {
					data = data[:item.length]
				}
				key := item.key
				if item.length >= 0 {
					key = fmt.Sprintf("%s<%d>", item.key, item.start)
				}
				writeLiteral(key, data)
			} else {
				writeLiteral(item.key, data)
			}
		}
	}
	if uidMode && !uidWritten {
		writeText("UID " + i64toa(m.ID))
	}
	buf.WriteString(")\r\n")
	return buf.Bytes()
}

func normalizeCRLF(raw []byte) []byte {
	s := strings.ReplaceAll(string(raw), "\r\n", "\n")
	s = strings.ReplaceAll(s, "\n", "\r\n")
	return []byte(s)
}

func sectionData(raw []byte, section string) []byte {
	body := normalizeCRLF(raw)
	upper := strings.ToUpper(strings.TrimSpace(section))
	switch {
	case upper == "":
		return body
	case upper == "HEADER":
		return headerPart(body)
	case upper == "TEXT":
		return textPart(body)
	case strings.HasPrefix(upper, "HEADER.FIELDS.NOT"):
		return filterHeaderFields(headerPart(body), parseHeaderFields(section), true)
	case strings.HasPrefix(upper, "HEADER.FIELDS"):
		return filterHeaderFields(headerPart(body), parseHeaderFields(section), false)
	default:
		return nil
	}
}

func headerPart(body []byte) []byte {
	if i := bytes.Index(body, []byte("\r\n\r\n")); i >= 0 {
		return body[:i+4]
	}
	return body
}

func textPart(body []byte) []byte {
	if i := bytes.Index(body, []byte("\r\n\r\n")); i >= 0 {
		return body[i+4:]
	}
	return nil
}

func parseHeaderFields(section string) []string {
	open := strings.IndexByte(section, '(')
	closeIdx := strings.LastIndexByte(section, ')')
	if open < 0 || closeIdx <= open {
		return nil
	}
	fields := strings.Fields(section[open+1 : closeIdx])
	for i := range fields {
		fields[i] = strings.ToUpper(strings.TrimSpace(fields[i]))
	}
	return fields
}

func filterHeaderFields(header []byte, fields []string, invert bool) []byte {
	want := make(map[string]bool, len(fields))
	for _, f := range fields {
		want[f] = true
	}
	var out bytes.Buffer
	lines := bytes.Split(header, []byte("\r\n"))
	for _, line := range lines {
		if len(line) == 0 {
			break
		}
		name := line
		if i := bytes.IndexByte(line, ':'); i >= 0 {
			name = line[:i]
		}
		match := want[strings.ToUpper(string(name))]
		if match != invert {
			out.Write(line)
			out.WriteString("\r\n")
		}
	}
	out.WriteString("\r\n")
	return out.Bytes()
}

func internalDate(m *store.Message) string {
	return time.Unix(m.DateReceived, 0).Format("_2-Jan-2006 15:04:05 -0700")
}

func flagsString(m *store.Message) string {
	var flags []string
	if m.IsRead {
		flags = append(flags, `\Seen`)
	}
	if m.IsFlagged {
		flags = append(flags, `\Flagged`)
	}
	if m.Deleted {
		flags = append(flags, `\Deleted`)
	}
	return "(" + strings.Join(flags, " ") + ")"
}

func nstring(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "NIL"
	}
	return quote(value)
}

func envelope(raw []byte) string {
	msg, err := mail.ReadMessage(bytes.NewReader(normalizeCRLF(raw)))
	if err != nil {
		return "NIL"
	}
	header := msg.Header
	subject := header.Get("Subject")
	decoder := &mime.WordDecoder{}
	if decoded, err := decoder.DecodeHeader(subject); err == nil {
		subject = decoded
	}
	from := addressList(header.Get("From"))
	sender := addressList(header.Get("Sender"))
	if sender == "NIL" {
		sender = from
	}
	replyTo := addressList(header.Get("Reply-To"))
	if replyTo == "NIL" {
		replyTo = from
	}
	return fmt.Sprintf("(%s %s %s %s %s %s %s %s %s %s)",
		nstring(header.Get("Date")),
		nstring(subject),
		from,
		sender,
		replyTo,
		addressList(header.Get("To")),
		addressList(header.Get("Cc")),
		addressList(header.Get("Bcc")),
		nstring(header.Get("In-Reply-To")),
		nstring(header.Get("Message-Id")),
	)
}

func addressList(value string) string {
	if strings.TrimSpace(value) == "" {
		return "NIL"
	}
	list, err := mail.ParseAddressList(value)
	if err != nil || len(list) == 0 {
		return "NIL"
	}
	var out strings.Builder
	out.WriteByte('(')
	for _, addr := range list {
		mailbox, host := splitAddress(addr.Address)
		fmt.Fprintf(&out, "(%s NIL %s %s)", nstring(addr.Name), nstring(mailbox), nstring(host))
	}
	out.WriteByte(')')
	return out.String()
}

func splitAddress(addr string) (string, string) {
	if i := strings.LastIndexByte(addr, '@'); i >= 0 {
		return addr[:i], addr[i+1:]
	}
	return addr, ""
}

func bodyStructure(raw []byte) string {
	body := normalizeCRLF(raw)
	contentType := "text/plain"
	if msg, err := mail.ReadMessage(bytes.NewReader(body)); err == nil {
		if ct := msg.Header.Get("Content-Type"); ct != "" {
			contentType = ct
		}
	}
	mainType, subType := "text", "plain"
	if parsed, _, err := mime.ParseMediaType(contentType); err == nil {
		if i := strings.IndexByte(parsed, '/'); i > 0 {
			mainType, subType = parsed[:i], parsed[i+1:]
		}
	}
	lines := bytes.Count(body, []byte("\r\n"))
	return fmt.Sprintf("(%s %s (\"CHARSET\" \"UTF-8\") NIL NIL \"7BIT\" %d %d)",
		quote(strings.ToUpper(mainType)), quote(strings.ToUpper(subType)), len(body), lines)
}

func (c *conn) storeFlags(tag, rest string, uidMode bool) {
	if !c.requireSelected(tag) {
		return
	}
	tokens := splitTokens(rest)
	if len(tokens) < 3 {
		c.bad(tag, "STORE expects set, item and flags")
		return
	}
	set, err := parseSeqSet(tokens[0])
	if err != nil {
		c.bad(tag, "invalid sequence set")
		return
	}
	op := strings.ToUpper(tokens[1])
	silent := strings.HasSuffix(op, ".SILENT")
	op = strings.TrimSuffix(op, ".SILENT")
	flags := parseFlagWords(tokens[2])

	msgs, err := c.srv.Store.MessagesForIMAP(c.selected.ID)
	if err != nil {
		c.no(tag, "cannot read mailbox")
		return
	}
	lastUID := int64(0)
	if len(msgs) > 0 {
		lastUID = msgs[len(msgs)-1].ID
	}
	for i, m := range msgs {
		var match bool
		if uidMode {
			match = set.contains(m.ID, lastUID)
		} else {
			match = set.contains(int64(i+1), int64(len(msgs)))
		}
		if !match {
			continue
		}
		read, flagged, deleted := m.IsRead, m.IsFlagged, m.Deleted
		apply := func(enable bool) {
			for _, flag := range flags {
				switch flag {
				case `\seen`:
					read = enable
				case `\flagged`:
					flagged = enable
				case `\deleted`:
					deleted = enable
				}
			}
		}
		switch op {
		case "FLAGS":
			read, flagged, deleted = false, false, false
			apply(true)
		case "+FLAGS":
			apply(true)
		case "-FLAGS":
			apply(false)
		default:
			c.bad(tag, "unsupported STORE item")
			return
		}
		wasDeleted := m.Deleted
		m.IsRead, m.IsFlagged, m.Deleted = read, flagged, deleted
		if err := c.srv.Store.UpdateMessage(m); err != nil {
			continue
		}
		kind := "change"
		if deleted && !wasDeleted {
			kind = "delete"
		}
		_, _ = c.srv.Store.AddChange(c.selected.ID, kind, "email", m.ID)
		if !silent {
			if err := c.writeLine("* %d FETCH (FLAGS %s)", i+1, flagsString(m)); err != nil {
				return
			}
		}
	}
	c.ok(tag, "STORE completed")
}

func (c *conn) search(tag, rest string, uidMode bool) {
	if !c.requireSelected(tag) {
		return
	}
	msgs, err := c.srv.Store.MessagesForIMAP(c.selected.ID)
	if err != nil {
		c.no(tag, "cannot read mailbox")
		return
	}
	var ids []string
	for i, m := range msgs {
		if !matchesSearch(rest, m) {
			continue
		}
		if uidMode {
			ids = append(ids, i64toa(m.ID))
		} else {
			ids = append(ids, itoa(i+1))
		}
	}
	line := "* SEARCH"
	if len(ids) > 0 {
		line += " " + strings.Join(ids, " ")
	}
	_ = c.writeLine("%s", line)
	c.ok(tag, "SEARCH completed")
}

func matchesSearch(criteria string, m *store.Message) bool {
	clean := strings.NewReplacer("(", " ", ")", " ").Replace(criteria)
	fields := strings.Fields(clean)
	if len(fields) == 0 {
		return true
	}
	for _, field := range fields {
		switch strings.ToUpper(field) {
		case "ALL":
		case "UNSEEN":
			if m.IsRead {
				return false
			}
		case "SEEN":
			if !m.IsRead {
				return false
			}
		case "FLAGGED":
			if !m.IsFlagged {
				return false
			}
		case "UNFLAGGED":
			if m.IsFlagged {
				return false
			}
		case "DELETED":
			if !m.Deleted {
				return false
			}
		case "UNDELETED":
			if m.Deleted {
				return false
			}
		default:
			return false
		}
	}
	return true
}
