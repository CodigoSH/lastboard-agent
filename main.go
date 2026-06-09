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
	"regexp"
	"strings"
	"time"
)

// containerIDPattern matches a valid Docker container ID or name. It rejects
// path-traversal segments (".." fails the leading character class) and anything
// that is not a plausible identifier, before the request reaches the socket.
var containerIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

func main() {
	token := os.Getenv("TOKEN")
	if token == "" {
		log.Fatal("TOKEN environment variable is required but not set")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "2375"
	}

	socketPath := os.Getenv("SOCKET_PATH")
	if socketPath == "" {
		socketPath = "/var/run/docker.sock"
	}

	log.Printf("Lastboard Agent starting on port %s", port)
	log.Printf("Docker socket: %s", socketPath)

	proxy := newDockerProxy(socketPath)

	// Allowlist — the exact Docker API surface Lastboard uses. Any path or
	// method not registered here returns 404, so a leaked token can only
	// list and control containers, never create, delete, or exec into them.
	docker := http.NewServeMux()
	docker.Handle("GET /containers/json", proxy)
	docker.Handle("GET /containers/{id}/stats", requireValidID(proxy))
	docker.Handle("POST /containers/{id}/start", requireValidID(proxy))
	docker.Handle("POST /containers/{id}/stop", requireValidID(proxy))
	docker.Handle("POST /containers/{id}/restart", requireValidID(proxy))

	mux := http.NewServeMux()

	// Health endpoint — public, no auth required.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	// Everything else is authenticated first, then matched against the
	// allowlist. Unauthenticated requests get 401 without revealing which
	// routes exist; authenticated requests to disallowed routes get 404.
	mux.Handle("/", authMiddleware(token, docker))

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// We DO NOT set ReadTimeout and WriteTimeout because they break
		// long-running streaming connections like Docker stats/events.
	}

	log.Fatal(srv.ListenAndServe())
}

// newDockerProxy builds a reverse proxy that streams requests to the Docker
// Engine over its Unix socket. A single shared instance is safe for concurrent
// use and handles streaming responses (stats, logs) gracefully.
func newDockerProxy(socketPath string) *httputil.ReverseProxy {
	targetURL, _ := url.Parse("http://docker")
	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	proxy.Transport = &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("docker proxy error: %v", err)
		writeJSONError(w, http.StatusBadGateway, "docker unreachable")
	}
	return proxy
}

// authMiddleware validates the Bearer token from the Authorization header.
// Uses constant-time comparison to prevent timing attacks.
func authMiddleware(token string, next http.Handler) http.Handler {
	expected := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		if subtle.ConstantTimeCompare([]byte(parts[1]), expected) != 1 {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// requireValidID rejects requests whose {id} path segment is not a plausible
// container ID or name, as defense-in-depth before forwarding to the socket.
func requireValidID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !containerIDPattern.MatchString(r.PathValue("id")) {
			writeJSONError(w, http.StatusBadRequest, "invalid container id")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// writeJSONError writes a JSON error response.
func writeJSONError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
