package provision

import (
	"fmt"
	"log"
	"os"
)

// phaseDefaultApps wires the sensible desktop defaults for the apps the
// provision installs: kitty as the terminal emulator, qimgv for images (GNOME
// otherwise opens images in the web browser), vlc for video. Each step is
// independent + best-effort so one missing app can't suppress the others.
// update-alternatives is system-wide (root); the mime defaults are per-user and
// must run as the target user, or they'd land in /root and never apply.
func (p *Provisioner) phaseDefaultApps(*Provisioner) error {
	if _, err := p.Runner.Run("", "update-alternatives", "--set", "x-terminal-emulator", "/usr/bin/kitty"); err != nil {
		log.Printf("  [WARN] kitty as default terminal: %v", err)
	}
	for _, mime := range []string{"image/jpeg", "image/png", "image/gif", "image/webp", "image/bmp", "image/tiff"} {
		if _, err := p.Runner.Run(p.Cfg.User, "xdg-mime", "default", "qimgv.desktop", mime); err != nil {
			log.Printf("  [WARN] default %s → qimgv: %v", mime, err)
		}
	}
	for _, mime := range []string{"video/mp4", "video/webm", "video/x-matroska", "video/quicktime", "video/x-msvideo", "video/3gpp"} {
		if _, err := p.Runner.Run(p.Cfg.User, "xdg-mime", "default", "vlc.desktop", mime); err != nil {
			log.Printf("  [WARN] default %s → vlc: %v", mime, err)
		}
	}
	return nil
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

// phaseSnaps installs Snap packages (moved from cloud-init for architectural consistency).
func (p *Provisioner) phaseSnaps(pr *Provisioner) error {
	for _, snap := range p.Cfg.Snaps {
		args := []string{"install", snap.Name}
		if snap.Classic {
			args = append(args, "--classic")
		}
		if _, err := p.Runner.Run("", "snap", args...); err != nil {
			log.Printf("  warn: snap install %s failed: %v", snap.Name, err)
		}
	}
	return nil
}

// phaseFlatpaks installs flatpak, adds flathub, and installs packages
func (p *Provisioner) phaseFlatpaks(pr *Provisioner) error {
	// 1. Ensure flatpak is installed
	if _, err := p.Runner.Run("", "apt-get", "install", "-y", "flatpak"); err != nil {
		return err
	}

	// 2. Add flathub repository
	if _, err := p.Runner.Run("", "flatpak", "remote-add", "--if-not-exists", "flathub", "https://dl.flathub.org/repo/flathub.flatpakrepo"); err != nil {
		return err
	}

	// 3. Install configured flatpaks
	for _, pkg := range p.Cfg.Flatpaks {
		if _, err := p.Runner.Run("", "flatpak", "install", "-y", "flathub", pkg); err != nil {
			log.Printf("  warn: flatpak install %s failed: %v", pkg, err)
		}
	}
	return nil
}

// phaseCCSwitch installs the cc-switch .deb if a path is configured. Optional.
func (p *Provisioner) phaseCCSwitch(*Provisioner) error {
	if p.Cfg.CCSwitchDeb == "" {
		return nil
	}
	if _, err := os.Stat(p.Cfg.CCSwitchDeb); err != nil {
		return fmt.Errorf("cc-switch deb not found: %s", p.Cfg.CCSwitchDeb)
	}
	_, err := p.Runner.Run("", "apt-get", "install", "-y", p.Cfg.CCSwitchDeb)
	return err
}
