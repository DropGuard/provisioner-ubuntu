package provision

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"provisioner-ubuntu/internal/config"
)

// fakeRunner records commands, can fail those containing a substring, and can
// return canned stdout for a command name (outputs) or per-package dpkg-query
// status (pkgStatus).
type fakeRunner struct {
	commands     []string
	failContains []string
	outputs      map[string]string // keyed by command name
	pkgStatus    map[string]string // package -> dpkg-query -f=${Status} result
}

func (f *fakeRunner) Run(user, name string, args ...string) (string, error) {
	line := user + "|" + name + " " + strings.Join(args, " ")
	f.commands = append(f.commands, line)
	for _, s := range f.failContains {
		if strings.Contains(line, s) {
			return "", fmt.Errorf("simulated failure: %s", s)
		}
	}
	if name == "dpkg-query" && len(args) >= 3 {
		if st, ok := f.pkgStatus[args[len(args)-1]]; ok {
			return st, nil
		}
		return "", fmt.Errorf("package %s not installed", args[len(args)-1])
	}
	if out, ok := f.outputs[name]; ok {
		return out, nil
	}
	return "", nil
}

func (f *fakeRunner) had(substr string) bool {
	for _, c := range f.commands {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

// TestRunAllOrchestration verifies the root/user split: user phases are invoked
// via the self re-exec path (`sudo -u dailyuser <self> provision-user <phase>`).
func TestRunAllOrchestration(t *testing.T) {
	cfg := config.Default()
	cfg.Fcitx5SetupPath = "" // no script to run
	cfg.CCSwitchDeb = ""     // skip
	cfg.EnvDPath = t.TempDir() + "/env.d/zz-provisioner.conf"
	// All packages default to "not installed" (empty pkgStatus), so the
	// install commands actually run.
	fr := &fakeRunner{}
	p := &Provisioner{Cfg: cfg, Runner: fr}
	if err := p.RunAll("/usr/local/bin/provisioner-ubuntu"); err != nil {
		t.Fatalf("RunAll should succeed: %v", err)
	}

	// Root phases run directly.
	for _, want := range []string{"|apt-get update", "|apt-get install -y build-essential", "|usermod -aG docker"} {
		if !fr.had(want) {
			t.Errorf("missing root phase command: %q", want)
		}
	}
	// User phases go through self re-exec.
	for _, want := range []string{"homebrew", "mise", "brew-packages", "mise-tools",
		"npm-globals", "gnome-theme", "gnome-dock", "gnome-shortcuts", "git-config"} {
		if !fr.had("provision-user " + want) {
			t.Errorf("missing user phase re-exec for %q", want)
		}
	}
}

// TestPhaseCorePackagesIdempotent verifies that already-installed packages are
// skipped (dpkg-query reports "install ok installed" => no apt-get install).
// It also guards the half-removed state: a package in "deinstall ok
// config-files" must NOT count as installed (idempotency must re-install it).
func TestPhaseCorePackagesIdempotent(t *testing.T) {
	cfg := config.Default()
	cfg.CorePackages = []string{"git", "curl", "ghost"}
	fr := &fakeRunner{pkgStatus: map[string]string{
		"curl":  "install ok installed",
		"ghost": "deinstall ok config-files", // half-removed: must re-install
	}}
	p := &Provisioner{Cfg: cfg, Runner: fr}
	if err := p.phaseCorePackages(p); err != nil {
		t.Fatalf("phaseCorePackages: %v", err)
	}
	if !fr.had("apt-get install -y git") {
		t.Errorf("git should have been installed (dpkg-query reported not installed)")
	}
	if fr.had("apt-get install -y curl") {
		t.Errorf("curl was installed despite install ok installed (idempotency broken)")
	}
	if !fr.had("apt-get install -y ghost") {
		t.Errorf("ghost (deinstall ok config-files) should have been reinstalled")
	}
}

// TestPhaseShellEnvIdempotent verifies .bashrc is only appended once.
func TestPhaseShellEnvIdempotent(t *testing.T) {
	cfg := config.Default()
	cfg.Home = t.TempDir()
	cfg.EnvDPath = t.TempDir() + "/env.d/zz-provisioner.conf"
	if err := os.WriteFile(cfg.Home+"/.bashrc", []byte("existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &Provisioner{Cfg: cfg, Runner: &fakeRunner{}}
	if err := p.phaseShellEnv(p); err != nil {
		t.Fatalf("phaseShellEnv: %v", err)
	}
	data, _ := os.ReadFile(cfg.Home + "/.bashrc")
	if strings.Count(string(data), "brew shellenv") != 1 {
		t.Errorf("expected brew shellenv added once, got:\n%s", data)
	}
	// Second run: no duplicate.
	if err := p.phaseShellEnv(p); err != nil {
		t.Fatal(err)
	}
	data2, _ := os.ReadFile(cfg.Home + "/.bashrc")
	if strings.Count(string(data2), "brew shellenv") != 1 {
		t.Errorf("bashrc modified twice (not idempotent):\n%s", data2)
	}
}

// TestPhaseGPUDrivers: NVIDIA detected => ubuntu-drivers autoinstall runs;
// no NVIDIA => skipped (AMD/Intel use kernel drivers).
func TestPhaseGPUDrivers(t *testing.T) {
	cfg := config.Default()

	noNvidia := &fakeRunner{outputs: map[string]string{"lspci": "00:02.0 VGA compatible controller: Intel Corporation\n"}}
	if err := (&Provisioner{Cfg: cfg, Runner: noNvidia}).phaseGPUDrivers(nil); err != nil {
		t.Fatalf("no-nvidia: %v", err)
	}
	if noNvidia.had("ubuntu-drivers") {
		t.Error("no NVIDIA GPU must skip ubuntu-drivers")
	}

	withNvidia := &fakeRunner{outputs: map[string]string{"lspci": "01:00.0 VGA compatible controller: NVIDIA Corporation GA106\n"}}
	if err := (&Provisioner{Cfg: cfg, Runner: withNvidia}).phaseGPUDrivers(nil); err != nil {
		t.Fatalf("nvidia: %v", err)
	}
	if !withNvidia.had("ubuntu-drivers autoinstall") {
		t.Error("NVIDIA GPU should trigger ubuntu-drivers autoinstall")
	}
}

// TestFstabEntry verifies the mountpoint derivation (last 8 chars of the
// sanitized serial) and the NTFS-specific option override.
func TestFstabEntry(t *testing.T) {
	tests := []struct {
		name     string
		uuid     string
		serial   string
		ptype    string
		wantMnt  string
		wantLine string
	}{
		{
			name:     "ext4 default options",
			uuid:     "abc-123",
			serial:   "SAMSUNG_MZVL21T0HCLR_S1234567",
			ptype:    "ext4",
			wantMnt:  "/mnt/data-S1234567",
			wantLine: "UUID=abc-123 /mnt/data-S1234567 ext4 defaults,nofail,uid=1000,gid=1000,x-systemd.automount 0 2\n",
		},
		{
			name:     "ntfs windows options",
			uuid:     "XYZ",
			serial:   "WD-BLACK-1TB",
			ptype:    "ntfs",
			wantMnt:  "/mnt/data-LACK_1TB",
			wantLine: "UUID=XYZ /mnt/data-LACK_1TB ntfs defaults,nofail,uid=1000,gid=1000,umask=022,windows_names,x-systemd.automount 0 2\n",
		},
		{
			name:     "short serial not truncated",
			uuid:     "U",
			serial:   "ab",
			ptype:    "vfat",
			wantMnt:  "/mnt/data-ab",
			wantLine: "UUID=U /mnt/data-ab vfat defaults,nofail,uid=1000,gid=1000,x-systemd.automount 0 2\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mnt, line := fstabEntry(tc.uuid, tc.serial, tc.ptype)
			if mnt != tc.wantMnt {
				t.Errorf("mnt = %q, want %q", mnt, tc.wantMnt)
			}
			if line != tc.wantLine {
				t.Errorf("line = %q, want %q", line, tc.wantLine)
			}
		})
	}
}

// TestSanitize verifies non-alphanumeric serial characters become underscores.
func TestSanitize(t *testing.T) {
	if got := sanitize("38F6_0156_326B_257E"); got != "38F6_0156_326B_257E" {
		t.Errorf("sanitize = %q", got)
	}
	if got := sanitize("a b/c:d"); got != "a_b_c_d" {
		t.Errorf("sanitize = %q, want a_b_c_d", got)
	}
}

// TestFavoritesExpr verifies the GVariant array literal for gsettings
// favorite-apps: each entry single-quoted and comma-separated inside [ ].
func TestFavoritesExpr(t *testing.T) {
	if got := favoritesExpr([]string{"firefox_firefox.desktop", "code_code.desktop"}); got != "['firefox_firefox.desktop', 'code_code.desktop']" {
		t.Errorf("favoritesExpr = %q", got)
	}
	if got := favoritesExpr(nil); got != "[]" {
		t.Errorf("favoritesExpr(nil) = %q, want []", got)
	}
}

// TestRunAllFailFast verifies the fail-fast contract: a failing core phase
// aborts the run (error returned) and later phases never execute — so the
// oneshot service can be marked failed and retried.
func TestRunAllFailFast(t *testing.T) {
	cfg := config.Default()
	cfg.Fcitx5SetupPath = ""
	cfg.CCSwitchDeb = ""
	cfg.EnvDPath = t.TempDir() + "/env.d/zz-provisioner.conf"
	fr := &fakeRunner{failContains: []string{"apt-get install -y build-essential"}}
	p := &Provisioner{Cfg: cfg, Runner: fr}
	err := p.RunAll("/usr/local/bin/provisioner-ubuntu")
	if err == nil {
		t.Fatal("RunAll must return an error when a FailFast phase fails")
	}
	if !strings.Contains(err.Error(), "core-packages") {
		t.Errorf("error should name the failing phase, got %v", err)
	}
	if fr.had("provision-user homebrew") {
		t.Error("later user phases must not run after a FailFast failure")
	}
	if fr.had("systemctl disable first-boot.service") {
		t.Error("disable-service must not run after a FailFast failure")
	}
}

// TestRunUserPhaseByName verifies the provision-user entry rejects unknown and
// non-user phases.
func TestRunUserPhaseByName(t *testing.T) {
	cfg := config.Default()
	cfg.Fcitx5SetupPath = ""
	p := &Provisioner{Cfg: cfg, Runner: &fakeRunner{}}
	if err := p.RunUserPhaseByName("homebrew"); err != nil {
		t.Errorf("homebrew should run: %v", err)
	}
	if err := p.RunUserPhaseByName("core-packages"); err == nil {
		t.Error("core-packages is a root phase; provision-user must reject it")
	}
	if err := p.RunUserPhaseByName("nope"); err == nil {
		t.Error("unknown phase should error")
	}
}

// TestNeedsSudo guards the double-sudo bug: inside a provision-user re-exec the
// process is already the target user, so Runner must NOT re-sudo (a second
// `sudo -u dailyuser` would prompt for a password and fail non-interactively).
func TestNeedsSudo(t *testing.T) {
	cases := []struct {
		user   string
		reexec bool
		want   bool
	}{
		{"", true, false},          // no target user
		{"", false, false},         // no target user
		{"dailyuser", true, false}, // provision-user re-exec: already that user
		{"dailyuser", false, true}, // root context: must sudo
	}
	for _, c := range cases {
		if got := needsSudo(c.user, c.reexec); got != c.want {
			t.Errorf("needsSudo(%q, %v) = %v, want %v", c.user, c.reexec, got, c.want)
		}
	}
}

// TestPhaseMiseShims verifies the tool-resolution mechanism (formerly a bash
// check): each mise shim is symlinked into ~/.local/bin so a non-interactive
// shell (an Agent / SSH bash -c) can find it.
func TestPhaseMiseShims(t *testing.T) {
	home := t.TempDir()
	shims := filepath.Join(home, ".local", "share", "mise", "shims")
	for _, tool := range []string{"go", "node"} {
		if err := os.MkdirAll(shims, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(shims, tool), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	p := &Provisioner{Cfg: config.Provision{Home: home}, Runner: &fakeRunner{}}
	if err := p.phaseMiseShims(p); err != nil {
		t.Fatalf("phaseMiseShims: %v", err)
	}
	for _, tool := range []string{"go", "node"} {
		link := filepath.Join(home, ".local", "bin", tool)
		got, err := os.Readlink(link)
		if err != nil {
			t.Errorf("%s: %v", link, err)
			continue
		}
		if want := filepath.Join(shims, tool); got != want {
			t.Errorf("%s -> %q, want %q", link, got, want)
		}
	}
}

// ── Dotfiles tests ────────────────────────────────────────────────────────

// TestPhaseDotfilesNewFileInExistingDir verifies that a new file under an
// already-existing directory (e.g. ~/.config) is deployed, fixing the old
// top-level "dir exists → skip everything" bug.
func TestPhaseDotfilesNewFileInExistingDir(t *testing.T) {
	src := t.TempDir()
	home := t.TempDir()

	// Source: .config/kitty/kitty.conf
	srcConf := filepath.Join(src, ".config", "kitty")
	if err := os.MkdirAll(srcConf, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcConf, "kitty.conf"), []byte("font_size 18"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Dest: ~/.config already exists (with some other app's file)
	dstExisting := filepath.Join(home, ".config", "other-app")
	if err := os.MkdirAll(dstExisting, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PROVISIONER_DOTFILES", src)
	p := &Provisioner{Cfg: config.Provision{Home: home, User: "dailyuser"}, Runner: &fakeRunner{}}
	if err := p.phaseDotfiles(p); err != nil {
		t.Fatalf("phaseDotfiles: %v", err)
	}

	// New file must exist with correct content.
	got, err := os.ReadFile(filepath.Join(home, ".config", "kitty", "kitty.conf"))
	if err != nil {
		t.Fatalf("kitty.conf was not deployed: %v", err)
	}
	if string(got) != "font_size 18" {
		t.Errorf("content = %q, want %q", string(got), "font_size 18")
	}
}

// TestPhaseDotfilesExistingFilePreserved verifies that files already present at
// the destination are NOT overwritten — protecting user local edits.
func TestPhaseDotfilesExistingFilePreserved(t *testing.T) {
	src := t.TempDir()
	home := t.TempDir()

	// Source: .bashrc with provisioner content.
	if err := os.WriteFile(filepath.Join(src, ".bashrc"), []byte("source-provisioned"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Dest: .bashrc already exists with user's local edits.
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte("local-edits"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PROVISIONER_DOTFILES", src)
	p := &Provisioner{Cfg: config.Provision{Home: home, User: "dailyuser"}, Runner: &fakeRunner{}}
	if err := p.phaseDotfiles(p); err != nil {
		t.Fatalf("phaseDotfiles: %v", err)
	}

	got, _ := os.ReadFile(filepath.Join(home, ".bashrc"))
	if string(got) != "local-edits" {
		t.Errorf("existing file was overwritten: got %q, want %q", string(got), "local-edits")
	}
}

// TestPhaseDotfilesMixedDeploy verifies that when a directory has both new and
// existing files, only the new ones are deployed.
func TestPhaseDotfilesMixedDeploy(t *testing.T) {
	src := t.TempDir()
	home := t.TempDir()

	// Source: .config/app/{a.conf, b.conf}
	srcApp := filepath.Join(src, ".config", "app")
	if err := os.MkdirAll(srcApp, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(srcApp, "a.conf"), []byte("new-a"), 0o644)
	os.WriteFile(filepath.Join(srcApp, "b.conf"), []byte("new-b"), 0o644)

	// Dest: ~/.config/app/ already exists with a.conf (old), but not b.conf.
	dstApp := filepath.Join(home, ".config", "app")
	if err := os.MkdirAll(dstApp, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dstApp, "a.conf"), []byte("local-a"), 0o644)

	t.Setenv("PROVISIONER_DOTFILES", src)
	p := &Provisioner{Cfg: config.Provision{Home: home, User: "dailyuser"}, Runner: &fakeRunner{}}
	if err := p.phaseDotfiles(p); err != nil {
		t.Fatalf("phaseDotfiles: %v", err)
	}

	// a.conf preserved.
	a, _ := os.ReadFile(filepath.Join(dstApp, "a.conf"))
	if string(a) != "local-a" {
		t.Errorf("a.conf overwritten: %q", string(a))
	}
	// b.conf deployed.
	b, err := os.ReadFile(filepath.Join(dstApp, "b.conf"))
	if err != nil {
		t.Fatalf("b.conf not deployed: %v", err)
	}
	if string(b) != "new-b" {
		t.Errorf("b.conf = %q, want %q", string(b), "new-b")
	}
}

// TestPhaseDotfilesSymlink verifies that symlinks in the dotfiles source are
// preserved as symlinks at the destination.
func TestPhaseDotfilesSymlink(t *testing.T) {
	src := t.TempDir()
	home := t.TempDir()

	// Source: real-file and a symlink to it.
	if err := os.WriteFile(filepath.Join(src, "real-file"), []byte("target"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real-file", filepath.Join(src, "link-file")); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PROVISIONER_DOTFILES", src)
	p := &Provisioner{Cfg: config.Provision{Home: home, User: "dailyuser"}, Runner: &fakeRunner{}}
	if err := p.phaseDotfiles(p); err != nil {
		t.Fatalf("phaseDotfiles: %v", err)
	}

	// Symlink preserved (not dereferenced).
	target, err := os.Readlink(filepath.Join(home, "link-file"))
	if err != nil {
		t.Fatalf("link-file should be a symlink: %v", err)
	}
	if target != "real-file" {
		t.Errorf("symlink target = %q, want %q", target, "real-file")
	}
}

// TestPhaseDotfilesEmptyEnv verifies no-op when the env var is unset or the
// directory doesn't exist.
func TestPhaseDotfilesEmptyEnv(t *testing.T) {
	p := &Provisioner{Cfg: config.Provision{Home: t.TempDir(), User: "dailyuser"}, Runner: &fakeRunner{}}

	// Unset env var: no-op.
	if err := p.phaseDotfiles(p); err != nil {
		t.Errorf("empty env: %v", err)
	}

	// Nonexistent dir: no-op.
	t.Setenv("PROVISIONER_DOTFILES", "/nonexistent/dotfiles")
	if err := p.phaseDotfiles(p); err != nil {
		t.Errorf("nonexistent dir: %v", err)
	}
}

// ── Phase filter tests ─────────────────────────────────────────────────────

// TestRunAllPhaseFilter verifies --phase filters to a single phase.
func TestRunAllPhaseFilter(t *testing.T) {
	cfg := config.Default()
	cfg.Fcitx5SetupPath = ""
	cfg.CCSwitchDeb = ""
	cfg.EnvDPath = t.TempDir() + "/env.d/zz-provisioner.conf"
	fr := &fakeRunner{}
	p := &Provisioner{Cfg: cfg, Runner: fr, Phase: "docker-group"}
	if err := p.RunAll("/usr/local/bin/provisioner-ubuntu"); err != nil {
		t.Fatalf("RunAll with phase filter: %v", err)
	}
	// Only the selected phase ran.
	if !fr.had("usermod -aG docker") {
		t.Error("docker-group should have run")
	}
	if fr.had("apt-get update") {
		t.Error("apt-update should NOT run when filtered to docker-group")
	}
}

// TestRunAllPhaseFilterUnknown verifies an unknown phase name returns an error.
func TestRunAllPhaseFilterUnknown(t *testing.T) {
	cfg := config.Default()
	cfg.Fcitx5SetupPath = ""
	cfg.EnvDPath = t.TempDir() + "/env.d/zz-provisioner.conf"
	fr := &fakeRunner{}
	p := &Provisioner{Cfg: cfg, Runner: fr, Phase: "nope"}
	err := p.RunAll("/usr/local/bin/provisioner-ubuntu")
	if err == nil {
		t.Fatal("expected error for unknown phase")
	}
	if !strings.Contains(err.Error(), "unknown phase") {
		t.Errorf("error should say 'unknown phase', got: %v", err)
	}
}

// TestRunAllPhaseFilterUserPhase verifies --phase works for user phases too.
func TestRunAllPhaseFilterUserPhase(t *testing.T) {
	cfg := config.Default()
	cfg.Fcitx5SetupPath = ""
	cfg.CCSwitchDeb = ""
	cfg.EnvDPath = t.TempDir() + "/env.d/zz-provisioner.conf"
	fr := &fakeRunner{}
	p := &Provisioner{Cfg: cfg, Runner: fr, Phase: "git-config"}
	if err := p.RunAll("/usr/local/bin/provisioner-ubuntu"); err != nil {
		t.Fatalf("RunAll with user phase filter: %v", err)
	}
	if !fr.had("provision-user git-config") {
		t.Error("git-config user phase should be dispatched")
	}
	if fr.had("apt-get update") {
		t.Error("root phases should NOT run when filtered to git-config")
	}
}

// TestRunAllPhaseFilterEmpty runs all phases (backward compat).
func TestRunAllPhaseFilterEmpty(t *testing.T) {
	cfg := config.Default()
	cfg.Fcitx5SetupPath = ""
	cfg.CCSwitchDeb = ""
	cfg.EnvDPath = t.TempDir() + "/env.d/zz-provisioner.conf"
	fr := &fakeRunner{}
	p := &Provisioner{Cfg: cfg, Runner: fr, Phase: ""}
	if err := p.RunAll("/usr/local/bin/provisioner-ubuntu"); err != nil {
		t.Fatalf("RunAll with no filter: %v", err)
	}
	if !fr.had("apt-get update") {
		t.Error("all phases should run when Phase is empty")
	}
}
