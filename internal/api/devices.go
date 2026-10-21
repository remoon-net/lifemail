package api

import (
	"net/http"
	"net/url"
	"strings"
)

type adminDeviceView struct {
	UserID        string `json:"user_id"`
	Address       string `json:"address"`
	DeviceID      string `json:"device_id"`
	DeviceType    string `json:"device_type"`
	Protocol      string `json:"protocol"`
	LastSeen      int64  `json:"last_seen"`
	WipeRequested bool   `json:"wipe_requested"`
	CreatedAt     int64  `json:"created_at"`
	LastIP        string `json:"last_ip"`
	AppVersion    string `json:"app_version"`
}

// devices serves /api/admin/devices and
// /api/admin/devices/{userID}/{deviceID}[/wipe].
func (h *Admin) devices(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "devices are not available")
		return
	}
	if !h.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="lifemail-admin"`)
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/devices"), "/")
	if rest == "" {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		h.listDevices(w)
		return
	}

	parts := strings.Split(rest, "/")
	if len(parts) < 2 {
		writeJSONError(w, http.StatusBadRequest, "expected /api/admin/devices/{user}/{device}")
		return
	}
	userID, err := url.PathUnescape(parts[0])
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	deviceID, err := url.PathUnescape(parts[1])
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid device id")
		return
	}
	action := ""
	if len(parts) > 2 {
		action = parts[2]
	}

	switch {
	case r.Method == http.MethodPost && action == "wipe":
		if !sameOrigin(r) {
			writeJSONError(w, http.StatusForbidden, "cross-origin request rejected")
			return
		}
		if err := h.Store.RequestWipe(userID, deviceID); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if h.Log != nil {
			h.Log.Info("remote wipe requested", "user", userID, "device", deviceID)
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case r.Method == http.MethodDelete && action == "":
		if !sameOrigin(r) {
			writeJSONError(w, http.StatusForbidden, "cross-origin request rejected")
			return
		}
		if err := h.Store.DeleteDevice(userID, deviceID); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if h.Log != nil {
			h.Log.Info("device forgotten", "user", userID, "device", deviceID)
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		w.Header().Set("Allow", "DELETE")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Admin) listDevices(w http.ResponseWriter) {
	devices, err := h.Store.ListDevices()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	views := make([]adminDeviceView, 0, len(devices))
	for _, d := range devices {
		views = append(views, adminDeviceView{
			UserID:        d.UserID,
			Address:       d.Address,
			DeviceID:      d.DeviceID,
			DeviceType:    d.DeviceType,
			Protocol:      d.Protocol,
			LastSeen:      d.LastSeen,
			WipeRequested: d.WipeRequested,
			CreatedAt:     d.CreatedAt,
			LastIP:        d.LastIP,
			AppVersion:    d.AppVersion,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": views})
}
