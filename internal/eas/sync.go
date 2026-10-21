package eas

import (
	"errors"
	"strings"
	"time"

	"remoon.net/lifemail/internal/store"
	"remoon.net/lifemail/internal/wbxml"
)

// cmdSync implements the AirSync Sync command for all three collection types.
func (s *Server) cmdSync(user *store.User, deviceID string, req *wbxml.Node) (*wbxml.Node, error) {
	resp := wbxml.N(pageAirSync, "Sync")
	collections := req.Child("Collections")
	if collections == nil {
		return resp, nil
	}
	out := wbxml.N(pageAirSync, "Collections")
	for _, col := range collections.ChildrenNamed("Collection") {
		out.Children = append(out.Children, s.syncCollection(user, deviceID, col))
	}
	resp.Children = append(resp.Children, out)
	return resp, nil
}

func (s *Server) syncCollection(user *store.User, deviceID string, req *wbxml.Node) *wbxml.Node {
	out := wbxml.N(pageAirSync, "Collection")
	collectionID := parseInt(req.ChildText("CollectionId"))
	out.Children = append(out.Children,
		wbxml.T(pageAirSync, "CollectionId", i64toa(collectionID)))

	folder, err := s.Store.FolderByID(collectionID)
	if err != nil || folder.UserID != user.ID || folder.Deleted {
		out.Children = append(out.Children, wbxml.T(pageAirSync, "Status", "5"))
		return out
	}

	key := strings.TrimSpace(req.ChildText("SyncKey"))
	window := atoiDefault(req.ChildText("WindowSize"), 100)
	if window <= 0 {
		window = 100
	}
	if window > 500 {
		window = 500
	}
	options := req.Child("Options")
	deletesAsMoves := req.Child("DeletesAsMoves") != nil

	state, err := s.Store.GetSyncState(user.ID, deviceID, folder.ID)
	if err != nil {
		out.Children = append(out.Children, wbxml.T(pageAirSync, "Status", "5"))
		return out
	}
	initial := key == "" || key == "0"
	if initial {
		if err := s.Store.ResetSyncState(user.ID, deviceID, folder.ID); err != nil {
			out.Children = append(out.Children, wbxml.T(pageAirSync, "Status", "5"))
			return out
		}
		state = &store.SyncState{UserID: user.ID, DeviceID: deviceID, FolderID: folder.ID, SyncKey: "1"}
		if err := s.Store.SaveSyncState(state); err != nil {
			out.Children = append(out.Children, wbxml.T(pageAirSync, "Status", "5"))
			return out
		}
	} else if state == nil || state.SyncKey != key {
		out.Children = append(out.Children, wbxml.T(pageAirSync, "Status", "3"))
		return out
	}

	skipped := map[int64]bool{}
	applied := false
	if commands := req.Child("Commands"); commands != nil {
		responses, skip := s.applyClientCommands(user, folder, commands, deletesAsMoves)
		for seq := range skip {
			skipped[seq] = true
		}
		applied = len(responses) > 0 || len(skip) > 0
		if len(responses) > 0 {
			out.Children = append(out.Children, wbxml.N(pageAirSync, "Responses", responses...))
		}
	}

	pref := bodyPrefFrom(options)
	// The filter window belongs to the initial sync only. Applying it again to
	// a folder that simply has no changes yet (Seq 0) would drop messages that
	// arrived moments ago, and the advanced SyncKey would lose them for good.
	cutoff := int64(0)
	if initial && folder.Class == "Email" {
		if ft := atoiDefault(options.ChildText("FilterType"), 0); ft > 0 {
			if window := filterWindow(ft); window > 0 {
				cutoff = time.Now().Add(-window).Unix()
			}
		}
	}

	commands, more, newSeq, err := s.serverChanges(user, deviceID, folder, state.Seq, window, pref, cutoff, skipped)
	if err != nil {
		out.Children = append(out.Children, wbxml.T(pageAirSync, "Status", "5"))
		return out
	}

	previousSeq := state.Seq
	state.Seq = newSeq
	// The SyncKey identifies the collection state. It must only move forward
	// when the collection actually changed; bumping it on every request makes
	// clients re-sync the folder in a loop and never finish loading it.
	if newSeq != previousSeq || applied || more {
		state.SyncKey = i64toa(parseInt(state.SyncKey) + 1)
	}
	if err := s.Store.SaveSyncState(state); err != nil {
		out.Children = append(out.Children, wbxml.T(pageAirSync, "Status", "5"))
		return out
	}

	out.Children = append(out.Children,
		wbxml.T(pageAirSync, "SyncKey", state.SyncKey),
		wbxml.T(pageAirSync, "Status", "1"))
	// Clients pick the parser for a collection from this Class element. Without
	// it every collection is treated as email, so calendar and contact items
	// are received but never stored.
	if folder.Class != "" {
		out.Children = append(out.Children, wbxml.T(pageAirSync, "Class", folder.Class))
	}
	if len(commands.Children) > 0 {
		out.Children = append(out.Children, commands)
	}
	if more {
		out.Children = append(out.Children, wbxml.N(pageAirSync, "MoreAvailable"))
	}
	return out
}

