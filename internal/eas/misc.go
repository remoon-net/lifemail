package eas

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"remoon.net/lifemail/internal/store"
	"remoon.net/lifemail/internal/wbxml"
)

type pingFolder struct {
	id    int64
	class string
}

// maxPingHeartbeat caps how long a single Ping request is held. Real clients
// ask for 15 minutes; two minutes keeps a single long poll from pinning a
// client connection for too long.
const maxPingHeartbeat = 120

// cmdPing implements long-poll change notification.
func (s *Server) cmdPing(ctx context.Context, user *store.User, deviceID string, req *wbxml.Node) (*wbxml.Node, error) {
	heartbeat := atoiDefault(req.ChildText("HeartbeatInterval"), 60)
	if heartbeat <= 0 {
		heartbeat = 60
	}
	if heartbeat > maxPingHeartbeat {
		heartbeat = maxPingHeartbeat
	}

	var candidates []pingFolder
	if node := req.Child("Folders"); node != nil {
		for _, fn := range node.ChildrenNamed("Folder") {
			idText := fn.ChildText("ServerId")
			if idText == "" {
				idText = fn.ChildText("Id")
			}
			class := fn.ChildText("Class")
			if class == "" {
				class = "Email"
			}
			candidates = append(candidates, pingFolder{id: parseInt(idText), class: class})
		}
	} else {
		all, err := s.Store.FoldersByUser(user.ID)
		if err != nil {
			return nil, err
		}
		for _, f := range all {
			if !f.Deleted {
				candidates = append(candidates, pingFolder{id: f.ID, class: f.Class})
			}
		}
	}

	// Ownership is checked once; the poll loop then only queries changes.
	var targets []pingFolder
	for _, pf := range candidates {
		f, err := s.Store.FolderByID(pf.id)
		if err != nil || f.UserID != user.ID || f.Deleted {
			continue
		}
		targets = append(targets, pf)
	}
	// No usable folder is not an error worth answering immediately: a client
	// whose folder ids are stale (the server was rebuilt, the mailbox was
	// recreated) would ping again at once. Observed at hundreds of requests per
	// second with MIUI Mail. Falling through makes the poll simply time out.

	// A device may leave earlier long polls behind when the user keeps
	// refreshing. Cancel the previous one so only a single Ping is held per
	// account on that device; a phone with two accounts must be able to keep
	// one poll per account.
	pingKey := user.ID + "|" + deviceID
	cancel := s.acquirePing(pingKey)
	defer s.releasePing(pingKey, cancel)

	deadline := time.Now().Add(time.Duration(heartbeat) * time.Second)
	for {
		for _, pf := range targets {
			state, err := s.Store.GetSyncState(user.ID, deviceID, pf.id)
			if err != nil {
				continue
			}
			if state == nil {
				// The device has not synced this folder yet. That is not a change
				// notification: answering "changes" here makes clients such as
				// MIUI Mail ping in a tight loop instead of syncing.
				continue
			}
			has, err := s.Store.HasChangesAfter(pf.id, state.Seq)
			if err == nil && has && s.shouldNotify(pingKey, pf.id, state.Seq) {
				return pingResponse(2), nil
			}
		}
		if !time.Now().Before(deadline) {
			return pingResponse(1), nil
		}
		select {
		case <-ctx.Done():
			return pingResponse(1), nil
		case <-cancel:
			return pingResponse(1), nil
		case <-time.After(2 * time.Second):
		}
	}
}

// acquirePing registers the in-flight Ping for a device, cancelling the one it
// replaces.
func (s *Server) acquirePing(deviceID string) chan struct{} {
	s.pingMu.Lock()
	defer s.pingMu.Unlock()
	if s.pingWait == nil {
		s.pingWait = make(map[string]chan struct{})
	}
	if previous, ok := s.pingWait[deviceID]; ok {
		close(previous)
	}
	cancel := make(chan struct{})
	s.pingWait[deviceID] = cancel
	return cancel
}

func (s *Server) releasePing(deviceID string, cancel chan struct{}) {
	s.pingMu.Lock()
	defer s.pingMu.Unlock()
	if current, ok := s.pingWait[deviceID]; ok && current == cancel {
		delete(s.pingWait, deviceID)
	}
}

// cancelPing ends the long poll held for an account on a device. Clients that
// serialise their EAS traffic (MIUI Mail) otherwise wait for the poll to end
// before an upload can run, which makes saving a calendar entry feel stuck for
// several seconds.
func (s *Server) cancelPing(deviceKey string) {
	s.pingMu.Lock()
	defer s.pingMu.Unlock()
	if cancel, ok := s.pingWait[deviceKey]; ok {
		close(cancel)
		delete(s.pingWait, deviceKey)
	}
}

