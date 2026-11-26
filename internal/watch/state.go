package watch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// seenCap bounds the per-(mailbox,folder) seen-id set used to tell new from changed.
const seenCap = 2000

// State persists the delta cursor and recently-seen message ids for one
// (mailbox, folder).
type State struct {
	DeltaLink string   `json:"delta_link"`
	Seen      []string `json:"seen"`
	// Floor is the receivedDateTime lower bound the cursor was primed with
	// (RFC 3339, UTC). Mail received before it is never delivered as new, even
	// if Graph later reports a change on it. Empty for cursors primed before
	// the floor existed: those keep the old behaviour.
	Floor string `json:"floor,omitempty"`
}

// FileStore stores State as 0600 JSON files under dir.
type FileStore struct {
	dir string
}

// NewFileStore returns a store rooted at dir (created on first Put).
func NewFileStore(dir string) *FileStore { return &FileStore{dir: dir} }

func (s *FileStore) path(mailbox, folder string) string {
	return filepath.Join(s.dir, sanitize(mailbox)+"__"+sanitize(folder)+".json")
}

// Get returns the stored State, or an empty State if none exists.
func (s *FileStore) Get(mailbox, folder string) (State, error) {
	data, err := os.ReadFile(s.path(mailbox, folder))
	if err != nil {
		if os.IsNotExist(err) {
			return State{}, nil
		}
		return State{}, err
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return State{}, err
	}
	return st, nil
}

// Put writes State (0600), trimming the seen-set to seenCap.
func (s *FileStore) Put(mailbox, folder string, st State) error {
	st.Seen = trimSeen(st.Seen, seenCap)
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(s.path(mailbox, folder), data, 0o600)
}

func trimSeen(seen []string, cap int) []string {
	if len(seen) <= cap {
		return seen
	}
	return seen[len(seen)-cap:]
}

func sanitize(s string) string {
	return strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_").Replace(s)
}
