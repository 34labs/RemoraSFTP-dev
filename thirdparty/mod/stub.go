// Package mod is a minimal local stub of golang.org/x/mod used only to
// satisfy the offline build module graph. It is never compiled into a
// release binary; real builds with network access use the upstream module
// (see the replace directives in go.mod).
package mod
