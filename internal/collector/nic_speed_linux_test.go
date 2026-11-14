//go:build linux

package collector

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNICSpeedFunc(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for iface, content := range map[string]string{
		"eth0": "1000\n",
		"eth1": "-1\n",
	} {
		path := filepath.Join(root, "class", "net", iface, "speed")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}

	speed := newNICSpeedFunc(root)

	mbps, ok := speed("eth0")
	require.True(t, ok)
	require.InDelta(t, 1000, mbps, 1e-9)

	_, ok = speed("eth1")
	require.False(t, ok, "a -1 speed file means unknown")

	_, ok = speed("missing0")
	require.False(t, ok)
}
