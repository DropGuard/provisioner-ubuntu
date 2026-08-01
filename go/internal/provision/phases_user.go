package provision

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// The user phases run via binary self re-exec as the target user
// (provisioner-ubuntu provision-user <name>). The cmd/provisioner
// provision-user command sets PATH to include the mise shims + brew, so plain
// tool names resolve here.

func (p *Provisioner) userPath() string {
	return strings.Join([]string{
		filepath.Join(p.Cfg.BrewPrefix, "bin"),
		filepath.Join(p.Cfg.Home, ".local", "bin"),
		filepath.Join(p.Cfg.Home, ".local", "share", "mise", "shims"),
		os.Getenv("PATH"),
	}, ":")
}

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

func (p *Provisioner) phaseNPMGlobals(*Provisioner) error {
	for _, pkg := range p.Cfg.NPMGlobals {
		if _, err := p.Runner.Run("", "npm", "install", "-g", pkg); err != nil {
			log.Printf("  [WARN] npm -g %s failed: %v", pkg, err)
		}
	}
	return nil
}

func (p *Provisioner) phaseOpenCode(*Provisioner) error {
	opencode := filepath.Join(p.Cfg.Home, ".local", "bin", "opencode")
	if _, err := os.Stat(opencode); err == nil {
		return nil
	}
	_, err := p.Runner.Run(p.Cfg.User, "bash", "-lc",
		`curl -fsSL https://opencode.ai/install | bash`)
	return err
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

// gnomeSet runs a gsettings command as the target user. Without a live session
// the change lands in the user's dconf and applies at next login.
func (p *Provisioner) gnomeSet(schema, key, value string) error {
	_, err := p.Runner.Run(p.Cfg.User, "gsettings", "set", schema, key, value)
	return err
}

func (p *Provisioner) phaseGnomeTheme(*Provisioner) error {
	if !p.Cfg.GnomeTheme {
		return nil
	}
	if err := p.gnomeSet("org.gnome.desktop.interface", "color-scheme", "prefer-dark"); err != nil {
		return err
	}
	_, err := p.Runner.Run(p.Cfg.User, "gsettings", "set", "org.gnome.desktop.interface", "gtk-theme", "Yaru-dark")
	return err
}

func (p *Provisioner) phaseGnomeDock(*Provisioner) error {
	_, err := p.Runner.Run(p.Cfg.User, "gsettings", "set", "org.gnome.shell", "favorite-apps", favoritesExpr(p.Cfg.DockFavorites))
	return err
}

// favoritesExpr renders the GVariant array literal gsettings expects for
// org.gnome.shell favorite-apps: each entry single-quoted, comma-separated,
// wrapped in [ ] (e.g. "['a.desktop', 'b.desktop']").
func favoritesExpr(apps []string) string {
	return "[" + strings.Join(mapSlice(apps, func(s string) string {
		return "'" + s + "'"
	}), ", ") + "]"
}

func (p *Provisioner) phaseGnomeShortcuts(*Provisioner) error {
	base := "org.gnome.settings-daemon.plugins.media-keys.custom-keybinding:" +
		"/org/gnome/settings-daemon/plugins/media-keys/custom-keybindings/custom0/"
	p.Runner.Run(p.Cfg.User, "gsettings", "set", "org.gnome.settings-daemon.plugins.media-keys", "custom-keybindings",
		"['/org/gnome/settings-daemon/plugins/media-keys/custom-keybindings/custom0/']")
	p.Runner.Run(p.Cfg.User, "gsettings", "set", base, "name", "Terminal")
	p.Runner.Run(p.Cfg.User, "gsettings", "set", base, "command", "gnome-terminal")
	_, err := p.Runner.Run(p.Cfg.User, "gsettings", "set", base, "binding", "<Super>Return")
	return err
}

func (p *Provisioner) phaseGitConfig(*Provisioner) error {
	// Identity comes from env (GIT_USER_NAME / GIT_USER_EMAIL), set when running.
	if name := os.Getenv("GIT_USER_NAME"); name != "" {
		p.Runner.Run(p.Cfg.User, "git", "config", "--global", "user.name", name)
	}
	if email := os.Getenv("GIT_USER_EMAIL"); email != "" {
		p.Runner.Run(p.Cfg.User, "git", "config", "--global", "user.email", email)
	}
	// Sensible defaults regardless of identity.
	p.Runner.Run(p.Cfg.User, "git", "config", "--global", "init.defaultBranch", "main")
	p.Runner.Run(p.Cfg.User, "git", "config", "--global", "pull.rebase", "false")
	return nil
}

func (p *Provisioner) phaseDotfiles(*Provisioner) error {
	// Dotfiles ship alongside the binary under share/provisioner-ubuntu/dotfiles.
	dotfilesDir := os.Getenv("PROVISIONER_DOTFILES")
	if dotfilesDir == "" {
		return nil
	}
	entries, err := os.ReadDir(dotfilesDir)
	if err != nil {
		return nil // no dotfiles dir — skip
	}
	for _, e := range entries {
		src := filepath.Join(dotfilesDir, e.Name())
		dst := filepath.Join(p.Cfg.Home, e.Name())
		if _, err := os.Stat(dst); err == nil {
			continue // already exists
		}
		if err := copyTree(src, dst); err != nil {
			log.Printf("  [WARN] dotfile %s: %v", e.Name(), err)
			continue
		}
		p.Runner.Run("", "chown", "-R", p.Cfg.User+":"+p.Cfg.User, dst)
	}
	return nil
}

// --- helpers ---

func mapSlice[T any, U any](in []T, f func(T) U) []U {
	out := make([]U, len(in))
	for i, v := range in {
		out[i] = f(v)
	}
	return out
}

func copyTree(src, dst string) error {
	return filepathWalkCopy(src, dst)
}
