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
	// sent `all=true` to the list and `stream=false` to stats (with `one-shot=true`
	// since the Docker page reads a host without waiting for the daemon's second
	// sample), and nothing else; any other parameter is dropped before the request
	// is forwarded.
	// Left open, a leaked token could still ask the daemon to size every
	// container on disk (`size=true`), hold a stats stream open for ever, or
	// stop a container with a timeout of days (`t=`).
	docker := http.NewServeMux()
	docker.Handle("GET /containers/json", onlyQuery([]string{"all"}, nil, proxy))
	docker.Handle("GET /containers/{id}/stats", requireValidID(statsQuery(proxy)))
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

	// Read-only, for the overview: how big the machine is, so that the CPU and
	// memory of its containers can be read against something. Docker's own
	// answer describes the whole host — its name, kernel, registries, swarm —
	// and only the two figures leave.
	docker.Handle("GET /info", onlyQuery(nil, nil, newInfoProxy(socketPath)))

	// Read-only, for the overview: how much disk Docker itself uses. Only the
	// sizes leave — not the image names and tags, the volume names and mount
	// points, or the build cache's records.
	// An engine of API 1.52 or later answers without its records unless asked
	// for them, and the sizes are in the records. The client's own query is
	// dropped; this one is the agent's.
	docker.Handle("GET /system/df", onlyQuery(nil, map[string]string{"verbose": "true"}, newDiskProxy(socketPath)))

	// Read-only: which engine answers behind this agent and its release, so the
	// dashboard can say Docker or Podman. The daemon's own answer names the
	// kernel, the platform and the build; only those two words leave.
	docker.Handle("GET /version", onlyQuery(nil, nil, newReducedProxy(socketPath, maxInfo, onlyEngine, "unreadable engine version")))

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

// maxInfo bounds what is read of the daemon's description of its host.
const maxInfo = 1 << 20

var errUnreadableInfo = errors.New("host information could not be read")

// newInfoProxy forwards the daemon's description of its host cut down to the
// number of processors and the memory. An answer that cannot be read as that
// description is not passed on as it came: it might carry far more.
func newInfoProxy(socketPath string) *httputil.ReverseProxy {
	return newReducedProxy(socketPath, maxInfo, onlyCapacity, "unreadable host information")
}

// maxDisk bounds what is read of the daemon's account of its disk use. It is
// larger than the host's description because it lists every image, container,
// volume and cache record the host has.
const maxDisk = 16 << 20

// newDiskProxy forwards the daemon's account of the disk it uses cut down to
// sizes.
func newDiskProxy(socketPath string) *httputil.ReverseProxy {
	return newReducedProxy(socketPath, maxDisk, onlySizes, "unreadable disk usage")
}

