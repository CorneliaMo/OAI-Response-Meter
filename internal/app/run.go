package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/cornelia/oai-response-meter/internal/daemon"
	"github.com/cornelia/oai-response-meter/internal/mitmwrap"
	"github.com/cornelia/oai-response-meter/internal/store"
)

type RunConfig struct {
	Root       string
	Mitmdump   string
	Socket     string
	DB         string
	JSONL      string
	ListenHost string
	ListenPort string
	QueueSize  int
}

func RunCommand(args []string) error {
	config, err := parseRunConfig(args)
	if err != nil {
		return err
	}
	return Run(context.Background(), config)
}

func Run(ctx context.Context, config RunConfig) error {
	if config.Root == "" {
		root, err := os.Getwd()
		if err != nil {
			return err
		}
		config.Root = root
	}
	fillDefaults(&config)

	mitmdumpPath, err := mitmwrap.ResolveMitmdump(config.Root, config.Mitmdump)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	sink, err := store.Open(ctx, config.DB, config.JSONL)
	if err != nil {
		return err
	}
	defer sink.Close()

	usageDaemon, err := daemon.New(daemon.Config{
		SocketPath:    config.Socket,
		BatchSize:     100,
		FlushInterval: 500 * time.Millisecond,
	}, sink)
	if err != nil {
		return err
	}

	daemonDone := make(chan error, 1)
	go func() { daemonDone <- usageDaemon.Run(ctx) }()
	if err := waitForSocket(ctx, config.Socket, 2*time.Second); err != nil {
		stop()
		return err
	}

	cmd, err := mitmwrap.Command(ctx, mitmwrap.Config{
		MitmdumpPath: mitmdumpPath,
		AddonPath:    filepath.Join(config.Root, "mitm", "addon.py"),
		SocketPath:   config.Socket,
		ListenHost:   config.ListenHost,
		ListenPort:   config.ListenPort,
		QueueSize:    config.QueueSize,
	})
	if err != nil {
		stop()
		return err
	}
	if err := cmd.Start(); err != nil {
		stop()
		return fmt.Errorf("start mitmdump: %w", err)
	}

	waitErr := cmd.Wait()
	stop()
	daemonErr := <-daemonDone
	if waitErr != nil && !errors.Is(ctx.Err(), context.Canceled) {
		return fmt.Errorf("mitmdump exited: %w", waitErr)
	}
	return daemonErr
}

func parseRunConfig(args []string) (RunConfig, error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var config RunConfig
	fs.StringVar(&config.Root, "root", "", "project root")
	fs.StringVar(&config.Mitmdump, "mitmdump", "", "mitmdump binary path")
	fs.StringVar(&config.Socket, "socket", "", "unix datagram socket path")
	fs.StringVar(&config.DB, "db", "", "sqlite database path")
	fs.StringVar(&config.JSONL, "jsonl", "", "jsonl audit log path")
	fs.StringVar(&config.ListenHost, "listen-host", "127.0.0.1", "mitmproxy listen host")
	fs.StringVar(&config.ListenPort, "listen-port", "8080", "mitmproxy listen port")
	fs.IntVar(&config.QueueSize, "queue-size", 10000, "addon queue size")
	if err := fs.Parse(args); err != nil {
		return RunConfig{}, err
	}
	return config, nil
}

func fillDefaults(config *RunConfig) {
	dataDir := filepath.Join(config.Root, "data")
	if config.Socket == "" {
		config.Socket = filepath.Join(os.TempDir(), "oai-meter.sock")
	}
	if config.DB == "" {
		config.DB = filepath.Join(dataDir, "usage.db")
	}
	if config.JSONL == "" {
		config.JSONL = filepath.Join(dataDir, "usage.jsonl")
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
}

func waitForSocket(ctx context.Context, socketPath string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(socketPath); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	return fmt.Errorf("socket %s was not created within %s", socketPath, timeout)
}
