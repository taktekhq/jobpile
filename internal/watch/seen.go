package watch

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// SeenStore tracks which Message-IDs jobpile has already notified
// about, so a reply is never reported twice, even across runs.
type SeenStore struct {
	path string
	ids  map[string]bool
}

type seenFile struct {
	Seen []string `json:"seen"`
}

// LoadSeen reads the seen-state file at path. A missing file is
// treated as an empty store, since the first run has not written one
// yet.
func LoadSeen(path string) (*SeenStore, error) {
	s := &SeenStore{path: path, ids: map[string]bool{}}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("watch: reading %s: %w", path, err)
	}
	if len(b) == 0 {
		return s, nil
	}
	var f seenFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("watch: parsing %s: %w", path, err)
	}
	for _, id := range f.Seen {
		s.ids[id] = true
	}
	return s, nil
}

// Has reports whether id has already been recorded as seen.
func (s *SeenStore) Has(id string) bool {
	return s.ids[id]
}

// Add records id as seen in memory. Call Save to write it to disk.
func (s *SeenStore) Add(id string) {
	s.ids[id] = true
}

// Save writes the current set back to the file LoadSeen read it
// from, with ids sorted for a stable diff.
func (s *SeenStore) Save() error {
	ids := make([]string, 0, len(s.ids))
	for id := range s.ids {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	b, err := json.MarshalIndent(seenFile{Seen: ids}, "", "  ")
	if err != nil {
		return fmt.Errorf("watch: encoding seen state: %w", err)
	}
	if err := os.WriteFile(s.path, b, 0o644); err != nil {
		return fmt.Errorf("watch: writing %s: %w", s.path, err)
	}
	return nil
}
