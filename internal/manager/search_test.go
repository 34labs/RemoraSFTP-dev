package manager

import (
	"context"
	"testing"
	"time"

	"remorasftp/internal/apppaths"
	"remorasftp/internal/config"
	"remorasftp/internal/credentials"
	"remorasftp/internal/events"
	"remorasftp/internal/protocoltest"
	"remorasftp/internal/trust"
)

// newTestSearchManager wires a manager around an in-memory FS. Search does
// not touch the transfer subsystem, so no transfers.Manager is needed here
// (and importing one from the manager package's tests would itself create
// the manager -> transfers -> manager cycle this file exists to avoid).
func newTestSearchManager(t *testing.T, fs *protocoltest.FakeFS) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	apppaths.SetDir(dir)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	creds, err := credentials.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := trust.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus(dir, "off")
	mgr := New(cfg, creds, tr, bus)
	mgr.InjectTestSession("sess", TestSession{
		ConnID: "test-conn", ConnName: "Test", Protocol: config.ProtoSFTP,
		Host: "fake", StartDir: "/", Client: fs, ConnectedAt: time.Now(),
	})
	return mgr, "sess"
}

func TestSearch(t *testing.T) {
	fs := protocoltest.NewFakeFS()
	mgr, sess := newTestSearchManager(t, fs)
	fs.Add("/web", true, nil)
	fs.Add("/web/index.html", false, []byte("<html></html>"))
	fs.Add("/web/js", true, nil)
	fs.Add("/web/js/app.js", false, []byte("console.log(1)"))
	fs.Add("/web/secret.log", false, []byte("x"))
	fs.Add("/.hidden", true, nil)
	fs.Add("/.hidden/x.html", false, []byte("<i></i>"))

	// Extension search, recursive, hidden excluded.
	res, _, err := mgr.Search(context.Background(), sess, SearchQuery{
		Path: "/", Extension: "html", Recursive: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Path != "/web/index.html" {
		t.Fatalf("hidden html must be excluded, got %+v", res)
	}

	// With hidden included.
	res, _, err = mgr.Search(context.Background(), sess, SearchQuery{
		Path: "/", Extension: ".html", Recursive: true, IncludeHidden: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("want 2 html files, got %d", len(res))
	}

	// Non-recursive: only the starting dir.
	res, _, err = mgr.Search(context.Background(), sess, SearchQuery{
		Path: "/web", Query: "app",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 0 {
		t.Fatalf("non-recursive must not descend, got %+v", res)
	}

	// Recursive query match.
	res, _, err = mgr.Search(context.Background(), sess, SearchQuery{
		Path: "/", Query: "app", Recursive: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Name != "app.js" {
		t.Fatalf("query match failed: %+v", res)
	}

	// Case sensitivity.
	res, _, _ = mgr.Search(context.Background(), sess, SearchQuery{
		Path: "/", Query: "APP", Recursive: true, CaseSensitive: true,
	})
	if len(res) != 0 {
		t.Fatalf("case-sensitive query must not match, got %+v", res)
	}
}

func TestSearchQueryMatches(t *testing.T) {
	q := SearchQuery{Query: "read", StartsWith: true}
	if !q.matches("readme.txt", "") || q.matches("README.md", "") {
		t.Error("startsWith/case behavior wrong")
	}
	qc := SearchQuery{Query: "READ", StartsWith: true, CaseSensitive: false}
	if !qc.matches("README.md", "") {
		t.Error("case-insensitive prefix failed")
	}
	qe := SearchQuery{Extension: ".txt"}
	if !qe.matches("a.txt", ".txt") || qe.matches("a.TXT", ".txt") || qe.matches("a.tar", ".txt") {
		t.Error("extension match wrong")
	}
}