// pingResponse answers a Ping request. Exchange and Z-Push send only the status
// here: echoing the folder list made MIUI Mail re-ping in a tight loop instead
// of running Sync.
func pingResponse(status int) *wbxml.Node {
	return wbxml.N(pagePing, "Ping", wbxml.T(pagePing, "Status", itoa(status)))
}

// pingNotifyInterval is how long the same folder position stays muted after one
// change notification. Clients that never consume a pending change would
// otherwise turn Ping into a busy loop.
var pingNotifyInterval = 30 * time.Second

type pingNotifyEntry struct {
	seq int64
	at  time.Time
}

// shouldNotify reports whether a change at seq for the folder still has to be
// announced to the account on that device.
func (s *Server) shouldNotify(scopedDevice string, folderID, seq int64) bool {
	key := scopedDevice + "|" + i64toa(folderID)
	now := time.Now()

	s.pingMu.Lock()
	defer s.pingMu.Unlock()
	if s.pingNotified == nil {
		s.pingNotified = make(map[string]pingNotifyEntry)
	}
	if entry, ok := s.pingNotified[key]; ok {
		if entry.seq == seq && now.Sub(entry.at) < pingNotifyInterval {
			return false
		}
	}
	if len(s.pingNotified) > 1024 {
		for k, entry := range s.pingNotified {
			if now.Sub(entry.at) >= pingNotifyInterval {
				delete(s.pingNotified, k)
			}
		}
	}
	s.pingNotified[key] = pingNotifyEntry{seq: seq, at: now}
	return true
}

// cmdGetItemEstimate reports how many changes are pending per collection.
func (s *Server) cmdGetItemEstimate(user *store.User, deviceID string, req *wbxml.Node) (*wbxml.Node, error) {
	resp := wbxml.N(pageGetItemEstimate, "GetItemEstimate")
	folders := req.Child("Folders")
	if folders == nil {
		folders = req.Child("Collections")
	}
	if folders != nil {
		for _, col := range folders.Children {
			if col.Name != "Folder" && col.Name != "Collection" {
				continue
			}
			id := parseInt(col.ChildText("FolderId"))
			if id == 0 {
				id = parseInt(col.ChildText("CollectionId"))
			}

			after := int64(0)
			status := 1
			if state, err := s.Store.GetSyncState(user.ID, deviceID, id); err == nil && state != nil {
				after = state.Seq
			}
			estimate, err := s.Store.CountChangesAfter(id, after)
			if err != nil {
				status = 5
			}
			// The response schema allows exactly one Collection per
			// Response, and Class is request-only: repeating Status once per
			// folder inside a single Response made clients drop the answer.
			response := wbxml.N(pageGetItemEstimate, "Response",
				wbxml.T(pageGetItemEstimate, "Status", itoa(status)))
			if status == 1 {
				response.Children = append(response.Children,
					wbxml.N(pageGetItemEstimate, "Collection",
						wbxml.T(pageGetItemEstimate, "CollectionId", i64toa(id)),
						wbxml.T(pageGetItemEstimate, "Estimate", itoa(estimate))))
			}
			resp.Children = append(resp.Children, response)
		}
	}
	return resp, nil
}

