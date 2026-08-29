package config

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

const envPrefix = "BEANSTORE_"

// Config is the validated daemon configuration.
type Config struct {
	ListenAddress string
	LogLevel      slog.Level
	// VolumeGroup is the vg holding the daemon's thin pool.
	VolumeGroup string
	// ThinPool is the thin pool all volumes live in.
	ThinPool string
	// CreatePool makes the daemon create a missing thin pool at
	// startup instead of refusing to start.
	CreatePool bool
	// PoolSize is the size of a pool created at startup.
	PoolSize PoolSize
	// MaxInboundTransfers limits concurrent inbound transfers.
	MaxInboundTransfers int
	// TransferGrace is how long a dropped transfer stream may resume
	// before the transfer is destroyed.
	TransferGrace time.Duration
}

// PoolSize is a thin pool bootstrap size: a byte count, or a
// percentage of the vg's free space when Percent is set.
type PoolSize struct {
	Bytes   uint64
	Percent uint64
}

type rawConfig struct {
	ListenAddress       string `koanf:"listen_address"`
	LogLevel            string `koanf:"log_level"`
	VolumeGroup         string `koanf:"volume_group"`
	ThinPool            string `koanf:"thin_pool"`
	CreatePool          bool   `koanf:"create_pool"`
	PoolSize            string `koanf:"pool_size"`
	MaxInboundTransfers int    `koanf:"max_inbound_transfers"`
	TransferGrace       string `koanf:"transfer_grace"`
}

var defaults = map[string]any{
	"listen_address":        "127.0.0.1:50051",
	"log_level":             "info",
	"max_inbound_transfers": 4,
	"transfer_grace":        "60s",
}

// Load reads the configuration. An empty path skips the file layer. A
// missing file is an error only when required is true, so the default
// path can be absent on dev machines while an explicitly given one
// must exist.
func Load(path string, required bool) (Config, error) {
	k := koanf.New(".")

	err := k.Load(confmap.Provider(defaults, "."), nil)
	if err != nil {
		return Config{}, fmt.Errorf("loading defaults: %w", err)
	}

	if path != "" {
		err = k.Load(file.Provider(path), yaml.Parser())
		if err != nil {
			if required || !errors.Is(err, fs.ErrNotExist) {
				return Config{}, fmt.Errorf("loading config file %s: %w", path, err)
			}
		}
	}

	err = k.Load(env.Provider(envPrefix, ".", func(name string) string {
		return strings.ToLower(strings.TrimPrefix(name, envPrefix))
	}), nil)
	if err != nil {
		return Config{}, fmt.Errorf("loading environment: %w", err)
	}

	var raw rawConfig
	err = k.Unmarshal("", &raw)
	if err != nil {
		return Config{}, fmt.Errorf("unmarshalling config: %w", err)
	}

	return validate(raw)
}

func validate(raw rawConfig) (Config, error) {
	if raw.ListenAddress == "" {
		return Config{}, errors.New("listen_address must be set")
	}

	level, err := parseLevel(raw.LogLevel)
	if err != nil {
		return Config{}, err
	}

	if raw.VolumeGroup == "" {
		return Config{}, errors.New("volume_group must be set")
	}
	if raw.ThinPool == "" {
		return Config{}, errors.New("thin_pool must be set")
	}

	var poolSize PoolSize
	if raw.CreatePool {
		poolSize, err = parsePoolSize(raw.PoolSize)
		if err != nil {
			return Config{}, err
		}
	} else if raw.PoolSize != "" {
		return Config{}, errors.New("pool_size requires create_pool")
	}

	if raw.MaxInboundTransfers <= 0 {
		return Config{}, errors.New("max_inbound_transfers must be positive")
	}
	grace, err := time.ParseDuration(raw.TransferGrace)
	if err != nil || grace <= 0 {
		return Config{}, fmt.Errorf("transfer_grace must be a positive duration: %q", raw.TransferGrace)
	}

	return Config{
		ListenAddress:       raw.ListenAddress,
		LogLevel:            level,
		VolumeGroup:         raw.VolumeGroup,
		ThinPool:            raw.ThinPool,
		CreatePool:          raw.CreatePool,
		PoolSize:            poolSize,
		MaxInboundTransfers: raw.MaxInboundTransfers,
		TransferGrace:       grace,
	}, nil
}

// parsePoolSize parses a byte count with an optional K, M, G or T
// binary suffix, or a percentage of the vg's free space like "90%".
func parsePoolSize(value string) (PoolSize, error) {
	if value == "" {
		return PoolSize{}, errors.New("create_pool requires pool_size")
	}

	if strings.HasSuffix(value, "%") {
		percent, err := strconv.ParseUint(strings.TrimSuffix(value, "%"), 10, 64)
		if err != nil || percent == 0 || percent > 100 {
			return PoolSize{}, fmt.Errorf("pool_size percentage must be 1-100: %q", value)
		}

		return PoolSize{Percent: percent}, nil
	}

	multiplier := uint64(1)
	number := value
	switch {
	case strings.HasSuffix(value, "K"):
		multiplier, number = 1<<10, strings.TrimSuffix(value, "K")

	case strings.HasSuffix(value, "M"):
		multiplier, number = 1<<20, strings.TrimSuffix(value, "M")

	case strings.HasSuffix(value, "G"):
		multiplier, number = 1<<30, strings.TrimSuffix(value, "G")

	case strings.HasSuffix(value, "T"):
		multiplier, number = 1<<40, strings.TrimSuffix(value, "T")
	}

	bytes, err := strconv.ParseUint(number, 10, 64)
	if err != nil || bytes == 0 {
		return PoolSize{}, fmt.Errorf("pool_size must be bytes with an optional K, M, G or T suffix, or a percentage: %q", value)
	}

	return PoolSize{Bytes: bytes * multiplier}, nil
}

func parseLevel(name string) (slog.Level, error) {
	switch strings.ToLower(name) {
	case "debug":
		return slog.LevelDebug, nil

	case "info":
		return slog.LevelInfo, nil

	case "warn":
		return slog.LevelWarn, nil

	case "error":
		return slog.LevelError, nil

	default:
		return 0, fmt.Errorf("unknown log_level: %q", name)
	}
}
