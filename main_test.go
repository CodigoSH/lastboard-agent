package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const testToken = "0123456789abcdef0123456789abcdef"

// fakeDocker stands in for the Docker socket and remembers every request that
// reached it. The allowlist's promise is about what gets through to the
// daemon, so that is what these tests look at — a 404 from the agent means
// nothing if the request was forwarded anyway.
type fakeDocker struct {
	mu   sync.Mutex
	hits []*http.Request
	// answers, by path, what the daemon says; anything else gets {}.
	answers map[string]string
}

func (f *fakeDocker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.hits = append(f.hits, r)
	answer, ok := f.answers[r.URL.Path]
	f.mu.Unlock()
	if !ok {
		answer = `{}`
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(answer))
}

func (f *fakeDocker) answer(path, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.answers == nil {
		f.answers = map[string]string{}
	}
	f.answers[path] = body
}

func (f *fakeDocker) received() []*http.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*http.Request(nil), f.hits...)
}

// newAgent starts a fake Docker daemon on a Unix socket and the agent in
// front of it.
func newAgent(t *testing.T) (*httptest.Server, *fakeDocker) {
	t.Helper()

	socket := filepath.Join(t.TempDir(), "docker.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen on fake socket: %v", err)
	}
	docker := &fakeDocker{}
	daemon := &http.Server{Handler: docker}
	go daemon.Serve(ln)
	t.Cleanup(func() { daemon.Close() })

	agent := httptest.NewServer(newHandler(testToken, socket))
	t.Cleanup(agent.Close)
	return agent, docker
}