// cmdMoveItems moves messages between folders.
func (s *Server) cmdMoveItems(user *store.User, req *wbxml.Node) (*wbxml.Node, error) {
	resp := wbxml.N(pageMove, "MoveItems")
	for _, mv := range req.ChildrenNamed("Move") {
		srcID := parseInt(mv.ChildText("SrcMsgId"))
		srcFolder := parseInt(mv.ChildText("SrcFldId"))
		dstFolder := parseInt(mv.ChildText("DstFldId"))

		status := "3" // success
		reason := ""
		msg, err := s.Store.MessageByID(srcID)
		switch {
		case err != nil || msg.UserID != user.ID || msg.FolderID != srcFolder:
			status = "1" // invalid source
			reason = "source message not in the given folder"
		default:
			if dstFolder == 0 {
				// MIUI Mail offers "move to spam" even when it has not received
				// the Junk folder yet, so it sends an empty destination.
				if junk, err := s.junkFolder(user.ID); err == nil {
					dstFolder = junk.ID
					if s.Log != nil {
						s.Log.Info("eas move without destination routed to junk",
							"user", user.Name, "message", srcID, "folder", junk.ID)
					}
				}
			}
			dst, err := s.Store.FolderByID(dstFolder)
			switch {
			case err != nil:
				reason = "destination folder does not exist"
			case dst.UserID != user.ID:
				reason = "destination folder belongs to another account"
			case dst.Deleted:
				reason = "destination folder is deleted"
			}
			if reason != "" {
				status = "2" // invalid destination
				break
			}
			if dstFolder == srcFolder {
				status = "4"
				reason = "source and destination are the same folder"
				break
			}
			if err := s.Store.MoveMessage(srcID, dstFolder); err != nil {
				status = "5"
				reason = err.Error()
				break
			}
			_, _ = s.Store.AddChange(srcFolder, "delete", "email", srcID)
			_, _ = s.Store.AddChange(dstFolder, "add", "email", srcID)
		}
		if status != "3" && s.Log != nil {
			s.Log.Warn("eas move failed", "user", user.Name, "status", status,
				"reason", reason, "message", srcID, "src_folder", srcFolder,
				"dst_folder", dstFolder)
		}
		// Response carries SrcMsgId/Status/DstMsgId directly; clients such as
		// MIUI Mail skip a nested <Move> element entirely.
		response := wbxml.N(pageMove, "Response",
			wbxml.T(pageMove, "SrcMsgId", i64toa(srcID)),
			wbxml.T(pageMove, "Status", status))
		if status == "3" {
			response.Children = append(response.Children, wbxml.T(pageMove, "DstMsgId", i64toa(srcID)))
		}
		resp.Children = append(resp.Children, response)
	}
	return resp, nil
}

// junkFolder returns the account's Spam folder, creating it only if an older
// database never had one.
func (s *Server) junkFolder(userID string) (*store.Folder, error) {
	junk, err := s.Store.FolderByType(userID, 12)
	if errors.Is(err, store.ErrNotFound) {
		return s.Store.CreateFolder(userID, 0, "Spam", 12)
	}
	return junk, err
}

// cmdItemOperations serves body and attachment fetches, folder purges and
// conversation moves.
func (s *Server) cmdItemOperations(user *store.User, req *wbxml.Node) (*wbxml.Node, error) {
	response := wbxml.N(pageItemOperations, "Response")
	handled := 0

	for _, fetch := range req.ChildrenNamed("Fetch") {
		response.Children = append(response.Children, s.itemFetch(user, fetch))
		handled++
	}
	for _, empty := range req.ChildrenNamed("EmptyFolderContents") {
		response.Children = append(response.Children, s.itemEmptyFolder(user, empty))
		handled++
	}
	for _, move := range req.ChildrenNamed("Move") {
		response.Children = append(response.Children, s.itemMoveConversation(move))
		handled++
	}
	if handled == 0 {
		// Reporting success for something we never performed is worse than an
		// error: the client shows a folder as emptied when it is not.
		return wbxml.N(pageItemOperations, "ItemOperations",
			wbxml.T(pageItemOperations, "Status", "2")), nil
	}
	return wbxml.N(pageItemOperations, "ItemOperations",
		wbxml.T(pageItemOperations, "Status", "1"),
		response), nil
}

// itemFetch answers a body or attachment fetch.
func (s *Server) itemFetch(user *store.User, fetch *wbxml.Node) *wbxml.Node {
	collectionID := parseInt(fetch.ChildText("CollectionId"))
	serverID := parseInt(fetch.ChildText("ServerId"))
	fileRef := strings.TrimSpace(fetch.ChildText("FileReference"))
	pref := bodyPrefFrom(fetch.Child("Options"))

	fetchResp := wbxml.N(pageItemOperations, "Fetch")
	fetchResp.Children = append(fetchResp.Children,
		wbxml.T(pageItemOperations, "Status", "1"),
		wbxml.T(pageAirSync, "CollectionId", i64toa(collectionID)),
		wbxml.T(pageAirSync, "ServerId", i64toa(serverID)),
		wbxml.T(pageAirSync, "Class", "Email"))

	if fileRef != "" {
		msgID, attID := parseAttachmentRef(fileRef)
		att, err := s.Store.AttachmentByID(attID)
		if err != nil || att.MessageID != msgID {
			fetchResp.Children[0] = wbxml.T(pageItemOperations, "Status", "3")
			return fetchResp
		}
		fetchResp.Children = append(fetchResp.Children,
			wbxml.T(pageAirSyncBase, "FileReference", fileRef))
		// MS-ASCMD: an attachment fetch answers with itemoperations
		// Range/Total/Data inside Properties. Without Total the client treats
		// the download as zero bytes, and Data has to be the base64 text of
		// the chunk, not an opaque blob.
		start, end := fetchRange(fetch, len(att.Data))
		props := wbxml.N(pageItemOperations, "Properties")
		props.Children = append(props.Children,
			wbxml.T(pageItemOperations, "Range", fmt.Sprintf("%d-%d", start, end)),
			wbxml.T(pageItemOperations, "Total", i64toa(int64(len(att.Data)))),
			wbxml.T(pageItemOperations, "Data", base64.StdEncoding.EncodeToString(att.Data[start:end+1])),
		)
		fetchResp.Children = append(fetchResp.Children, props)
		return fetchResp
	}

	folder, err := s.Store.FolderByID(collectionID)
	if err != nil || folder.UserID != user.ID || folder.Deleted {
		fetchResp.Children[0] = wbxml.T(pageItemOperations, "Status", "3")
		return fetchResp
	}
	app, gone, _, err := s.loadAppData(folder, serverID, pref)
	if err != nil || gone {
		fetchResp.Children[0] = wbxml.T(pageItemOperations, "Status", "3")
		return fetchResp
	}
	props := wbxml.N(pageItemOperations, "Properties")
	props.Children = app.Children
	fetchResp.Children = append(fetchResp.Children, props)
	return fetchResp
}

