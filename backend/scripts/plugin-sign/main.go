// Standalone signer for Sub2API .s2plugin archives. Uses Go standard library only.
package main

import (
	"archive/zip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type signature struct {
	Algorithm string `json:"algorithm"`
	KeyID     string `json:"key_id"`
	Signature string `json:"signature"`
}
type archive struct {
	reader  *zip.ReadCloser
	raw     []byte
	entries map[string]*zip.File
	files   map[string]string
}

func readFile(f *zip.File) ([]byte, error) {
	if f.UncompressedSize64 > 128*1024*1024 {
		return nil, errors.New("archive entry too large")
	}
	r, e := f.Open()
	if e != nil {
		return nil, e
	}
	defer r.Close()
	return io.ReadAll(io.LimitReader(r, 128*1024*1024+1))
}
func loadArchive(name string) (*archive, error) {
	z, e := zip.OpenReader(name)
	if e != nil {
		return nil, e
	}
	a := &archive{reader: z, entries: map[string]*zip.File{}}
	ok := false
	defer func() {
		if !ok {
			z.Close()
		}
	}()
	for _, f := range z.File {
		if f.Name == "" || strings.HasPrefix(f.Name, "/") || strings.Contains(f.Name, "\\") || path.Clean(f.Name) != f.Name || strings.Contains(f.Name, ":") || f.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("invalid entry path")
		}
		for _, part := range strings.Split(f.Name, "/") {
			if part == ".." || part == "." || part == "" {
				return nil, errors.New("invalid entry path")
			}
		}
		if a.entries[f.Name] != nil {
			return nil, errors.New("duplicate archive entry")
		}
		a.entries[f.Name] = f
	}
	f := a.entries["manifest.json"]
	if f == nil {
		return nil, errors.New("manifest.json missing")
	}
	a.raw, e = readFile(f)
	if e != nil {
		return nil, e
	}
	var manifest struct {
		Files map[string]string `json:"files"`
	}
	if e = json.Unmarshal(a.raw, &manifest); e != nil {
		return nil, e
	}
	a.files = manifest.Files
	if len(a.files) == 0 {
		return nil, errors.New("empty manifest.files")
	}
	for name, want := range a.files {
		if name == "manifest.json" || name == "signature.json" {
			return nil, errors.New("manifest/signature must not be in files")
		}
		f := a.entries[name]
		if f == nil {
			return nil, fmt.Errorf("declared file missing: %s", name)
		}
		data, e := readFile(f)
		if e != nil {
			return nil, e
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != want {
			return nil, fmt.Errorf("SHA256 mismatch: %s", name)
		}
	}
	for name := range a.entries {
		if name != "manifest.json" && name != "signature.json" {
			if _, exists := a.files[name]; !exists {
				return nil, fmt.Errorf("undeclared file: %s", name)
			}
		}
	}
	ok = true
	return a, nil
}
func exclusiveWrite(name string, data []byte, mode os.FileMode) error {
	if name == "" {
		return errors.New("output path required")
	}
	if e := os.MkdirAll(filepath.Dir(name), 0700); e != nil {
		return e
	}
	f, e := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if e != nil {
		return e
	}
	_, e = f.Write(data)
	ce := f.Close()
	if e != nil {
		return e
	}
	return ce
}
func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: go run signer.go keygen|sign|verify [flags]")
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	private := fs.String("private", "", "local PKCS8 private PEM file")
	public := fs.String("public", "", "raw 32-byte Ed25519 public key, Base64 text file")
	input := fs.String("in", "", "input .s2plugin")
	output := fs.String("out", "", "new signed .s2plugin (never overwrites input)")
	keyID := fs.String("key-id", "", "publisher ID matching trusted_publishers")
	if e := fs.Parse(args[1:]); e != nil {
		return e
	}
	switch args[0] {
	case "keygen":
		if *private == "" || *public == "" || *private == *public {
			return errors.New("distinct -private and -public paths required")
		}
		for _, name := range []string{*private, *public} {
			if _, e := os.Stat(name); !os.IsNotExist(e) {
				return fmt.Errorf("key output exists or is inaccessible: %s", name)
			}
		}
		pub, priv, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			return e
		}
		raw, e := x509.MarshalPKCS8PrivateKey(priv)
		if e != nil {
			return e
		}
		if e = exclusiveWrite(*private, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: raw}), 0600); e != nil {
			return e
		}
		if e = exclusiveWrite(*public, []byte(base64.StdEncoding.EncodeToString(pub)+"\n"), 0644); e != nil {
			return e
		}
		fmt.Println("KEYGEN=PASS; PRIVATE_KEY_CONTENT=NOT_PRINTED; PUBLIC_KEY_FILE=" + *public)
	case "sign":
		if *input == "" || *output == "" || *private == "" || strings.TrimSpace(*keyID) == "" {
			return errors.New("-in -out -private -key-id required")
		}
		inPath, e := filepath.Abs(*input)
		if e != nil {
			return e
		}
		outPath, e := filepath.Abs(*output)
		if e != nil {
			return e
		}
		if strings.EqualFold(inPath, outPath) {
			return errors.New("input and output must differ")
		}
		a, e := loadArchive(*input)
		if e != nil {
			return e
		}
		defer a.reader.Close()
		if a.entries["signature.json"] != nil {
			return errors.New("already signed; use the original unsigned archive")
		}
		raw, e := os.ReadFile(*private)
		if e != nil {
			return e
		}
		block, _ := pem.Decode(raw)
		if block == nil {
			return errors.New("invalid private PEM")
		}
		parsed, e := x509.ParsePKCS8PrivateKey(block.Bytes)
		if e != nil {
			return e
		}
		priv, ok := parsed.(ed25519.PrivateKey)
		if !ok {
			return errors.New("private key is not Ed25519")
		}
		sig := ed25519.Sign(priv, a.raw)
		if !ed25519.Verify(priv.Public().(ed25519.PublicKey), a.raw, sig) {
			return errors.New("signature self-check failed")
		}
		encoded, e := json.MarshalIndent(signature{"ed25519", *keyID, base64.StdEncoding.EncodeToString(sig)}, "", "  ")
		if e != nil {
			return e
		}
		encoded = append(encoded, '\n')
		if e = os.MkdirAll(filepath.Dir(*output), 0700); e != nil {
			return e
		}
		f, e := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if e != nil {
			return e
		}
		completed := false
		defer func() {
			f.Close()
			if !completed {
				os.Remove(*output)
			}
		}()
		zw := zip.NewWriter(f)
		for _, entry := range a.reader.File {
			if e = zw.Copy(entry); e != nil {
				return e
			}
		}
		h := &zip.FileHeader{Name: "signature.json", Method: zip.Deflate}
		h.SetMode(0644)
		w, e := zw.CreateHeader(h)
		if e != nil {
			return e
		}
		if _, e = w.Write(encoded); e != nil {
			return e
		}
		if e = zw.Close(); e != nil {
			return e
		}
		if e = f.Sync(); e != nil {
			return e
		}
		if e = f.Close(); e != nil {
			return e
		}
		completed = true
		fmt.Println("SIGN=PASS; OUTPUT=" + *output)
	case "verify":
		if *input == "" || *public == "" || *keyID == "" {
			return errors.New("-in -public -key-id required")
		}
		a, e := loadArchive(*input)
		if e != nil {
			return e
		}
		defer a.reader.Close()
		entry := a.entries["signature.json"]
		if entry == nil {
			return errors.New("signature.json missing")
		}
		raw, e := readFile(entry)
		if e != nil {
			return e
		}
		var s signature
		if e = json.Unmarshal(raw, &s); e != nil {
			return e
		}
		if s.Algorithm != "ed25519" || s.KeyID != *keyID {
			return errors.New("algorithm/key_id mismatch")
		}
		publicRaw, e := os.ReadFile(*public)
		if e != nil {
			return e
		}
		pub, e := base64.StdEncoding.DecodeString(strings.TrimSpace(string(publicRaw)))
		if e != nil || len(pub) != ed25519.PublicKeySize {
			return errors.New("invalid public key")
		}
		sig, e := base64.StdEncoding.DecodeString(s.Signature)
		if e != nil || !ed25519.Verify(ed25519.PublicKey(pub), a.raw, sig) {
			return errors.New("signature verification failed")
		}
		fmt.Printf("VERIFY=PASS; SIGNATURE=trusted; FILES=%d\n", len(a.files))
	default:
		return errors.New("unknown subcommand")
	}
	return nil
}
func main() {
	if e := run(os.Args[1:]); e != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", e)
		os.Exit(1)
	}
}
