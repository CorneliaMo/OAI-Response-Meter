package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/cornelia/oai-response-meter/internal/daemon"
	"github.com/cornelia/oai-response-meter/internal/dashboard"
	"github.com/cornelia/oai-response-meter/internal/mitmwrap"
	"github.com/cornelia/oai-response-meter/internal/store"
)

type RunConfig struct {
	Root          string
	Mitmdump      string
	Socket        string
	DB            string
	JSONL         string
	ListenHost    string
	ListenPort    string
	NoDashboard   bool
	DashboardHost string
	DashboardPort string
	QueueSize     int
	Verbose       bool
	ConfigPath    string
	UpstreamProxy string
}

type fileConfig struct {
	UpstreamProxy string `json:"upstream_proxy"`
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
	if err := applyConfigFile(&config); err != nil {
		return err
	}
	if err := applyProxyEnv(&config); err != nil {
		return err
	}
	fillDefaults(&config)

	mitmdumpPath, err := mitmwrap.ResolveMitmdump(config.Root, config.Mitmdump)
	if err != nil {
		return err
	}
	if config.Verbose {
		fmt.Fprintf(os.Stderr, "[app] root=%s\n", config.Root)
		fmt.Fprintf(os.Stderr, "[app] mitmdump=%s\n", mitmdumpPath)
		fmt.Fprintf(os.Stderr, "[app] addon=%s\n", filepath.Join(config.Root, "mitm", "addon.py"))
		fmt.Fprintf(os.Stderr, "[app] socket=%s db=%s jsonl=%s\n", config.Socket, config.DB, config.JSONL)
		fmt.Fprintf(os.Stderr, "[app] proxy listen=%s:%s queue_size=%d\n", config.ListenHost, config.ListenPort, config.QueueSize)
		if config.UpstreamProxy == "" {
			fmt.Fprintln(os.Stderr, "[app] upstream_proxy=<none>")
		} else {
			fmt.Fprintf(os.Stderr, "[app] upstream_proxy=%s\n", config.UpstreamProxy)
		}
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
		Verbose:       config.Verbose,
	}, sink)
	if err != nil {
		return err
	}

	var dashboardServer *dashboard.Server
	if !config.NoDashboard {
		dashboardServer, err = dashboard.Start(ctx, dashboard.Config{
			Addr:   net.JoinHostPort(config.DashboardHost, config.DashboardPort),
			DBPath: config.DB,
		})
		if err != nil {
			return err
		}
		defer dashboardServer.Close(context.Background())
		fmt.Fprintf(os.Stdout, "dashboard: %s\n", dashboardServer.URL())
	}

	daemonDone := make(chan error, 1)
	go func() { daemonDone <- usageDaemon.Run(ctx) }()
	if err := waitForSocket(ctx, config.Socket, 2*time.Second); err != nil {
		stop()
		return err
	}

	cmd, err := mitmwrap.Command(ctx, mitmwrap.Config{
		MitmdumpPath:  mitmdumpPath,
		AddonPath:     filepath.Join(config.Root, "mitm", "addon.py"),
		SocketPath:    config.Socket,
		ListenHost:    config.ListenHost,
		ListenPort:    config.ListenPort,
		QueueSize:     config.QueueSize,
		Quiet:         true,
		UpstreamProxy: config.UpstreamProxy,
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
	fs.StringVar(&config.ConfigPath, "config", "", "json config file path")
	fs.StringVar(&config.UpstreamProxy, "upstream-proxy", "", "upstream explicit HTTP(S) proxy URL")
	fs.StringVar(&config.ListenHost, "listen-host", "127.0.0.1", "mitmproxy listen host")
	fs.StringVar(&config.ListenPort, "listen-port", "8080", "mitmproxy listen port")
	fs.BoolVar(&config.NoDashboard, "no-dashboard", false, "disable local dashboard")
	fs.StringVar(&config.DashboardHost, "dashboard-host", "127.0.0.1", "dashboard listen host")
	fs.StringVar(&config.DashboardPort, "dashboard-port", "8081", "dashboard listen port")
	fs.IntVar(&config.QueueSize, "queue-size", 10000, "addon queue size")
	fs.BoolVar(&config.Verbose, "verbose", false, "print detailed sanitized debug logs")
	if err := fs.Parse(args); err != nil {
		return RunConfig{}, err
	}
	return config, nil
}

func applyConfigFile(config *RunConfig) error {
	if config.ConfigPath == "" {
		return nil
	}
	data, err := os.ReadFile(config.ConfigPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	var file fileConfig
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}
	if config.UpstreamProxy == "" && file.UpstreamProxy != "" {
		config.UpstreamProxy = file.UpstreamProxy
	}
	return validateUpstreamProxy(config.UpstreamProxy)
}

func applyProxyEnv(config *RunConfig) error {
	if config.UpstreamProxy != "" {
		return validateUpstreamProxy(config.UpstreamProxy)
	}
	for _, name := range []string{
		"OAI_METER_UPSTREAM_PROXY",
		"HTTPS_PROXY",
		"https_proxy",
		"HTTP_PROXY",
		"http_proxy",
		"ALL_PROXY",
		"all_proxy",
	} {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			continue
		}
		if err := validateUpstreamProxy(value); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		config.UpstreamProxy = value
		return nil
	}
	return nil
}

func validateUpstreamProxy(proxy string) error {
	if strings.TrimSpace(proxy) == "" {
		return nil
	}
	parsed, err := url.Parse(proxy)
	if err != nil {
		return fmt.Errorf("invalid upstream proxy %q: %w", proxy, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("upstream proxy %q must use http:// or https://", proxy)
	}
	if parsed.Host == "" {
		return fmt.Errorf("upstream proxy %q must include a host", proxy)
	}
	return nil
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
	if config.DashboardHost == "" {
		config.DashboardHost = "127.0.0.1"
	}
	if config.DashboardPort == "" {
		config.DashboardPort = "8081"
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
