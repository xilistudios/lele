package main

import (
	"context"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type webServerOptions struct {
	Host string
	Port int
}

func parseWebServerOptions(args []string) webServerOptions {
	opts := webServerOptions{Host: "0.0.0.0", Port: 3005}

	cfg, err := loadConfig()
	if err == nil && cfg.Channels.Web.Enabled && cfg.Channels.Web.Port != 0 {
		if cfg.Channels.Web.Host != "" {
			opts.Host = cfg.Channels.Web.Host
		}
		opts.Port = cfg.Channels.Web.Port
	}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--host":
			if i+1 < len(args) {
				opts.Host = args[i+1]
				i++
			}
		case "--port":
			if i+1 < len(args) {
				if port, err := strconv.Atoi(args[i+1]); err == nil && port > 0 {
					opts.Port = port
				}
				i++
			}
		}
	}
	return opts
}

func webCmd() {
	if len(os.Args) < 3 {
		webHelp()
		return
	}

	subcommand := os.Args[2]
	switch subcommand {
	case "start":
		webStartCmd(parseWebServerOptions(os.Args[3:]))
	case "stop":
		webStopCmd()
	case "status":
		webStatusCmd()
	case "serve":
		webServeCmd(parseWebServerOptions(os.Args[3:]))
	default:
		fmt.Printf("Unknown web command: %s\n", subcommand)
		webHelp()
	}
}

func webHelp() {
	fmt.Println("\nWeb commands:")
	fmt.Println("  start              Start the web server in background")
	fmt.Println("  stop               Stop the web server")
	fmt.Println("  status             Show web server status")
	fmt.Println()
	fmt.Println("Options:")
	fmt.Println("  --host <host>     Bind host (default: 0.0.0.0)")
	fmt.Println("  --port <port>     Bind port (default: 3005)")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  lele web start")
	fmt.Println("  lele web start --host 0.0.0.0 --port 3005")
	fmt.Println("  lele web stop")
	fmt.Println("  lele web status")
}

func webPIDPath() string {
	return filepath.Join(getLeleDir(), "web.pid")
}

func webLogPath() string {
	return filepath.Join(getLeleDir(), "logs", "web.log")
}

func webStartCmd(opts webServerOptions) {
	if running, pid := webServerRunning(); running {
		fmt.Printf("Web server already running (pid %d)\n", pid)
		return
	}

	exe, err := os.Executable()
	if err != nil {
		fmt.Printf("Error resolving executable: %v\n", err)
		os.Exit(1)
	}

	if err := os.MkdirAll(filepath.Dir(webLogPath()), 0755); err != nil {
		fmt.Printf("Error preparing log directory: %v\n", err)
		os.Exit(1)
	}

	logFile, err := os.OpenFile(webLogPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Printf("Error opening web log file: %v\n", err)
		os.Exit(1)
	}
	defer logFile.Close()

	cmd := exec.Command(exe, "web", "serve", "--host", opts.Host, "--port", strconv.Itoa(opts.Port))
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil

	if err := cmd.Start(); err != nil {
		fmt.Printf("Error starting web server: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(webPIDPath(), []byte(strconv.Itoa(cmd.Process.Pid)), 0644); err != nil {
		_ = cmd.Process.Kill()
		fmt.Printf("Error writing pid file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ Web server started on http://%s:%d (pid %d)\n", opts.Host, opts.Port, cmd.Process.Pid)
}

func webStopCmd() {
	pid, err := readWebPID()
	if err != nil {
		fmt.Println("Web server is not running")
		return
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		fmt.Printf("Error finding web server process: %v\n", err)
		_ = os.Remove(webPIDPath())
		return
	}

	_ = proc.Kill()
	_ = os.Remove(webPIDPath())
	fmt.Printf("✓ Web server stopped (pid %d)\n", pid)
}

func webStatusCmd() {
	pid, err := readWebPID()
	if err != nil {
		fmt.Println("Web server: stopped")
		return
	}

	if pid > 0 {
		fmt.Printf("Web server: running (pid %d)\n", pid)
		return
	}

	_ = os.Remove(webPIDPath())
	fmt.Println("Web server: stopped")
}

func webServeCmd(opts webServerOptions) {
	distFS, err := fs.Sub(embeddedFiles, "web/dist")
	if err != nil {
		fmt.Println("Web app assets are missing. Build the frontend with `make build` or `npm run build --prefix web`.")
		os.Exit(1)
	}

	addr := netJoinHostPort(opts.Host, opts.Port)
	handler := serveEmbeddedWebApp(distFS)
	server := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	defer func() {
		_ = os.Remove(webPIDPath())
	}()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	fmt.Printf("Serving web app on http://%s\n", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Printf("Web server error: %v\n", err)
		os.Exit(1)
	}
}

func serveEmbeddedWebApp(distFS fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" || strings.HasSuffix(path, "/") {
			path = "index.html"
		}

		if data, err := fs.ReadFile(distFS, path); err == nil {
			if ct := mime.TypeByExtension(filepath.Ext(path)); ct != "" {
				w.Header().Set("Content-Type", ct)
			} else if path == "index.html" {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
			}
			_, _ = w.Write(data)
			return
		}

		if path != "index.html" {
			if data, err := fs.ReadFile(distFS, "index.html"); err == nil {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = w.Write(data)
				return
			}
		}

		http.NotFound(w, r)
	})
}

func readWebPID() (int, error) {
	data, err := os.ReadFile(webPIDPath())
	if err != nil {
		return 0, err
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, err
	}

	return pid, nil
}

func webServerRunning() (bool, int) {
	pid, err := readWebPID()
	if err != nil {
		return false, 0
	}
	if pid > 0 {
		return true, pid
	}
	_ = os.Remove(webPIDPath())
	return false, 0
}

func netJoinHostPort(host string, port int) string {
	return fmt.Sprintf("%s:%d", host, port)
}
