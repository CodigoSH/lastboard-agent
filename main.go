package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"
)

// contextKey is a private type for context keys in this package.
type contextKey string

const (
	contextKeyDockerClient  contextKey = "dockerClient"
	contextKeyDockerBaseURL contextKey = "dockerBaseURL"
)

func main() {
	token := os.Getenv("TOKEN")
	if token == "" {
		log.Fatal("TOKEN environment variable is required but not set")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "2377"
	}

	socketPath := os.Getenv("SOCKET_PATH")
	if socketPath == "" {
		socketPath = "/var/run/docker.sock"
	}

	log.Printf("Lastboard Agent starting on port %s", port)
	log.Printf("Docker socket: %s", socketPath)

	mux := http.NewServeMux()

	// Health endpoint — public, no auth required
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	// Create a single shared ReverseProxy to safely stream to the Docker socket
	targetURL, _ := url.Parse("http://docker")
	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	proxy.Transport = &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
	}

	// Catch-all: forward every path to Docker as-is — version is client-controlled.
	// /health is registered first so it takes priority without auth.
	dockerHandler := authMiddleware(token, proxy)
	mux.Handle("/{path...}", dockerHandler)

	srv := &http.Server{
		Addr:              "0.0.0.0:" + port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// We DO NOT set ReadTimeout and WriteTimeout because they break
		// long-running streaming connections like Docker stats/events.
	}

	log.Fatal(srv.ListenAndServe())
}

// authMiddleware validates the Bearer token from the Authorization header.
// Uses constant-time comparison to prevent timing attacks.
func authMiddleware(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		provided := []byte(parts[1])
		expected := []byte(token)

		if subtle.ConstantTimeCompare(provided, expected) != 1 {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// _ Removed dockerMiddleware and proxyHandler since httputil.ReverseProxy handles streaming gracefully

// writeJSONError writes a JSON error response.
func writeJSONError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
