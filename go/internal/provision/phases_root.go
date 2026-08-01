package provision

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// installed reports whether pkg is actually installed. `dpkg -s` exits 0 even
// for packages left in "deinstall ok config-files" state (removed but config
// retained), so it must not be used for idempotency checks — that would skip
// re-installing a half-removed package. Match the exact status string instead.
func (p *Provisioner) installed(pkg string) bool {
	out, err := p.Runner.Run("", "dpkg-query", "-W", "-f=${Status}", pkg)
	if err != nil {
		return false
	}
	return strings.Contains(out, "install ok installed")
}

func (p *Provisioner) phaseAptUpdate(*Provisioner) error {
	if _, err := p.Runner.Run("", "apt-get", "update", "-qq"); err != nil {
		return err
	}
	_, err := p.Runner.Run("", "apt-get", "upgrade", "-y", "-qq")
	return err
}

func (p *Provisioner) phaseCorePackages(*Provisioner) error {
	for _, pkg := range p.Cfg.CorePackages {
		if p.installed(pkg) {
			continue
		}
		if _, err := p.Runner.Run("", "apt-get", "install", "-y", pkg); err != nil {
			return fmt.Errorf("core package %s: %w", pkg, err)
		}
	}
	for _, pkg := range p.Cfg.ExtraPackages {
		if p.installed(pkg) {
			continue
		}
		if _, err := p.Runner.Run("", "apt-get", "install", "-y", pkg); err != nil {
			log.Printf("  [WARN] extra package %s failed: %v", pkg, err)
		}
	}
	return nil
}

func (p *Provisioner) phaseDockerGroup(*Provisioner) error {
	if !p.Cfg.AddUserToDocker {
		return nil
	}
	_, err := p.Runner.Run("", "usermod", "-aG", "docker", p.Cfg.User)
	return err
}

// phaseGPUDrivers installs the recommended NVIDIA driver if an NVIDIA GPU is
// detected. AMD/Intel use in-kernel drivers (amdgpu/i915) and need nothing.
func (p *Provisioner) phaseGPUDrivers(*Provisioner) error {
	if !p.Cfg.GPUDrivers {
		return nil
	}
	out, err := p.Runner.Run("", "lspci")
	if err != nil {
		return err
	}
	if !strings.Contains(strings.ToLower(out), "nvidia") {
		return nil // no NVIDIA GPU — kernel drivers cover AMD/Intel
	}
	// ubuntu-drivers (in ubuntu-drivers-common) detects and installs the
	// recommended driver + kernel modules. Best-effort: failures warn.
	if _, err := p.Runner.Run("", "ubuntu-drivers", "autoinstall"); err != nil {
		return fmt.Errorf("ubuntu-drivers autoinstall: %w", err)
	}
	return nil
}

// phaseFcitx5 installs the fcitx5 packages (root), then runs the user-side
// setup script as the target user. The script skips its own `sudo apt-get`
// once fcitx5 is present, so it never prompts for a password.
func (p *Provisioner) phaseFcitx5(pr *Provisioner) error {
	if !p.installed("fcitx5") {
		args := append([]string{"install", "-y"}, p.Cfg.Fcitx5Packages...)
		if _, err := p.Runner.Run("", "apt-get", args...); err != nil {
			return fmt.Errorf("fcitx5 apt install: %w", err)
		}
	}
	if p.Cfg.Fcitx5SetupPath == "" {
		return nil
	}
	if _, err := p.Runner.Run(p.Cfg.User, p.Cfg.Fcitx5SetupPath); err != nil {
		// D-Bus may not be ready before the user session starts; the script
		// degrades gracefully. Report as a warning, not a fatal error.
		log.Printf("  [WARN] fcitx5 user setup: %v (session D-Bus may not be ready — re-run after login)", err)
	}
	return nil
}

// phaseShellEnv writes the global PATH via /etc/environment.d and appends the
// brew/mise activation block to the user's .bashrc.
func (p *Provisioner) phaseShellEnv(*Provisioner) error {
	envd := p.Cfg.EnvDPath
	if _, err := os.Stat(envd); os.IsNotExist(err) {
		os.MkdirAll(filepath.Dir(envd), 0o755)
		if err := os.WriteFile(envd, []byte(p.Cfg.EnvDConf), 0o644); err != nil {
			return err
		}
	}
	bashrc := filepath.Join(p.Cfg.Home, ".bashrc")
	data, err := os.ReadFile(bashrc)
	if err != nil {
		return err
	}
	if strings.Contains(string(data), "brew shellenv") {
		return nil // already added
	}
	f, err := os.OpenFile(bashrc, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString("\n" + p.Cfg.BashrcAdditions); err != nil {
		return err
	}
	_, err = p.Runner.Run("", "chown", p.Cfg.User+":"+p.Cfg.User, bashrc)
	return err
}

func (p *Provisioner) phaseDisableService(*Provisioner) error {
	_, err := p.Runner.Run("", "systemctl", "disable", "first-boot.service")
	return err
}
