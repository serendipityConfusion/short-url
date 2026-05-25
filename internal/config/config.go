package config

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
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
	LockTTL    time.Duration
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
			LockTTL:    3 * time.Second,
		},
		Internal: InternalConfig{
			AuthHeader:       "X-Internal-Token",
			BatchCreateLimit: 100,
		},
	}
}

type rawConfig struct {
	ConfigSource *rawConfigSource
	Server       *rawServerConfig
	MySQL        *rawMySQLConfig
	Redis        *rawRedisConfig
	ShortURL     *rawShortURLConfig
	Internal     *rawInternalConfig
}

type rawConfigSource struct {
	Type  string
	Path  string
	URL   string
	Token string
}

type rawServerConfig struct {
	Addr    *string
	BaseURL *string
}

type rawMySQLConfig struct {
	DSNs         []string
	TableCount   *int
	MaxOpenConns *int
	MaxIdleConns *int
}

type rawRedisConfig struct {
	Addr       *string
	Username   *string
	Password   *string
	DB         *int
	CodeTTL    *string
	LongURLTTL *string
	LockTTL    *string
}

type rawShortURLConfig struct {
	DefaultExpire *string
}

type rawInternalConfig struct {
	APIToken         *string
	AuthMode         *string
	AuthHeader       *string
	BatchCreateLimit *int
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
	content, err := io.ReadAll(r)
	if err != nil {
		return rawConfig{}, fmt.Errorf("read config: %w", err)
	}
	raw, err := parseTOMLConfig(string(content))
	if err != nil {
		return rawConfig{}, fmt.Errorf("decode config: %w", err)
	}
	return raw, nil
}

func parseTOMLConfig(content string) (rawConfig, error) {
	var raw rawConfig
	section := ""
	for lineNo, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(stripComment(line))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"))
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return rawConfig{}, fmt.Errorf("line %d: expected key = value", lineNo+1)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if err := applyTOMLValue(&raw, section, key, value); err != nil {
			return rawConfig{}, fmt.Errorf("line %d: %w", lineNo+1, err)
		}
	}
	return raw, nil
}

func applyTOMLValue(raw *rawConfig, section, key, value string) error {
	switch section {
	case "config_source":
		if raw.ConfigSource == nil {
			raw.ConfigSource = &rawConfigSource{}
		}
		switch key {
		case "type":
			raw.ConfigSource.Type = parseTOMLString(value)
		case "path":
			raw.ConfigSource.Path = parseTOMLString(value)
		case "url":
			raw.ConfigSource.URL = parseTOMLString(value)
		case "token":
			raw.ConfigSource.Token = parseTOMLString(value)
		default:
			return fmt.Errorf("unknown config_source.%s", key)
		}
	case "server":
		if raw.Server == nil {
			raw.Server = &rawServerConfig{}
		}
		switch key {
		case "addr":
			raw.Server.Addr = stringPtr(parseTOMLString(value))
		case "base_url":
			raw.Server.BaseURL = stringPtr(parseTOMLString(value))
		default:
			return fmt.Errorf("unknown server.%s", key)
		}
	case "mysql":
		if raw.MySQL == nil {
			raw.MySQL = &rawMySQLConfig{}
		}
		switch key {
		case "dsns":
			raw.MySQL.DSNs = parseTOMLStringArray(value)
		case "table_count":
			raw.MySQL.TableCount = intPtr(parseTOMLInt(value))
		case "max_open_conns":
			raw.MySQL.MaxOpenConns = intPtr(parseTOMLInt(value))
		case "max_idle_conns":
			raw.MySQL.MaxIdleConns = intPtr(parseTOMLInt(value))
		default:
			return fmt.Errorf("unknown mysql.%s", key)
		}
	case "redis":
		if raw.Redis == nil {
			raw.Redis = &rawRedisConfig{}
		}
		switch key {
		case "addr":
			raw.Redis.Addr = stringPtr(parseTOMLString(value))
		case "username":
			raw.Redis.Username = stringPtr(parseTOMLString(value))
		case "password":
			raw.Redis.Password = stringPtr(parseTOMLString(value))
		case "db":
			raw.Redis.DB = intPtr(parseTOMLInt(value))
		case "code_ttl":
			raw.Redis.CodeTTL = stringPtr(parseTOMLString(value))
		case "long_url_ttl":
			raw.Redis.LongURLTTL = stringPtr(parseTOMLString(value))
		case "lock_ttl":
			raw.Redis.LockTTL = stringPtr(parseTOMLString(value))
		default:
			return fmt.Errorf("unknown redis.%s", key)
		}
	case "short_url":
		if raw.ShortURL == nil {
			raw.ShortURL = &rawShortURLConfig{}
		}
		switch key {
		case "default_expire":
			raw.ShortURL.DefaultExpire = stringPtr(parseTOMLString(value))
		default:
			return fmt.Errorf("unknown short_url.%s", key)
		}
	case "internal":
		if raw.Internal == nil {
			raw.Internal = &rawInternalConfig{}
		}
		switch key {
		case "api_token":
			raw.Internal.APIToken = stringPtr(parseTOMLString(value))
		case "auth_mode":
			raw.Internal.AuthMode = stringPtr(parseTOMLString(value))
		case "auth_header":
			raw.Internal.AuthHeader = stringPtr(parseTOMLString(value))
		case "batch_create_limit":
			raw.Internal.BatchCreateLimit = intPtr(parseTOMLInt(value))
		default:
			return fmt.Errorf("unknown internal.%s", key)
		}
	default:
		return fmt.Errorf("unknown section %q", section)
	}
	return nil
}

func stripComment(line string) string {
	inString := false
	escaped := false
	for i, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && inString {
			escaped = true
			continue
		}
		if r == '"' {
			inString = !inString
			continue
		}
		if r == '#' && !inString {
			return line[:i]
		}
	}
	return line
}

func parseTOMLString(value string) string {
	value = strings.TrimSpace(value)
	parsed, err := strconv.Unquote(value)
	if err == nil {
		return parsed
	}
	return strings.Trim(value, `"`)
}

func parseTOMLInt(value string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return parsed
}

func parseTOMLStringArray(value string) []string {
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(strings.TrimPrefix(value, "["), "]")
	if strings.TrimSpace(value) == "" {
		return nil
	}

	var result []string
	var current strings.Builder
	inString := false
	escaped := false
	for _, r := range value {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && inString {
			current.WriteRune(r)
			escaped = true
			continue
		}
		if r == '"' {
			current.WriteRune(r)
			inString = !inString
			continue
		}
		if r == ',' && !inString {
			result = append(result, parseTOMLString(current.String()))
			current.Reset()
			continue
		}
		current.WriteRune(r)
	}
	if strings.TrimSpace(current.String()) != "" {
		result = append(result, parseTOMLString(current.String()))
	}
	return result
}

func stringPtr(value string) *string {
	return &value
}

func intPtr(value int) *int {
	return &value
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
		if raw.Redis.LockTTL != nil {
			cfg.Redis.LockTTL = parseDuration(*raw.Redis.LockTTL, cfg.Redis.LockTTL)
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
