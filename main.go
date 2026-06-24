package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/agentic/mcp-proxy/internal/api"
	"github.com/agentic/mcp-proxy/internal/auth"
	"github.com/agentic/mcp-proxy/internal/config"
	"github.com/agentic/mcp-proxy/internal/proxy"
	"github.com/agentic/mcp-proxy/internal/store"
)

//go:embed all:web/dist
var webFS embed.FS

func main() {
	// Load config
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Initialize store
	dbStore, err := store.New(cfg.DBPath)
	if err != nil {
		log.Fatalf("Failed to initialize store: %v", err)
	}
	defer dbStore.Close()

	// Initialize auth
	authSvc := auth.New(dbStore, cfg.JWTSecret)

	// Ensure default admin user exists (for password login fallback)
	if err := authSvc.EnsureDefaultAdmin(cfg.AdminUsername, cfg.AdminPassword); err != nil {
		log.Printf("Warning: failed to ensure default admin: %v", err)
	}

	// Initialize OIDC if configured
	if cfg.OIDCEnabled() {
		provider, err := auth.NewOIDCProvider(auth.OIDCConfig{
			Issuer:       cfg.OIDCIssuer,
			ClientID:     cfg.OIDCClientID,
			ClientSecret: cfg.OIDCClientSecret,
			RedirectURL:  cfg.OIDCRedirectURL,
		})
		if err != nil {
			log.Printf("Warning: OIDC initialization failed: %v (password login still available)", err)
		} else {
			authSvc.SetOIDCProvider(provider)
			log.Printf("OIDC enabled: issuer=%s client_id=%s", cfg.OIDCIssuer, cfg.OIDCClientID)
		}
	}

	// Initialize proxy manager
	proxyMgr := proxy.New(dbStore)
	proxyMgr.StartAll()

	// Initialize API handlers
	handlers := api.New(dbStore, proxyMgr, authSvc)

	// Build root mux
	mux := http.NewServeMux()

	// Setup API routes
	handlers.SetupRoutes(mux)

	// Serve embedded frontend
	if err := serveFrontend(mux, cfg); err != nil {
		log.Printf("Warning: frontend not embedded, serving from disk: %v", err)
		serveFrontendFromDisk(mux, cfg)
	}

	// Apply CORS middleware
	finalHandler := auth.CORSMiddleware(mux)

	// Create server
	srv := &http.Server{
		Addr:         cfg.ListenAddr(),
		Handler:      finalHandler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		log.Println("Shutting down...")
		proxyMgr.StopAll()
		srv.Close()
	}()

	log.Printf("MCP Proxy starting on http://0.0.0.0:%s", cfg.Port)
	log.Printf("Default admin: %s (set MCP_PROXY_ADMIN_PASS to change)", cfg.AdminUsername)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server failed: %v", err)
	}
}

func serveFrontend(mux *http.ServeMux, cfg *config.Config) error {
	// Try embedded files first
	distFS, err := fs.Sub(webFS, "web/dist")
	if err != nil {
		return err
	}

	if _, err := distFS.Open("index.html"); err != nil {
		return fmt.Errorf("no embedded index.html")
	}

	fileServer := http.FileServer(http.FS(distFS))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// SPA fallback: serve index.html for non-API, non-file routes
		path := r.URL.Path
		if path != "/" && !startsWith(path, "/api/") {
			// Check if file exists
			if _, err := fs.Stat(distFS, strings.TrimPrefix(path, "/")); err != nil {
				r.URL.Path = "/"
			}
		}
		fileServer.ServeHTTP(w, r)
	})

	log.Println("Serving embedded frontend")
	return nil
}

func serveFrontendFromDisk(mux *http.ServeMux, cfg *config.Config) {
	distPath := cfg.WebDistPath
	if _, err := os.Stat(distPath + "/index.html"); err != nil {
		log.Printf("Frontend dist not found at %s, API-only mode", distPath)
		return
	}

	fileServer := http.FileServer(http.Dir(distPath))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path != "/" && !startsWith(path, "/api/") {
			fullPath := distPath + path
			if _, err := os.Stat(fullPath); err != nil {
				r.URL.Path = "/"
			}
		}
		fileServer.ServeHTTP(w, r)
	})
	log.Printf("Serving frontend from %s", distPath)
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
