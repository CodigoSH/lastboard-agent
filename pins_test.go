package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// A version that is written down twice can drift, and nothing notices until the
// day it matters: the binary on the release page and the image in the registry
// are built by different files, each names its own Go, and the tests run on a
// third.
//
// This does NOT ask the internet whether something newer exists. A test that
// goes red because somebody else published a release turns the build red on a
// day nobody touched the code. What it checks is ours, offline and
// deterministic: the same tool pinned in several files is pinned to one
// version.
func TestGoVersionAgrees(t *testing.T) {
	pins := []struct {
		file  string
		find  *regexp.Regexp
		human string
	}{
		{"go.mod", regexp.MustCompile(`(?m)^go ([0-9.]+)$`), "go X.Y.Z"},
		{"Dockerfile", regexp.MustCompile(`golang:([0-9.]+)-alpine`), "FROM golang:X.Y.Z-alpine"},
		{".forgejo/workflows/release.yml", regexp.MustCompile(`go-version:\s*'([0-9.]+)'`), "go-version: 'X.Y.Z'"},
		{".forgejo/workflows/tests.yml", regexp.MustCompile(`go-version:\s*'([0-9.]+)'`), "go-version: 'X.Y.Z'"},
	}

	found := map[string]string{}
	for _, p := range pins {
		body, err := os.ReadFile(p.file)
		if err != nil {
			t.Fatalf("%s pins Go but could not be read: %v", p.file, err)
		}
		ms := p.find.FindAllSubmatch(body, -1)
		if ms == nil {
			// A pin that moved or was removed is exactly the drift this
			// exists to catch; passing here would be checking nothing.
			t.Fatalf("no Go version found in %s.\nExpected something like: %s\n"+
				"If the pin moved, move this test with it.", p.file, p.human)
		}
		// Every occurrence: release.yml sets up Go once to build and once
		// to sign, and either could drift on its own.
		for i, m := range ms {
			found[fmt.Sprintf("%s (#%d)", p.file, i+1)] = string(m[1])
		}
	}

	want := found["go.mod (#1)"]
	for file, got := range found {
		if got != want {
			t.Errorf("Go is pinned to two different versions:\n  go.mod: %s\n  %s: %s\n"+
				"They have to agree, or the release binary and the image are built "+
				"by different compilers.", want, file, got)
		}
	}
}

// The release preflight runs staticcheck so that a tag which would turn the
// Tests workflow red cannot be cut. That only holds while both run the same
// version.
func TestStaticcheckVersionAgrees(t *testing.T) {
	pins := []struct {
		file string
		find *regexp.Regexp
	}{
		{"scripts/release.sh", regexp.MustCompile(`STATICCHECK_VERSION="(v[0-9.]+)"`)},
		{".forgejo/workflows/tests.yml", regexp.MustCompile(`honnef\.co/go/tools/cmd/staticcheck@(v[0-9.]+)`)},
	}
	found := map[string]string{}
	for _, p := range pins {
		body, err := os.ReadFile(p.file)
		if err != nil {
			t.Fatalf("read %s: %v", p.file, err)
		}
		m := p.find.FindSubmatch(body)
		if m == nil {
			t.Fatalf("no staticcheck version found in %s (looked for %s)", p.file, p.find)
		}
		found[p.file] = string(m[1])
	}
	if a, b := found["scripts/release.sh"], found[".forgejo/workflows/tests.yml"]; a != b {
		t.Errorf("staticcheck is pinned to two versions: release.sh %s, tests.yml %s", a, b)
	}
}

// Nothing that builds a release may float. A moving tag means the artefact that
// was tested and the artefact that was published are not the same thing.
//
// Publishing the image AS `latest` is deliberate and is not what this looks
// for: that is `value=latest` in the metadata step, never a quoted "latest"
// used as a version or an image tag.
func TestNothingIsPinnedToLatest(t *testing.T) {
	files := []string{
		"Dockerfile",
		".forgejo/workflows/release.yml",
		".forgejo/workflows/docker-release.yml",
		".forgejo/workflows/tests.yml",
		".forgejo/workflows/site-rebuild.yml",
		"scripts/release.sh",
	}
	floats := []*regexp.Regexp{
		regexp.MustCompile(`@latest\b`),
		regexp.MustCompile(`"latest"|'latest'`),
		regexp.MustCompile(`(?m)^FROM\s+\S+:latest`),
		regexp.MustCompile(`(?m)^FROM\s+(--\S+\s+)*[^\s:@-][^\s:@]*(\s|$)`), // an image with no tag at all
		regexp.MustCompile(`container:\s*\S+:latest`),
	}

	for _, name := range files {
		body, err := os.ReadFile(name)
		if err != nil {
			// Every file listed here exists; one that cannot be read means
			// this test is looking in the wrong place.
			t.Fatalf("read %s: %v", name, err)
		}
		for _, line := range strings.Split(string(body), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || trimmed[0] == '#' {
				continue
			}
			for _, re := range floats {
				if re.MatchString(line) {
					t.Errorf("%s floats:\n  %s\nPin it. The version that built the "+
						"release has to be the version that was tested.", name, trimmed)
				}
			}
		}
	}
}

