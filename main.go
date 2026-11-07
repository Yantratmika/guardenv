// guardenv keeps .env files out of git in plaintext while keeping them in the
// repository in encrypted form (guarded.env).
package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	configPath = ".guardenv/config"
	hookMarker = "# guardenv-managed"
	magic      = "GUARDENV1\n"
)

// version is set at release time via -ldflags "-X main.version=...".
var version = "dev"

type Config struct {
	Secret string   `json:"secret"` // file name under ~/.guardenv
	Files  []string `json:"files"`  // .env files, relative to repo root, slash separated
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	args := os.Args[2:]
	switch os.Args[1] {
	case "init":
		err = cmdInit()
	case "encrypt":
		err = cmdEncrypt(hasFlag(args, "--stage"))
	case "decrypt":
		err = cmdDecrypt(hasFlag(args, "--force"))
	case "install-hooks":
		err = installHooks()
	case "version", "--version":
		fmt.Println("guardenv", version)
	case "help", "-h", "--help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "guardenv:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Println(`guardenv - keep .env secrets in git, encrypted

Usage:
  guardenv init            find .env files, create .guardenv/config and a secret, install git hooks
  guardenv encrypt [--stage]   encrypt each .env to guarded.env (--stage: git add the results)
  guardenv decrypt [--force]   decrypt each guarded.env to .env (--force: overwrite local changes)
  guardenv install-hooks   (re)install pre-commit / post-checkout / post-merge hooks`)
}

func hasFlag(args []string, f string) bool {
	for _, a := range args {
		if a == f {
			return true
		}
	}
	return false
}

// ---------- repo / config helpers ----------

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func repoRoot() (string, error) {
	root, err := git(".", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", errors.New("not inside a git repository")
	}
	return filepath.FromSlash(root), nil
}

func loadConfig(root string) (*Config, error) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(configPath)))
	if err != nil {
		return nil, fmt.Errorf("cannot read %s (run 'guardenv init'): %w", configPath, err)
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("bad %s: %w", configPath, err)
	}
	if c.Secret == "" || strings.ContainsAny(c.Secret, `/\`) || c.Secret == ".." {
		return nil, errors.New("invalid secret name in config")
	}
	return &c, nil
}

func secretDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".guardenv"), nil
}

func loadKey(c *Config) ([]byte, error) {
	dir, err := secretDir()
	if err != nil {
		return nil, err
	}
	p := filepath.Join(dir, c.Secret)
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("secret %s not found - obtain it from your team and save it there: %w", p, err)
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("secret %s is not a valid 32-byte hex key", p)
	}
	return key, nil
}

// guardedName maps ".env" -> "guarded.env", ".env.local" -> "guarded.env.local".
func guardedPath(envRel string) string {
	dir, base := filepath.Split(filepath.FromSlash(envRel))
	return dir + "guarded" + base
}

// ---------- crypto ----------

func encrypt(key, plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append([]byte(magic), nonce...)
	return gcm.Seal(out, nonce, plain, []byte(magic)), nil
}

func decrypt(key, data []byte) ([]byte, error) {
	if !bytes.HasPrefix(data, []byte(magic)) {
		return nil, errors.New("not a guardenv file")
	}
	data = data[len(magic):]
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(data) < gcm.NonceSize() {
		return nil, errors.New("file truncated")
	}
	plain, err := gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], []byte(magic))
	if err != nil {
		return nil, errors.New("decryption failed (wrong secret or corrupted file)")
	}
	return plain, nil
}

// ---------- commands ----------

func cmdInit() error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	if err := ensureGitignore(root); err != nil {
		return err
	}
	files, err := findEnvFiles(root)
	if err != nil {
		return err
	}

	cfg, err := loadConfig(root)
	if err != nil {
		// fresh setup
		cfg = &Config{}
		b := make([]byte, 8)
		rand.Read(b)
		cfg.Secret = filepath.Base(root) + "-" + hex.EncodeToString(b)
		dir, err := secretDir()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, cfg.Secret), []byte(hex.EncodeToString(key)+"\n"), 0o600); err != nil {
			return err
		}
		fmt.Println("created secret", filepath.Join(dir, cfg.Secret), "(share it securely with your team!)")
	} else if _, err := loadKey(cfg); err != nil {
		return err
	}
	cfg.Files = mergeUnique(cfg.Files, files)

	b, _ := json.MarshalIndent(cfg, "", "  ")
	p := filepath.Join(root, filepath.FromSlash(configPath))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(p, append(b, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Println("wrote", configPath, "tracking:", strings.Join(cfg.Files, ", "))
	return installHooks()
}

func mergeUnique(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range append(a, b...) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func ensureGitignore(root string) error {
	p := filepath.Join(root, ".gitignore")
	b, err := os.ReadFile(p)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	var add []string
	for _, pat := range []string{".env", ".env.*"} {
		if !hasLine(string(b), pat) {
			add = append(add, pat)
		}
	}
	// keep example files committable
	if len(add) > 0 && !hasLine(string(b), "!.env.example") {
		add = append(add, "!.env.example")
	}
	if len(add) == 0 {
		return nil
	}
	s := string(b)
	if s != "" && !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	s += "\n# guardenv: plaintext secrets (encrypted copies are guarded.env*)\n" + strings.Join(add, "\n") + "\n"
	fmt.Println("updated .gitignore")
	return os.WriteFile(p, []byte(s), 0o644)
}

func hasLine(s, line string) bool {
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) == line {
			return true
		}
	}
	return false
}

func isEnvName(n string) bool {
	if n != ".env" && !strings.HasPrefix(n, ".env.") {
		return false
	}
	for _, skip := range []string{".example", ".sample", ".template", ".dist"} {
		if strings.HasSuffix(n, skip) {
			return false
		}
	}
	return true
}

// findEnvFiles walks the repo, skipping .git and directories ignored by git
// (node_modules, build output, ...).
func findEnvFiles(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			if rel != "." {
				if _, err := git(root, "check-ignore", "-q", filepath.ToSlash(rel)); err == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if isEnvName(d.Name()) {
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	return out, err
}

func cmdEncrypt(stage bool) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	cfg, err := loadConfig(root)
	if err != nil {
		return err
	}
	key, err := loadKey(cfg)
	if err != nil {
		return err
	}
	for _, rel := range cfg.Files {
		envP := filepath.Join(root, filepath.FromSlash(rel))
		plain, err := os.ReadFile(envP)
		if errors.Is(err, fs.ErrNotExist) {
			continue // nothing to guard; keep any existing guarded file
		} else if err != nil {
			return err
		}
		gRel := guardedPath(rel)
		gP := filepath.Join(root, gRel)
		// Avoid churn: keep the existing ciphertext if the content is unchanged.
		if old, err := os.ReadFile(gP); err == nil {
			if cur, err := decrypt(key, old); err == nil && bytes.Equal(cur, plain) {
				continue
			}
		}
		enc, err := encrypt(key, plain)
		if err != nil {
			return err
		}
		if err := os.WriteFile(gP, enc, 0o644); err != nil {
			return err
		}
		fmt.Println("encrypted", rel, "->", filepath.ToSlash(gRel))
		if stage {
			if _, err := git(root, "add", "--", filepath.ToSlash(gRel)); err != nil {
				return fmt.Errorf("git add %s failed", gRel)
			}
		}
	}
	if stage {
		git(root, "add", "--", configPath)
	}
	return nil
}

func cmdDecrypt(force bool) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	cfg, err := loadConfig(root)
	if err != nil {
		return err
	}
	key, err := loadKey(cfg)
	if err != nil {
		return err
	}
	var failed bool
	for _, rel := range cfg.Files {
		gP := filepath.Join(root, guardedPath(rel))
		enc, err := os.ReadFile(gP)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		plain, err := decrypt(key, enc)
		if err != nil {
			fmt.Fprintf(os.Stderr, "guardenv: %s: %v\n", rel, err)
			failed = true
			continue
		}
		envP := filepath.Join(root, filepath.FromSlash(rel))
		if cur, err := os.ReadFile(envP); err == nil {
			if bytes.Equal(cur, plain) {
				continue
			}
			if !force {
				fmt.Fprintf(os.Stderr, "guardenv: %s differs from guarded copy; kept local file (use 'guardenv decrypt --force' to overwrite)\n", rel)
				continue
			}
		}
		if err := os.MkdirAll(filepath.Dir(envP), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(envP, plain, 0o600); err != nil {
			return err
		}
		fmt.Println("decrypted", filepath.ToSlash(guardedPath(rel)), "->", rel)
	}
	if failed {
		return errors.New("some files could not be decrypted")
	}
	return nil
}

func installHooks() error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	hooksDir, err := git(root, "rev-parse", "--git-path", "hooks")
	if err != nil {
		return err
	}
	hooksDir = filepath.FromSlash(hooksDir)
	if !filepath.IsAbs(hooksDir) {
		hooksDir = filepath.Join(root, hooksDir)
	}
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return err
	}
	hooks := map[string]string{
		"pre-commit":    "guardenv encrypt --stage || exit 1",
		"post-checkout": "guardenv decrypt || true",
		"post-merge":    "guardenv decrypt || true",
	}
	for name, cmd := range hooks {
		p := filepath.Join(hooksDir, name)
		block := hookMarker + "\n" + cmd + "\n"
		existing, err := os.ReadFile(p)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			existing = []byte("#!/bin/sh\n")
		case err != nil:
			return err
		case bytes.Contains(existing, []byte(hookMarker)):
			continue
		case !bytes.HasSuffix(existing, []byte("\n")):
			existing = append(existing, '\n')
		}
		if err := os.WriteFile(p, append(existing, []byte(block)...), 0o755); err != nil {
			return err
		}
		fmt.Println("installed hook", name)
	}
	return nil
}
