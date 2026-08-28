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

type commandOptions struct {
	openUI bool
}

func main() {
	if err := runCommand(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		slog.Error("AIStudio2API failed to start", "error", err)
		os.Exit(1)
	}
}

func runCommand(args []string) error {
	cfg, err := config.Load(".env")
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if len(args) != 0 && args[0] == "setup" {
		return runSetup(ctx, cfg, args[1:])
	}
	options, err := parseFlags(args, &cfg)
	if err != nil {
		return err
	}
	service, admin, closeRuntime, err := newRuntime(ctx, cfg)
	if err != nil {
		return err
	}
	return errors.Join(runServer(ctx, cfg, options, service, admin), closeRuntime())
}

func parseFlags(args []string, cfg *config.Config) (commandOptions, error) {
	flags := flag.NewFlagSet("aistudio2api", flag.ContinueOnError)
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Initial setup: aistudio2api setup")
		fmt.Fprintln(flags.Output(), "Start server: aistudio2api [flags]")
		flags.PrintDefaults()
	}
	authStates := flags.String("auth", cfg.AuthStates, "Account auth state files, directories, or comma-separated paths")
	listenAddr := flags.String("listen", cfg.ListenAddr, "Server listen address")
	proxy := flags.String("proxy", cfg.Proxy, "HTTP, HTTPS, or SOCKS5 proxy to use")
	openUI := flags.Bool("open-ui", len(args) == 0, "Open admin UI in browser after startup")
	if err := flags.Parse(args); err != nil {
		return commandOptions{}, err
	}
	if flags.NArg() != 0 {
		return commandOptions{}, fmt.Errorf("unknown argument %q", flags.Arg(0))
	}

	cfg.AuthStates = strings.TrimSpace(*authStates)
	cfg.ListenAddr = strings.TrimSpace(*listenAddr)
	cfg.Proxy = strings.TrimSpace(*proxy)
	if err := cfg.Validate(); err != nil {
		return commandOptions{}, err
	}
	return commandOptions{openUI: *openUI}, nil
}

func runServer(ctx context.Context, cfg config.Config, options commandOptions, service aistudio.Service, admin *runtimeAdmin) error {
	admin.requests.log("service", "INFO", fmt.Sprintf("App startup | 4/4 | Listening HTTP | addr=%s", cfg.ListenAddr))
	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.ListenAddr, err)
	}
	apiHandler := api.NewHandler(service, api.Config{APIKey: cfg.ProxyAPIKey, Admin: admin})
	server := &http.Server{
		Handler:           rootHandler(apiHandler),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	serveError := make(chan error, 1)
	go func() {
		serveError <- server.Serve(listener)
	}()

	address := browserAddress(listener.Addr().String())
	admin.requests.log("service", "INFO", "Admin service ready | addr=http://"+address)
	if options.openUI {
		if err := openBrowser("http://" + address); err != nil {
			_ = server.Close()
			<-serveError
			return err
		}
		admin.requests.log("service", "INFO", "Admin UI opened | addr=http://"+address)
	}

	select {
	case err := <-serveError:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown HTTP server: %w", err)
		}
		if err := <-serveError; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

func rootHandler(apiHandler http.Handler) http.Handler {
	root := http.NewServeMux()
	root.Handle("/health", apiHandler)
	root.Handle("/api/", apiHandler)
	root.Handle("/v1/", apiHandler)
	root.Handle("/v1beta/", apiHandler)
	root.Handle("/", webui.Handler())
	return root
}

func browserAddress(address string) string {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return address
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

func openBrowser(url string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		command = exec.Command("open", url)
	default:
		command = exec.Command("xdg-open", url)
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("open admin UI: %w", err)
	}
	if err := command.Process.Release(); err != nil {
		return fmt.Errorf("release admin UI process: %w", err)
	}
	return nil
}
