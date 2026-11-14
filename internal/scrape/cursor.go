package scrape

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// snapshotInterval is how often cursors are written to disk.
const snapshotInterval = 10 * time.Second

// cursorStore keeps the next_since value per target URL. Values are advanced
// only after a successful publish, so a crash never skips unsent samples.
type cursorStore struct {
	mu     sync.Mutex
	path   string
	values map[string]int64
}

// newCursorStore loads cursors from path. A missing or corrupt file is not
// fatal: the scraper starts empty, which makes the first scrape fetch the whole
// exporter buffer (accepted duplicates, ADR-0007).
func newCursorStore(path string, log *slog.Logger) *cursorStore {
	values, err := loadCursors(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			log.Warn("cursor file missing, starting empty", "path", path)
		} else {
			log.Warn("cursor file unreadable, starting empty", "path", path, "err", err)
		}
		values = make(map[string]int64)
	}
	return &cursorStore{path: path, values: values}
}

// loadCursors reads a cursor file. An empty path yields an empty map.
func loadCursors(path string) (map[string]int64, error) {
	if path == "" {
		return make(map[string]int64), nil
	}

	// The path comes from the operator via SCRAPER_CURSOR_FILE.
	data, err := os.ReadFile(path) //nolint:gosec // operator-supplied cursor path
	if err != nil {
		return nil, err
	}

	values := make(map[string]int64)
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, fmt.Errorf("decode cursor file %s: %w", path, err)
	}
	if values == nil { // a file containing "null" is valid JSON but has no map
		values = make(map[string]int64)
	}
	return values, nil
}

// get returns the stored cursor for a target, or zero when none is known.
func (c *cursorStore) get(target string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.values[target]
}

// set stores a new cursor for a target.
func (c *cursorStore) set(target string, next int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.values[target] = next
}

// save atomically replaces the cursor file: it writes a temp file in the same
// directory and renames it over the target, so readers never see a partial
// file.
func (c *cursorStore) save() error {
	if c.path == "" {
		return nil
	}

	c.mu.Lock()
	data, err := json.Marshal(c.values)
	c.mu.Unlock()
	if err != nil {
		return fmt.Errorf("marshal cursors: %w", err)
	}

	dir := filepath.Dir(c.path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create cursor directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".cursors-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp cursor file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp cursor file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp cursor file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp cursor file: %w", err)
	}
	if err := os.Rename(tmpName, c.path); err != nil {
		return fmt.Errorf("replace cursor file: %w", err)
	}
	return nil
}

// snapshotLoop saves cursors every snapshotInterval until ctx is done.
func (c *cursorStore) snapshotLoop(ctx context.Context, log *slog.Logger) {
	ticker := time.NewTicker(snapshotInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.save(); err != nil {
				log.Warn("cursor snapshot failed", "path", c.path, "err", err)
			}
		}
	}
}
