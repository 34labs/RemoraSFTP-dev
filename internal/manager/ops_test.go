package manager

import (
	"context"
	"errors"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"remorasftp/internal/apppaths"
	"remorasftp/internal/config"
	"remorasftp/internal/credentials"
	"remorasftp/internal/events"
	"remorasftp/internal/protocol"
	"remorasftp/internal/transfers"
	"remorasftp/internal/trust"
)

// fakeNode is one in-memory filesystem node.
type fakeNode struct {
	isDir  bool
	data   []byte
	symlink bool
}

// fakeFS is an in-memory protocol.Client backed by a path map. It is the
// test double for the shared protocol layer: everything the manager does
// (list/stat/mkdir/remove/rename/upload/download) runs through it.
type fakeFS struct {
	mu   sync.Mutex
	roots map[string]*fakeNode
	closed bool
}

func newFakeFS() *fakeFS {
	f := &fakeFS{roots: map[string]*fakeNode{"/": {isDir: true}}}
	return f
}

func (f *fakeFS) ensureParent(p string) {
	dir := path.Dir(p)
	for dir != "/" && dir != "." {
		if _, ok := f.roots[dir]; !ok {
			f.roots[dir] = &fakeNode{isDir: true}
		}
		dir = path.Dir(dir)
	}
}

func (f *fakeFS) add(p string, isDir bool, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureParent(p)
	f.roots[p] = &fakeNode{isDir: isDir, data: data}
}

func (f *fakeFS) Connect(context.Context) error { return nil }
func (f *fakeFS) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}
func (f *fakeFS) Capabilities() protocol.Capabilities {
	return protocol.Capabilities{
		Protocol: config.ProtoSFTP, Encrypted: true,
		UnixPermissions: true, Chmod: true, Symlinks: true,
		ServerSideRename: true, MaxPreviewBytes: 1 << 20,
	}
}
func (f *fakeFS) ServerInfo() protocol.ServerInfo {
	return protocol.ServerInfo{Protocol: config.ProtoSFTP, Host: "fake", Port: 22, StartDir: "/", Encrypted: true}
}

