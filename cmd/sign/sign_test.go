package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The DER SubjectPublicKeyInfo prefix that turns 32 raw Ed25519 bytes into
// the body of a PEM public key.
const ed25519SPKIPrefix = "MCowBQYDK2VwAyEA"

var pemBody = regexp.MustCompile(`(?s)-----BEGIN PUBLIC KEY-----\n(\S+)\n-----END PUBLIC KEY-----`)

func readme(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	return string(body)
}

// The README's verification recipe, exactly as a reader would paste it.
func readmeRecipe(t *testing.T) string {
	t.Helper()
	text := readme(t)
	for _, block := range strings.Split(text, "```bash\n")[1:] {
		block = block[:strings.Index(block, "```")]
		if strings.Contains(block, "openssl pkeyutl -verify") {
			return block
		}
	}
	t.Fatal("README.md has no bash block that runs openssl pkeyutl -verify")
	return ""
}

// TestTheReadmeRecipeVerifiesWhatWeSign.
//
// Two implementations have to agree: this signer, in Go, and the openssl
// commands in the README, run by someone else on their own machine. Each would
// pass its own tests while disagreeing, and every release would then fail to
// verify for exactly the people who bother to check.
//
// So this signs with the real code and runs the README's block itself, changing
// only the key (the published one cannot sign here) and the download, which is
// replaced by the file already on disk.
func TestTheReadmeRecipeVerifiesWhatWeSign(t *testing.T) {
	openssl, err := exec.LookPath("openssl")
	if err != nil {
		t.Skip("openssl is not on this machine; the README recipe needs it")
	}
	// `pkeyutl -rawin` arrived in OpenSSL 3.0, and the README says so.
	version, _ := exec.Command(openssl, "version").Output()
	if !strings.HasPrefix(strings.TrimSpace(string(version)), "OpenSSL 3") {
		t.Skipf("this machine has %s; the recipe needs OpenSSL 3.0", strings.TrimSpace(string(version)))
	}

	recipe := readmeRecipe(t)

	// The README promises OpenSSL 3.0 or newer. On 3.0, verifying Ed25519
	// without -rawin fails with "operation not supported for this keytype"
	// (measured on 3.0.20); newer releases accept it either way, so running
	// the recipe here cannot notice the flag going missing. Hence this.
	if !strings.Contains(recipe, "-rawin") {
		t.Error("the README recipe dropped -rawin, which OpenSSL 3.0 needs to verify Ed25519")
	}

	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	prefix, _ := base64.StdEncoding.DecodeString(ed25519SPKIPrefix)
	testPEM := base64.StdEncoding.EncodeToString(append(prefix, public...))

	m := pemBody.FindStringSubmatch(recipe)
	if m == nil {
		t.Fatal("the README recipe carries no PEM public key")
	}
	recipe = strings.Replace(recipe, m[1], testPEM, 1)
	var lines []string
	for _, line := range strings.Split(recipe, "\n") {
		if !strings.HasPrefix(line, "curl ") {
			lines = append(lines, line)
		}
	}
	recipe = "set -e\n" + strings.Join(lines, "\n")

	run := func(sums string) (string, error) {
		dir := t.TempDir()
		path := filepath.Join(dir, "checksums.txt")
		if err := os.WriteFile(path, []byte(sums), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("RELEASE_SIGNING_KEY", base64.StdEncoding.EncodeToString(private))
		signFile(path)
		cmd := exec.Command("bash", "-c", recipe)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	signed := "deadbeef  lastboard-agent-linux-amd64.tar.gz\n"
	out, err := run(signed)
	if err != nil || !strings.Contains(out, "Signature Verified Successfully") {
		t.Fatalf("the README recipe did not verify a signature this code produced: %v\n%s", err, out)
	}

	// And the other direction, or the check above proves only that openssl
	// ran: the recipe has to refuse a checksum file that is not the one signed.
	dir := t.TempDir()
	sums := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(sums, []byte(signed), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RELEASE_SIGNING_KEY", base64.StdEncoding.EncodeToString(private))
	signFile(sums)
	if err := os.WriteFile(sums, []byte("0000beef  lastboard-agent-linux-amd64.tar.gz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", recipe)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Errorf("the README recipe accepted a changed checksums.txt:\n%s", out)
	}
}

// TestTheReadmePublishesTheListedKeys.
//
// The README is where a reader gets the key, and releasePublicKeys is what this
// tool checks against. If they diverge, the README tells people to trust a key
// the releases are not signed with.
func TestTheReadmePublishesTheListedKeys(t *testing.T) {
	if !signingConfigured() {
		t.Fatal("releasePublicKeys is empty, but the release workflow signs every release")
	}

	published := map[string]bool{}
	for _, m := range pemBody.FindAllStringSubmatch(readme(t), -1) {
		der, err := base64.StdEncoding.DecodeString(m[1])
		if err != nil || !strings.HasPrefix(m[1], ed25519SPKIPrefix) || len(der) != 12+ed25519.PublicKeySize {
			t.Fatalf("README.md has a PEM that is not an Ed25519 public key: %s", m[1])
		}
		published[base64.StdEncoding.EncodeToString(der[12:])] = true
	}
	if len(published) == 0 {
		t.Fatal("README.md publishes no public key")
	}

	listed := map[string]bool{}
	for _, key := range releasePublicKeys {
		listed[key] = true
		if !published[key] {
			t.Errorf("%s is in releasePublicKeys but not published in README.md", key)
		}
	}
	for key := range published {
		if !listed[key] {
			t.Errorf("README.md publishes %s, which is not in releasePublicKeys", key)
		}
	}
}

// verifyRelease accepts a signature from any listed key and nothing else.
func TestVerifyRelease(t *testing.T) {
	saved := releasePublicKeys
	t.Cleanup(func() { releasePublicKeys = saved })

	old, oldPriv, _ := ed25519.GenerateKey(nil)
	current, currentPriv, _ := ed25519.GenerateKey(nil)
	_, strangerPriv, _ := ed25519.GenerateKey(nil)
	releasePublicKeys = []string{
		base64.StdEncoding.EncodeToString(current),
		base64.StdEncoding.EncodeToString(old),
	}

	data := []byte("deadbeef  lastboard-agent-linux-amd64.tar.gz\n")
	sign := func(key ed25519.PrivateKey, d []byte) string {
		return base64.StdEncoding.EncodeToString(ed25519.Sign(key, d))
	}

	if err := verifyRelease(data, sign(currentPriv, data)); err != nil {
		t.Errorf("current key: %v", err)
	}
	if err := verifyRelease(data, sign(oldPriv, data)); err != nil {
		t.Errorf("a key still listed during rotation: %v", err)
	}
	if verifyRelease(data, sign(strangerPriv, data)) == nil {
		t.Error("a key that is not listed was accepted")
	}
	if verifyRelease([]byte("changed"), sign(currentPriv, data)) == nil {
		t.Error("a signature over other data was accepted")
	}
	if verifyRelease(data, "not base64!") == nil {
		t.Error("garbage was accepted as a signature")
	}
}