// newReducedProxy forwards a read-only answer after `reduce` has cut it down to
// what leaves this machine. An answer that is too long or that `reduce` cannot
// read is not passed on as it came: it might carry far more.
func newReducedProxy(socketPath string, limit int, reduce func([]byte) ([]byte, error), unreadable string) *httputil.ReverseProxy {
	proxy := newDockerProxy(socketPath)
	proxy.ModifyResponse = func(resp *http.Response) error {
		if resp.StatusCode != http.StatusOK {
			return nil
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
		resp.Body.Close()
		if err != nil || len(raw) > limit {
			return errUnreadableInfo
		}
		out, err := reduce(raw)
		if err != nil {
			return errUnreadableInfo
		}
		resp.Body = io.NopCloser(bytes.NewReader(out))
		resp.ContentLength = int64(len(out))
		resp.Header.Set("Content-Length", strconv.Itoa(len(out)))
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("docker proxy error: %v", err)
		if errors.Is(err, errUnreadableInfo) {
			writeJSONError(w, http.StatusBadGateway, unreadable)
			return
		}
		writeJSONError(w, http.StatusBadGateway, "docker unreachable")
	}
	return proxy
}

// onlySizes keeps, from the daemon's account of its disk use, the sizes and
// the counts that say what is in use — and nothing that names anything.
//
// The account comes in two shapes. The old one is flat (LayersSize, Images,
// Containers, Volumes, BuildCache); engines from the 1.52 API on, and Podman,
// answer one object per kind (ImageUsage, ContainerUsage, VolumeUsage,
// BuildCacheUsage) with the records under Items and leave the old keys empty.
// Both are read, and what leaves is always the old shape, so the page that
// reads it has one thing to understand whichever engine is behind this agent.
func onlySizes(raw []byte) ([]byte, error) {
	type usage struct {
		Size     int64 `json:"Size"`
		RefCount int64 `json:"RefCount"`
	}
	type image struct {
		Size       int64 `json:"Size"`
		SharedSize int64 `json:"SharedSize"`
		Containers int64 `json:"Containers"`
	}
	type container struct {
		SizeRw int64  `json:"SizeRw"`
		State  string `json:"State"`
	}
	type volume struct {
		UsageData *usage `json:"UsageData"`
	}
	type cache struct {
		Size  int64 `json:"Size"`
		InUse bool  `json:"InUse"`
	}
	type flat struct {
		LayersSize int64       `json:"LayersSize"`
		Images     []image     `json:"Images"`
		Containers []container `json:"Containers"`
		Volumes    []volume    `json:"Volumes"`
		BuildCache []cache     `json:"BuildCache"`
	}
	type kind[T any] struct {
		TotalSize int64 `json:"TotalSize"`
		Items     []T   `json:"Items"`
	}
	var modern struct {
		ImageUsage       *kind[image]     `json:"ImageUsage"`
		ImagesUsage      *kind[image]     `json:"ImagesUsage"`
		ContainerUsage   *kind[container] `json:"ContainerUsage"`
		ContainersUsage  *kind[container] `json:"ContainersUsage"`
		VolumeUsage      *kind[volume]    `json:"VolumeUsage"`
		VolumesUsage     *kind[volume]    `json:"VolumesUsage"`
		BuildCacheUsage  *kind[cache]     `json:"BuildCacheUsage"`
		BuildCachesUsage *kind[cache]     `json:"BuildCachesUsage"`
	}

	var in flat
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	// The same bytes read again for the other shape. A failure here leaves the
	// flat reading standing.
	_ = json.Unmarshal(raw, &modern)

	pick := func(a, b *kind[image]) *kind[image] {
		if a != nil {
			return a
		}
		return b
	}
	if img := pick(modern.ImageUsage, modern.ImagesUsage); img != nil && len(in.Images) == 0 {
		in.Images = img.Items
		if in.LayersSize == 0 {
			in.LayersSize = img.TotalSize
		}
	}
	if c := modern.ContainerUsage; c != nil && len(in.Containers) == 0 {
		in.Containers = c.Items
	} else if c := modern.ContainersUsage; c != nil && len(in.Containers) == 0 {
		in.Containers = c.Items
	}
	if v := modern.VolumeUsage; v != nil && len(in.Volumes) == 0 {
		in.Volumes = v.Items
	} else if v := modern.VolumesUsage; v != nil && len(in.Volumes) == 0 {
		in.Volumes = v.Items
	}
	if b := modern.BuildCacheUsage; b != nil && len(in.BuildCache) == 0 {
		in.BuildCache = b.Items
	} else if b := modern.BuildCachesUsage; b != nil && len(in.BuildCache) == 0 {
		in.BuildCache = b.Items
	}
	return json.Marshal(in)
}

// cpuInfoPath is where the kernel lists the processors it sees. A variable so
// a test can stand another file in its place.
var cpuInfoPath = "/proc/cpuinfo"

// physicalCores counts the cores of the host's processors, not their threads:
// the distinct (socket, core) pairs the kernel lists. The daemon reports only
// the threads, so a host that hyper-threads would read as twice its cores. 0
// means the kernel did not say (some ARM boards and virtual machines list no
// core numbers), and the figure is left out rather than guessed.
func physicalCores() int {
	raw, err := os.ReadFile(cpuInfoPath)
	if err != nil {
		return 0
	}
	cores := map[string]struct{}{}
	socket, core, haveCore := "", "", false
	flush := func() {
		if haveCore {
			cores[socket+"/"+core] = struct{}{}
		}
		socket, core, haveCore = "", "", false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "physical id":
			socket = strings.TrimSpace(value)
		case "core id":
			core, haveCore = strings.TrimSpace(value), true
		}
	}
	flush()
	return len(cores)
}

// onlyCapacity keeps NCPU and MemTotal of the daemon's answer, and adds the
// host's physical cores when the kernel lists them. Nothing else leaves.
func onlyCapacity(raw []byte) ([]byte, error) {
	var info struct {
		NCPU          int   `json:"NCPU"`
		MemTotal      int64 `json:"MemTotal"`
		PhysicalCores int   `json:"PhysicalCores,omitempty"`
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, err
	}
	// Never the daemon's own word: only what this host's kernel says.
	info.PhysicalCores = physicalCores()
	return json.Marshal(info)
}

// onlyEngine keeps which engine this is and its release. Podman names itself in
// the components it lists ("Podman Engine"); anything else is taken for Docker,
// which is what an engine that says nothing of the kind is.
func onlyEngine(raw []byte) ([]byte, error) {
	var in struct {
		Version    string `json:"Version"`
		Components []struct {
			Name string `json:"Name"`
		} `json:"Components"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	engine := "docker"
	for _, c := range in.Components {
		if strings.Contains(strings.ToLower(c.Name), "podman") {
			engine = "podman"
		}
	}
	version := in.Version
	if len(version) > 40 {
		version = version[:40]
	}
	return json.Marshal(struct {
		Engine  string `json:"engine"`
		Version string `json:"version"`
	}{engine, version})
}

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

// statsQuery forwards a stats request as a single reading, whatever the caller
// sent, and as a one-shot reading when it asked for exactly that.
//
// Docker streams stats forever unless told otherwise, and the server has no
// WriteTimeout (see main), so a stream would hold its connection for as long
// as the caller liked: stream=false is always set. A one-shot reading is
// answered at once, where an ordinary one waits for the daemon's next sample; it
// only ever costs the daemon less, so `true` is passed on and any other value is
// dropped. A daemon older than API 1.41 does not know the parameter and answers
// as it always did.
func statsQuery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out := url.Values{"stream": {"false"}}
		if r.URL.Query().Get("one-shot") == "true" {
			out.Set("one-shot", "true")
		}
		r = r.Clone(r.Context())
		r.URL.RawQuery = out.Encode()
		next.ServeHTTP(w, r)
	})
}

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
