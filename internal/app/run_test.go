package app

import (
	"path/filepath"
	"testing"
)

func TestParseRunConfig(t *testing.T) {
	config, err := parseRunConfig([]string{
		"--root", "/tmp/project",
		"--mitmdump", "/tmp/mitmdump",
		"--socket", "/tmp/socket",
		"--db", "/tmp/usage.db",
		"--jsonl", "/tmp/usage.jsonl",
		"--listen-host", "localhost",
		"--listen-port", "18080",
		"--queue-size", "123",
	})
	if err != nil {
		t.Fatalf("parseRunConfig() error = %v", err)
	}
	if config.Root != "/tmp/project" || config.ListenPort != "18080" || config.QueueSize != 123 {
		t.Fatalf("parseRunConfig() = %+v", config)
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
