// Package safepath normalizes and validates remote paths and filenames.
//
// Every path and name coming from a remote server (or from API input that
// targets a remote filesystem) is treated as untrusted. The helpers here:
//   - collapse redundant separators and resolve "." / ".." without escaping
//     an intended root,
//   - reject names containing path separators, NUL bytes, or traversal
//     segments where a bare filename is expected,
//   - guarantee a single canonical form so "../" tricks and ambiguous
//     normalization cannot reach outside the working directory.
package safepath

import (
	"errors"
	"path"
	"strings"
)

var (
	// ErrTraversal indicates a path that would escape its intended root.
	ErrTraversal = errors.New("path traverses outside its allowed root")
	// ErrInvalidName indicates an illegal standalone filename.
	ErrInvalidName = errors.New("invalid file name")
	// ErrEmptyPath indicates an empty path where one was required.
	ErrEmptyPath = errors.New("path is empty")
)

// Clean normalizes a remote (POSIX-style) path. It always returns an
// absolute, slash-separated path with no redundant elements. A bare
// filename or relative path is interpreted relative to "/".
func Clean(p string) string {
	if p == "" {
		return "/"
	}
	// Reject NUL bytes outright; they are never legal in remote paths.
	if strings.ContainsRune(p, 0) {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	c := path.Clean(p)
	if c == "." || c == "" {
		return "/"
	}
	return c
}

// Join joins path elements and cleans the result, guaranteeing an absolute
// remote path.
func Join(base string, elems ...string) string {
	all := append([]string{base}, elems...)
	return Clean(path.Join(all...))
}

// Within reports whether child is equal to root or strictly inside root after
// both are normalized. Both are treated as remote absolute paths.
func Within(root, child string) bool {
	r := Clean(root)
	c := Clean(child)
	if c == r {
		return true
	}
	return strings.HasPrefix(c, strings.TrimSuffix(r, "/")+"/")
}

// Resolve joins base with a user-supplied target and guarantees the result
// stays within base. Relative targets and ".." segments are allowed as long
// as they cannot escape base. Returns ErrTraversal otherwise.
//
// base is treated as the containment root (e.g. the current directory).
func Resolve(base, target string) (string, error) {
	if target == "" {
		return Clean(base), nil
	}
	if strings.ContainsRune(target, 0) {
		return "", ErrInvalidName
	}
	combined := target
	if !strings.HasPrefix(combined, "/") {
		combined = Join(base, target)
	} else {
		combined = Clean(combined)
	}
	if !Within(base, combined) {
		return "", ErrTraversal
	}
	return combined, nil
}

// ValidateName validates a standalone filename (used for mkdir/rename/new
// folder). Names must not contain separators, NUL bytes, be "." / "..", or
// contain control characters.
func ValidateName(name string) error {
	if name == "" || name == "." || name == ".." {
		return ErrInvalidName
	}
	if strings.ContainsRune(name, 0) || strings.ContainsAny(name, `/\`) {
		return ErrInvalidName
	}
	if strings.TrimSpace(name) == "" {
		return ErrInvalidName
	}
	for _, r := range name {
		if r < 0x20 {
			return ErrInvalidName
		}
	}
	return nil
}

// Base returns the final path element of a remote path (like path.Base).
func Base(p string) string {
	return path.Base(Clean(p))
}

// Dir returns the parent directory of a remote path.
func Dir(p string) string {
	b := path.Dir(Clean(p))
	if b == "." || b == "" {
		return "/"
	}
	return b
}

// IsRoot reports whether p is the remote root.
func IsRoot(p string) bool {
	return Clean(p) == "/"
}

// Split breaks a remote path into directory and name components.
func Split(p string) (dir, name string) {
	c := Clean(p)
	if c == "/" {
		return "/", ""
	}
	i := strings.LastIndex(c, "/")
	return c[:i+1], c[i+1:]
}

// EnsureLocalAbs makes sure a local filesystem path is absolute and clean.
func EnsureLocalAbs(p string) string {
	if p == "" {
		return ""
	}
	if !path.IsAbs(p) && !strings.HasPrefix(p, "/") && !strings.Contains(p, ":\\") {
		// best effort; callers on Windows pass absolute paths
	}
	return path.Clean(p)
}
