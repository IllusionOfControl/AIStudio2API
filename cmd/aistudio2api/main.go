package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
	"github.com/Mag1cFall/AIStudio2API/internal/api"
	"github.com/Mag1cFall/AIStudio2API/internal/config"
	"github.com/Mag1cFall/AIStudio2API/internal/webui"
)

const (
	defaultConfigPath = ".env"
	serverReadTimeout = 10 * time.Second
	serverIdleTimeout = 2 * time.Minute
	shutdownTimeout   = 10 * time.Second
)

type cliOptions struct {
	configPath string
	authStates string
	listenAddr string
	proxyAddr  string
	openUI     bool
	autoStart  bool
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		slog.Error("AIStudio2API failed to start", "error", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if len(args) != 0 && args[0] == "setup" {
		cfg, err := config.Load(defaultConfigPath)
		if err != nil {
			return fmt.Errorf("failed to load configuration for setup: %w", err)
		}
		return runSetup(ctx, cfg, args[1:])
	}

	opts, err := parseFlags(args)
	if err != nil {
		return err
	}

	cfg, err := loadAndMergeConfig(opts)
	if err != nil {
		return err
	}

	service, admin, closeRuntime, err := newRuntime(ctx, cfg)
	if err != nil {
		return fmt.Errorf("runtime initialization failed: %w", err)
	}
	defer func() {
		if closeErr := closeRuntime(); closeErr != nil {
			slog.Error("Failed to cleanly close runtime resources", "error", closeErr)
		}
	}()

	return runServer(ctx, cfg, opts, service, admin)
}

func parseFlags(args []string) (cliOptions, error) {
	flags := flag.NewFlagSet("aistudio2api", flag.ContinueOnError)
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "AIStudio2API - Google AI Studio to OpenAI/Anthropic/Gemini Gateway")
		fmt.Fprintln(flags.Output(), "\nUsage:")
		fmt.Fprintln(flags.Output(), "  First-time setup:  aistudio2api setup [options]")
		fmt.Fprintln(flags.Output(), "  Start server:      aistudio2api [flags]")
		fmt.Fprintln(flags.Output(), "\nFlags:")
		flags.PrintDefaults()
	}

	var opts cliOptions

	flags.StringVar(&opts.configPath, "config", defaultConfigPath, "Path to environment configuration file")
	flags.StringVar(&opts.authStates, "auth", "", "Path to account states file/directory (overrides config)")
	flags.StringVar(&opts.listenAddr, "listen", "", "HTTP listen address, e.g., 127.0.0.1:2048 (overrides config)")
	flags.StringVar(&opts.proxyAddr, "proxy", "", "Outbound HTTP/HTTPS/SOCKS5 proxy URL (overrides config)")
	flags.BoolVar(&opts.openUI, "open-ui", len(args) == 0, "Automatically open the admin web UI in default browser on launch")
	flags.BoolVar(&opts.autoStart, "auto-start", false, "Automatically start the generation service on server startup")

	if err := flags.Parse(args); err != nil {
		return cliOptions{}, err
	}
	if flags.NArg() != 0 {
		return cliOptions{}, fmt.Errorf("unknown positional argument %q", flags.Arg(0))
	}

	return opts, nil
}

func loadAndMergeConfig(opts cliOptions) (config.Config, error) {
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		return config.Config{}, fmt.Errorf("failed to load configuration from %s: %w", opts.configPath, err)
	}

	if strings.TrimSpace(opts.authStates) != "" {
		cfg.AuthStates = strings.TrimSpace(opts.authStates)
	}
	if strings.TrimSpace(opts.listenAddr) != "" {
		cfg.ListenAddr = strings.TrimSpace(opts.listenAddr)
	}
	if strings.TrimSpace(opts.proxyAddr) != "" {
		cfg.Proxy = strings.TrimSpace(opts.proxyAddr)
	}

	if err := cfg.Validate(); err != nil {
		return config.Config{}, fmt.Errorf("invalid merged configuration: %w", err)
	}

	return cfg, nil
}

func runServer(ctx context.Context, cfg config.Config, options cliOptions, service aistudio.Service, admin *runtimeAdmin) error {
	admin.requests.Log("service", "INFO", fmt.Sprintf("App startup | 4/4 | Listening HTTP | addr=%s", cfg.ListenAddr))

	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("failed to bind addr %s: %w", cfg.ListenAddr, err)
	}

	apiHandler := api.NewHandler(service, api.Config{
		APIKey: cfg.ProxyAPIKey,
		Admin:  admin,
	})

	server := &http.Server{
		Handler:           buildRootMux(apiHandler),
		ReadHeaderTimeout: serverReadTimeout,
		IdleTimeout:       serverIdleTimeout,
	}

	serveErrCh := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErrCh <- err
		}
		close(serveErrCh)
	}()

	addr := resolveBrowserAddress(listener.Addr().String())
	admin.requests.Log("service", "INFO", fmt.Sprintf("Admin service ready | addr=http://%s", addr))

	if options.openUI {
		if err := openSystemBrowser("http://" + addr); err != nil {
			slog.Warn("Failed to open browser automatically", "error", err)
		} else {
			admin.requests.Log("service", "INFO", "Admin UI opened | addr=http://"+addr)
		}
	}

	select {
	case err := <-serveErrCh:
		return err
	case <-ctx.Done():
		slog.Info("Shutdown signal received, draining active connections...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("graceful shutdown failed: %w", err)
		}
		return <-serveErrCh
	}
}

func buildRootMux(apiHandler http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/health", apiHandler)
	mux.Handle("/api/", apiHandler)
	mux.Handle("/v1/", apiHandler)
	mux.Handle("/v1beta/", apiHandler)
	mux.Handle("/", webui.Handler())
	return mux
}

func resolveBrowserAddress(address string) string {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return address
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

func openSystemBrowser(url string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("unable to launch browser: %w", err)
	}
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("failed to detach browser process: %w", err)
	}
	return nil
}
