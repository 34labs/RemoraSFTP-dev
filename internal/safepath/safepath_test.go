package safepath

import "testing"

func TestClean(t *testing.T) {
	cases := map[string]string{
		"":             "/",
		".":            "/",
		"/":            "/",
		"foo":          "/foo",
		"/foo/":        "/foo",
		"/foo/../bar":  "/bar",
		"/a/b/../../c": "/c",
		"/a//b":        "/a/b",
		"/a/./b":       "/a/b",
		"with\x00nul":  "/",
	}
	for in, want := range cases {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJoin(t *testing.T) {
	if got := Join("/a", "b", "c"); got != "/a/b/c" {
		t.Errorf("Join = %q", got)
	}
	if got := Join("/a", "../b"); got != "/b" {
		t.Errorf("Join traversal = %q", got)
	}
}

func TestWithin(t *testing.T) {
	if !Within("/home/user", "/home/user/docs") {
		t.Error("child should be within root")
	}
	if !Within("/home/user", "/home/user") {
		t.Error("root should be within itself")
	}
	if Within("/home/user", "/home/other") {
		t.Error("sibling must not be within root")
	}
	if Within("/home/user", "/home/user2") {
		t.Error("prefix-attack path must not be within root")
	}
}

func TestResolve(t *testing.T) {
	got, err := Resolve("/srv/a", "b/c")
	if err != nil || got != "/srv/a/b/c" {
		t.Errorf("relative resolve = %q, %v", got, err)
	}
	got, err = Resolve("/srv/a", "../a/x")
	if err != nil || got != "/srv/a/x" {
		t.Errorf("bounded .. = %q, %v", got, err)
	}
	if _, err := Resolve("/srv/a", "../../etc/passwd"); err != ErrTraversal {
		t.Errorf("escape should be ErrTraversal, got %v", err)
	}
	if _, err := Resolve("/srv/a", "/etc/passwd"); err != ErrTraversal {
		t.Errorf("absolute escape should be ErrTraversal, got %v", err)
	}
}

func TestValidateName(t *testing.T) {
	bad := []string{"", ".", "..", "a/b", `a\b`, "nul\x00", "   ", "a\nb"}
	for _, n := range bad {
		if err := ValidateName(n); err == nil {
			t.Errorf("ValidateName(%q) should fail", n)
		}
	}
	good := []string{"file.txt", "my folder", "report-2024.pdf", ".hidden", "weird name-1 (final).doc"}
	for _, n := range good {
		if err := ValidateName(n); err != nil {
			t.Errorf("ValidateName(%q) unexpected error: %v", n, err)
		}
	}
}

func TestSplit(t *testing.T) {
	d, n := Split("/a/b/c.txt")
	if d != "/a/b/" || n != "c.txt" {
		t.Errorf("Split = %q %q", d, n)
	}
	d, n = Split("/")
	if d != "/" || n != "" {
		t.Errorf("Split root = %q %q", d, n)
	}
}
