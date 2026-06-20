package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseRunConfig(t *testing.T) {
	config, err := parseRunConfig([]string{
		"--root", "/tmp/project",
		"--mitmdump", "/tmp/mitmdump",
		"--config", "/tmp/config.json",
		"--upstream-proxy", "http://127.0.0.1:7890",
		"--socket", "/tmp/socket",
		"--db", "/tmp/usage.db",
		"--jsonl", "/tmp/usage.jsonl",
		"--listen-host", "localhost",
		"--listen-port", "18080",
		"--queue-size", "123",
		"--verbose",
	})
	if err != nil {
		t.Fatalf("parseRunConfig() error = %v", err)
	}
	if config.Root != "/tmp/project" || config.ListenPort != "18080" || config.QueueSize != 123 {
		t.Fatalf("parseRunConfig() = %+v", config)
	}
	if config.ConfigPath != "/tmp/config.json" || config.UpstreamProxy != "http://127.0.0.1:7890" {
		t.Fatalf("proxy config = %+v", config)
	}
	if !config.Verbose {
		t.Fatal("Verbose = false, want true")
	}
}

func TestApplyConfigFileSetsUpstreamProxy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"upstream_proxy":"http://127.0.0.1:7890"}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	config := RunConfig{ConfigPath: path}

	if err := applyConfigFile(&config); err != nil {
		t.Fatalf("applyConfigFile() error = %v", err)
	}
	if config.UpstreamProxy != "http://127.0.0.1:7890" {
		t.Fatalf("UpstreamProxy = %q", config.UpstreamProxy)
	}
}

func TestExplicitUpstreamProxyOverridesConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"upstream_proxy":"http://from-config:7890"}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	config := RunConfig{ConfigPath: path, UpstreamProxy: "https://from-cli:7890"}

	if err := applyConfigFile(&config); err != nil {
		t.Fatalf("applyConfigFile() error = %v", err)
	}
	if config.UpstreamProxy != "https://from-cli:7890" {
		t.Fatalf("UpstreamProxy = %q", config.UpstreamProxy)
	}
}

func TestApplyProxyEnvUsesEnvironment(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:7890")
	config := RunConfig{}

	if err := applyProxyEnv(&config); err != nil {
		t.Fatalf("applyProxyEnv() error = %v", err)
	}
	if config.UpstreamProxy != "http://127.0.0.1:7890" {
		t.Fatalf("UpstreamProxy = %q", config.UpstreamProxy)
	}
}

func TestValidateUpstreamProxyRejectsUnsupportedScheme(t *testing.T) {
	if err := validateUpstreamProxy("socks5://127.0.0.1:1080"); err == nil {
		t.Fatal("validateUpstreamProxy() error = nil")
	}
}

func clearProxyEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"OAI_METER_UPSTREAM_PROXY",
		"HTTPS_PROXY",
		"https_proxy",
		"HTTP_PROXY",
		"http_proxy",
		"ALL_PROXY",
		"all_proxy",
	} {
		t.Setenv(name, "")
	}
}

func TestFillDefaults(t *testing.T) {
	config := RunConfig{Root: "/tmp/project"}
	fillDefaults(&config)

	if config.Socket == "" {
		t.Fatal("Socket default is empty")
	}
	if config.DB != filepath.Join("/tmp/project", "data", "usage.db") {
		t.Fatalf("DB = %q", config.DB)
	}
	if config.JSONL != filepath.Join("/tmp/project", "data", "usage.jsonl") {
		t.Fatalf("JSONL = %q", config.JSONL)
	}
	if config.ListenHost != "127.0.0.1" || config.ListenPort != "8080" || config.QueueSize != 10000 {
		t.Fatalf("defaults = %+v", config)
	}
}