func filterWindow(filterType int) time.Duration {
	switch filterType {
	case 1: // one day
		return 24 * time.Hour
	case 2: // three days
		return 3 * 24 * time.Hour
	case 3: // one week
		return 7 * 24 * time.Hour
	case 4: // two weeks
		return 14 * 24 * time.Hour
	case 5: // one month
		return 30 * 24 * time.Hour
	case 6: // three months
		return 90 * 24 * time.Hour
	case 7: // six months
		return 180 * 24 * time.Hour
	case 8: // one year
		return 365 * 24 * time.Hour
	}
	return 0
}

// serverChanges turns the folder change feed into Sync Commands.
func (s *Server) serverChanges(user *store.User, deviceID string, folder *store.Folder,
	after int64, window int, pref bodyPref, cutoff int64, skipped map[int64]bool) (*wbxml.Node, bool, int64, error) {

	known, err := s.Store.KnownItems(user.ID, deviceID, folder.ID)
	if err != nil {
		return nil, false, after, err
	}
	changes, err := s.Store.ChangesSince(folder.ID, after, window*4+len(skipped))
	if err != nil {
		return nil, false, after, err
	}

	type pending struct {
		id   int64
		kind string
	}
	var order []pending
	index := make(map[int64]int)
	lastSeq := after
	for _, ch := range changes {
		if skipped[ch.Seq] {
			lastSeq = ch.Seq
			continue
		}
		if len(order) >= window {
			break
		}
		if i, ok := index[ch.ItemID]; ok {
			order[i].kind = ch.Kind
		} else {
			index[ch.ItemID] = len(order)
			order = append(order, pending{id: ch.ItemID, kind: ch.Kind})
		}
		lastSeq = ch.Seq
	}

	commands := wbxml.N(pageAirSync, "Commands")
	for _, p := range order {
		if node := s.itemCommand(user, deviceID, folder, p.id, p.kind, known, pref, cutoff); node != nil {
			commands.Children = append(commands.Children, node)
		}
	}
	more, err := s.Store.HasChangesAfter(folder.ID, lastSeq)
	if err != nil {
		return nil, false, lastSeq, err
	}
	return commands, more, lastSeq, nil
}

