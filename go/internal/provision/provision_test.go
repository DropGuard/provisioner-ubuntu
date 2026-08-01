package provision

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"provisioner-ubuntu/internal/config"
)

// fakeRunner records commands, can fail those containing a substring, and can
// return canned stdout for a command name (outputs).
type fakeRunner struct {
	commands     []string
	failContains []string
	outputs      map[string]string // keyed by command name
}

func (f *fakeRunner) Run(user, name string, args ...string) (string, error) {
	line := user + "|" + name + " " + strings.Join(args, " ")
	f.commands = append(f.commands, line)
	for _, s := range f.failContains {
		if strings.Contains(line, s) {
			return "", fmt.Errorf("simulated failure: %s", s)
		}
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
	// Make core packages look uninstalled so the install command actually runs.
	fr := &fakeRunner{failContains: []string{"dpkg -s"}}
	p := &Provisioner{Cfg: cfg, Runner: fr}
	p.RunAll("/usr/local/bin/provisioner-ubuntu")

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
// skipped (dpkg -s succeeds => no apt-get install for it).
func TestPhaseCorePackagesIdempotent(t *testing.T) {
	cfg := config.Default()
	cfg.CorePackages = []string{"git", "curl"}
	fr := &fakeRunner{failContains: []string{"dpkg -s git"}} // git NOT installed, curl IS
	p := &Provisioner{Cfg: cfg, Runner: fr}
	if err := p.phaseCorePackages(p); err != nil {
		t.Fatalf("phaseCorePackages: %v", err)
	}
	if !fr.had("apt-get install -y git") {
		t.Errorf("git should have been installed (dpkg -s failed)")
	}
	if fr.had("apt-get install -y curl") {
		t.Errorf("curl was installed despite dpkg -s succeeding (idempotency broken)")
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
