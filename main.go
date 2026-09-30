package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
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
	if msg := weakTokenWarning(token); msg != "" {
		log.Printf("WARNING: %s", msg)
	}

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           newHandler(token, socketPath),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// We DO NOT set ReadTimeout and WriteTimeout because they break
		// long-running streaming connections like Docker stats/events.
	}

	log.Fatal(srv.ListenAndServe())
}

// minTokenLength is what `openssl rand -hex 32` gives with room to spare.
// Counted in bytes on purpose: this is about how hard the token is to guess,
// not how much of it fits on a screen.
const minTokenLength = 32

// exampleTokens are the placeholders the documentation shows. Someone who
// copied the compose file without changing it is running with a token that
// is published on the internet.
var exampleTokens = []string{
	"change-this-to-a-secret-token",
	"your-secret-token",
	"your_secure_random_token",
}

// weakTokenWarning says what is wrong with a token, or "" if nothing is.
//
// It only warns. Refusing to start would take down every agent that updates
// through `:latest` with a short token, and the operator would find out from a
// dashboard that stopped showing containers, not from this message.
func weakTokenWarning(token string) string {
	for _, example := range exampleTokens {
		if token == example {
			return "TOKEN is the example value from the documentation. " +
				"Anyone who has read it can control your containers. " +
				"Generate a real one with: openssl rand -hex 32"
		}
	}
	if len(token) < minTokenLength {
		return fmt.Sprintf("TOKEN is %d characters long; use at least %d. "+
			"Generate one with: openssl rand -hex 32", len(token), minTokenLength)
	}
	return ""
}

// newHandler builds everything the agent serves: the public health endpoint
// and the authenticated allowlist in front of the Docker socket.
func newHandler(token, socketPath string) http.Handler {
	proxy := newDockerProxy(socketPath)

	// Allowlist — the exact Docker API surface Lastboard uses. Any path or
	// method not registered here returns 404, so a leaked token can only
	// list and control containers, never create, delete, or exec into them.
	//
	// The query string is part of that surface. Every Lastboard release has
	// sent `all=true` to the list and `stream=false` to stats, and nothing
	// else; any other parameter is dropped before the request is forwarded.
	// Left open, a leaked token could still ask the daemon to size every
	// container on disk (`size=true`), hold a stats stream open for ever, or
	// stop a container with a timeout of days (`t=`).
	docker := http.NewServeMux()
	docker.Handle("GET /containers/json", onlyQuery([]string{"all"}, nil, proxy))
	docker.Handle("GET /containers/{id}/stats", requireValidID(onlyQuery(nil, singleReading, proxy)))
	docker.Handle("POST /containers/{id}/start", requireValidID(onlyQuery(nil, nil, proxy)))
	docker.Handle("POST /containers/{id}/stop", requireValidID(onlyQuery(nil, nil, proxy)))
	docker.Handle("POST /containers/{id}/restart", requireValidID(onlyQuery(nil, nil, proxy)))

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

	return mux
}

// newDockerProxy builds a reverse proxy that streams requests to the Docker
// Engine over its Unix socket. A single shared instance is safe for concurrent
// use and handles streaming responses (stats, logs) gracefully.
func newDockerProxy(socketPath string) *httputil.ReverseProxy {
	targetURL, _ := url.Parse("http://docker")
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(targetURL)
			// The token is the agent's, not Docker's. The daemon ignores it,
			// but there is no reason to hand it on past the check.
			pr.Out.Header.Del("Authorization")
		},
	}
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

// singleReading forces stats to answer once and close.
//
// Docker streams stats forever unless told otherwise, and the server has no
// WriteTimeout (see main), so a stream would hold its connection for as long
// as the caller liked. Lastboard only ever asks for one reading.
var singleReading = map[string]string{"stream": "false"}

// onlyQuery forwards the request with the query reduced to the keys in keep,
// and each key in force set to its value whatever the caller sent.
func onlyQuery(keep []string, force map[string]string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		in := r.URL.Query()
		out := url.Values{}
		for _, key := range keep {
			if values, ok := in[key]; ok {
				out[key] = values
			}
		}
		for key, value := range force {
			out.Set(key, value)
		}
		r = r.Clone(r.Context())
		r.URL.RawQuery = out.Encode()
		next.ServeHTTP(w, r)
	})
}

// writeJSONError writes a JSON error response.
func writeJSONError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
