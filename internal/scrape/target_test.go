package scrape

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSamplesURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		base  string
		since int64
		want  string
	}{
		{name: "bare host", base: "http://web-01:9100", since: 0, want: "http://web-01:9100/v1/samples?since=0"},
		{name: "trailing slash", base: "http://web-01:9100/", since: 10, want: "http://web-01:9100/v1/samples?since=10"},
		{name: "path prefix", base: "http://simulator:9200/d/sim-00001", since: 1760000010, want: "http://simulator:9200/d/sim-00001/v1/samples?since=1760000010"},
		{name: "existing query", base: "http://web-01:9100?x=1", since: 5, want: "http://web-01:9100/v1/samples?since=5&x=1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := samplesURL(tt.base, tt.since)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestRandomJitterWithinInterval(t *testing.T) {
	t.Parallel()

	const interval = 10 * time.Second
	for range 100 {
		jitter := randomJitter(interval)
		require.GreaterOrEqual(t, jitter, time.Duration(0))
		require.Less(t, jitter, interval)
	}
}
