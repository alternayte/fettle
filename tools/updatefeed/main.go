// Command updatefeed writes update.json, the signed release feed of the desktop app
// (§15.1). Each package gets its SHA-256 and an Ed25519 signature over the version, the
// platform and the hash, which the app checks before it installs.
//
//	FETTLE_UPDATE_SIGNING_KEY=<base64 seed> go run ./tools/updatefeed \
//	  -version 1.0.0 -base https://github.com/alternayte/fettle/releases/download/v1.0.0 \
//	  -out dist/update.json darwin-arm64=dist/Fettle-1.0.0-darwin-universal.tar.gz ...
//
// With -keygen it prints a new key pair instead.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/alternayte/fettle/internal/desktop"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "updatefeed:", err)
		os.Exit(1)
	}
}

func run() error {
	version := flag.String("version", "", "the release version, without v")
	base := flag.String("base", "", "the URL of the folder that holds the packages")
	out := flag.String("out", "update.json", "the feed file to write")
	notes := flag.String("notes", "", "release notes for the update question")
	keygen := flag.Bool("keygen", false, "print a new signing key and its public key")
	flag.Parse()
	if *keygen {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		fmt.Printf("FETTLE_UPDATE_SIGNING_KEY=%s\n", base64.StdEncoding.EncodeToString(priv.Seed()))
		fmt.Printf("public key (main.updateKey of the desktop build): %s\n", base64.StdEncoding.EncodeToString(pub))
		return nil
	}
	if *version == "" || *base == "" || flag.NArg() == 0 {
		return errors.New("usage: updatefeed -version V -base URL [-out file] platform=package")
	}
	seed, err := base64.StdEncoding.DecodeString(os.Getenv("FETTLE_UPDATE_SIGNING_KEY"))
	if err != nil || len(seed) != ed25519.SeedSize {
		return errors.New("FETTLE_UPDATE_SIGNING_KEY must be a base64 Ed25519 seed of 32 bytes")
	}
	priv := ed25519.NewKeyFromSeed(seed)
	m := desktop.Manifest{Version: *version, Notes: *notes, Packages: map[string]desktop.Package{}}
	for _, arg := range flag.Args() {
		platform, file, ok := strings.Cut(arg, "=")
		if !ok {
			return fmt.Errorf("%q is not platform=package", arg)
		}
		f, err := os.Open(file)
		if err != nil {
			return err
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		_ = f.Close()
		if err != nil {
			return err
		}
		sum := hex.EncodeToString(h.Sum(nil))
		m.Packages[platform] = desktop.Package{URL: strings.TrimRight(*base, "/") + "/" + filepath.Base(file), SHA256: sum, Signature: desktop.Sign(priv, *version, platform, sum)}
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(*out, append(b, '\n'), 0o644)
}
