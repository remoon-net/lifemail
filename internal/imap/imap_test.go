package imap

import (
	"bufio"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"remoon.net/lifemail/internal/blobstore"
	"remoon.net/lifemail/internal/store"
)

func TestLoginSelectFetch(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	blobs, err := blobstore.Open(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetBlobs(blobs)

	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	user, err := db.CreateUser("alice", "alice@example.com", "Alice", string(hash))
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := db.FolderByType(user.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("From: Bob <bob@example.com>\r\nTo: <alice@example.com>\r\nSubject: hello\r\n\r\nbody\r\n")
	id, err := db.InsertMessage(&store.Message{
		UserID:       user.ID,
		FolderID:     inbox.ID,
		Subject:      "hello",
		DateReceived: time.Now().Unix(),
		Size:         int64(len(raw)),
		Raw:          raw,
		Importance:   1,
		MessageClass: "IPM.Note",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddChange(inbox.ID, "add", "email", id); err != nil {
		t.Fatal(err)
	}

	client, server := net.Pipe()
	go (&Server{Store: db, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}).serveConn(server)
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(10 * time.Second))
	reader := bufio.NewReader(client)

	exec := func(command, done string) string {
		if _, err := io.WriteString(client, command+"\r\n"); err != nil {
			t.Fatalf("write %q: %v", command, err)
		}
		return readUntil(t, reader, done)
	}

	if got := readUntil(t, reader, "ready"); !strings.Contains(got, "IMAP4rev1") {
		t.Fatalf("greeting = %q", got)
	}
	if got := exec("a1 LOGIN alice@example.com secret", "a1 OK"); !strings.Contains(got, "a1 OK") {
		t.Fatalf("login = %q", got)
	}
	if got := exec("a2 SELECT INBOX", "a2 OK"); !strings.Contains(got, "* 1 EXISTS") {
		t.Fatalf("select = %q", got)
	}
	got := exec("a3 UID FETCH 1:* (UID FLAGS BODY.PEEK[])", "a3 OK")
	if !strings.Contains(got, "Subject: hello") {
		t.Fatalf("fetch missing message: %q", got)
	}
	if !strings.Contains(got, "UID 1") {
		t.Fatalf("fetch missing UID: %q", got)
	}
}

func readUntil(t *testing.T, reader *bufio.Reader, marker string) string {
	t.Helper()
	var b strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			b.WriteString(line)
		}
		if err != nil {
			t.Fatalf("read until %q: %v (data %q)", marker, err, b.String())
		}
		if strings.Contains(line, marker) {
			return b.String()
		}
	}
}
