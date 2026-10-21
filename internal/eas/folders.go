package eas

import (
	"errors"

	"remoon.net/lifemail/internal/store"
	"remoon.net/lifemail/internal/wbxml"
)

func (s *Server) cmdFolderSync(user *store.User, req *wbxml.Node) (*wbxml.Node, error) {
	since := parseInt(req.ChildText("SyncKey"))
	resp := wbxml.N(pageFolderHierarchy, "FolderSync")

	if since > user.FolderSeq {
		resp.Children = append(resp.Children, wbxml.T(pageFolderHierarchy, "Status", "9"))
		return resp, nil
	}

	folders, err := s.Store.FoldersByUser(user.ID)
	if err != nil {
		return nil, err
	}
	changes := wbxml.N(pageFolderHierarchy, "Changes")
	var items []*wbxml.Node // Add/Delete elements, Count is prepended later
	for _, f := range folders {
		if since != 0 && f.Seq <= since {
			continue
		}
		if f.Deleted {
			if since == 0 {
				continue
			}
			del := wbxml.N(pageFolderHierarchy, "Delete",
				wbxml.T(pageFolderHierarchy, "ServerId", i64toa(f.ID)))
			items = append(items, del)
			continue
		}
		add := wbxml.N(pageFolderHierarchy, "Add")
		add.Children = append(add.Children,
			wbxml.T(pageFolderHierarchy, "ServerId", i64toa(f.ID)),
			wbxml.T(pageFolderHierarchy, "ParentId", i64toa(f.ParentID)),
			wbxml.T(pageFolderHierarchy, "DisplayName", f.Name),
			wbxml.T(pageFolderHierarchy, "Type", itoa(f.Type)),
			wbxml.T(pageFolderHierarchy, "ContentClass", contentClass(f.Class)),
		)
		items = append(items, add)
	}
	changes.Children = append(changes.Children, wbxml.T(pageFolderHierarchy, "Count", itoa(len(items))))
	changes.Children = append(changes.Children, items...)

	key := user.FolderSeq
	if key < 1 {
		key = 1
	}
	resp.Children = append(resp.Children,
		wbxml.T(pageFolderHierarchy, "Status", "1"),
		wbxml.T(pageFolderHierarchy, "SyncKey", i64toa(key)),
		changes,
	)
	return resp, nil
}

func (s *Server) folderSyncKey(userID string) string {
	u, err := s.Store.UserByID(userID)
	if err != nil || u.FolderSeq < 1 {
		return "1"
	}
	return i64toa(u.FolderSeq)
}

func (s *Server) cmdFolderCreate(user *store.User, req *wbxml.Node) (*wbxml.Node, error) {
	resp := wbxml.N(pageFolderHierarchy, "FolderCreate")
	name := req.ChildText("DisplayName")
	typ := atoiDefault(req.ChildText("Type"), 1)
	parent := parseInt(req.ChildText("ParentId"))
	if name == "" {
		resp.Children = append(resp.Children, wbxml.T(pageFolderHierarchy, "Status", "5"))
		return resp, nil
	}
	if parent != 0 {
		pf, err := s.Store.FolderByID(parent)
		if err != nil || pf.UserID != user.ID || pf.Deleted {
			resp.Children = append(resp.Children, wbxml.T(pageFolderHierarchy, "Status", "4"))
			return resp, nil
		}
	}
	f, err := s.Store.CreateFolder(user.ID, parent, name, typ)
	if errors.Is(err, store.ErrDuplicate) {
		resp.Children = append(resp.Children, wbxml.T(pageFolderHierarchy, "Status", "2"))
		return resp, nil
	}
	if err != nil {
		return nil, err
	}
	resp.Children = append(resp.Children,
		wbxml.T(pageFolderHierarchy, "Status", "1"),
		wbxml.T(pageFolderHierarchy, "SyncKey", s.folderSyncKey(user.ID)),
		wbxml.T(pageFolderHierarchy, "ServerId", i64toa(f.ID)),
	)
	return resp, nil
}

func (s *Server) cmdFolderDelete(user *store.User, req *wbxml.Node) (*wbxml.Node, error) {
	resp := wbxml.N(pageFolderHierarchy, "FolderDelete")
	id := parseInt(req.ChildText("ServerId"))
	f, err := s.Store.FolderByID(id)
	if err != nil || f.UserID != user.ID || f.Deleted {
		resp.Children = append(resp.Children, wbxml.T(pageFolderHierarchy, "Status", "3"))
		return resp, nil
	}
	if store.IsSystemFolder(f.Type) {
		resp.Children = append(resp.Children, wbxml.T(pageFolderHierarchy, "Status", "4"))
		return resp, nil
	}
	if err := s.Store.DeleteFolder(id); err != nil {
		return nil, err
	}
	resp.Children = append(resp.Children,
		wbxml.T(pageFolderHierarchy, "Status", "1"),
		wbxml.T(pageFolderHierarchy, "SyncKey", s.folderSyncKey(user.ID)),
	)
	return resp, nil
}

func (s *Server) cmdFolderUpdate(user *store.User, req *wbxml.Node) (*wbxml.Node, error) {
	resp := wbxml.N(pageFolderHierarchy, "FolderUpdate")
	id := parseInt(req.ChildText("ServerId"))
	f, err := s.Store.FolderByID(id)
	if err != nil || f.UserID != user.ID || f.Deleted {
		resp.Children = append(resp.Children, wbxml.T(pageFolderHierarchy, "Status", "3"))
		return resp, nil
	}
	if store.IsSystemFolder(f.Type) {
		resp.Children = append(resp.Children, wbxml.T(pageFolderHierarchy, "Status", "4"))
		return resp, nil
	}
	if name := req.ChildText("DisplayName"); name != "" && name != f.Name {
		if err := s.Store.RenameFolder(id, name); err != nil {
			return nil, err
		}
	}
	if parentText := req.ChildText("ParentId"); parentText != "" {
		parent := parseInt(parentText)
		if parent != f.ParentID {
			if err := s.Store.ReparentFolder(id, parent); err != nil {
				return nil, err
			}
		}
	}
	resp.Children = append(resp.Children,
		wbxml.T(pageFolderHierarchy, "Status", "1"),
		wbxml.T(pageFolderHierarchy, "SyncKey", s.folderSyncKey(user.ID)),
	)
	return resp, nil
}

// contentClass maps a folder class to the EAS ContentClass name. Together with
// Type it tells the client which local store the folder belongs to.
func contentClass(class string) string {
	if class == "" {
		return "Email"
	}
	return class
}
