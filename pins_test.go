package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// A version that is written down twice can drift, and nothing notices until the
// day it matters: the binary on the release page and the image in the registry
// are built by different files, and each names its own Go.
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
	}

	found := map[string]string{}
	for _, p := range pins {
		body, err := os.ReadFile(p.file)
		if err != nil {
			t.Fatalf("%s pins Go but could not be read: %v", p.file, err)
		}
		m := p.find.FindSubmatch(body)
		if m == nil {
			// A pin that moved or was removed is exactly the drift this
			// exists to catch; passing here would be checking nothing.
			t.Fatalf("no Go version found in %s.\nExpected something like: %s\n"+
				"If the pin moved, move this test with it.", p.file, p.human)
		}
		found[p.file] = string(m[1])
	}

	want := found["go.mod"]
	for file, got := range found {
		if got != want {
			t.Errorf("Go is pinned to two different versions:\n  go.mod: %s\n  %s: %s\n"+
				"They have to agree, or the release binary and the image are built "+
				"by different compilers.", want, file, got)
		}
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
