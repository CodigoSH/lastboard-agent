package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
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

	// Catch-all: forward every path to Docker as-is — version is client-controlled.
	// /health is registered first so it takes priority without auth.
	dockerHandler := authMiddleware(token, dockerMiddleware(socketPath, http.HandlerFunc(proxyHandler)))
	mux.Handle("/", dockerHandler)

	srv := &http.Server{
		Addr:         "0.0.0.0:" + port,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
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

// dockerMiddleware creates an http.Client connected to the Docker Unix socket
// and attaches it and the base URL to the request context.
func dockerMiddleware(socketPath string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		transport := &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
			},
		}

		client := &http.Client{
			Transport: transport,
			Timeout:   60 * time.Second,
		}

		ctx := context.WithValue(r.Context(), contextKeyDockerClient, client)
		ctx = context.WithValue(ctx, contextKeyDockerBaseURL, "http://docker")

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// proxyHandler forwards the request to the Docker socket and copies the
// response back to the client unchanged.
func proxyHandler(w http.ResponseWriter, r *http.Request) {
	client := r.Context().Value(contextKeyDockerClient).(*http.Client)
	baseURL := r.Context().Value(contextKeyDockerBaseURL).(string)

	// Build the target URL: base + path + query string
	targetURL := baseURL + r.URL.Path
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	// Create the outgoing request, forwarding method, body, and headers
	var body io.Reader
	if r.Body != nil {
		body = r.Body
		defer r.Body.Close()
	}

	req, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, body)
	if err != nil {
		http.Error(w, "failed to create upstream request", http.StatusInternalServerError)
		return
	}
	req.Host = "localhost"

	// Forward relevant request headers
	for key, values := range r.Header {
		// Skip hop-by-hop headers
		switch strings.ToLower(key) {
		case "authorization", "connection", "te", "trailers", "transfer-encoding", "upgrade":
			continue
		}
		for _, v := range values {
			req.Header.Add(key, v)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "upstream Docker request failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for key, values := range resp.Header {
		for _, v := range values {
			w.Header().Add(key, v)
		}
	}

	// Copy status code and body
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// writeJSONError writes a JSON error response.
func writeJSONError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
