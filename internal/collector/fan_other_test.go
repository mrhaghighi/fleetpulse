//go:build !linux

package collector

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFanCollectorUnsupportedPlatform(t *testing.T) {
	t.Parallel()

	col := newFanCollector(defaultSysfsRoot, 5000)

	_, ok, err := col.Collect(context.Background())
	require.NoError(t, err)
	require.False(t, ok)
}
