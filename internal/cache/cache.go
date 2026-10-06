// Package cache stores small JSON documents per repository in the user's
// cache directory. All operations are best effort: a cache failure never
// fails a command.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Dir returns the cache root, honoring VB_CACHE_DIR.
func Dir() string {
	if d := os.Getenv("VB_CACHE_DIR"); d != "" {
		return d
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "vb-cache")
	}
	return filepath.Join(base, "vb")
}

// Store is the cache of one repository.
type Store struct {
	dir string
}

// ForRepo returns the store for a repository identified by its common git dir.
func ForRepo(commonDir string) *Store {
	abs, err := filepath.Abs(commonDir)
	if err != nil {
		abs = commonDir
	}
	sum := sha256.Sum256([]byte(filepath.ToSlash(abs)))
	return &Store{dir: filepath.Join(Dir(), hex.EncodeToString(sum[:])[:16])}
}

// Path returns the file path for a cache entry.
func (s *Store) Path(name string) string { return filepath.Join(s.dir, name) }

// Load decodes a cache entry into v and returns its modification time.
func (s *Store) Load(name string, v any) (time.Time, bool) {
	p := s.Path(name)
	data, err := os.ReadFile(p)
	if err != nil {
		return time.Time{}, false
	}
	if err := json.Unmarshal(data, v); err != nil {
		return time.Time{}, false
	}
	st, err := os.Stat(p)
	if err != nil {
		return time.Time{}, false
	}
	return st.ModTime(), true
}

// Save encodes v into a cache entry atomically.
func (s *Store) Save(name string, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	p := s.Path(name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return
	}
	tmp.Close()
	if err := os.Rename(tmp.Name(), p); err != nil {
		os.Remove(tmp.Name())
	}
}

// Prune keeps only the newest keep files in a cache subdirectory.
func (s *Store) Prune(sub string, keep int) {
	dir := s.Path(sub)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) <= keep {
		return
	}
	type f struct {
		name string
		mod  time.Time
	}
	var files []f
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || e.IsDir() {
			continue
		}
		files = append(files, f{e.Name(), info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	for _, x := range files[min(keep, len(files)):] {
		os.Remove(filepath.Join(dir, x.name))
	}
}

// KV is a small persistent key-value map for results keyed by commit ids,
// such as merge forecasts and patch ids. When a run adds entries, only the
// entries used during that run are written back, so the file never grows
// without bound.
type KV struct {
	store *Store
	name  string

	mu    sync.Mutex
	old   map[string]json.RawMessage
	used  map[string]json.RawMessage
	dirty bool
}

// OpenKV loads a KV file from the store.
func (s *Store) OpenKV(name string) *KV {
	kv := &KV{store: s, name: name, old: map[string]json.RawMessage{}, used: map[string]json.RawMessage{}}
	s.Load(name, &kv.old)
	return kv
}

// Get decodes the value stored under key.
func (kv *KV) Get(key string, v any) bool {
	if kv == nil {
		return false
	}
	kv.mu.Lock()
	defer kv.mu.Unlock()
	raw, ok := kv.used[key]
	if !ok {
		raw, ok = kv.old[key]
		if !ok {
			return false
		}
		kv.used[key] = raw
	}
	return json.Unmarshal(raw, v) == nil
}

// Put stores a value under key.
func (kv *KV) Put(key string, v any) {
	if kv == nil {
		return
	}
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	kv.mu.Lock()
	kv.used[key] = data
	kv.dirty = true
	kv.mu.Unlock()
}

// Flush writes the entries used in this run back to disk.
func (kv *KV) Flush() {
	if kv == nil {
		return
	}
	kv.mu.Lock()
	defer kv.mu.Unlock()
	if !kv.dirty {
		return
	}
	kv.store.Save(kv.name, kv.used)
}
