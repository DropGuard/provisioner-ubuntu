package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestLoadMissingDir verifies that an absent config dir returns the built-in
// defaults unchanged (provision running without the copied config/).
func TestLoadMissingDir(t *testing.T) {
	cfg, err := Load(t.TempDir() + "/nonexistent")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	d := Default()
	if len(cfg.CorePackages) != len(d.CorePackages) || cfg.CorePackages[0] != d.CorePackages[0] {
		t.Errorf("missing dir should keep defaults, got %v", cfg.CorePackages)
	}
	if cfg.BrewPackages[0] != d.BrewPackages[0] {
		t.Errorf("missing dir should keep default brew, got %v", cfg.BrewPackages)
	}
}

// TestLoadOverrides verifies the config files replace (not append) the
// defaults, and that the core/extra split honors the "# --- core" marker.
func TestLoadOverrides(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "apt-packages.list", `# --- core (fail-fast) ---
vim
htop

# --- tools ---
ripgrep
jq
`)
	writeFile(t, dir, "brew-packages.list", `# Homebrew
yazi
gh
`)
	writeFile(t, dir, "mise.toml", `[tools]
node = "lts"
python = "latest"
`)
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.CorePackages) != 2 || cfg.CorePackages[0] != "vim" || cfg.CorePackages[1] != "htop" {
		t.Errorf("core packages = %v, want [vim htop]", cfg.CorePackages)
	}
	if len(cfg.ExtraPackages) != 2 || cfg.ExtraPackages[0] != "ripgrep" || cfg.ExtraPackages[1] != "jq" {
		t.Errorf("extra packages = %v, want [ripgrep jq]", cfg.ExtraPackages)
	}
	if len(cfg.BrewPackages) != 2 || cfg.BrewPackages[0] != "yazi" {
		t.Errorf("brew = %v, want [yazi gh]", cfg.BrewPackages)
	}
	if cfg.MiseConfig != "[tools]\nnode = \"lts\"\npython = \"latest\"\n" {
		t.Errorf("MiseConfig not verbatim: %q", cfg.MiseConfig)
	}
	if cfg.MiseTools["node"] != "lts" || cfg.MiseTools["python"] != "latest" {
		t.Errorf("MiseTools = %v", cfg.MiseTools)
	}
}

// TestLoadOverridesReplacesNotAppends: a config file replaces the default
// entirely — the default core package must not leak through.
func TestLoadOverridesReplacesNotAppends(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "apt-packages.list", "# --- core ---\nonly-this\n")
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.CorePackages) != 1 || cfg.CorePackages[0] != "only-this" {
		t.Errorf("core = %v, want [only-this] (default must not leak)", cfg.CorePackages)
	}
}

// TestLoadMissingFileKeepsDefault: if only some files exist, the others keep
// their built-in defaults.
func TestLoadMissingFileKeepsDefault(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "brew-packages.list", "bat\n")
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.BrewPackages) != 1 || cfg.BrewPackages[0] != "bat" {
		t.Errorf("brew = %v, want [bat]", cfg.BrewPackages)
	}
	d := Default()
	if len(cfg.CorePackages) != len(d.CorePackages) {
		t.Errorf("core should keep default when apt-packages.list missing, got %v", cfg.CorePackages)
	}
}

// TestValidateOK: a well-formed config directory passes validation.
func TestValidateOK(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "apt-packages.list", `# --- core ---
build-essential
golang-1.26-go

# --- tools ---
libc6-dev
jq
`)
	writeFile(t, dir, "brew-packages.list", "yazi\ngh\nhomebrew/core/tokei\n")
	writeFile(t, dir, "mise.toml", `[tools]
node = "lts"
python = "latest"
`)
	if errs := Validate(dir); len(errs) != 0 {
		t.Fatalf("Validate = %v, want none", errs)
	}
}

// TestValidateMissingDir: an absent dir is not an error (defaults apply).
func TestValidateMissingDir(t *testing.T) {
	if errs := Validate(t.TempDir() + "/nonexistent"); len(errs) != 0 {
		t.Errorf("Validate(nonexistent) = %v, want none", errs)
	}
}

// TestValidateMalformedPackages: a bad package name is caught with its file
// and line so the fix is obvious before the config is burned into a USB/ISO.
func TestValidateMalformedPackages(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "apt-packages.list", "curl\nBuild Essential\n")
	writeFile(t, dir, "brew-packages.list", "yazi\n\n\nBAD-NAME!\n")
	errs := Validate(dir)
	if len(errs) != 2 {
		t.Fatalf("Validate = %v, want 2 errors (one per bad file)", errs)
	}
	joined := errs[0].Error() + " " + errs[1].Error()
	if !strings.Contains(joined, "apt-packages.list:2") {
		t.Errorf("should flag apt-packages.list line 2, got %v", errs)
	}
	if !strings.Contains(joined, "brew-packages.list:4") {
		t.Errorf("should flag brew-packages.list line 4, got %v", errs)
	}
}

// TestValidateMiseEmptyTools: a [tools] section with no entries means the
// toolchain phase installs nothing — validation must reject it.
func TestValidateMiseEmptyTools(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "mise.toml", "[tools]\n")
	errs := Validate(dir)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "mise.toml") {
		t.Errorf("Validate = %v, want a mise.toml error", errs)
	}
}

// TestValidateMiseMalformedEntry: a non-`key = "value"` line under [tools]
// (the exact shape parseMiseTools understands) must be rejected.
func TestValidateMiseMalformedEntry(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "mise.toml", "[tools]\nnode = lts\n")
	errs := Validate(dir)
	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "node = lts") {
		t.Errorf("Validate = %v, want a malformed-entry error", errs)
	}
}