func (s *Server) itemCommand(user *store.User, deviceID string, folder *store.Folder,
	id int64, kind string, known map[int64]bool, pref bodyPref, cutoff int64) *wbxml.Node {

	if kind != "delete" {
		app, gone, date, err := s.loadAppData(folder, id, pref)
		if err != nil {
			// Never fail silently: an item that cannot be rendered would
			// otherwise vanish once the SyncKey moves past it.
			s.Log.Warn("eas item skipped", "folder", folder.ID, "id", id, "err", err)
			return nil
		}
		if gone {
			kind = "delete"
		} else if cutoff > 0 && date > 0 && date < cutoff {
			return nil
		}
		if kind != "delete" {
			name := "Change"
			if !known[id] {
				name = "Add"
			}
			known[id] = true
			_ = s.Store.MarkItemKnown(user.ID, deviceID, folder.ID, id)
			return wbxml.N(pageAirSync, name,
				wbxml.T(pageAirSync, "ServerId", i64toa(id)),
				app)
		}
	}
	if !known[id] {
		return nil
	}
	delete(known, id)
	_ = s.Store.ForgetItem(user.ID, deviceID, folder.ID, id)
	return wbxml.N(pageAirSync, "Delete", wbxml.T(pageAirSync, "ServerId", i64toa(id)))
}

// loadAppData builds the ApplicationData for one item. gone reports that the
// item no longer lives in the folder (deleted or moved).
func (s *Server) loadAppData(folder *store.Folder, id int64, pref bodyPref) (node *wbxml.Node, gone bool, date int64, err error) {
	switch folder.Class {
	case "Contacts":
		c, err := s.Store.ContactByID(id)
		if errors.Is(err, store.ErrNotFound) {
			return nil, true, 0, nil
		}
		if err != nil {
			return nil, false, 0, err
		}
		if c.Deleted || c.FolderID != folder.ID {
			return nil, true, 0, nil
		}
		return contactAppData(c), false, 0, nil
	case "Calendar":
		e, err := s.Store.EventByID(id)
		if errors.Is(err, store.ErrNotFound) {
			return nil, true, 0, nil
		}
		if err != nil {
			return nil, false, 0, err
		}
		if e.Deleted || e.FolderID != folder.ID {
			return nil, true, 0, nil
		}
		if exceptions, err := s.Store.EventExceptions(e.ID); err == nil {
			e.Exceptions = exceptions
		}
		return eventAppData(e, s.calendarTimeZone(folder.UserID)), false, 0, nil
	default:
		m, err := s.Store.MessageByID(id)
		if errors.Is(err, store.ErrNotFound) {
			return nil, true, 0, nil
		}
		if err != nil {
			return nil, false, 0, err
		}
		if m.Deleted || m.FolderID != folder.ID {
			return nil, true, 0, nil
		}
		atts, err := s.Store.AttachmentsByMessage(id)
		if err != nil {
			return nil, false, 0, err
		}
		return emailAppData(m, atts, pref), false, m.DateReceived, nil
	}
}

