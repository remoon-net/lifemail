package eas

import (
	"encoding/base64"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"remoon.net/lifemail/internal/store"
	"remoon.net/lifemail/internal/wbxml"
)

// MeetingResponse lets the user accept, tentatively accept or decline a
// meeting request. Status values follow MS-ASCMD: 1 created, 7 tentative,
// 9 declined on the event side.
func (s *Server) cmdMeetingResponse(user *store.User, req *wbxml.Node) (*wbxml.Node, error) {
	resp := wbxml.N(pageMeetingResponse, "MeetingResponse")
	for _, item := range req.ChildrenNamed("Request") {
		folderID := parseInt(item.ChildText("CollectionId"))
		if folderID == 0 {
			folderID = parseInt(item.ChildText("FolderId"))
		}
		requestID := parseInt(item.ChildText("RequestId"))
		userResponse := atoiDefault(item.ChildText("UserResponse"), -1)
		// A recurring meeting can be answered per occurrence; InstanceId names
		// the one the user reacted to. Without this the whole series would be
		// marked as answered.
		instanceID := strings.TrimSpace(item.ChildText("InstanceId"))

		status := "1"
		event, err := s.Store.EventByID(requestID)
		switch {
		case err != nil || event.UserID != user.ID || event.FolderID != folderID:
			status = "2"
		default:
			meetingStatus := 0
			switch userResponse {
			case 1:
				// MS-ASCAL: 9 = tentatively accepted, 7 = declined, 3 = received.
				meetingStatus = 9
			case 2:
				meetingStatus = 3
			case 3:
				meetingStatus = 7
			default:
				status = "2"
			}
			if status == "1" {
				if instanceID != "" {
					occurrence := parseEasTime(instanceID)
					if occurrence.IsZero() {
						status = "2"
					} else if err := s.Store.SetExceptionMeetingStatus(event.ID, occurrence.Unix(), meetingStatus); err != nil {
						status = "3"
					} else if _, err := s.Store.AddChange(folderID, "change", "event", requestID); err != nil {
						status = "3"
					} else {
						s.sendMeetingReply(user, event, userResponse)
					}
					break
				}
				event.MeetingStatus = meetingStatus
				if err := s.Store.UpdateEvent(event); err != nil {
					status = "3"
				} else if _, err := s.Store.AddChange(folderID, "change", "event", requestID); err != nil {
					status = "3"
				} else {
					s.sendMeetingReply(user, event, userResponse)
				}
			}
		}

		result := wbxml.N(pageMeetingResponse, "Result",
			wbxml.T(pageMeetingResponse, "Status", status),
			wbxml.T(pageMeetingResponse, "RequestId", i64toa(requestID)),
			wbxml.T(pageMeetingResponse, "FolderId", i64toa(folderID)),
		)
		resp.Children = append(resp.Children, result)
	}
	if len(resp.Children) == 0 {
		resp.Children = append(resp.Children, wbxml.T(pageMeetingResponse, "Status", "2"))
	}
	return resp, nil
}

