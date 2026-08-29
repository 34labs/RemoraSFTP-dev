package transfers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"remorasftp/internal/events"
	"remorasftp/internal/protocol"
)

// fakeClient is an in-process protocol.Client stand-in that copies an upload
// to a sink or serves a download from a source, honoring context cancellation.
type fakeClient struct {
	protocol.Client // embedded interface; only Upload/Download are exercised
	mu              sync.Mutex
	uploads         map[string]*bytes.Buffer
	src             []byte
	closed          bool
}

func newFakeClient() *fakeClient {
	return &fakeClient{uploads: map[string]*bytes.Buffer{}}
}

func (f *fakeClient) Capabilities() protocol.Capabilities {
	return protocol.Capabilities{ResumeDownload: true, ResumeUpload: true}
}
func (f *fakeClient) Close() error { f.closed = true; return nil }

func (f *fakeClient) Upload(ctx context.Context, dst string, r io.Reader, off int64, prog protocol.ProgressFunc) error {
	f.mu.Lock()
	buf := &bytes.Buffer{}
	f.uploads[dst] = buf
	f.mu.Unlock()
	bw := &progressWriterAdapter{w: buf, fn: prog, base: off}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		b := make([]byte, 4096)
		n, err := r.Read(b)
		if n > 0 {
			if _, werr := bw.Write(b[:n]); werr != nil {
				return werr
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (f *fakeClient) Download(ctx context.Context, src string, w io.Writer, off int64, prog protocol.ProgressFunc) error {
	data := f.src
	if off > 0 && int(off) < len(data) {
		data = data[off:]
	}
	for i := 0; i < len(data); i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, err := w.Write(data[i : i+1]); err != nil {
			return err
		}
		if prog != nil {
			prog(int64(i) + 1)
		}
	}
	return nil
}

type progressWriterAdapter struct {
	w    io.Writer
	fn   func(int64)
	base int64
	n    int64
}

func (p *progressWriterAdapter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.n += int64(n)
	if p.fn != nil {
		p.fn(p.base + p.n)
	}
	return n, err
}

func TestUploadStreamCompletes(t *testing.T) {
	bus := events.NewBus(t.TempDir(), "off")
	fc := newFakeClient()
	tm := NewWithClient(bus, fc, 2)

	payload := strings.Repeat("remorasftp-upload-", 1000) // ~15KB, never fully buffered
	body := io.NopCloser(strings.NewReader(payload))

	done := make(chan struct{})
	j := tm.EnqueueUpload(&Job{
		ID: "j1", SessionID: "sess", RemotePath: "/out.bin", Name: "out.bin",
		Total: int64(len(payload)),
	}, func(_ context.Context, _ int64) (io.ReadCloser, error) {
		return body, nil
	})
	go func() { tm.Wait(j.ID); close(done) }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("upload did not finish in time")
	}

	snap := tm.Snapshot(j.ID)
	if snap.Status != StatusCompleted {
		t.Fatalf("status = %s, want completed (err=%q)", snap.Status, snap.Error)
	}
	if snap.Done != int64(len(payload)) {
		t.Fatalf("done = %d, want %d", snap.Done, len(payload))
	}
	got := fc.uploads["/out.bin"].String()
	if got != payload {
		t.Fatal("uploaded content mismatch (streaming corruption)")
	}
}

func TestCancelStopsTransfer(t *testing.T) {
	bus := events.NewBus(t.TempDir(), "off")
	fc := newFakeClient()
	fc.src = make([]byte, 50<<20) // 50MB download, slow (1 byte per iter)
	tm := NewWithClient(bus, fc, 2)

	dir := t.TempDir()
	final := filepath.Join(dir, "big.bin")
	open, part, err := LocalDownloadWriter(dir, "big.bin")
	if err != nil {
		t.Fatal(err)
	}
	j := tm.EnqueueDownload(&Job{
		ID: "j2", SessionID: "sess", RemotePath: "/big.bin", Name: "big.bin",
		LocalPath: final, Total: 50 << 20, Resumable: true,
	}, func(ctx context.Context, off int64) (io.WriteCloser, error) {
		return open(ctx, off)
	})

	time.Sleep(100 * time.Millisecond)
	if err := tm.Cancel(j.ID); err != nil {
		t.Fatal(err)
	}
	tm.Wait(j.ID)

	snap := tm.Snapshot(j.ID)
	if snap.Status != StatusCanceled {
		t.Fatalf("status = %s, want canceled", snap.Status)
	}
	// Partial artifact must not remain as the final file.
	if _, err := os.Stat(final); err == nil {
		t.Fatal("final file should not exist after cancellation")
	}
	AbortPart(part)
	if _, err := os.Stat(part); err == nil {
		t.Fatal("partial file should be removed by AbortPart")
	}
}

func TestTempIsolatedAndCleaned(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SFTPBOX_DATA_DIR", dir)
	tmp, err := os.MkdirTemp("", "remorasftp-shared")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	// LocalDownloadWriter creates a per-job subdirectory under data/tmp.
	open, part, err := LocalDownloadWriter(dir, "x.txt")
	if err != nil {
		t.Fatal(err)
	}
	wc, err := open(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wc.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := wc.(interface{ Commit() error }).Commit(); err != nil {
		t.Fatal(err)
	}
	// After successful close the part file is renamed away; the job dir is gone.
	if _, err := os.Stat(part); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("part %s should be removed/renamed, stat err=%v", part, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "x.txt")); err != nil {
		t.Fatalf("final file missing: %v", err)
	}
}

func TestShutdownCancelsAll(t *testing.T) {
	bus := events.NewBus(t.TempDir(), "off")
	fc := newFakeClient()
	fc.src = make([]byte, 20<<20)
	tm := NewWithClient(bus, fc, 1)
	dir := t.TempDir()

	for i := 0; i < 3; i++ {
		open, part, err := LocalDownloadWriter(dir, fmt.Sprintf("f%d.bin", i))
		if err != nil {
			t.Fatal(err)
		}
		_ = part
		tm.EnqueueDownload(&Job{
			ID: fmt.Sprintf("f%d", i), SessionID: "sess", RemotePath: fmt.Sprintf("/f%d.bin", i),
			Name: fmt.Sprintf("f%d.bin", i), Total: 20 << 20,
		}, func(ctx context.Context, off int64) (io.WriteCloser, error) { return open(ctx, off) })
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	tm.Shutdown(ctx) // must not hang and must terminate workers
}
