package main

import (
	"context"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"os"
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
	if err == nil {
		// Use unified server port as default
		effectivePort := cfg.EffectiveServerPort()
		if effectivePort != 0 {
			opts.Port = effectivePort
		}
		if host := cfg.EffectiveServerHost(); host != "" {
			opts.Host = host
		}
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
	fmt.Println("\n💡 The web UI is served by the unified gateway server.")
	fmt.Println("   Use 'lele gateway' to start the web UI:")
	fmt.Println()
	cfg, err := loadConfig()
	if err == nil {
		host := cfg.EffectiveServerHost()
		port := cfg.EffectiveServerPort()
		if host == "0.0.0.0" {
			host = "127.0.0.1"
		}
		fmt.Printf("   → lele gateway\n")
		fmt.Printf("   → http://%s:%d\n", host, port)
	} else {
		fmt.Printf("   → lele gateway\n")
	}
	fmt.Println()
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

func netJoinHostPort(host string, port int) string {
	return fmt.Sprintf("%s:%d", host, port)
}
