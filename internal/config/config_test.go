package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const storageConfig = "volume_group: vg0\nthin_pool: pool0\n"

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestWithoutStorageConfigFails(t *testing.T) {
	_, err := Load("", false)

	assert.ErrorContains(t, err, "volume_group must be set")
}

func TestDefaultsApply(t *testing.T) {
	path := writeConfig(t, storageConfig)

	cfg, err := Load(path, true)

	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:50051", cfg.ListenAddress)
	assert.Equal(t, slog.LevelInfo, cfg.LogLevel)
	assert.Equal(t, "vg0", cfg.VolumeGroup)
	assert.Equal(t, "pool0", cfg.ThinPool)
	assert.False(t, cfg.CreatePool)
}

func TestFileOverridesDefaults(t *testing.T) {
	path := writeConfig(t, storageConfig+"listen_address: 10.0.0.1:7000\nlog_level: debug\n")

	cfg, err := Load(path, true)

	require.NoError(t, err)
	assert.Equal(t, "10.0.0.1:7000", cfg.ListenAddress)
	assert.Equal(t, slog.LevelDebug, cfg.LogLevel)
}

func TestEnvOverridesFile(t *testing.T) {
	path := writeConfig(t, storageConfig+"listen_address: 10.0.0.1:7000\n")
	t.Setenv("BEANSTORE_LISTEN_ADDRESS", "10.0.0.2:8000")

	cfg, err := Load(path, true)

	require.NoError(t, err)
	assert.Equal(t, "10.0.0.2:8000", cfg.ListenAddress)
}

func TestMissingRequiredFileFails(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "absent.yaml"), true)

	assert.Error(t, err)
}

func TestUnknownLogLevelFails(t *testing.T) {
	path := writeConfig(t, storageConfig+"log_level: loud\n")

	_, err := Load(path, true)

	assert.ErrorContains(t, err, "unknown log_level")
}

func TestEmptyListenAddressFails(t *testing.T) {
	path := writeConfig(t, storageConfig+"listen_address: \"\"\n")

	_, err := Load(path, true)

	assert.ErrorContains(t, err, "listen_address must be set")
}

func TestMissingThinPoolFails(t *testing.T) {
	path := writeConfig(t, "volume_group: vg0\n")

	_, err := Load(path, true)

	assert.ErrorContains(t, err, "thin_pool must be set")
}

func TestCreatePoolSizes(t *testing.T) {
	cases := []struct {
		size string
		want PoolSize
	}{
		{"1073741824", PoolSize{Bytes: 1 << 30}},
		{"512K", PoolSize{Bytes: 512 << 10}},
		{"64M", PoolSize{Bytes: 64 << 20}},
		{"500G", PoolSize{Bytes: 500 << 30}},
		{"2T", PoolSize{Bytes: 2 << 40}},
		{"90%", PoolSize{Percent: 90}},
	}
	for _, c := range cases {
		path := writeConfig(t, storageConfig+"create_pool: true\npool_size: \""+c.size+"\"\n")

		cfg, err := Load(path, true)

		require.NoError(t, err, c.size)
		assert.Equal(t, c.want, cfg.PoolSize, c.size)
	}
}

func TestBadPoolSizesFail(t *testing.T) {
	for _, size := range []string{"", "0", "0%", "101%", "ten", "10Q", "%"} {
		path := writeConfig(t, storageConfig+"create_pool: true\npool_size: \""+size+"\"\n")

		_, err := Load(path, true)

		assert.Error(t, err, "pool_size %q", size)
	}
}

func TestPoolSizeWithoutCreatePoolFails(t *testing.T) {
	path := writeConfig(t, storageConfig+"pool_size: 500G\n")

	_, err := Load(path, true)

	assert.ErrorContains(t, err, "pool_size requires create_pool")
}
