package provision

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// phaseFcitx5 installs fcitx5 (and Rime), downloads the Rime-Ice dictionary,
// and writes the environment/autostart configuration for the target user.
func (p *Provisioner) phaseFcitx5(pr *Provisioner) error {
	if !p.installed("fcitx5") {
		args := append([]string{"install", "-y"}, p.Cfg.Fcitx5Packages...)
		if _, err := p.Runner.Run("", "apt-get", args...); err != nil {
			return fmt.Errorf("fcitx5 apt install: %w", err)
		}
	}

	// 1. Download Rime-Ice
	rimeDir := filepath.Join(p.Cfg.Home, ".local", "share", "fcitx5", "rime")
	if _, err := os.Stat(filepath.Join(rimeDir, ".git")); os.IsNotExist(err) {
		os.MkdirAll(filepath.Dir(rimeDir), 0o755)
		p.Runner.Run("", "chown", "-R", p.Cfg.User+":"+p.Cfg.User, filepath.Dir(filepath.Dir(rimeDir)))
		if _, err := p.Runner.Run(p.Cfg.User, "git", "clone", "--depth=1", "https://github.com/iDvel/rime-ice.git", rimeDir); err != nil {
			log.Printf("  [WARN] fcitx5 rime-ice clone failed: %v", err)
		}
	}

	// 2. Write environment.d
	envDir := filepath.Join(p.Cfg.Home, ".config", "environment.d")
	os.MkdirAll(envDir, 0o755)
	envConf := "GTK_IM_MODULE=fcitx5\nQT_IM_MODULE=fcitx5\nXMODIFIERS=@im=fcitx5\nSDL_IM_MODULE=fcitx5\nGLFW_IM_MODULE=ibus\n"
	os.WriteFile(filepath.Join(envDir, "fcitx5.conf"), []byte(envConf), 0o644)

	// 3. Write autostart
	autoDir := filepath.Join(p.Cfg.Home, ".config", "autostart")
	os.MkdirAll(autoDir, 0o755)
	fcitx5Desktop := "[Desktop Entry]\nType=Application\nName=Fcitx 5\nExec=/usr/bin/fcitx5\nTerminal=false\nX-GNOME-Autostart-enabled=true\nX-GNOME-AutoRestart=true\n"
	os.WriteFile(filepath.Join(autoDir, "fcitx5.desktop"), []byte(fcitx5Desktop), 0o644)

	rimeDesktop := "[Desktop Entry]\nType=Application\nName=Fcitx5 Rime Setup\nExec=/usr/local/bin/provision setup-im\nTerminal=false\nX-GNOME-Autostart-enabled=true\nX-GNOME-Autostart-Delay=5\n"
	os.WriteFile(filepath.Join(autoDir, "fcitx5-rime.desktop"), []byte(rimeDesktop), 0o644)

	// 4. Wayland Clipboard Sync Service
	systemdDir := filepath.Join(p.Cfg.Home, ".config", "systemd", "user")
	os.MkdirAll(filepath.Join(systemdDir, "default.target.wants"), 0o755)
	wlClipSvc := "[Unit]\nDescription=Wayland Clipboard to Primary Sync\nAfter=graphical-session.target\n\n[Service]\nType=simple\nExecStart=/usr/bin/wl-paste --watch /usr/bin/wl-copy -p\nRestart=always\nRestartSec=2\n\n[Install]\nWantedBy=default.target\n"
	svcPath := filepath.Join(systemdDir, "wl-clip-sync.service")
	os.WriteFile(svcPath, []byte(wlClipSvc), 0o644)
	os.Symlink(svcPath, filepath.Join(systemdDir, "default.target.wants", "wl-clip-sync.service"))

	// Fix ownership of .config
	p.Runner.Run("", "chown", "-R", p.Cfg.User+":"+p.Cfg.User, filepath.Join(p.Cfg.Home, ".config"))

	return nil
}

func (p *Provisioner) phaseDisableService(*Provisioner) error {
	_, err := p.Runner.Run("", "systemctl", "disable", "first-boot.service")
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
