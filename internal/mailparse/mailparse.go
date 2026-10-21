// Package mailparse turns raw RFC 5322 messages into the fields the EAS
// server stores and exposes.
package mailparse

import (
	"bytes"
	"io"
	"mime"
	"net/mail"
	"strings"
	"time"

	_ "github.com/emersion/go-message/charset"
	gm "github.com/emersion/go-message/mail"
)

const maxPartSize = 30 << 20

// Part is an attachment or inline MIME part.
type Part struct {
	FileName    string
	ContentType string
	ContentID   string
	Inline      bool
	Data        []byte
}

// Message is the parsed representation of an email.
type Message struct {
	Subject   string
	FromName  string
	FromAddr  string
	To        string
	Cc        string
	Bcc       string
	Date      time.Time
	MessageID string
	// SenderAddr is the Sender header, set when the message was sent on
	// behalf of the From address.
	SenderAddr string
	SenderName string
	// References is the message-id chain of the thread, oldest first.
	References []string
	InReplyTo  string
	Text       string
	HTML       string
	Parts      []Part
}

// Parse decodes a raw MIME message. It returns whatever could be parsed even
// when the document is malformed.
func Parse(raw []byte) (*Message, error) {
	reader, err := gm.CreateReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	out := &Message{}
	h := reader.Header
	out.Subject, _ = h.Subject()
	if t, err := h.Date(); err == nil {
		out.Date = t
	}
	out.MessageID = strings.TrimSpace(h.Get("Message-Id"))
	if list, err := h.AddressList("From"); err == nil && len(list) > 0 {
		out.FromName = list[0].Name
		out.FromAddr = list[0].Address
	}
	// Sender names the mailbox that actually sent the message when it differs
	// from From (a delegate sending on someone's behalf).
	if list, err := h.AddressList("Sender"); err == nil && len(list) > 0 {
		out.SenderName = list[0].Name
		out.SenderAddr = list[0].Address
	}
	out.References = messageIDs(h.Get("References"))
	out.InReplyTo = firstMessageID(h.Get("In-Reply-To"))
	out.To = formatAddressList(h, "To")
	out.Cc = formatAddressList(h, "Cc")
	out.Bcc = formatAddressList(h, "Bcc")

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return out, err
		}
		switch ph := part.Header.(type) {
		case *gm.InlineHeader:
			ct, _, _ := ph.ContentType()
			data, _ := io.ReadAll(io.LimitReader(part.Body, maxPartSize))
			switch {
			case strings.HasPrefix(ct, "text/plain") && out.Text == "":
				out.Text = string(data)
			case strings.HasPrefix(ct, "text/html") && out.HTML == "":
				out.HTML = string(data)
			case strings.HasPrefix(ct, "text/calendar"):
				// Calendar payloads arrive inline but behave like
				// attachments: invitation and RSVP handling needs the raw
				// iCalendar, which text body fields would lose.
				out.Parts = append(out.Parts, Part{
					ContentType: ct,
					Inline:      true,
					Data:        data,
				})
			case !strings.HasPrefix(ct, "text/"):
				// A body part that is not text is an embedded resource, such as
				// an image referenced from the HTML as cid:. Dropping it left
				// the client with nothing to render.
				out.Parts = append(out.Parts, Part{
					FileName:    inlineFileName(ph.Get("Content-Disposition")),
					ContentType: ct,
					ContentID:   strings.Trim(ph.Get("Content-Id"), "<> "),
					Inline:      true,
					Data:        data,
				})
			}
		case *gm.AttachmentHeader:
			name, _ := ph.Filename()
			ct, _, _ := ph.ContentType()
			disp, _, _ := ph.ContentDisposition()
			data, _ := io.ReadAll(io.LimitReader(part.Body, maxPartSize))
			out.Parts = append(out.Parts, Part{
				FileName:    name,
				ContentType: ct,
				ContentID:   strings.Trim(part.Header.Get("Content-Id"), "<> "),
				Inline:      strings.EqualFold(disp, "inline"),
				Data:        data,
			})
		}
	}
	return out, nil
}

// inlineFileName pulls the filename parameter out of a Content-Disposition
// header on a part that go-message classified as inline.
func inlineFileName(disposition string) string {
	if disposition == "" {
		return ""
	}
	if _, params, err := mime.ParseMediaType(disposition); err == nil {
		return params["filename"]
	}
	return ""
}

// Addresses splits a header value into bare email addresses.
func Addresses(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	list, err := mail.ParseAddressList(value)
	if err != nil {
		// Fall back to comma splitting for slightly malformed headers.
		var out []string
		for _, candidate := range strings.Split(value, ",") {
			candidate = strings.TrimSpace(candidate)
			if i := strings.LastIndex(candidate, "<"); i >= 0 {
				if j := strings.Index(candidate[i:], ">"); j > 0 {
					candidate = candidate[i+1 : i+j]
				}
			}
			if candidate != "" {
				out = append(out, strings.ToLower(candidate))
			}
		}
		return out
	}
	out := make([]string, 0, len(list))
	for _, a := range list {
		out = append(out, strings.ToLower(a.Address))
	}
	return out
}

func formatAddressList(h gm.Header, key string) string {
	list, err := h.AddressList(key)
	if err != nil {
		return h.Get(key)
	}
	parts := make([]string, 0, len(list))
	for _, a := range list {
		parts = append(parts, a.String())
	}
	return strings.Join(parts, ", ")
}
