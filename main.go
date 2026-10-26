package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// containerIDPattern matches a valid Docker container ID or name. It rejects
// path-traversal segments (".." fails the leading character class) and anything
// that is not a plausible identifier, before the request reaches the socket.
var containerIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

// version is stamped at build time with -ldflags "-X main.version=<tag>".
// A build that was not stamped says so rather than claiming a number.
var version = "dev"

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

	log.Printf("Lastboard Agent %s starting on port %s", version, port)
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

	// Read-only, for the Docker page: one container's details and its recent
	// log lines. The details leave with the environment reduced to the
	// variables' names — the values are where passwords and keys live, and
	// they never leave this machine. The logs are a bounded batch, never a
	// stream: follow is forced off and tail is capped.
	docker.Handle("GET /containers/{id}/json", requireValidID(onlyQuery(nil, nil, newInspectProxy(socketPath))))
	docker.Handle("GET /containers/{id}/logs", requireValidID(boundedLogs(newLogsProxy(socketPath))))

	mux := http.NewServeMux()

	// Health endpoint — public, no auth required. It carries the version so
	// an operator can tell which agent a host runs without the token.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": version})
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

// maxInspect and maxLogs bound what one answer from the daemon may weigh as it
// passes through. A container's details are a few kilobytes; a thousand log
// lines rarely reach a megabyte.
const (
	maxInspect = 2 << 20
	maxLogs    = 4 << 20
)

// newInspectProxy forwards a container's details with each environment
// variable cut down to its name. An answer that cannot be read as the details
// is not passed on as it came: it might carry the values.
func newInspectProxy(socketPath string) *httputil.ReverseProxy {
	proxy := newDockerProxy(socketPath)
	proxy.ModifyResponse = func(resp *http.Response) error {
		if resp.StatusCode != http.StatusOK {
			return nil
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, maxInspect+1))
		resp.Body.Close()
		if err != nil || len(raw) > maxInspect {
			return errUnreadableDetails
		}
		out, err := withoutEnvValues(raw)
		if err != nil {
			return errUnreadableDetails
		}
		resp.Body = io.NopCloser(bytes.NewReader(out))
		resp.ContentLength = int64(len(out))
		resp.Header.Set("Content-Length", strconv.Itoa(len(out)))
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("docker proxy error: %v", err)
		if errors.Is(err, errUnreadableDetails) {
			writeJSONError(w, http.StatusBadGateway, "unreadable container details")
			return
		}
		writeJSONError(w, http.StatusBadGateway, "docker unreachable")
	}
	return proxy
}

var errUnreadableDetails = errors.New("container details could not be read")

// withoutEnvValues rewrites Config.Env from ["NAME=value", …] to ["NAME", …].
func withoutEnvValues(raw []byte) ([]byte, error) {
	var details map[string]any
	if err := json.Unmarshal(raw, &details); err != nil {
		return nil, err
	}
	if config, ok := details["Config"].(map[string]any); ok {
		if env, ok := config["Env"].([]any); ok {
			names := make([]any, 0, len(env))
			for _, e := range env {
				if s, ok := e.(string); ok {
					name, _, _ := strings.Cut(s, "=")
					names = append(names, name)
				}
			}
			config["Env"] = names
		}
	}
	return json.Marshal(details)
}

// newLogsProxy forwards a batch of log lines, cut off at maxLogs.
func newLogsProxy(socketPath string) *httputil.ReverseProxy {
	proxy := newDockerProxy(socketPath)
	proxy.ModifyResponse = func(resp *http.Response) error {
		resp.Body = struct {
			io.Reader
			io.Closer
		}{io.LimitReader(resp.Body, maxLogs), resp.Body}
		resp.ContentLength = -1
		resp.Header.Del("Content-Length")
		return nil
	}
	return proxy
}

// maxLogTail is the most lines one request may ask for.
const maxLogTail = 1000

var logSince = regexp.MustCompile(`^[0-9]{1,12}(\.[0-9]{1,9})?$`)

// boundedLogs keeps only what Lastboard asks of the logs — which streams, with
// timestamps, how many lines, since when — and forces the rest: never follow,
// never more than maxLogTail lines.
func boundedLogs(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		in := r.URL.Query()
		out := url.Values{}
		for _, key := range []string{"stdout", "stderr", "timestamps"} {
			if v := in.Get(key); v == "1" || v == "true" {
				out.Set(key, "1")
			}
		}
		tail := 100
		if n, err := strconv.Atoi(in.Get("tail")); err == nil && n > 0 {
			tail = min(n, maxLogTail)
		}
		out.Set("tail", strconv.Itoa(tail))
		if since := in.Get("since"); logSince.MatchString(since) {
			out.Set("since", since)
		}
		out.Set("follow", "0")
		r = r.Clone(r.Context())
		r.URL.RawQuery = out.Encode()
		next.ServeHTTP(w, r)
	})
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
