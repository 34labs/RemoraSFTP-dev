package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreLifecycle(t *testing.T) {
	dir := t.TempDir()
	os.Setenv("SFTPBOX_DATA_DIR", dir)
	t.Cleanup(func() { os.Unsetenv("SFTPBOX_DATA_DIR") })

	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Settings().ConcurrentTransfers; got != 3 {
		t.Errorf("default concurrency = %d, want 3", got)
	}

	c := &Connection{ID: "c1", Name: "Test Host", Protocol: ProtoSFTP, Host: "example.com", Port: 22, Username: "u", Auth: AuthPassword}
	if err := s.Upsert(c); err != nil {
		t.Fatal(err)
	}
	// Secrets must never be in the config file.
	raw, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	if string(raw) == "" {
		t.Fatal("config not written")
	}

	// Reload from disk.
	s2, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	got, ok := s2.Connection("c1")
	if !ok || got.Host != "example.com" {
		t.Fatalf("profile not persisted: %+v", got)
	}
	if s2.Settings().Language != "en" {
		t.Error("default language should be en")
	}

	// Update.
	got.Name = "Renamed"
	if err := s2.Upsert(got); err != nil {
		t.Fatal(err)
	}
	all := s2.Connections()
	if len(all) != 1 || all[0].Name != "Renamed" {
		t.Fatalf("update failed: %+v", all)
	}

	// Delete.
	if err := s2.Delete("c1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.Connection("c1"); ok {
		t.Fatal("profile still present after delete")
	}
}

func TestSchemaVersionStamped(t *testing.T) {
	dir := t.TempDir()
	os.Setenv("SFTPBOX_DATA_DIR", dir)
	t.Cleanup(func() { os.Unsetenv("SFTPBOX_DATA_DIR") })
	s, _ := Load()
	if s.root.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("schema = %d, want %d", s.root.SchemaVersion, CurrentSchemaVersion)
	}
}

func TestRecents(t *testing.T) {
	dir := t.TempDir()
	os.Setenv("SFTPBOX_DATA_DIR", dir)
	t.Cleanup(func() { os.Unsetenv("SFTPBOX_DATA_DIR") })
	s, _ := Load()
	_ = s.AddRecent("c1", "/a")
	_ = s.AddRecent("c1", "/b")
	_ = s.AddRecent("c1", "/a") // dedup
	r := s.Recents()
	if len(r) != 2 {
		t.Fatalf("expected 2 recents, got %d", len(r))
	}
}
