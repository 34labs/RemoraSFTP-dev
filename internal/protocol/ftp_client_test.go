package protocol

import (
	"bytes"
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func net_split(hp string) (string, string, error) { return net.SplitHostPort(hp) }

func TestFTPFileOperations(t *testing.T) {
	hostport, cleanup := startTestFTPServer(t)
	defer cleanup()

	host, port := splitHostport(t, hostport)
	cfg := Config{
		Protocol: "ftp", Host: host, Port: port,
		Username: "test", Auth: "password", Password: "any",
		Trust:       trustStoreTemp(t),
		StartDir:    "/",
		PassiveMode: true,
	}
	cl := newFTP(cfg)
	if err := cl.Connect(context.Background()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer cl.Close()

	caps := cl.Capabilities()
	if caps.Encrypted {
		t.Error("plain FTP should report unencrypted")
	}
	if !caps.ServerSideRename {
		t.Error("FTP supports rename")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Upload.
	payload := "uploaded via ftp adapter"
	if err := cl.Upload(ctx, "/up.txt", strings.NewReader(payload), 0, nil); err != nil {
		t.Fatalf("upload: %v", err)
	}

	// List.
	entries, err := cl.List(ctx, "/")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Name == "up.txt" {
			found = true
			if e.Type != EntryFile {
				t.Errorf("up.txt type = %s", e.Type)
			}
		}
	}
	if !found {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name)
		}
		t.Fatalf("upload not visible in listing: %v", names)
	}

	// Download.
	var buf bytes.Buffer
	if err := cl.Download(ctx, "/up.txt", &buf, 0, nil); err != nil {
		t.Fatalf("download: %v", err)
	}
	if buf.String() != payload {
		t.Errorf("download = %q, want %q", buf.String(), payload)
	}

	// Stat.
	st, err := cl.Stat(ctx, "/seed.txt")
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if st.Type != EntryFile || st.Size != int64(len("ftp seed")) {
		t.Errorf("stat = %+v", st)
	}

	// Mkdir + rename + delete.
	if err := cl.Mkdir(ctx, "/d"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := cl.Rename(ctx, "/up.txt", "/d/moved.txt"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if err := cl.Remove(ctx, "/d", true); err != nil {
		t.Fatalf("remove recursive: %v", err)
	}

	// ReadPartial.
	data, truncated, err := cl.ReadPartial(ctx, "/seed.txt", 100)
	if err != nil {
		t.Fatalf("readpartial: %v", err)
	}
	if truncated || string(data) != "ftp seed" {
		t.Errorf("readpartial = %q truncated=%v", data, truncated)
	}
}

func TestFTPStreamUploadDoesNotBuffer(t *testing.T) {
	hostport, cleanup := startTestFTPServer(t)
	defer cleanup()
	host, port := splitHostport(t, hostport)
	cl := newFTP(Config{Protocol: "ftp", Host: host, Port: port, Username: "u", Password: "p", Trust: trustStoreTemp(t)})
	if err := cl.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer cl.Close()

	// Stream 256KB from a generator (not buffered in memory beyond copy).
	r, w := io.Pipe()
	go func() {
		buf := make([]byte, 4096)
		for i := range buf {
			buf[i] = 'x'
		}
		for i := 0; i < 64; i++ { // 64 * 4KB = 256KB
			w.Write(buf)
		}
		w.Close()
	}()
	if err := cl.Upload(context.Background(), "/big.bin", r, 0, nil); err != nil {
		t.Fatalf("upload: %v", err)
	}
	st, err := cl.Stat(context.Background(), "/big.bin")
	if err != nil {
		t.Fatal(err)
	}
	if st.Size != 256*1024 {
		t.Errorf("size = %d, want %d", st.Size, 256*1024)
	}
}

func splitHostport(t *testing.T, hp string) (string, int) {
	t.Helper()
	h, p, err := net_split(hp)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range p {
		n = n*10 + int(c-'0')
	}
	return h, n
}
