package config

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadInlineConfig(t *testing.T) {
	configPath := writeConfig(t, `
[server]
addr = ":9000"
base_url = "https://short.example.com/"

[mysql]
dsns = ["user:pass@tcp(db:3306)/short_url?parseTime=true"]
table_count = 32

[redis]
addr = "redis:6379"
code_ttl = "2h"

[internal]
api_token = "secret"
auth_mode = "header"
auth_header = "X-Service-Token"
batch_create_limit = 50
`)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Server.Addr != ":9000" {
		t.Fatalf("addr = %q", cfg.Server.Addr)
	}
	if cfg.Server.BaseURL != "https://short.example.com" {
		t.Fatalf("base url = %q", cfg.Server.BaseURL)
	}
	if cfg.MySQL.TableCount != 32 {
		t.Fatalf("table count = %d", cfg.MySQL.TableCount)
	}
	if cfg.Redis.CodeTTL != 2*time.Hour {
		t.Fatalf("code ttl = %s", cfg.Redis.CodeTTL)
	}
	if cfg.Internal.AuthMode != "header" || cfg.Internal.AuthHeader != "X-Service-Token" {
		t.Fatalf("internal auth = %q %q", cfg.Internal.AuthMode, cfg.Internal.AuthHeader)
	}
}

func TestLoadFromFileConfigSource(t *testing.T) {
	sourcePath := writeConfig(t, `
[mysql]
dsns = ["user:pass@tcp(db:3306)/short_url?parseTime=true"]

[internal]
auth_mode = "none"
`)
	bootstrapPath := writeConfig(t, `
[config_source]
type = "file"
path = "`+escapeTOMLString(sourcePath)+`"
`)

	cfg, err := Load(bootstrapPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.MySQL.DSNs) != 1 {
		t.Fatalf("mysql dsns = %#v", cfg.MySQL.DSNs)
	}
	if cfg.Internal.AuthMode != "none" {
		t.Fatalf("auth mode = %q", cfg.Internal.AuthMode)
	}
}

func TestLoadFromHTTPConfigSource(t *testing.T) {
	oldTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.Header.Get("Authorization"); got != "Bearer center-token" {
			t.Fatalf("authorization = %q", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(`
[mysql]
dsns = ["user:pass@tcp(db:3306)/short_url?parseTime=true"]

[internal]
api_token = "secret"
auth_mode = "bearer"
`)),
		}, nil
	})
	t.Cleanup(func() {
		http.DefaultTransport = oldTransport
	})

	bootstrapPath := writeConfig(t, `
[config_source]
type = "http"
url = "https://config.example.com/short-url.toml"
token = "center-token"
`)

	cfg, err := Load(bootstrapPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.MySQL.DSNs) != 1 {
		t.Fatalf("mysql dsns = %#v", cfg.MySQL.DSNs)
	}
	if cfg.Internal.AuthMode != "bearer" {
		t.Fatalf("auth mode = %q", cfg.Internal.AuthMode)
	}
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func escapeTOMLString(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, `"`, `\"`)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
