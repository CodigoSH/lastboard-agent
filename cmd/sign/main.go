// Command sign is the release-signing helper.
//
// It uses Ed25519 (Go stdlib only — no external tooling) so the whole signing
// ceremony stays self-contained. Modes:
//
//	sign keygen        Generate a keypair. Prints the base64 PRIVATE key (store
//	                   it as the CI secret RELEASE_SIGNING_KEY) and the base64
//	                   PUBLIC key (add it to releasePublicKeys below and to the
//	                   README).
//
//	sign <file>        Sign <file> with the key in $RELEASE_SIGNING_KEY and write
//	                   the detached base64 signature to <file>.sig. Used by the
//	                   release workflow on checksums.txt.
//
//	sign verify <file> <sig>
//	                   Verify <sig> over <file> against releasePublicKeys. Handy
//	                   to self-test the keypair before cutting a release.
//
//	sign configured    Exit 0 if a release public key is listed.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
)

// releasePublicKeys are the base64 Ed25519 public keys a release may be signed
// with. The agent has no updater and no installer, so what verifies a release
// is a person following the README, which publishes the same keys as PEM; a
// test keeps the two in step.
//
// More than one entry is how a key is rotated without a gap: the new key is
// listed (and published) before CI starts signing with it, and the old one is
// dropped a release later.
var releasePublicKeys = []string{
	"Itijs5mx/5hS0kJkzNgHweFgFakZRm3JHhrxL+lVYXA=",
}

func signingConfigured() bool {
	for _, key := range releasePublicKeys {
		if key != "" {
			return true
		}
	}
	return false
}

// verifyRelease accepts the signature if any listed key verifies it.
func verifyRelease(data []byte, sigBase64 string) error {
	sig, err := base64.StdEncoding.DecodeString(sigBase64)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return errors.New("signature is not a base64 Ed25519 signature")
	}
	for _, key := range releasePublicKeys {
		pub, err := base64.StdEncoding.DecodeString(key)
		if err != nil || len(pub) != ed25519.PublicKeySize {
			return fmt.Errorf("listed public key %q is not a base64 Ed25519 key", key)
		}
		if ed25519.Verify(pub, data, sig) {
			return nil
		}
	}
	return errors.New("no listed key verifies this signature")
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}

	switch os.Args[1] {
	case "keygen":
		keygen()
	case "verify":
		if len(os.Args) != 4 {
			usage()
		}
		verify(os.Args[2], os.Args[3])
	case "configured":
		// Exit status only, for the release workflow: a build that carries a
		// public key must not publish a release nobody can verify with it.
		if !signingConfigured() {
			os.Exit(1)
		}
	default:
		signFile(os.Args[1])
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  sign configured            exit 0 if a release public key is listed")
	fmt.Fprintln(os.Stderr, "  sign keygen              generate a keypair")
	fmt.Fprintln(os.Stderr, "  sign <file>              sign <file> -> <file>.sig using $RELEASE_SIGNING_KEY")
	fmt.Fprintln(os.Stderr, "  sign verify <file> <sig> verify <sig> over <file> against the listed public keys")
	os.Exit(2)
}

func verify(filePath, sigPath string) {
	if !signingConfigured() {
		fail(fmt.Errorf("no public key listed (releasePublicKeys is empty)"))
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		fail(err)
	}
	sig, err := os.ReadFile(sigPath)
	if err != nil {
		fail(err)
	}
	if err := verifyRelease(data, strings.TrimSpace(string(sig))); err != nil {
		fail(fmt.Errorf("verification FAILED: %w", err))
	}
	fmt.Println("signature OK")
}

func keygen() {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fail(err)
	}
	fmt.Println("PRIVATE (store as CI secret RELEASE_SIGNING_KEY):")
	fmt.Println(base64.StdEncoding.EncodeToString(priv))
	fmt.Println()
	fmt.Println("PUBLIC (add to releasePublicKeys in cmd/sign/main.go and to the README):")
	fmt.Println(base64.StdEncoding.EncodeToString(pub))
}

func signFile(pathArg string) {
	keyB64 := os.Getenv("RELEASE_SIGNING_KEY")
	if keyB64 == "" {
		fail(fmt.Errorf("RELEASE_SIGNING_KEY is not set"))
	}
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil || len(key) != ed25519.PrivateKeySize {
		fail(fmt.Errorf("RELEASE_SIGNING_KEY must be a base64 Ed25519 private key"))
	}
	data, err := os.ReadFile(pathArg)
	if err != nil {
		fail(err)
	}
	sig := ed25519.Sign(ed25519.PrivateKey(key), data)
	outPath := pathArg + ".sig"
	if err := os.WriteFile(outPath, []byte(base64.StdEncoding.EncodeToString(sig)+"\n"), 0o644); err != nil {
		fail(err)
	}
	fmt.Println("wrote", outPath)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "sign:", err)
	os.Exit(1)
}
