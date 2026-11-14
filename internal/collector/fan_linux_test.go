//go:build linux

package collector

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFanCollectorReadsSysfs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFanFile(t, root, "hwmon0/fan1_input", "3000\n")
	writeFanFile(t, root, "hwmon0/fan1_max", "6000\n")

	col := newFanCollector(root, 5000)

	value, ok, err := col.Collect(context.Background())
	require.NoError(t, err)
	require.True(t, ok)
	require.InDelta(t, 50, value, 1e-9)
}
