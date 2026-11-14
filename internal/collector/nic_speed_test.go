package collector

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadInterfaceSpeed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		want    float64
		ok      bool
	}{
		{name: "megabits", content: "1000\n", want: 1000, ok: true},
		{name: "fractional", content: "2500.5", want: 2500.5, ok: true},
		{name: "unknown speed", content: "-1\n", ok: false},
		{name: "zero speed", content: "0\n", ok: false},
		{name: "garbage", content: "fast\n", ok: false},
		{name: "nan", content: "NaN\n", ok: false},
		{name: "infinity", content: "+Inf\n", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "speed")
			require.NoError(t, os.WriteFile(path, []byte(tt.content), 0o644))

			got, ok := readInterfaceSpeed(path)
			require.Equal(t, tt.ok, ok)
			if tt.ok {
				require.InDelta(t, tt.want, got, 1e-9)
			} else {
				require.Zero(t, got)
			}
		})
	}

	t.Run("missing file", func(t *testing.T) {
		t.Parallel()

		got, ok := readInterfaceSpeed(filepath.Join(t.TempDir(), "speed"))
		require.False(t, ok)
		require.Zero(t, got)
	})
}