// itemEmptyFolder deletes everything a folder holds. Clients use it for
// "empty trash" and "empty spam".
func (s *Server) itemEmptyFolder(user *store.User, node *wbxml.Node) *wbxml.Node {
	collectionID := parseInt(node.ChildText("CollectionId"))
	result := wbxml.N(pageItemOperations, "EmptyFolderContents")
	fail := func() *wbxml.Node {
		result.Children = append(result.Children,
			wbxml.T(pageItemOperations, "Status", "3"),
			wbxml.T(pageAirSync, "CollectionId", i64toa(collectionID)))
		return result
	}

	folder, err := s.Store.FolderByID(collectionID)
	if err != nil || folder.UserID != user.ID || folder.Deleted {
		return fail()
	}
	messages, err := s.Store.MessagesByFolder(folder.ID, 0)
	if err != nil {
		return fail()
	}
	for _, m := range messages {
		if err := s.Store.SoftDeleteMessage(m.ID); err != nil {
			return fail()
		}
		if _, err := s.Store.AddChange(folder.ID, "delete", "email", m.ID); err != nil {
			s.Log.Warn("cannot record empty-folder change", "err", err, "message", m.ID)
		}
	}
	if node.Path("Options", "DeleteSubFolders") != nil {
		all, err := s.Store.FoldersByUser(user.ID)
		if err == nil {
			for _, child := range all {
				if child.ParentID == folder.ID && !child.Deleted {
					if err := s.Store.DeleteFolder(child.ID); err != nil {
						s.Log.Warn("cannot delete subfolder", "err", err, "folder", child.ID)
					}
				}
			}
		}
	}
	s.Log.Info("eas emptied folder", "user", user.Name, "folder", folder.Name,
		"messages", len(messages))
	result.Children = append(result.Children,
		wbxml.T(pageItemOperations, "Status", "1"),
		wbxml.T(pageAirSync, "CollectionId", i64toa(collectionID)))
	return result
}

// itemMoveConversation handles the conversation-based move. Accounts here have
// no conversation grouping, so the request cannot be honoured and saying so is
// better than silently doing nothing.
func (s *Server) itemMoveConversation(node *wbxml.Node) *wbxml.Node {
	return wbxml.N(pageItemOperations, "Move",
		wbxml.T(pageItemOperations, "Status", "3"))
}

func parseAttachmentRef(ref string) (int64, int64) {
	parts := strings.SplitN(ref, ":", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	return parseInt(parts[0]), parseInt(parts[1])
}

// fetchRange reads the "first-last" byte range the client asked for and clamps
// it to the attachment. Clients download large attachments in chunks, and a
// response that ignores the range is rejected as a corrupt download.
func fetchRange(fetch *wbxml.Node, size int) (int, int) {
	if size <= 0 {
		return 0, 0
	}
	last := size - 1
	spec := ""
	if opts := fetch.Child("Options"); opts != nil {
		spec = strings.TrimSpace(opts.ChildText("Range"))
	}
	if spec == "" {
		spec = strings.TrimSpace(fetch.ChildText("Range"))
	}
	if spec == "" {
		return 0, last
	}
	parts := strings.SplitN(spec, "-", 2)
	start := int(parseInt(parts[0]))
	end := last
	if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
		end = int(parseInt(parts[1]))
	}
	if start < 0 {
		start = 0
	}
	if start > last {
		start = last
	}
	if end > last {
		end = last
	}
	if end < start {
		end = last
	}
	return start, end
}