// Every build that ships stamps the version into main.version. Nothing else
// would notice if one stopped: -X on a variable that does not exist is not an
// error, and the release would simply say "dev" in its log and on /health.
func TestReleaseBuildsStampTheVersion(t *testing.T) {
	stamps := []struct {
		file string
		find *regexp.Regexp
	}{
		{"main.go", regexp.MustCompile(`(?m)^var version = "dev"$`)},
		{"Dockerfile", regexp.MustCompile(`(?m)^[^#]*-X main\.version=\$\{VERSION\}`)},
		{".forgejo/workflows/docker-release.yml", regexp.MustCompile(`(?m)^[^#]*--build-arg VERSION="\$\{GITHUB_REF_NAME\}"`)},
		{".forgejo/workflows/release.yml", regexp.MustCompile(`(?m)^[^#]*-X main\.version=\$\{GITHUB_REF_NAME\}`)},
	}
	for _, s := range stamps {
		body, err := os.ReadFile(s.file)
		if err != nil {
			t.Fatalf("read %s: %v", s.file, err)
		}
		if !s.find.Match(body) {
			t.Errorf("%s does not stamp the version (looked for %s)", s.file, s.find)
		}
	}
}

// A release rebuilds the website once it is published. site-rebuild.yml only
// runs on a push to itself or when dispatched, so if release.yml stops
// dispatching it, nothing fails: the site just goes on advertising the
// previous version.
func TestReleaseDispatchesTheSiteRebuild(t *testing.T) {
	body, err := os.ReadFile(".forgejo/workflows/release.yml")
	if err != nil {
		t.Fatalf("read release.yml: %v", err)
	}
	var job strings.Builder
	in := false
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") && strings.HasSuffix(line, ":") {
			in = line == "  trigger-site-rebuild:"
		}
		if in && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			job.WriteString(line + "\n")
		}
	}
	for _, want := range []string{
		"needs: release",
		"/actions/workflows/site-rebuild.yml/dispatches",
	} {
		if !strings.Contains(job.String(), want) {
			t.Errorf("release.yml's trigger-site-rebuild job lacks %q", want)
		}
	}
	if _, err := os.Stat(".forgejo/workflows/site-rebuild.yml"); err != nil {
		t.Errorf("release.yml dispatches site-rebuild.yml, which does not exist: %v", err)
	}
}

// The README tells readers to check checksums.txt.sig. If release.yml stops
// signing or stops uploading the signature, nothing else fails: the release
// simply ships without one.
func TestReleaseIsSigned(t *testing.T) {
	body, err := os.ReadFile(".forgejo/workflows/release.yml")
	if err != nil {
		t.Fatalf("read release.yml: %v", err)
	}
	var code strings.Builder
	for _, line := range strings.Split(string(body), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			code.WriteString(line + "\n")
		}
	}
	for _, want := range []string{
		"RELEASE_SIGNING_KEY: ${{ secrets.RELEASE_SIGNING_KEY }}",
		"go run ./cmd/sign artifacts/checksums.txt",
		"go run ./cmd/sign verify artifacts/checksums.txt artifacts/checksums.txt.sig",
		"filename=checksums.txt.sig",
	} {
		if !strings.Contains(code.String(), want) {
			t.Errorf("release.yml does not sign the release: no %q outside comments", want)
		}
	}
}

// NOTICE says the agent compiles in no third-party module. That stays true
// only while go.mod requires none, so a module that arrives without being
// named there fails here first.
func TestNoticeMatchesGoMod(t *testing.T) {
	mod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	notice, err := os.ReadFile("NOTICE")
	if err != nil {
		t.Fatalf("read NOTICE: %v", err)
	}
	if regexp.MustCompile(`(?m)^require\b`).Match(mod) {
		t.Errorf("go.mod requires a module, and NOTICE says none is compiled in.\n" +
			"Name it, with its licence, under THIRD-PARTY COMPONENTS, and change this test.")
	}
	if !strings.Contains(string(notice), "None. Everything the agent and its signing tool do uses the Go standard") {
		t.Error("NOTICE no longer says the agent has no third-party modules; this test should change with it")
	}
}