func call(t *testing.T, agent *httptest.Server, method, path, token string) int {
	t.Helper()
	req, err := http.NewRequest(method, agent.URL+path, nil)
	if err != nil {
		t.Fatalf("build %s %s: %v", method, path, err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	// Report redirects as they are instead of following them: a redirect
	// that ended up on an allowed route would be a way through.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// The five calls Lastboard makes reach the daemon, unchanged.
func TestAllowedRoutesReachDocker(t *testing.T) {
	agent, docker := newAgent(t)

	allowed := []struct{ method, path string }{
		{"GET", "/containers/json?all=true"},
		{"GET", "/containers/abc123/stats?stream=false"},
		{"POST", "/containers/abc123/start"},
		{"POST", "/containers/my_app.web-1/stop"},
		{"POST", "/containers/abc123/restart"},
		{"GET", "/containers/abc123/json"},
		{"GET", "/containers/abc123/logs?stdout=1&stderr=1&timestamps=1&tail=100"},
	}
	for _, c := range allowed {
		if got := call(t, agent, c.method, c.path, testToken); got != http.StatusOK {
			t.Errorf("%s %s: status %d, want 200", c.method, c.path, got)
		}
	}

	hits := docker.received()
	if len(hits) != len(allowed) {
		t.Fatalf("docker received %d requests, want %d", len(hits), len(allowed))
	}
	for i, c := range allowed {
		path, _, _ := strings.Cut(c.path, "?")
		if hits[i].Method != c.method || hits[i].URL.Path != path {
			t.Errorf("docker received %s %s, want %s %s",
				hits[i].Method, hits[i].URL.Path, c.method, path)
		}
	}
	if got := hits[0].URL.Query().Get("all"); got != "true" {
		t.Errorf("list lost its query: all=%q, want \"true\"", got)
	}
}

// Everything outside the allowlist is refused, even with a valid token, and
// never reaches the daemon. These are the calls that would turn a leaked
// token into control of the host.
func TestEverythingElseIsRefused(t *testing.T) {
	agent, docker := newAgent(t)

	refused := []struct{ method, path string }{
		{"POST", "/containers/create"},
		{"POST", "/containers/abc123/exec"},
		{"POST", "/exec/abc123/start"},
		{"DELETE", "/containers/abc123"},
		{"POST", "/containers/abc123/kill"},
		{"POST", "/containers/json"},
		{"GET", "/containers/abc123/archive"},
		{"GET", "/containers/abc123/export"},
		{"GET", "/containers/abc123/top"},
		{"GET", "/images/json"},
		{"POST", "/images/create"},
		{"GET", "/version"},
		{"GET", "/v1.47/containers/json"},
		{"GET", "/containers/../images/json"},
	}
	for _, c := range refused {
		if got := call(t, agent, c.method, c.path, testToken); got < 300 {
			t.Errorf("%s %s: status %d, want it refused", c.method, c.path, got)
		}
	}
	if hits := docker.received(); len(hits) != 0 {
		for _, h := range hits {
			t.Errorf("reached docker: %s %s", h.Method, h.URL.Path)
		}
	}
}

// Without the token nothing is forwarded, and the answer is the same 401 for
// an allowed route and a forbidden one, so it does not reveal which exist.
func TestTokenIsRequired(t *testing.T) {
	agent, docker := newAgent(t)

	for _, token := range []string{"", "wrong", testToken + "x", strings.ToUpper(testToken)} {
		for _, path := range []string{"/containers/json", "/containers/create", "/nothing"} {
			if got := call(t, agent, "GET", path, token); got != http.StatusUnauthorized {
				t.Errorf("GET %s with token %q: status %d, want 401", path, token, got)
			}
		}
	}

	// The scheme is case-insensitive, as RFC 7235 says; the token is not.
	req, _ := http.NewRequest("GET", agent.URL+"/containers/json", nil)
	req.Header.Set("Authorization", "bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("lower-case scheme: status %d, want 200", resp.StatusCode)
	}

	if hits := docker.received(); len(hits) != 1 {
		t.Errorf("docker received %d requests, want only the one with the right token", len(hits))
	}
}

// /health answers without a token, so an operator can check the agent is up
// before they have the token at hand.
func TestHealthIsPublic(t *testing.T) {
	agent, docker := newAgent(t)

	if got := call(t, agent, "GET", "/health", ""); got != http.StatusOK {
		t.Errorf("GET /health: status %d, want 200", got)
	}
	if hits := docker.received(); len(hits) != 0 {
		t.Errorf("/health reached docker %d times; it should not touch the socket", len(hits))
	}
}

// An {id} that is not a plausible container ID or name stops at the agent.
func TestInvalidContainerIDIsRejected(t *testing.T) {
	agent, docker := newAgent(t)

	for _, id := range []string{"-rm", ".hidden", "_x", "a%2Fb", strings.Repeat("a", 129)} {
		path := "/containers/" + id + "/start"
		if got := call(t, agent, "POST", path, testToken); got != http.StatusBadRequest {
			t.Errorf("POST %s: status %d, want 400", path, got)
		}
	}
	if hits := docker.received(); len(hits) != 0 {
		t.Errorf("an invalid id reached docker %d times", len(hits))
	}
}

// Stats always reach Docker as a single reading, whatever the caller asked
// for.
func TestStatsAreASingleReading(t *testing.T) {
	agent, docker := newAgent(t)

	for _, query := range []string{"", "?stream=true", "?stream=1&one-shot=true", "?stream=true&stream=true"} {
		call(t, agent, "GET", "/containers/abc123/stats"+query, testToken)
	}

	hits := docker.received()
	if len(hits) != 4 {
		t.Fatalf("docker received %d requests, want 4", len(hits))
	}
	for i, h := range hits {
		if got := h.URL.Query()["stream"]; len(got) != 1 || got[0] != "false" {
			t.Errorf("request %d reached docker with stream=%q, want exactly [\"false\"]", i, got)
		}
	}
}

// Only the parameters Lastboard sends reach Docker; the rest are dropped on
// the way. Each route is asked with the one parameter that would hurt on it.
func TestOnlyKnownQueryParametersReachDocker(t *testing.T) {
	agent, docker := newAgent(t)

	cases := []struct {
		method, path string
		want         string // the query Docker must receive, encoded
	}{
		{"GET", "/containers/json?all=true&size=true&filters=%7B%7D", "all=true"},
		{"GET", "/containers/json?size=true", ""},
		{"GET", "/containers/abc123/stats?one-shot=true&stream=true", "stream=false"},
		{"POST", "/containers/abc123/start?detachKeys=ctrl-x", ""},
		{"POST", "/containers/abc123/stop?t=999999&signal=SIGKILL", ""},
		{"POST", "/containers/abc123/restart?t=999999", ""},
		// Logs: never followed, at most a thousand lines, since only as a time.
		{"GET", "/containers/abc123/logs?stdout=1&stderr=1&timestamps=1&tail=50&follow=1&details=1&until=9", "follow=0&stderr=1&stdout=1&tail=50&timestamps=1"},
		{"GET", "/containers/abc123/logs?stdout=1&tail=999999&since=1700000000.123456789", "follow=0&since=1700000000.123456789&stdout=1&tail=1000"},
		{"GET", "/containers/abc123/logs?stdout=1&tail=all&since=yesterday", "follow=0&stdout=1&tail=100"},
	}
	for _, c := range cases {
		call(t, agent, c.method, c.path, testToken)
	}

	hits := docker.received()
	if len(hits) != len(cases) {
		t.Fatalf("docker received %d requests, want %d", len(hits), len(cases))
	}
	for i, c := range cases {
		if got := hits[i].URL.RawQuery; got != c.want {
			t.Errorf("%s %s reached docker with query %q, want %q", c.method, c.path, got, c.want)
		}
	}
}

// The agent's token is checked and stops there; Docker never sees it.
func TestTokenIsNotForwarded(t *testing.T) {
	agent, docker := newAgent(t)

	call(t, agent, "GET", "/containers/json", testToken)

	hits := docker.received()
	if len(hits) != 1 {
		t.Fatalf("docker received %d requests, want 1", len(hits))
	}
	if got := hits[0].Header.Get("Authorization"); got != "" {
		t.Errorf("docker received Authorization %q; the token should end at the agent", got)
	}
}

// /health names the build, so an operator can tell which agent a host runs.
func TestHealthCarriesTheVersion(t *testing.T) {
	agent, _ := newAgent(t)

	resp, err := http.Get(agent.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode /health: %v", err)
	}
	if body["status"] != "ok" || body["version"] != version {
		t.Errorf("/health = %v, want status ok and version %q", body, version)
	}
}

func TestWeakTokenWarning(t *testing.T) {
	for _, short := range []string{"short", strings.Repeat("a", minTokenLength-1)} {
		if msg := weakTokenWarning(short); !strings.Contains(msg, "characters long") {
			t.Errorf("token %q: got %q, want the length warning", short, msg)
		}
	}
	// The examples are all short too, so the length check alone would warn
	// about them. What is asserted is that they are named for what they are.
	for _, example := range exampleTokens {
		if msg := weakTokenWarning(example); !strings.Contains(msg, "example value") {
			t.Errorf("token %q: got %q, want the example-value warning", example, msg)
		}
	}
	for _, fine := range []string{testToken, strings.Repeat("f", 64)} {
		if msg := weakTokenWarning(fine); msg != "" {
			t.Errorf("token of length %d warned: %s", len(fine), msg)
		}
	}
}

// A container's details leave the agent with the environment reduced to the
// variables' names: the values are where passwords live, and they stay here.
func TestDetailsLeaveWithoutTheEnvironmentsValues(t *testing.T) {
	agent, docker := newAgent(t)
	docker.answer("/containers/abc123/json", `{"Id":"abc123","Name":"/db","Config":{"Image":"postgres","Env":["POSTGRES_PASSWORD=hunter2","PATH=/usr/bin","EMPTY="]}}`)

	req, _ := http.NewRequest("GET", agent.URL+"/containers/abc123/json", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got struct {
		Name   string
		Config struct {
			Image string
			Env   []string
		}
	}
	raw := new(strings.Builder)
	if err := json.NewDecoder(io.TeeReader(resp.Body, raw)).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if strings.Contains(raw.String(), "hunter2") {
		t.Fatalf("a value left the agent: %s", raw.String())
	}
	if strings.Join(got.Config.Env, ",") != "POSTGRES_PASSWORD,PATH,EMPTY" || got.Name != "/db" || got.Config.Image != "postgres" {
		t.Errorf("details = %+v, want the names and everything else as it was", got)
	}
}

// Details the agent cannot read are not passed on as they came.
func TestUnreadableDetailsAreNotPassedOn(t *testing.T) {
	agent, docker := newAgent(t)
	docker.answer("/containers/abc123/json", `not json, POSTGRES_PASSWORD=hunter2`)
	req, _ := http.NewRequest("GET", agent.URL+"/containers/abc123/json", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("unreadable details = %d, want 502", resp.StatusCode)
	}
	// Docker answered, so the reason is not that it could not be reached.
	if !strings.Contains(string(body), "unreadable container details") {
		t.Errorf("unreadable details said %q, want it named as such", body)
	}
	if strings.Contains(string(body), "hunter2") {
		t.Errorf("the value got through: %q", body)
	}
}
