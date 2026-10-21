package store

// Device holds per-device protocol state.
type Device struct {
	UserID        string
	Address       string
	DeviceID      string
	DeviceType    string
	Protocol      string
	PolicyKey     string
	LastSeen      int64
	WipeRequested bool
	CreatedAt     int64
	LastIP        string
	AppVersion    string
}

type deviceRow struct {
	UserID        string `db:"user_id"`
	DeviceID      string `db:"device_id"`
	DeviceType    string `db:"device_type"`
	Protocol      string `db:"protocol"`
	PolicyKey     string `db:"policy_key"`
	LastSeen      int64  `db:"last_seen"`
	WipeRequested int    `db:"wipe_requested"`
	CreatedAt     int64  `db:"created_at"`
	LastIP        string `db:"last_ip"`
	AppVersion    string `db:"app_version"`
	Address       string `db:"address"`
}

func (r deviceRow) device() *Device {
	return &Device{
		UserID: r.UserID, Address: r.Address, DeviceID: r.DeviceID, DeviceType: r.DeviceType,
		Protocol: r.Protocol, PolicyKey: r.PolicyKey, LastSeen: r.LastSeen,
		WipeRequested: r.WipeRequested != 0, CreatedAt: r.CreatedAt, LastIP: r.LastIP,
		AppVersion: r.AppVersion,
	}
}

// ListDevices returns all registered devices with the account address.
func (s *Store) ListDevices() ([]*Device, error) {
	var rows []deviceRow
	if err := s.getAll(&rows,
		`SELECT d.user_id, d.device_id, d.device_type, d.protocol, d.policy_key,
		        d.last_seen, d.wipe_requested, d.created_at, d.last_ip, d.app_version,
		        COALESCE(p.address, u.name, '') AS address
		 FROM devices d
		 JOIN users u ON u.id = d.user_id
		 LEFT JOIN aliases p ON p.user_id = d.user_id AND p.is_primary = 1
		 ORDER BY d.last_seen DESC`); err != nil {
		return nil, err
	}
	out := make([]*Device, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.device())
	}
	return out, nil
}

// UpsertDevice records a device connection.
func (s *Store) UpsertDevice(userID, deviceID, deviceType, protocol string) error {
	return s.RecordDevice(userID, deviceID, deviceType, protocol, "")
}

// RecordDevice records a device connection together with the address it came
// from, so the admin console can show where a device synced last.
func (s *Store) RecordDevice(userID, deviceID, deviceType, protocol, ip string) error {
	if deviceID == "" {
		return nil
	}
	now := nowUnix()
	_, err := s.exec(
		`INSERT INTO devices (user_id, device_id, device_type, protocol, policy_key,
		                      last_seen, wipe_requested, created_at, last_ip, app_version)
		 VALUES (?, ?, ?, ?, '', ?, 0, ?, ?, '')
		 ON CONFLICT (user_id, device_id) DO UPDATE SET
		   device_type = excluded.device_type,
		   protocol = excluded.protocol,
		   last_seen = excluded.last_seen,
		   last_ip = excluded.last_ip`,
		userID, deviceID, deviceType, protocol, now, now, ip)
	return err
}

// SetPolicyKey stores the provisioned policy key for a device.
func (s *Store) SetPolicyKey(userID, deviceID, policyKey string) error {
	_, err := s.exec(
		`UPDATE devices SET policy_key = ? WHERE user_id = ? AND device_id = ?`,
		policyKey, userID, deviceID)
	return err
}

// PolicyKey returns the device's acknowledged policy key ("" when none).
func (s *Store) PolicyKey(userID, deviceID string) (string, error) {
	var key string
	err := s.get(&key,
		`SELECT policy_key FROM devices WHERE user_id = ? AND device_id = ?`, userID, deviceID)
	if err == ErrNotFound {
		return "", nil
	}
	return key, err
}

// DeleteDevice forgets a device and everything the server synced to it.
func (s *Store) DeleteDevice(userID, deviceID string) error {
	if err := s.ResetDeviceSync(userID, deviceID); err != nil {
		return err
	}
	_, err := s.exec(`DELETE FROM devices WHERE user_id = ? AND device_id = ?`, userID, deviceID)
	return err
}

// ResetDeviceSync forgets every sync key and known item of a device so the
// next sync starts from scratch.
func (s *Store) ResetDeviceSync(userID, deviceID string) error {
	for _, stmt := range []string{
		`DELETE FROM sync_items WHERE user_id = ? AND device_id = ?`,
		`DELETE FROM sync_states WHERE user_id = ? AND device_id = ?`,
	} {
		if _, err := s.exec(stmt, userID, deviceID); err != nil {
			return err
		}
	}
	return nil
}

// RequestWipe marks a device for remote wipe. The wipe is delivered the next
// time the device sends a Provision request.
func (s *Store) RequestWipe(userID, deviceID string) error {
	_, err := s.exec(
		`UPDATE devices SET wipe_requested = 1 WHERE user_id = ? AND device_id = ?`,
		userID, deviceID)
	return err
}

// WipeRequested reports whether a remote wipe is outstanding for the device.
func (s *Store) WipeRequested(deviceID string) (bool, error) {
	var wipe int
	err := s.get(&wipe,
		`SELECT COALESCE(MAX(wipe_requested), 0) FROM devices WHERE device_id = ?`, deviceID)
	if err != nil {
		return false, err
	}
	return wipe != 0, nil
}

// ClearWipe forgets a delivered wipe request.
func (s *Store) ClearWipe(deviceID string) error {
	_, err := s.exec(`UPDATE devices SET wipe_requested = 0 WHERE device_id = ?`, deviceID)
	return err
}