func (f *fakeFS) List(_ context.Context, dir string) ([]protocol.Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	dir = path.Clean(dir)
	n, ok := f.roots[dir]
	if !ok || !n.isDir {
		return nil, errors.New("no such directory: " + dir)
	}
	out := []protocol.Entry{}
	for p, node := range f.roots {
		if path.Dir(p) != dir || p == dir {
			continue
		}
		e := protocol.Entry{Name: path.Base(p), Path: p}
		switch {
		case node.symlink:
			e.Type = protocol.EntrySymlink
			e.IsSymlink = true
		case node.isDir:
			e.Type = protocol.EntryDir
		default:
			e.Type = protocol.EntryFile
			e.Size = int64(len(node.data))
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (f *fakeFS) Stat(_ context.Context, p string) (protocol.Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p = path.Clean(p)
	n, ok := f.roots[p]
	if !ok {
		return protocol.Entry{}, errors.New("no such file: " + p)
	}
	e := protocol.Entry{Name: path.Base(p), Path: p}
	switch {
	case n.symlink:
		e.Type = protocol.EntrySymlink
		e.IsSymlink = true
	case n.isDir:
		e.Type = protocol.EntryDir
	default:
		e.Type = protocol.EntryFile
		e.Size = int64(len(n.data))
	}
	return e, nil
}

func (f *fakeFS) Mkdir(_ context.Context, p string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p = path.Clean(p)
	if _, ok := f.roots[p]; ok {
		return errors.New("exists: " + p)
	}
	f.ensureParent(p)
	f.roots[p] = &fakeNode{isDir: true}
	return nil
}

func (f *fakeFS) Remove(_ context.Context, p string, recursive bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p = path.Clean(p)
	n, ok := f.roots[p]
	if !ok {
		return errors.New("no such file: " + p)
	}
	if n.isDir && recursive {
		for q := range f.roots {
			if q == p || strings.HasPrefix(q, p+"/") {
				delete(f.roots, q)
			}
		}
		return nil
	}
	if n.isDir {
		for q := range f.roots {
			if strings.HasPrefix(q, p+"/") {
				return errors.New("directory not empty: " + p)
			}
		}
	}
	delete(f.roots, p)
	return nil
}

func (f *fakeFS) Rename(_ context.Context, from, to string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	from, to = path.Clean(from), path.Clean(to)
	n, ok := f.roots[from]
	if !ok {
		return errors.New("no such file: " + from)
	}
	if _, ok := f.roots[to]; ok {
		return errors.New("exists: " + to)
	}
	// Move the subtree.
	renamed := map[string]*fakeNode{}
	for q, node := range f.roots {
		if q == from || strings.HasPrefix(q, from+"/") {
			nq := to + strings.TrimPrefix(q, from)
			renamed[nq] = node
		}
	}
	for q := range f.roots {
		if q == from || strings.HasPrefix(q, from+"/") {
			delete(f.roots, q)
		}
	}
	f.roots = mergeMaps(f.roots, renamed)
	return nil
}

func mergeMaps(a, b map[string]*fakeNode) map[string]*fakeNode {
	for k, v := range b {
		a[k] = v
	}
	return a
}

func (f *fakeFS) Chmod(context.Context, string, os.FileMode) error { return nil }

func (f *fakeFS) Upload(_ context.Context, dst string, r io.Reader, _ int64, _ protocol.ProgressFunc) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureParent(dst)
	f.roots[path.Clean(dst)] = &fakeNode{data: data}
	return nil
}

func (f *fakeFS) Download(_ context.Context, src string, w io.Writer, off int64, _ protocol.ProgressFunc) error {
	f.mu.Lock()
	n, ok := f.roots[path.Clean(src)]
	f.mu.Unlock()
	if !ok || n.isDir {
		return errors.New("no such file: " + src)
	}
	if off > 0 && off < int64(len(n.data)) {
		_, err := w.Write(n.data[off:])
		return err
	}
	_, err := w.Write(n.data)
	return err
}

func (f *fakeFS) ReadPartial(_ context.Context, p string, max int64) ([]byte, bool, error) {
	f.mu.Lock()
	n, ok := f.roots[path.Clean(p)]
	f.mu.Unlock()
	if !ok || n.isDir {
		return nil, false, errors.New("no such file: " + p)
	}
	if int64(len(n.data)) > max {
		return append([]byte(nil), n.data[:max]...), true, nil
	}
	return append([]byte(nil), n.data...), false, nil
}

func (f *fakeFS) read(p string) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n, ok := f.roots[path.Clean(p)]; ok {
		return n.data
	}
	return nil
}

func (f *fakeFS) has(p string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.roots[path.Clean(p)]
	return ok
}

// newTestManager wires a manager + transfer manager around an in-memory FS.
func newTestManager(t *testing.T, fs *fakeFS) (*Manager, *transfers.Manager, string) {
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
	tm := transfers.New(bus, mgr, 2)
	mgr.InjectTestSession("sess", TestSession{
		ConnID: "test-conn", ConnName: "Test", Protocol: config.ProtoSFTP,
		Host: "fake", StartDir: "/", Client: fs, ConnectedAt: time.Now(),
	})
	return mgr, tm, "sess"
}

func waitJob(t *testing.T, tm *transfers.Manager, id string) transfers.JobSnapshot {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		s := tm.Snapshot(id)
		switch s.Status {
		case transfers.StatusCompleted, transfers.StatusFailed, transfers.StatusCanceled:
			return s
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("job did not finish in time")
	return transfers.JobSnapshot{}
}

func TestStartCopyGuards(t *testing.T) {
	fs := newFakeFS()
	mgr, tm, sess := newTestManager(t, fs)

	cases := []struct {
		from, to string
		wantErr  string
	}{
		{"/", "/x", "root"},
		{"/a", "/a", "inside"},
		{"/a", "/a/b", "inside"},
		{"/a", "/nope/b", "does not exist"},
	}
	for i, c := range cases {
		fs.add("/a", true, nil)
		if c.to != "/a" && c.to != "/a/b" {
			fs.add("/a/b", true, nil)
		}
		_, err := mgr.StartCopy(context.Background(), sess, c.from, c.to, true, protocol.PolicyRefuse, tm)
		if err == nil {
			t.Errorf("case %d: expected error for %s -> %s", i, c.from, c.to)
			continue
		}
		if c.wantErr != "" && !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("case %d: error %q does not contain %q", i, err.Error(), c.wantErr)
		}
	}
}

func TestCopyFileAndTree(t *testing.T) {
	fs := newFakeFS()
	mgr, tm, sess := newTestManager(t, fs)

	fs.add("/src/a.txt", false, []byte("alpha"))
	fs.add("/src/sub", true, nil)
	fs.add("/src/sub/b.txt", false, []byte("beta"))
	fs.add("/dst", true, nil)

	job, err := mgr.StartCopy(context.Background(), sess, "/src", "/dst/src", false, protocol.PolicyRefuse, tm)
	if err != nil {
		t.Fatal(err)
	}
	snap := waitJob(t, tm, job.ID)
	if snap.Status != transfers.StatusCompleted {
		t.Fatalf("status = %s, err=%q", snap.Status, snap.Error)
	}
	if string(fs.read("/dst/src/a.txt")) != "alpha" {
		t.Error("file a.txt not copied")
	}
	if string(fs.read("/dst/src/sub/b.txt")) != "beta" {
		t.Error("nested file b.txt not copied")
	}
	// Source must survive a copy.
	if !fs.has("/src/a.txt") {
		t.Error("source removed by copy")
	}
}

func TestMoveTreeRemovesSource(t *testing.T) {
	fs := newFakeFS()
	mgr, tm, sess := newTestManager(t, fs)

	fs.add("/src/a.txt", false, []byte("alpha"))
	fs.add("/dst", true, nil)

	job, err := mgr.StartCopy(context.Background(), sess, "/src", "/dst/src", true, protocol.PolicyRefuse, tm)
	if err != nil {
		t.Fatal(err)
	}
	snap := waitJob(t, tm, job.ID)
	if snap.Status != transfers.StatusCompleted {
		t.Fatalf("status = %s, err=%q", snap.Status, snap.Error)
	}
	if !fs.has("/dst/src/a.txt") {
		t.Error("moved file missing at destination")
	}
	if fs.has("/src/a.txt") {
		t.Error("source file still present after move")
	}
}

func TestCopyConflictPolicies(t *testing.T) {
	fs := newFakeFS()
	mgr, tm, sess := newTestManager(t, fs)

	// refuse: destination exists → job fails with conflict note.
	fs.add("/src/a.txt", false, []byte("new"))
	fs.add("/dst", true, nil)
	fs.add("/dst/a.txt", false, []byte("old"))
	job, err := mgr.StartCopy(context.Background(), sess, "/src/a.txt", "/dst/a.txt", false, protocol.PolicyRefuse, tm)
	if err != nil {
		t.Fatal(err)
	}
	snap := waitJob(t, tm, job.ID)
	if snap.Status == transfers.StatusCompleted {
		t.Error("refuse policy must fail on conflict")
	}

	// replace: destination overwritten.
	job, err = mgr.StartCopy(context.Background(), sess, "/src/a.txt", "/dst/a.txt", false, protocol.PolicyReplace, tm)
	if err != nil {
		t.Fatal(err)
	}
	snap = waitJob(t, tm, job.ID)
	if snap.Status != transfers.StatusCompleted {
		t.Fatalf("replace status = %s, err=%q", snap.Status, snap.Error)
	}
	if string(fs.read("/dst/a.txt")) != "new" {
		t.Error("replace did not overwrite")
	}

	// skip: an existing destination is left untouched, job completes.
	fs.add("/src2", true, nil)
	fs.add("/src2/b.txt", false, []byte("b-new"))
	job, err = mgr.StartCopy(context.Background(), sess, "/src2/b.txt", "/dst/a.txt", false, protocol.PolicySkip, tm)
	if err != nil {
		t.Fatal(err)
	}
	snap = waitJob(t, tm, job.ID)
	if snap.Status != transfers.StatusCompleted {
		t.Fatalf("skip status = %s, err=%q", snap.Status, snap.Error)
	}
	if string(fs.read("/dst/a.txt")) != "new" {
		t.Error("skip policy modified the destination")
	}

	// rename: non-colliding name created.
	fs.add("/src3", true, nil)
	fs.add("/src3/c.txt", false, []byte("c-new"))
	fs.add("/dst/c.txt", false, []byte("c-old"))
	job, err = mgr.StartCopy(context.Background(), sess, "/src3/c.txt", "/dst/c.txt", false, protocol.PolicyRename, tm)
	if err != nil {
		t.Fatal(err)
	}
	snap = waitJob(t, tm, job.ID)
	if snap.Status != transfers.StatusCompleted {
		t.Fatalf("rename status = %s, err=%q", snap.Status, snap.Error)
	}
	if !fs.has("/dst/c (1).txt") {
		t.Error("renamed copy missing: /dst/c (1).txt")
	}
	if string(fs.read("/dst/c.txt")) != "c-old" {
		t.Error("original overwritten by rename policy")
	}
}

func TestCopyFolderPolicies(t *testing.T) {
	fs := newFakeFS()
	mgr, tm, sess := newTestManager(t, fs)

	// rename: an existing same-named destination folder must NOT be merged
	// into — a sibling "src (1)" is created instead.
	fs.add("/src", true, nil)
	fs.add("/src/a.txt", false, []byte("a"))
	fs.add("/dst", true, nil)
	fs.add("/dst/src", true, nil)
	fs.add("/dst/src/keep.txt", false, []byte("keep"))
	job, err := mgr.StartCopy(context.Background(), sess, "/src", "/dst/src", false, protocol.PolicyRename, tm)
	if err != nil {
		t.Fatal(err)
	}
	snap := waitJob(t, tm, job.ID)
	if snap.Status != transfers.StatusCompleted {
		t.Fatalf("rename folder status = %s, err=%q", snap.Status, snap.Error)
	}
	if !fs.has("/dst/src (1)/a.txt") {
		t.Error("renamed folder copy missing: /dst/src (1)/a.txt")
	}
	if string(fs.read("/dst/src/keep.txt")) != "keep" {
		t.Error("rename policy merged into the existing folder")
	}

	// replace: merge into the existing folder (Explorer semantics).
	fs.add("/src2", true, nil)
	fs.add("/src2/x.txt", false, []byte("x"))
	job, err = mgr.StartCopy(context.Background(), sess, "/src2", "/dst/src", false, protocol.PolicyReplace, tm)
	if err != nil {
		t.Fatal(err)
	}
	snap = waitJob(t, tm, job.ID)
	if snap.Status != transfers.StatusCompleted {
		t.Fatalf("replace folder status = %s, err=%q", snap.Status, snap.Error)
	}
	if !fs.has("/dst/src/x.txt") {
		t.Error("merge did not copy into the existing folder")
	}
	if string(fs.read("/dst/src/keep.txt")) != "keep" {
		t.Error("merge clobbered unrelated files")
	}

	// refuse: existing destination folder fails the job.
	fs.add("/src3", true, nil)
	job, err = mgr.StartCopy(context.Background(), sess, "/src3", "/dst/src", false, protocol.PolicyRefuse, tm)
	if err != nil {
		t.Fatal(err)
	}
	snap = waitJob(t, tm, job.ID)
	if snap.Status == transfers.StatusCompleted {
		t.Error("refuse policy must fail when the destination folder exists")
	}
	if !strings.Contains(snap.Error, "already exists") {
		t.Errorf("expected a conflict note, got %q", snap.Error)
	}
}

func TestSearch(t *testing.T) {
	fs := newFakeFS()
	mgr, _, sess := newTestManager(t, fs)
	fs.add("/web", true, nil)
	fs.add("/web/index.html", false, []byte("<html></html>"))
	fs.add("/web/js", true, nil)
	fs.add("/web/js/app.js", false, []byte("console.log(1)"))
	fs.add("/web/secret.log", false, []byte("x"))
	fs.add("/.hidden", true, nil)
	fs.add("/.hidden/x.html", false, []byte("<i></i>"))

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
