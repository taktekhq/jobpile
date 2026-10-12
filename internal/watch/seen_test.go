package watch

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSeenMissingFileIsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch-seen.json")
	s, err := LoadSeen(path)
	if err != nil {
		t.Fatalf("LoadSeen: %v", err)
	}
	if s.Has("anything") {
		t.Error("a fresh store must not already have any id")
	}
}

func TestSeenStoreAddAndSaveRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch-seen.json")
	s, err := LoadSeen(path)
	if err != nil {
		t.Fatalf("LoadSeen: %v", err)
	}
	s.Add("msgid-1@example.com")
	s.Add("msgid-2@example.com")
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := LoadSeen(path)
	if err != nil {
		t.Fatalf("LoadSeen (reload): %v", err)
	}
	if !reloaded.Has("msgid-1@example.com") {
		t.Error("reloaded store must have msgid-1@example.com")
	}
	if !reloaded.Has("msgid-2@example.com") {
		t.Error("reloaded store must have msgid-2@example.com")
	}
	if reloaded.Has("msgid-3@example.com") {
		t.Error("reloaded store must not have an id never added")
	}
}

func TestSeenStoreHasIsFalseUntilAdded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch-seen.json")
	s, err := LoadSeen(path)
	if err != nil {
		t.Fatalf("LoadSeen: %v", err)
	}
	if s.Has("not-yet") {
		t.Error("Has must be false before Add")
	}
	s.Add("not-yet")
	if !s.Has("not-yet") {
		t.Error("Has must be true right after Add, even before Save")
	}
}

func TestLoadSeenRejectsInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch-seen.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	if _, err := LoadSeen(path); err == nil {
		t.Error("LoadSeen with invalid JSON: want error, got nil")
	}
}

func TestLoadSeenEmptyFileIsEmptyStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch-seen.json")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	s, err := LoadSeen(path)
	if err != nil {
		t.Fatalf("LoadSeen: %v", err)
	}
	if s.Has("anything") {
		t.Error("an empty file must load as an empty store")
	}
}
