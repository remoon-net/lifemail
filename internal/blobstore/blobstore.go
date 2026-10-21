// Package blobstore keeps attachment payloads in a plain directory tree so the
// database only holds metadata. A directory can later be mounted from remote
// storage (for example with rclone) without touching the database.
package blobstore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Store writes blobs below a root directory.
type Store struct {
	root string
}

// Open prepares the directory, creating it when missing.
func Open(root string) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("blobstore: directory is required")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	return &Store{root: root}, nil
}

// Root reports the directory blobs are written to.
func (s *Store) Root() string { return s.root }

// Key builds a stable, sharded relative name for a blob. kind separates
// payload families (for example "att" for attachments and "raw" for MIME
// sources).
func Key(kind string, messageID int64, fileName string, data []byte) string {
	sum := sha256.Sum256(data)
	prefix := fmt.Sprintf("%s/%02x", kind, uint64(messageID)%256)
	return filepath.Join(prefix, fmt.Sprintf("%d-%s%s", messageID, hex.EncodeToString(sum[:8]), extension(fileName)))
}

// Put writes data to the relative key, replacing any previous content.
func (s *Store) Put(key string, data []byte) error {
	path := s.Path(key)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// Get reads a blob back.
func (s *Store) Get(key string) ([]byte, error) {
	return os.ReadFile(s.Path(key))
}

// Remove deletes a blob, ignoring missing files.
func (s *Store) Remove(key string) error {
	err := os.Remove(s.Path(key))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Path resolves a relative key to an absolute file path.
func (s *Store) Path(key string) string {
	return filepath.Join(s.root, filepath.FromSlash(key))
}

// extension keeps a short, safe suffix so files stay recognisable on disk.
func extension(fileName string) string {
	ext := strings.ToLower(filepath.Ext(fileName))
	if len(ext) < 2 || len(ext) > 8 {
		return ".bin"
	}
	for _, r := range ext[1:] {
		alnum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if !alnum {
			return ".bin"
		}
	}
	return ext
}
