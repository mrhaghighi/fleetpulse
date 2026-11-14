package scrape

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCursorFileRoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "cursors.json")
	store := newCursorStore(path, discardLogger())
	store.set("http://web-01:9100", 1760000010)
	store.set("http://web-02:9100", 1760000020)

	require.NoError(t, store.save())

	loaded, err := loadCursors(path)
	require.NoError(t, err)
	require.Equal(t, map[string]int64{
		"http://web-01:9100": 1760000010,
		"http://web-02:9100": 1760000020,
	}, loaded)

	// The atomic replace must not leave temp files behind.
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "cursors.json", entries[0].Name())
}

func TestCursorFileMissingStartsEmpty(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "cursors.json")
	store := newCursorStore(path, discardLogger())

	require.Zero(t, store.get("http://web-01:9100"))
	require.Empty(t, store.values)
}

func TestCursorFileCorruptStartsEmpty(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "cursors.json")
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o600))

	_, err := loadCursors(path)
	require.Error(t, err)

	store := newCursorStore(path, discardLogger())
	require.Zero(t, store.get("http://web-01:9100"))
	require.Empty(t, store.values)
}

func TestCursorFileNullStartsEmpty(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "cursors.json")
	require.NoError(t, os.WriteFile(path, []byte("null"), 0o600))

	loaded, err := loadCursors(path)
	require.NoError(t, err)
	require.Empty(t, loaded)

	store := newCursorStore(path, discardLogger())
	require.NotPanics(t, func() { store.set("http://web-01:9100", 10) })
	require.Equal(t, int64(10), store.get("http://web-01:9100"))
}

func TestCursorStoreEmptyPathIsNoop(t *testing.T) {
	t.Parallel()

	store := newCursorStore("", discardLogger())
	store.set("http://web-01:9100", 10)
	require.Equal(t, int64(10), store.get("http://web-01:9100"))
	require.NoError(t, store.save())
}