// sendMeetingReply tells the organiser what the attendee decided. Devices only
// send MeetingResponse and never compose the answer themselves, so without this
// the organiser never learns the outcome.
func (s *Server) sendMeetingReply(user *store.User, event *store.Event, userResponse int) {
	organiser := strings.TrimSpace(event.OrganizerEmail)
	if organiser == "" || strings.EqualFold(organiser, user.Address) {
		// Nothing to tell, or the user invited themselves.
		return
	}
	var verb string
	switch userResponse {
	case 1:
		verb = "Tentative"
	case 2:
		verb = "Accepted"
	case 3:
		verb = "Declined"
	default:
		return
	}

	name := user.DisplayName
	if name == "" {
		name = user.Name
	}
	from := (&mail.Address{Name: name, Address: user.Address}).String()
	when := time.Unix(event.StartTime, 0).Format("2006-01-02 15:04") +
		" - " + time.Unix(event.EndTime, 0).Format("15:04")
	body := fmt.Sprintf("%s has %s this meeting.\r\n\r\nSubject: %s\r\nWhen: %s\r\nWhere: %s\r\n",
		name, strings.ToLower(verb), event.Subject, when, event.Location)

	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + organiser + "\r\n")
	b.WriteString("Subject: " + mimeSubject(verb+": "+event.Subject) + "\r\n")
	b.WriteString("Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	// Keep other auto-responders from answering this answer.
	b.WriteString("Auto-Submitted: auto-replied\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)

	// Reuse the normal outbound path so local and external recipients behave
	// exactly like a message the device composed itself.
	node := wbxml.N(pageComposeMail, "SendMail",
		wbxml.T(pageComposeMail, "Mime", base64.StdEncoding.EncodeToString([]byte(b.String()))))
	if status := s.sendComposed(user, node, false); status != "1" {
		s.Log.Warn("meeting reply not delivered", "status", status,
			"user", user.Name, "organiser", organiser)
	}
}

// ResolveRecipients resolves typed addresses against local accounts so that
// clients can show a resolved attendee list before sending an invitation.
func (s *Server) cmdResolveRecipients(user *store.User, req *wbxml.Node) (*wbxml.Node, error) {
	resp := wbxml.N(pageResolveRecipients, "ResolveRecipients",
		wbxml.T(pageResolveRecipients, "Status", "1"))

	seen := make(map[string]bool)
	// Availability is optional; clients ask for it when composing an invitation
	// so the attendee picker can show who is busy.
	var (
		wantBusy  bool
		busyStart int64
		busyEnd   int64
	)
	if options := req.Child("Options"); options != nil {
		if av := options.Child("Availability"); av != nil {
			wantBusy = true
			if t := parseEasTime(av.ChildText("StartTime")); !t.IsZero() {
				busyStart = t.Unix()
			}
			if t := parseEasTime(av.ChildText("EndTime")); !t.IsZero() {
				busyEnd = t.Unix()
			}
		}
	}

	for _, to := range req.ChildrenNamed("To") {
		addr := strings.TrimSpace(to.Text)
		key := strings.ToLower(addr)
		if addr == "" || seen[key] {
			continue
		}
		seen[key] = true

		var matched []*store.User
		if owner, err := s.Store.UserByAddress(addr); err == nil && owner != nil {
			matched = append(matched, owner)
		}

		response := wbxml.N(pageResolveRecipients, "Response",
			wbxml.T(pageResolveRecipients, "To", addr),
			wbxml.T(pageResolveRecipients, "Status", "1"),
			wbxml.T(pageResolveRecipients, "RecipientCount", itoa(len(matched))),
		)
		for _, recipient := range matched {
			node := wbxml.N(pageResolveRecipients, "Recipient",
				wbxml.T(pageResolveRecipients, "Type", "1"), // 1 = GAL entry
				wbxml.T(pageResolveRecipients, "DisplayName", displayNameOf(recipient)),
				wbxml.T(pageResolveRecipients, "EmailAddress", recipient.Address),
			)
			if wantBusy {
				node.Children = append(node.Children,
					wbxml.N(pageResolveRecipients, "Availability",
						wbxml.T(pageResolveRecipients, "Status", "1"),
						wbxml.T(pageResolveRecipients, "MergedFreeBusy",
							s.mergedFreeBusy(recipient, busyStart, busyEnd))))
			}
			response.Children = append(response.Children, node)
		}
		resp.Children = append(resp.Children, response)
	}
	return resp, nil
}

// mergedFreeBusy renders one character per 30 minute slot, as MS-ASCMD asks:
// 0 free, 1 tentative, 2 busy, 3 out of office.
//
// Recurring events are not expanded: only the stored instance contributes, so a
// weekly meeting shows up once. Expanding rules here would need a full RRULE
// evaluator and is not worth the cost for a UI hint.
func (s *Server) mergedFreeBusy(user *store.User, from, to int64) string {
	const slot = int64(1800)
	if user == nil || to <= from {
		return ""
	}
	slots := int((to - from + slot - 1) / slot)
	if slots <= 0 {
		return ""
	}
	if slots > 2000 {
		slots = 2000
	}
	buf := make([]byte, slots)
	for i := range buf {
		buf[i] = '0'
	}

	folder, err := s.Store.FolderByType(user.ID, 8) // Calendar
	if err != nil {
		return string(buf)
	}
	events, err := s.Store.EventsByFolder(folder.ID)
	if err != nil {
		return string(buf)
	}
	for _, e := range events {
		if e.EndTime <= from || e.StartTime >= to {
			continue
		}
		code := byte('2') // busy
		switch e.BusyStatus {
		case 0:
			code = '0' // free
		case 1:
			code = '1' // tentative
		case 3:
			code = '3' // out of office
		}
		startIdx := int((e.StartTime - from) / slot)
		// An event ending exactly on a slot boundary must not mark the next
		// slot, so shift the end back by one second before dividing.
		endIdx := int((e.EndTime - 1 - from) / slot)
		if startIdx < 0 {
			startIdx = 0
		}
		if endIdx >= slots {
			endIdx = slots - 1
		}
		for i := startIdx; i <= endIdx; i++ {
			if code > buf[i] {
				buf[i] = code
			}
		}
	}
	return string(buf)
}

// cmdSearch implements server-side search for the Mailbox and GAL stores.
func (s *Server) cmdSearch(user *store.User, req *wbxml.Node) (*wbxml.Node, error) {
	resp := wbxml.N(pageSearch, "Search",
		wbxml.T(pageSearch, "Status", "1"))

	for _, st := range req.ChildrenNamed("Store") {
		name := strings.TrimSpace(st.ChildText("Name"))
		term, start, limit := searchParams(st)
		storeResp := wbxml.N(pageSearch, "Store")

		switch {
		case strings.EqualFold(name, "GAL"):
			users, err := s.Store.SearchUsers(term, limit)
			if err != nil {
				storeResp.Children = append(storeResp.Children, wbxml.T(pageSearch, "Status", "3"))
				break
			}
			storeResp.Children = append(storeResp.Children, wbxml.T(pageSearch, "Status", "1"))
			for _, u := range users {
				storeResp.Children = append(storeResp.Children,
					wbxml.N(pageSearch, "Result",
						wbxml.N(pageSearch, "Properties",
							wbxml.T(pageGAL, "DisplayName", displayNameOf(u)),
							wbxml.T(pageGAL, "EmailAddress", u.Address),
						),
					),
				)
			}
			if len(users) > 0 {
				storeResp.Children = append(storeResp.Children,
					wbxml.T(pageSearch, "Range", fmt.Sprintf("0-%d", len(users)-1)))
			}
			storeResp.Children = append(storeResp.Children,
				wbxml.T(pageSearch, "Total", itoa(len(users))))
		default:
			messages, total, err := s.Store.SearchMessages(user.ID, term, limit, start)
			if err != nil {
				storeResp.Children = append(storeResp.Children, wbxml.T(pageSearch, "Status", "3"))
				break
			}
			storeResp.Children = append(storeResp.Children, wbxml.T(pageSearch, "Status", "1"))
			for _, m := range messages {
				storeResp.Children = append(storeResp.Children,
					wbxml.N(pageSearch, "Result",
						wbxml.N(pageSearch, "Properties",
							wbxml.T(pageAirSync, "ServerId", i64toa(m.ID)),
							wbxml.T(pageAirSync, "Class", "Email"),
							wbxml.T(pageEmail, "Subject", m.Subject),
							wbxml.T(pageEmail, "From", formatAddress(m.FromName, m.FromAddr)),
							wbxml.T(pageEmail, "To", m.ToAddr),
							wbxml.T(pageEmail, "DateReceived", formatEasTime(time.Unix(m.DateReceived, 0))),
							wbxml.T(pageEmail, "Read", boolString(m.IsRead)),
						),
					),
				)
			}
			if len(messages) > 0 {
				storeResp.Children = append(storeResp.Children,
					wbxml.T(pageSearch, "Range",
						fmt.Sprintf("%d-%d", start, start+len(messages)-1)))
			}
			storeResp.Children = append(storeResp.Children,
				wbxml.T(pageSearch, "Total", itoa(total)))
		}

		resp.Children = append(resp.Children,
			wbxml.N(pageSearch, "Response", storeResp))
	}
	return resp, nil
}

// searchParams extracts the search term and the requested result window from a
// Search Store element.
func searchParams(st *wbxml.Node) (term string, start, limit int) {
	limit = 50
	if query := st.Child("Query"); query != nil {
		term = strings.TrimSpace(query.Text)
		if term == "" {
			term = strings.Join(searchTerms(query), " ")
		}
	}
	if rangeNode := st.Path("Options", "Range"); rangeNode != nil {
		parts := strings.SplitN(strings.TrimSpace(rangeNode.Text), "-", 2)
		if len(parts) == 2 {
			start = atoiDefault(parts[0], 0)
			end := atoiDefault(parts[1], start+limit-1)
			if end >= start {
				limit = end - start + 1
			}
		}
	}
	if start < 0 {
		start = 0
	}
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	return term, start, limit
}

// searchTerms flattens the search query tree into text conditions.
//
// Only FreeText (and the value of an EqualTo test) counts as a keyword. A
// ranged test such as LessThan/DateReceived/Value carries a date, and treating
// that as another search word made every real query match nothing.
func searchTerms(n *wbxml.Node) []string {
	if n == nil {
		return nil
	}
	var out []string
	switch n.Name {
	case "FreeText":
		if v := strings.TrimSpace(n.Text); v != "" {
			out = append(out, v)
		}
	case "EqualTo":
		if v := n.Path("Value"); v != nil {
			if text := strings.TrimSpace(v.Text); text != "" {
				out = append(out, text)
			}
		}
	}
	for _, c := range n.Children {
		out = append(out, searchTerms(c)...)
	}
	return out
}

func displayNameOf(u *store.User) string {
	if u == nil {
		return ""
	}
	if name := strings.TrimSpace(u.DisplayName); name != "" {
		return name
	}
	if u.Address != "" {
		return u.Address
	}
	return u.Name
}
