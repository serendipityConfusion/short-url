package config

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Server   ServerConfig
	MySQL    MySQLConfig
	Redis    RedisConfig
	ShortURL ShortURLConfig
	Internal InternalConfig
}

type ServerConfig struct {
	Addr    string
	BaseURL string
}

type MySQLConfig struct {
	DSNs         []string
	TableCount   int
	MaxOpenConns int
	MaxIdleConns int
}

type RedisConfig struct {
	Addr       string
	Username   string
	Password   string
	DB         int
	CodeTTL    time.Duration
	LongURLTTL time.Duration
}

type ShortURLConfig struct {
	DefaultExpire time.Duration
}

type InternalConfig struct {
	APIToken         string
	AuthMode         string
	AuthHeader       string
	BatchCreateLimit int
}

func Load(path string) (Config, error) {
	cfg := defaultConfig()

	raw, err := loadFileConfig(path)
	if err != nil {
		return Config{}, err
	}
	applyRawConfig(&cfg, raw)

	if raw.ConfigSource != nil {
		sourceRaw, err := loadSourceConfig(context.Background(), *raw.ConfigSource)
		if err != nil {
			return Config{}, err
		}
		applyRawConfig(&cfg, sourceRaw)
	}

	if err := validate(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func defaultConfig() Config {
	return Config{
		Server: ServerConfig{
			Addr:    ":8080",
			BaseURL: "http://localhost:8080",
		},
		MySQL: MySQLConfig{
			TableCount:   16,
			MaxOpenConns: 50,
			MaxIdleConns: 10,
		},
		Redis: RedisConfig{
			CodeTTL:    24 * time.Hour,
			LongURLTTL: 24 * time.Hour,
		},
		Internal: InternalConfig{
			AuthHeader:       "X-Internal-Token",
			BatchCreateLimit: 100,
		},
	}
}

type rawConfig struct {
	ConfigSource *rawConfigSource   `toml:"config_source"`
	Server       *rawServerConfig   `toml:"server"`
	MySQL        *rawMySQLConfig    `toml:"mysql"`
	Redis        *rawRedisConfig    `toml:"redis"`
	ShortURL     *rawShortURLConfig `toml:"short_url"`
	Internal     *rawInternalConfig `toml:"internal"`
}

type rawConfigSource struct {
	Type  string `toml:"type"`
	Path  string `toml:"path"`
	URL   string `toml:"url"`
	Token string `toml:"token"`
}

type rawServerConfig struct {
	Addr    *string `toml:"addr"`
	BaseURL *string `toml:"base_url"`
}

type rawMySQLConfig struct {
	DSNs         []string `toml:"dsns"`
	TableCount   *int     `toml:"table_count"`
	MaxOpenConns *int     `toml:"max_open_conns"`
	MaxIdleConns *int     `toml:"max_idle_conns"`
}

type rawRedisConfig struct {
	Addr       *string `toml:"addr"`
	Username   *string `toml:"username"`
	Password   *string `toml:"password"`
	DB         *int    `toml:"db"`
	CodeTTL    *string `toml:"code_ttl"`
	LongURLTTL *string `toml:"long_url_ttl"`
}

type rawShortURLConfig struct {
	DefaultExpire *string `toml:"default_expire"`
}

type rawInternalConfig struct {
	APIToken         *string `toml:"api_token"`
	AuthMode         *string `toml:"auth_mode"`
	AuthHeader       *string `toml:"auth_header"`
	BatchCreateLimit *int    `toml:"batch_create_limit"`
}

func loadFileConfig(path string) (rawConfig, error) {
	file, err := os.Open(path)
	if err != nil {
		return rawConfig{}, fmt.Errorf("open config file: %w", err)
	}
	defer file.Close()
	return decodeRawConfig(file)
}

func loadSourceConfig(ctx context.Context, source rawConfigSource) (rawConfig, error) {
	switch strings.ToLower(strings.TrimSpace(source.Type)) {
	case "", "inline":
		return rawConfig{}, nil
	case "file":
		if strings.TrimSpace(source.Path) == "" {
			return rawConfig{}, errors.New("config_source.path is required when type=file")
		}
		return loadFileConfig(source.Path)
	case "http":
		if strings.TrimSpace(source.URL) == "" {
			return rawConfig{}, errors.New("config_source.url is required when type=http")
		}
		return loadHTTPConfig(ctx, source.URL, source.Token)
	default:
		return rawConfig{}, fmt.Errorf("config_source.type must be one of inline, file, http")
	}
}

func loadHTTPConfig(ctx context.Context, url string, token string) (rawConfig, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return rawConfig{}, fmt.Errorf("create config center request: %w", err)
	}
	req.Header.Set("Accept", "application/toml, text/plain")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return rawConfig{}, fmt.Errorf("fetch config center: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return rawConfig{}, fmt.Errorf("fetch config center: status %d", resp.StatusCode)
	}
	return decodeRawConfig(io.LimitReader(resp.Body, 1<<20))
}

func decodeRawConfig(r io.Reader) (rawConfig, error) {
	var raw rawConfig
	decoder := toml.NewDecoder(r)
	if _, err := decoder.Decode(&raw); err != nil {
		return rawConfig{}, fmt.Errorf("decode config: %w", err)
	}
	return raw, nil
}

func applyRawConfig(cfg *Config, raw rawConfig) {
	if raw.Server != nil {
		if raw.Server.Addr != nil {
			cfg.Server.Addr = *raw.Server.Addr
		}
		if raw.Server.BaseURL != nil {
			cfg.Server.BaseURL = strings.TrimRight(*raw.Server.BaseURL, "/")
		}
	}
	if raw.MySQL != nil {
		if len(raw.MySQL.DSNs) > 0 {
			cfg.MySQL.DSNs = raw.MySQL.DSNs
		}
		if raw.MySQL.TableCount != nil {
			cfg.MySQL.TableCount = *raw.MySQL.TableCount
		}
		if raw.MySQL.MaxOpenConns != nil {
			cfg.MySQL.MaxOpenConns = *raw.MySQL.MaxOpenConns
		}
		if raw.MySQL.MaxIdleConns != nil {
			cfg.MySQL.MaxIdleConns = *raw.MySQL.MaxIdleConns
		}
	}
	if raw.Redis != nil {
		if raw.Redis.Addr != nil {
			cfg.Redis.Addr = *raw.Redis.Addr
		}
		if raw.Redis.Username != nil {
			cfg.Redis.Username = *raw.Redis.Username
		}
		if raw.Redis.Password != nil {
			cfg.Redis.Password = *raw.Redis.Password
		}
		if raw.Redis.DB != nil {
			cfg.Redis.DB = *raw.Redis.DB
		}
		if raw.Redis.CodeTTL != nil {
			cfg.Redis.CodeTTL = parseDuration(*raw.Redis.CodeTTL, cfg.Redis.CodeTTL)
		}
		if raw.Redis.LongURLTTL != nil {
			cfg.Redis.LongURLTTL = parseDuration(*raw.Redis.LongURLTTL, cfg.Redis.LongURLTTL)
		}
	}
	if raw.ShortURL != nil && raw.ShortURL.DefaultExpire != nil {
		cfg.ShortURL.DefaultExpire = parseDuration(*raw.ShortURL.DefaultExpire, cfg.ShortURL.DefaultExpire)
	}
	if raw.Internal != nil {
		if raw.Internal.APIToken != nil {
			cfg.Internal.APIToken = *raw.Internal.APIToken
		}
		if raw.Internal.AuthMode != nil {
			cfg.Internal.AuthMode = strings.ToLower(strings.TrimSpace(*raw.Internal.AuthMode))
		}
		if raw.Internal.AuthHeader != nil {
			cfg.Internal.AuthHeader = strings.TrimSpace(*raw.Internal.AuthHeader)
		}
		if raw.Internal.BatchCreateLimit != nil {
			cfg.Internal.BatchCreateLimit = *raw.Internal.BatchCreateLimit
		}
	}
}

func validate(cfg Config) error {
	if len(cfg.MySQL.DSNs) == 0 {
		return errors.New("mysql.dsns is required")
	}
	if cfg.MySQL.TableCount <= 0 {
		return errors.New("mysql.table_count must be positive")
	}
	if cfg.Internal.BatchCreateLimit <= 0 {
		return errors.New("internal.batch_create_limit must be positive")
	}
	if err := validateInternalAuth(cfg.Internal); err != nil {
		return err
	}
	return nil
}

func validateInternalAuth(cfg InternalConfig) error {
	mode := cfg.AuthMode
	if mode == "" {
		if cfg.APIToken == "" {
			mode = "disabled"
		} else {
			mode = "any"
		}
	}
	switch mode {
	case "disabled", "none":
		return nil
	case "bearer", "header", "any":
		if cfg.APIToken == "" {
			return fmt.Errorf("internal.api_token is required when internal.auth_mode=%s", mode)
		}
		if (mode == "header" || mode == "any") && strings.TrimSpace(cfg.AuthHeader) == "" {
			return errors.New("internal.auth_header is required for header based internal auth")
		}
		return nil
	default:
		return fmt.Errorf("internal.auth_mode must be one of disabled, none, bearer, header, any")
	}
}

func parseDuration(value string, fallback time.Duration) time.Duration {
	if value == "" || value == "0" {
		return 0
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}