// cmdProvision hands out a permissive provisioning policy.
func (s *Server) cmdProvision(user *store.User, deviceID string, req *wbxml.Node) (*wbxml.Node, error) {
	// A wipe requested from the admin console is handed to the client on its
	// next provisioning round trip.
	if deviceID != "" {
		if wipe, err := s.Store.WipeRequested(deviceID); err == nil && wipe {
			if err := s.Store.ClearWipe(deviceID); err == nil && s.Log != nil {
				s.Log.Warn("eas remote wipe delivered", "user", user.Name, "device", deviceID)
			}
			// The client drops its local store, so start syncing from zero.
			_ = s.Store.ResetDeviceSync(user.ID, deviceID)
			return wbxml.N(pageProvision, "Provision",
				wbxml.T(pageProvision, "Status", "1"),
				wbxml.N(pageProvision, "RemoteWipe")), nil
		}
	}

	acknowledged := false
	if policies := req.Child("Policies"); policies != nil {
		for _, policy := range policies.ChildrenNamed("Policy") {
			if key := policy.ChildText("PolicyKey"); key != "" && key != "0" {
				acknowledged = true
			}
		}
	}

	policy := wbxml.N(pageProvision, "Policy")
	policy.Children = append(policy.Children,
		wbxml.T(pageProvision, "PolicyType", "MS-EAS-Provisioning-WBXML"),
		wbxml.T(pageProvision, "Status", "1"),
		wbxml.T(pageProvision, "PolicyKey", "1"))
	if !acknowledged {
		doc := wbxml.N(pageProvision, "EASProvisionDoc")
		doc.Children = append(doc.Children,
			wbxml.T(pageProvision, "DevicePasswordEnabled", "0"),
			wbxml.T(pageProvision, "AllowSimpleDevicePassword", "1"),
			wbxml.T(pageProvision, "MinDevicePasswordComplexCharacters", "0"),
			wbxml.T(pageProvision, "AttachmentsEnabled", "1"),
			wbxml.T(pageProvision, "AllowHTMLEmail", "1"),
			// Kilobytes. Zero is ambiguous: some clients read it as "no
			// limit", others as "attachments disabled" and then refuse to
			// download anything, showing 0 B. Give them a real number.
			wbxml.T(pageProvision, "MaxAttachmentSize", "20480"),
		)
		policy.Children = append(policy.Children, wbxml.N(pageProvision, "Data", doc))
	}

	resp := wbxml.N(pageProvision, "Provision",
		wbxml.T(pageProvision, "Status", "1"),
		wbxml.N(pageProvision, "Policies", policy))
	_ = s.Store.SetPolicyKey(user.ID, deviceID, "1")
	return resp, nil
}

// cmdSettings answers user information and out-of-office queries.
func (s *Server) cmdSettings(user *store.User, req *wbxml.Node) (*wbxml.Node, error) {
	resp := wbxml.N(pageSettings, "Settings")
	const ok = "1"

	if req.Child("UserInformation") != nil {
		get := wbxml.N(pageSettings, "Get",
			wbxml.N(pageSettings, "EmailAddresses",
				wbxml.T(pageSettings, "PrimarySmtpAddress", user.Address),
				wbxml.T(pageSettings, "SmtpAddress", user.Address),
			),
		)
		resp.Children = append(resp.Children,
			wbxml.T(pageSettings, "Status", ok),
			wbxml.N(pageSettings, "UserInformation",
				wbxml.T(pageSettings, "Status", ok), get))
		return resp, nil
	}
	if node := req.Child("Oof"); node != nil {
		resp.Children = append(resp.Children,
			wbxml.T(pageSettings, "Status", ok),
			s.oofResponse(user, node))
		return resp, nil
	}
	if req.Child("DeviceInformation") != nil {
		resp.Children = append(resp.Children,
			wbxml.T(pageSettings, "Status", ok),
			wbxml.N(pageSettings, "DeviceInformation",
				wbxml.T(pageSettings, "Status", ok)))
		return resp, nil
	}
	if req.Child("DevicePassword") != nil {
		// Settings/DevicePassword carries Status directly; wrapping it in a
		// Set element is only valid on the request side.
		resp.Children = append(resp.Children,
			wbxml.T(pageSettings, "Status", ok),
			wbxml.N(pageSettings, "DevicePassword",
				wbxml.T(pageSettings, "Status", ok)))
		return resp, nil
	}
	resp.Children = append(resp.Children, wbxml.T(pageSettings, "Status", ok))
	return resp, nil
}
