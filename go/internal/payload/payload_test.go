package payload

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTree creates files under dir for a fake repo.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func readAll(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

// TestBuildFullRepo verifies a complete payload: binary + the shipped scripts
// (with the payload filenames the late-commands reference) + config/ + dotfiles/.
func TestBuildFullRepo(t *testing.T) {
	repo := t.TempDir()
	bin := filepath.Join(repo, "provisioner")
	os.WriteFile(bin, []byte("ELF"), 0o755)
	writeTree(t, filepath.Join(repo, "scripts"), map[string]string{
		"first-boot.service": "[Unit]\n",
		"fav.sh":             "#!/bin/sh\n",
		"copy-to-usb.py":     "should not be copied\n", // not in the manifest
	})
	writeTree(t, filepath.Join(repo, "config"), map[string]string{
		"apt-packages.list": "curl\n",
		"mise.toml":         "[tools]\nnode = \"lts\"\n",
	})
	writeTree(t, filepath.Join(repo, "dotfiles"), map[string]string{
		".bashrc": "export FOO=1\n",
	})

	out := filepath.Join(t.TempDir(), "payload")
	if err := Build(Options{
		Out: out, Binary: bin,
		Scripts:  filepath.Join(repo, "scripts"),
		Config:   filepath.Join(repo, "config"),
		Dotfiles: filepath.Join(repo, "dotfiles"),
	}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	for _, name := range []string{"provision", "first-boot.service"} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Errorf("missing payload file %s: %v", name, err)
		}
	}
	if readAll(t, filepath.Join(out, "provision")) != "ELF" {
		t.Error("provision content mismatch")
	}
	// The non-manifest script must not leak into the payload.
	if _, err := os.Stat(filepath.Join(out, "copy-to-usb.py")); err == nil {
		t.Error("copy-to-usb.py should not be in the payload")
	}
	if readAll(t, filepath.Join(out, "config", "apt-packages.list")) != "curl\n" {
		t.Error("config not copied")
	}
	if readAll(t, filepath.Join(out, "dotfiles", ".bashrc")) != "export FOO=1\n" {
		t.Error("dotfiles not copied")
	}
	// Scripts and binary must be executable.
	for _, name := range []string{"provision", "fav.sh", "first-boot.service"} {
		fi, err := os.Stat(filepath.Join(out, name))
		if err != nil {
			continue
		}
		if fi.Mode()&0o111 == 0 {
			t.Errorf("%s not executable: %v", name, fi.Mode())
		}
	}
}

// TestBuildBinaryOnly verifies a minimal payload (binary only) works and that
// the repo root can be "." (no scripts/config/dotfiles present).
func TestBuildBinaryOnly(t *testing.T) {
	out := filepath.Join(t.TempDir(), "payload")
	if err := Build(Options{Out: out, Binary: "/bin/true"}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(out, "provision"))
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if !strings.HasPrefix(string(b), "\x7fELF") {
		t.Errorf("provision should be the copied binary, got %q", string(b[:4]))
	}
}

// TestBuildRequiresBinary guards the nocloud/provision contract: the
// late-commands copy it unconditionally, so a payload without it must fail
// loudly at build time instead of installing a system that never provisions.
func TestBuildRequiresBinary(t *testing.T) {
	if err := Build(Options{Out: filepath.Join(t.TempDir(), "payload")}); err == nil {
		t.Fatal("Build without Binary must fail")
	}
}

// TestBuildMissingRequiredScript verifies a payload missing a script the
// late-commands reference unconditionally (e.g. first-boot.service) fails the
// build — a silent skip would break provisioning after install.
func TestBuildMissingRequiredScript(t *testing.T) {
	repo := t.TempDir()
	bin := filepath.Join(repo, "provisioner")
	os.WriteFile(bin, []byte("ELF"), 0o755)
	// scripts/ has only the optional fav.sh — first-boot.service etc. are absent.
	writeTree(t, filepath.Join(repo, "scripts"), map[string]string{
		"fav.sh": "#!/bin/sh\n",
	})
	err := Build(Options{
		Out: filepath.Join(t.TempDir(), "payload"), Binary: bin,
		Scripts: filepath.Join(repo, "scripts"),
	})
	if err == nil {
		t.Fatal("Build must fail when a required script is missing")
	}
	if !strings.Contains(err.Error(), "first-boot.service") {
		t.Errorf("error should name the missing script, got: %v", err)
	}
}

// TestBuildRejectsBadConfig verifies build-payload fails on a malformed config
// file — the failure must surface before the config is burned into a USB/ISO.
func TestBuildRejectsBadConfig(t *testing.T) {
	repo := t.TempDir()
	bin := filepath.Join(repo, "provisioner")
	os.WriteFile(bin, []byte("ELF"), 0o755)
	writeTree(t, filepath.Join(repo, "config"), map[string]string{
		"apt-packages.list": "curl\nBuild Essential\n", // space = invalid package name
	})
	err := Build(Options{
		Out: filepath.Join(t.TempDir(), "payload"), Binary: bin,
		Config: filepath.Join(repo, "config"),
	})
	if err == nil {
		t.Fatal("Build must fail on a malformed apt-packages.list")
	}
	if !strings.Contains(err.Error(), "apt-packages.list") {
		t.Errorf("error should point at the bad file, got: %v", err)
	}
}
