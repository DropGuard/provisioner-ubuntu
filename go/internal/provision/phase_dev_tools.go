package provision

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// phaseBrewDir pre-creates the Homebrew prefix owned by the target user so the
// Homebrew install script (run as the user via provision-user) skips its sudo
// step — a non-interactive sudo inside the re-exec would prompt for a password
// and fail.
func (p *Provisioner) phaseBrewDir(*Provisioner) error {
	parent := filepath.Dir(p.Cfg.BrewPrefix) // /home/linuxbrew
	// mkdir -p is idempotent; chown every run so a pre-existing root-owned dir
	// (manual mkdir, failed run) can't leave the user-facing install script
	// needing sudo for /home/linuxbrew.
	_, err := p.Runner.Run("", "bash", "-lc",
		fmt.Sprintf("mkdir -p %q && chown %s:%s %q", parent, p.Cfg.User, p.Cfg.User, parent))
	return err
}

// The user phases run via binary self re-exec as the target user
// (provisioner-ubuntu provision-user <name>). The cmd/provisioner
// provision-user command sets PATH to include the mise shims + brew, so plain
// tool names resolve here.

func (p *Provisioner) phaseHomebrew(*Provisioner) error {
	brew := filepath.Join(p.Cfg.BrewPrefix, "bin", "brew")
	if _, err := os.Stat(brew); err == nil {
		return nil // already installed
	}
	_, err := p.Runner.Run(p.Cfg.User, "bash", "-lc",
		`NONINTERACTIVE=1 /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"`)
	return err
}

func (p *Provisioner) phaseMise(*Provisioner) error {
	mise := filepath.Join(p.Cfg.Home, ".local", "bin", "mise")
	if _, err := os.Stat(mise); err == nil {
		return nil
	}
	_, err := p.Runner.Run(p.Cfg.User, "bash", "-lc", "curl https://mise.run | sh")
	return err
}

func (p *Provisioner) phaseBrewPackages(*Provisioner) error {
	brew := filepath.Join(p.Cfg.BrewPrefix, "bin", "brew")
	if _, err := os.Stat(brew); err != nil {
		return fmt.Errorf("brew not installed at %s", brew)
	}
	for _, pkg := range p.Cfg.BrewPackages {
		if _, err := p.Runner.Run("", brew, "list", "--formula", pkg); err == nil {
			continue
		}
		if _, err := p.Runner.Run("", brew, "install", pkg); err != nil {
			log.Printf("  [WARN] brew install %s failed: %v", pkg, err)
		}
	}
	return nil
}

func (p *Provisioner) phaseMiseTools(*Provisioner) error {
	mise := filepath.Join(p.Cfg.Home, ".local", "bin", "mise")
	if _, err := os.Stat(mise); err != nil {
		return fmt.Errorf("mise not installed")
	}
	cfgPath := filepath.Join(p.Cfg.Home, ".config", "mise", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(cfgPath, []byte(p.Cfg.MiseConfig), 0o644); err != nil {
		return err
	}
	_, err := p.Runner.Run("", mise, "install", "-y")
	return err
}

// phaseMiseShims symlinks every mise shim into ~/.local/bin. Non-interactive,
// non-login shells (how an Agent / SSH `bash -c` runs) do NOT read .bashrc or
// .profile, and the BASH_ENV trick does not reach GUI-session processes (its
// /etc/environment injection is PAM-login-only; systemd --user processes never
// see it). ~/.local/bin, however, IS on the session PATH (via EnvDConf), so a
// symlink there makes each mise-managed tool resolvable from every shell type.
// Re-run this phase after installing new mise tools.
func (p *Provisioner) phaseMiseShims(*Provisioner) error {
	shims := filepath.Join(p.Cfg.Home, ".local", "share", "mise", "shims")
	bin := filepath.Join(p.Cfg.Home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(shims)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		link := filepath.Join(shims, e.Name())
		target := filepath.Join(bin, e.Name())
		if cur, err := os.Readlink(target); err == nil && cur == link {
			continue // already points at the shim
		}
		if st, err := os.Lstat(target); err == nil && st.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("%s exists and is not a symlink — refusing to overwrite", target)
		}
		_ = os.Remove(target)
		if err := os.Symlink(link, target); err != nil {
			return err
		}
	}
	return nil
}

func (p *Provisioner) phaseNPMGlobals(*Provisioner) error {
	for _, pkg := range p.Cfg.NPMGlobals {
		if _, err := p.Runner.Run("", "npm", "install", "-g", pkg); err != nil {
			log.Printf("  [WARN] npm -g %s failed: %v", pkg, err)
		}
	}
	return nil
}

func (p *Provisioner) phaseClaudeCode(*Provisioner) error {
	claude := filepath.Join(p.Cfg.Home, ".local", "bin", "claude")
	if _, err := os.Stat(claude); err == nil {
		return nil
	}
	_, err := p.Runner.Run(p.Cfg.User, "bash", "-lc",
		`curl -fsSL https://claude.ai/install.sh | bash`)
	return err
}
