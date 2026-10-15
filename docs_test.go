package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The documentation is published by codigosh.com, which builds a section from
// whatever sits in docs/. Three things go wrong there without any error: a link
// to a page that is not there, a section the sidebar does not know about, and a
// sentence that quotes what the agent says after the agent stopped saying it.
// This checks all three, offline.

func docPages(t *testing.T) []string {
	t.Helper()
	var pages []string
	err := filepath.Walk("docs", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".md") {
			pages = append(pages, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk docs: %v", err)
	}
	// A walk that finds nothing passes, and reports it as success.
	if len(pages) < 4 {
		t.Fatalf("found %d pages under docs/, expected the overview and three more: "+
			"this test is looking in the wrong place", len(pages))
	}
	return pages
}

// The site only adds the "Overview" entry when the front page is README.md.
func TestDocsHaveAnOverviewThePageBuilderRecognises(t *testing.T) {
	if _, err := os.Stat("docs/README.md"); err != nil {
		t.Fatalf("docs/README.md is missing: %v", err)
	}
	if _, err := os.Stat("docs/index.md"); err == nil {
		t.Error("docs/index.md exists: the site only recognises README.md as the front page")
	}
}

// Every folder is a section in the sidebar, and one that _meta.json does not
// name shows up in alphabetical order under its folder name.
func TestDocsMetaNamesEverySection(t *testing.T) {
	raw, err := os.ReadFile("docs/_meta.json")
	if err != nil {
		t.Fatalf("read docs/_meta.json: %v", err)
	}
	var meta struct {
		Overview string            `json:"overview"`
		Order    []string          `json:"order"`
		Labels   map[string]string `json:"labels"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("docs/_meta.json: %v", err)
	}
	if meta.Overview == "" {
		t.Error("docs/_meta.json has no overview")
	}

	entries, err := os.ReadDir("docs")
	if err != nil {
		t.Fatal(err)
	}
	folders := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() {
			folders[e.Name()] = true
		}
	}
	if len(folders) == 0 {
		t.Fatal("docs/ has no sections")
	}

	ordered := map[string]bool{}
	for _, name := range meta.Order {
		ordered[name] = true
		if !folders[name] {
			t.Errorf("_meta.json orders %q, which is not a folder under docs/", name)
		}
		if meta.Labels[name] == "" {
			t.Errorf("_meta.json gives %q no label", name)
		}
	}
	for name := range folders {
		if !ordered[name] {
			t.Errorf("docs/%s is not in _meta.json's order: the sidebar would sort it by its folder name", name)
		}
	}
}

var markdownLink = regexp.MustCompile(`\]\(([^)\s]+)\)`)

// A link to another page is rewritten to its published route by the site, and
// to a page that does not exist it is rewritten just the same.
func TestDocsLinksPointToPagesThatExist(t *testing.T) {
	for _, page := range docPages(t) {
		body, err := os.ReadFile(page)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range markdownLink.FindAllStringSubmatch(string(body), -1) {
			target := m[1]
			if regexp.MustCompile(`^(https?:|mailto:|#|/)`).MatchString(target) {
				continue
			}
			path := strings.SplitN(target, "#", 2)[0]
			if !strings.HasSuffix(path, ".md") {
				t.Errorf("%s links to %q: a relative link here has to be another page (.md); "+
					"the site only resolves those", page, target)
				continue
			}
			resolved := filepath.Join(filepath.Dir(page), path)
			if _, err := os.Stat(resolved); err != nil {
				t.Errorf("%s links to %q, which is not at %s", page, target, resolved)
			}
		}
	}
}

// What the troubleshooting page and the API reference quote has to be what the
// agent says. Each needle must be in the source and in the page that quotes it.
func TestDocsQuoteWhatTheAgentReallySays(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	quotes := []struct{ page, needle string }{
		{"docs/help/troubleshooting.md", "TOKEN environment variable is required but not set"},
		{"docs/help/troubleshooting.md", "TOKEN is the example value from the documentation"},
		{"docs/help/troubleshooting.md", "characters long; use at least"},
		{"docs/help/troubleshooting.md", "docker proxy error"},
		{"docs/reference/api.md", "invalid container id"},
		{"docs/reference/api.md", "unauthorized"},
		{"docs/reference/api.md", "docker unreachable"},
	}
	for _, q := range quotes {
		if !strings.Contains(string(source), q.needle) {
			t.Errorf("main.go no longer says %q, which %s quotes", q.needle, q.page)
		}
		page, err := os.ReadFile(q.page)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(page), q.needle) {
			t.Errorf("%s no longer quotes %q", q.page, q.needle)
		}
	}
}
