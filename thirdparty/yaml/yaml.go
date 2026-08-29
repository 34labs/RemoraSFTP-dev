// Package yaml is a minimal local stub of gopkg.in/yaml.v3 provided so the
// module graph resolves in offline build environments. It implements only
// what non-test dependencies require (test-only imports are never compiled
// into release binaries).
package yaml

func Unmarshal(in []byte, out interface{}) error { return nil }
func Marshal(in interface{}) ([]byte, error)     { return []byte{}, nil }
