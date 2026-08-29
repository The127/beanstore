package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestDefaultsWithoutFile(t *testing.T) {
	cfg, err := Load("", false)

	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:50051", cfg.ListenAddress)
	assert.Equal(t, slog.LevelInfo, cfg.LogLevel)
}

func TestFileOverridesDefaults(t *testing.T) {
	path := writeConfig(t, "listen_address: 10.0.0.1:7000\nlog_level: debug\n")

	cfg, err := Load(path, true)

	require.NoError(t, err)
	assert.Equal(t, "10.0.0.1:7000", cfg.ListenAddress)
	assert.Equal(t, slog.LevelDebug, cfg.LogLevel)
}

func TestEnvOverridesFile(t *testing.T) {
	path := writeConfig(t, "listen_address: 10.0.0.1:7000\n")
	t.Setenv("BEANSTORE_LISTEN_ADDRESS", "10.0.0.2:8000")

	cfg, err := Load(path, true)

	require.NoError(t, err)
	assert.Equal(t, "10.0.0.2:8000", cfg.ListenAddress)
}

func TestMissingRequiredFileFails(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "absent.yaml"), true)

	assert.Error(t, err)
}

func TestMissingOptionalFileIsSkipped(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"), false)

	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:50051", cfg.ListenAddress)
}

func TestUnknownLogLevelFails(t *testing.T) {
	path := writeConfig(t, "log_level: loud\n")

	_, err := Load(path, true)

	assert.ErrorContains(t, err, "unknown log_level")
}

func TestEmptyListenAddressFails(t *testing.T) {
	path := writeConfig(t, "listen_address: \"\"\n")

	_, err := Load(path, true)

	assert.ErrorContains(t, err, "listen_address must be set")
}
