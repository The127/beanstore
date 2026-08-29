package config

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"

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
}

type rawConfig struct {
	ListenAddress string `koanf:"listen_address"`
	LogLevel      string `koanf:"log_level"`
}

var defaults = map[string]any{
	"listen_address": "127.0.0.1:50051",
	"log_level":      "info",
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

	return Config{
		ListenAddress: raw.ListenAddress,
		LogLevel:      level,
	}, nil
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
