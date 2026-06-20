package mitmwrap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

type Config struct {
	MitmdumpPath string
	AddonPath    string
	SocketPath   string
	ListenHost   string
	ListenPort   string
	QueueSize    int
	Quiet        bool
	Verbose      bool
}

func DefaultMitmdumpPath(root string) string {
	return filepath.Join(root, "bin", "mitmdump")
}

func ResolveMitmdump(root, explicit string) (string, error) {
	if explicit != "" {
		return requireExecutable(explicit)
	}
	local := DefaultMitmdumpPath(root)
	if path, err := requireExecutable(local); err == nil {
		return path, nil
	}
	path, err := exec.LookPath("mitmdump")
	if err != nil {
		return "", fmt.Errorf("mitmdump not found; place it at %s or pass --mitmdump", local)
	}
	return path, nil
}

func Command(ctx context.Context, config Config) (*exec.Cmd, error) {
	if config.MitmdumpPath == "" {
		return nil, errors.New("mitmdump path is required")
	}
	if config.AddonPath == "" {
		return nil, errors.New("addon path is required")
	}
	if config.SocketPath == "" {
		return nil, errors.New("socket path is required")
	}
	if config.ListenHost == "" {
		config.ListenHost = "127.0.0.1"
	}
	if config.ListenPort == "" {
		config.ListenPort = "8080"
	}
	if config.QueueSize <= 0 {
		config.QueueSize = 10000
	}

	args := []string{
		"-s", config.AddonPath,
		"--listen-host", config.ListenHost,
		"--listen-port", config.ListenPort,
	}
	if config.Quiet {
		args = append(args, "--quiet")
	}
	cmd := exec.CommandContext(ctx, config.MitmdumpPath, args...)
	cmd.Env = append(os.Environ(),
		"OAI_METER_SOCKET="+config.SocketPath,
		fmt.Sprintf("OAI_METER_QUEUE_SIZE=%d", config.QueueSize),
	)
	if config.Verbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
	}
	cmd.Stdin = os.Stdin
	return cmd, nil
}

func requireExecutable(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory", path)
	}
	if info.Mode()&0o111 == 0 {
		return "", fmt.Errorf("%s is not executable", path)
	}
	return path, nil
}
