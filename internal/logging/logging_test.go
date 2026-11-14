package logging

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewLevels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		level        string
		debugEnabled bool
		infoEnabled  bool
	}{
		{level: "debug", debugEnabled: true, infoEnabled: true},
		{level: "info", debugEnabled: false, infoEnabled: true},
		{level: "warn", debugEnabled: false, infoEnabled: false},
		{level: "error", debugEnabled: false, infoEnabled: false},
		{level: "INFO", debugEnabled: false, infoEnabled: true},
	}

	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			t.Parallel()

			logger, err := New(tt.level, "json")
			require.NoError(t, err)
			require.NotNil(t, logger)

			ctx := context.Background()
			require.Equal(t, tt.debugEnabled, logger.Enabled(ctx, slog.LevelDebug))
			require.Equal(t, tt.infoEnabled, logger.Enabled(ctx, slog.LevelInfo))
		})
	}
}

func TestNewRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		level  string
		format string
	}{
		{level: "loud", format: "json"},
		{level: "", format: "json"},
		{level: "info", format: "yaml"},
	}

	for _, tt := range tests {
		t.Run(tt.level+"/"+tt.format, func(t *testing.T) {
			t.Parallel()

			logger, err := New(tt.level, tt.format)
			require.Error(t, err)
			require.Nil(t, logger)
		})
	}
}

// captureStdout redirects os.Stdout for the duration of fn.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	r, w, err := os.Pipe()
	require.NoError(t, err)

	original := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = original })

	fn()

	require.NoError(t, w.Close())
	os.Stdout = original

	out, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())

	return string(out)
}

func TestNewWritesJSON(t *testing.T) {
	out := captureStdout(t, func() {
		logger, err := New("debug", "json")
		require.NoError(t, err)
		logger.Info("hello", "answer", 42)
	})

	var line map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &line))
	require.Equal(t, "INFO", line["level"])
	require.Equal(t, "hello", line["msg"])

	answer, ok := line["answer"].(float64)
	require.True(t, ok, "answer must be a JSON number")
	require.InDelta(t, 42, answer, 0)
}

func TestNewWritesText(t *testing.T) {
	out := captureStdout(t, func() {
		logger, err := New("info", "text")
		require.NoError(t, err)
		logger.Warn("careful", "answer", 42)
	})

	require.Contains(t, out, "level=WARN")
	require.Contains(t, out, "msg=careful")
	require.Contains(t, out, "answer=42")
}
