package mitmwrap

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolveMitmdumpPrefersExplicitPath(t *testing.T) {
	dir := t.TempDir()
	explicit := filepath.Join(dir, executableName("custom-mitmdump"))
	writeExecutable(t, explicit)

	got, err := ResolveMitmdump(dir, explicit)
	if err != nil {
		t.Fatalf("ResolveMitmdump() error = %v", err)
	}
	if got != explicit {
		t.Fatalf("ResolveMitmdump() = %q, want %q", got, explicit)
	}
}

func TestResolveMitmdumpUsesProjectBin(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, "bin", executableName("mitmdump"))
	writeExecutable(t, local)

	got, err := ResolveMitmdump(dir, "")
	if err != nil {
		t.Fatalf("ResolveMitmdump() error = %v", err)
	}
	if got != local {
		t.Fatalf("ResolveMitmdump() = %q, want %q", got, local)
	}
}

func TestResolveMitmdumpReportsMissingBinary(t *testing.T) {
	dir := t.TempDir()
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", dir)
	t.Cleanup(func() { os.Setenv("PATH", oldPath) })

	_, err := ResolveMitmdump(dir, "")
	if err == nil {
		t.Fatal("ResolveMitmdump() error = nil")
	}
	if !strings.Contains(err.Error(), "place it at") {
		t.Fatalf("ResolveMitmdump() error = %v", err)
	}
}

func TestCommandBuildsMitmdumpArgsAndEnv(t *testing.T) {
	cmd, err := Command(context.Background(), Config{
		MitmdumpPath:  "/tmp/mitmdump",
		AddonPath:     "/tmp/addon.py",
		SocketPath:    "/tmp/oai-meter.sock",
		ListenHost:    "127.0.0.1",
		ListenPort:    "18080",
		QueueSize:     123,
		Quiet:         true,
		UpstreamProxy: "http://127.0.0.1:7890",
	})
	if err != nil {
		t.Fatalf("Command() error = %v", err)
	}
	wantArgs := []string{"/tmp/mitmdump", "-s", "/tmp/addon.py", "--listen-host", "127.0.0.1", "--listen-port", "18080", "--mode", "upstream:http://127.0.0.1:7890", "--quiet"}
	if strings.Join(cmd.Args, "\x00") != strings.Join(wantArgs, "\x00") {
		t.Fatalf("Args = %#v, want %#v", cmd.Args, wantArgs)
	}
	if !containsEnv(cmd.Env, "OAI_METER_SOCKET=/tmp/oai-meter.sock") {
		t.Fatalf("Env missing socket: %#v", cmd.Env)
	}
	if !containsEnv(cmd.Env, "OAI_METER_QUEUE_SIZE=123") {
		t.Fatalf("Env missing queue size: %#v", cmd.Env)
	}
}

func writeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func executableName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func containsEnv(env []string, needle string) bool {
	for _, item := range env {
		if item == needle {
			return true
		}
	}
	return false
}