func (s *Server) applyClientCommands(user *store.User, folder *store.Folder,
	commands *wbxml.Node, deletesAsMoves bool) ([]*wbxml.Node, map[int64]bool) {

	var responses []*wbxml.Node
	skipped := make(map[int64]bool)

	if len(commands.Children) > 0 {
		s.Log.Debug("eas client commands", "folder", folder.ID,
			"class", folder.Class, "items", len(commands.Children))
	}

	statusResponse := func(name string, id int64, clientID, status string) *wbxml.Node {
		n := wbxml.N(pageAirSync, name)
		if id != 0 {
			n.Children = append(n.Children, wbxml.T(pageAirSync, "ServerId", i64toa(id)))
		}
		if clientID != "" {
			n.Children = append(n.Children, wbxml.T(pageAirSync, "ClientId", clientID))
		}
		n.Children = append(n.Children, wbxml.T(pageAirSync, "Status", status))
		return n
	}

	for _, cmd := range commands.Children {
		switch cmd.Name {
		case "Add":
			clientID := cmd.ChildText("ClientId")
			ad := cmd.Child("ApplicationData")
			var (
				id  int64
				seq int64
				err error
			)
			switch folder.Class {
			case "Contacts":
				c := parseContactAppData(ad)
				c.UserID, c.FolderID = user.ID, folder.ID
				id, err = s.Store.InsertContact(c)
				if err == nil {
					seq, err = s.Store.AddChange(folder.ID, "add", "contact", id)
				}
			case "Calendar":
				e := parseEventAppData(ad)
				e.UserID, e.FolderID = user.ID, folder.ID
				s.rememberCalendarTimeZone(user.ID, ad.ChildText("TimeZone"))
				// The UID is how clients recognise an event after a fresh
				// login; reuse the client id instead of inventing a new one so
				// the server copy and the device copy keep matching.
				if e.UID == "" {
					e.UID = clientID
				}
				if e.UID == "" {
					e.UID = newEventUID()
				}
				if v := ad.ChildText("StartTime"); v != "" && parseEasTime(v).IsZero() {
					s.Log.Warn("eas calendar time not understood", "folder", folder.ID,
						"start", v, "end", ad.ChildText("EndTime"))
				}
				id, err = s.Store.InsertEvent(e)
				if err == nil && len(e.Exceptions) > 0 {
					if exErr := s.Store.ReplaceEventExceptions(id, e.Exceptions); exErr != nil {
						s.Log.Warn("cannot store calendar exceptions", "err", exErr, "event", id)
					}
				}
				if err == nil {
					seq, err = s.Store.AddChange(folder.ID, "add", "event", id)
				}
			default:
				m := &store.Message{
					UserID:       user.ID,
					FolderID:     folder.ID,
					DateReceived: time.Now().Unix(),
					MessageClass: "IPM.Note",
					Importance:   1,
				}
				parseEmailAppData(ad, m)
				m.Size = int64(len(m.Raw))
				id, err = s.Store.InsertMessage(m)
				if err == nil {
					seq, err = s.Store.AddChange(folder.ID, "add", "email", id)
				}
			}
			if err != nil {
				responses = append(responses, statusResponse("Add", 0, clientID, "6"))
				continue
			}
			if seq > 0 {
				skipped[seq] = true
			}
			responses = append(responses, statusResponse("Add", id, clientID, "1"))

		case "Change":
			id := parseInt(cmd.ChildText("ServerId"))
			ad := cmd.Child("ApplicationData")
			status := "1"
			var seq int64
			switch folder.Class {
			case "Contacts":
				c, err := s.Store.ContactByID(id)
				if err != nil || c.UserID != user.ID || c.FolderID != folder.ID {
					status = "3"
					break
				}
				parsed := parseContactAppData(ad)
				parsed.ID, parsed.UserID, parsed.FolderID = c.ID, c.UserID, c.FolderID
				if err := s.Store.UpdateContact(parsed); err != nil {
					status = "6"
					break
				}
				seq, _ = s.Store.AddChange(folder.ID, "change", "contact", id)
			case "Calendar":
				e, err := s.Store.EventByID(id)
				if err != nil || e.UserID != user.ID || e.FolderID != folder.ID {
					status = "3"
					break
				}
				s.rememberCalendarTimeZone(user.ID, ad.ChildText("TimeZone"))
				parsed := parseEventAppData(ad)
				parsed.ID, parsed.UserID, parsed.FolderID = e.ID, e.UserID, e.FolderID
				if parsed.UID == "" {
					parsed.UID = e.UID
				}
				// A change that does not carry the rule must not wipe it, nor the
				// occurrence overrides stored alongside it. When the client does
				// send a rule, its own Exceptions are the new truth.
				hasRule := ad != nil && ad.Child("Recurrence") != nil
				if !hasRule {
					parsed.Recurrence = e.Recurrence
					if exceptions, exErr := s.Store.EventExceptions(e.ID); exErr == nil {
						parsed.Exceptions = exceptions
					}
				}
				if err := s.Store.UpdateEvent(parsed); err != nil {
					status = "6"
					break
				}
				if hasRule {
					if exErr := s.Store.ReplaceEventExceptions(parsed.ID, parsed.Exceptions); exErr != nil {
						s.Log.Warn("cannot store calendar exceptions", "err", exErr, "event", parsed.ID)
					}
				}
				seq, _ = s.Store.AddChange(folder.ID, "change", "event", id)
			default:
				m, err := s.Store.MessageByID(id)
				if err != nil || m.UserID != user.ID || m.FolderID != folder.ID {
					status = "3"
					break
				}
				parseEmailAppData(ad, m)
				if err := s.Store.UpdateMessage(m); err != nil {
					status = "6"
					break
				}
				s.Log.Debug("eas email change applied", "folder", folder.ID,
					"id", id, "read", m.IsRead, "flagged", m.IsFlagged)
				seq, _ = s.Store.AddChange(folder.ID, "change", "email", id)
			}
			if seq > 0 {
				skipped[seq] = true
			}
			responses = append(responses, statusResponse("Change", id, "", status))

		case "Delete":
			id := parseInt(cmd.ChildText("ServerId"))
			if id == 0 {
				continue
			}
			seqs, status := s.deleteItem(user, folder, id, deletesAsMoves)
			for _, seq := range seqs {
				skipped[seq] = true
			}
			// Clients expect one response per command; a missing Delete answer
			// leaves MIUI Mail's calendar stuck until the operation times out.
			responses = append(responses, statusResponse("Delete", id, "", status))

		case "Fetch":
			id := parseInt(cmd.ChildText("ServerId"))
			pref := bodyPrefFrom(cmd.Child("Options"))
			app, gone, _, err := s.loadAppData(folder, id, pref)
			if err != nil || gone {
				responses = append(responses, statusResponse("Fetch", id, "", "3"))
				continue
			}
			responses = append(responses, wbxml.N(pageAirSync, "Fetch",
				wbxml.T(pageAirSync, "ServerId", i64toa(id)),
				wbxml.T(pageAirSync, "Status", "1"),
				app))
		}
	}
	return responses, skipped
}

// deleteItem soft-deletes an item and returns the change sequences to skip.
// status is the EAS status code for the Delete response: 1 on success, 3 when
// the item is unknown or owned by another account, 6 on a store error.
func (s *Server) deleteItem(user *store.User, folder *store.Folder, id int64, deletesAsMoves bool) ([]int64, string) {
	var seqs []int64
	addChange := func(folderID int64, kind, itemType string) {
		if seq, err := s.Store.AddChange(folderID, kind, itemType, id); err == nil && seq > 0 {
			seqs = append(seqs, seq)
		}
	}
	switch folder.Class {
	case "Contacts":
		c, err := s.Store.ContactByID(id)
		if err != nil || c.UserID != user.ID || c.FolderID != folder.ID {
			return nil, "3"
		}
		c.Deleted = true
		if err := s.Store.UpdateContact(c); err != nil {
			return nil, "6"
		}
		addChange(folder.ID, "delete", "contact")
	case "Calendar":
		e, err := s.Store.EventByID(id)
		if err != nil || e.UserID != user.ID || e.FolderID != folder.ID {
			return nil, "3"
		}
		e.Deleted = true
		if err := s.Store.UpdateEvent(e); err != nil {
			return nil, "6"
		}
		addChange(folder.ID, "delete", "event")
	default:
		m, err := s.Store.MessageByID(id)
		if err != nil || m.UserID != user.ID || m.FolderID != folder.ID {
			return nil, "3"
		}
		if deletesAsMoves {
			if trash, err := s.Store.FolderByType(user.ID, 4); err == nil && trash.ID != folder.ID {
				if err := s.Store.MoveMessage(id, trash.ID); err == nil {
					addChange(trash.ID, "add", "email")
					addChange(folder.ID, "delete", "email")
					return seqs, "1"
				}
			}
		}
		if err := s.Store.SoftDeleteMessage(id); err != nil {
			return nil, "6"
		}
		addChange(folder.ID, "delete", "email")
	}
	return seqs, "1"
}
