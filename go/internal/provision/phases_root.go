package provision

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"provisioner-ubuntu/internal/paths"
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

// phaseAptMirror rewrites the apt sources to the configured China mirror. The
// 26.04 desktop installer ignores autoinstall's `apt:` section for the target
// (empirically it writes a geo-picked mirror like jp.archive — verified by
// reading the installed image), so apt would be slow from mainland China
// without this override. Idempotent: sources already on the mirror are left as
// is; non-ubuntu URIs (e.g. the Enpass repo) are untouched.
func (p *Provisioner) phaseAptMirror(*Provisioner) error {
	if p.Cfg.AptMirror == "" {
		return nil
	}
	mirror := "http://" + p.Cfg.AptMirror + "/ubuntu/"
	// Any <country>.archive.ubuntu.com or security.ubuntu.com main URI.
	re := regexp.MustCompile(`https?://([a-z0-9-]*\.)?(archive\.ubuntu\.com|security\.ubuntu\.com)/ubuntu/`)

	paths := []string{"/etc/apt/sources.list"}
	if ents, err := os.ReadDir("/etc/apt/sources.list.d"); err == nil {
		for _, e := range ents {
			paths = append(paths, filepath.Join("/etc/apt/sources.list.d", e.Name()))
		}
	}
	for _, f := range paths {
		b, err := os.ReadFile(f)
		if err != nil {
			continue // not present on this system
		}
		out := re.ReplaceAllString(string(b), mirror)
		if out != string(b) {
			if err := os.WriteFile(f, []byte(out), 0o644); err != nil {
				return err
			}
			log.Printf("  apt mirror → %s (%s)", mirror, f)
		}
	}
	return nil
}

func (p *Provisioner) phaseAptUpdate(*Provisioner) error {
	if _, err := p.Runner.Run("", "apt-get", "update", "-qq"); err != nil {
		return err
	}
	_, err := p.Runner.Run("", "apt-get", "upgrade", "-y", "-qq")
	return err
}

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

// phaseEnpassRepo adds the Enpass apt repository and installs enpass. Enpass is
// a third-party password manager not in the Ubuntu archives, and the 26.04
// desktop installer ignores the autoinstall `apt:` section for the target — so
// enpass is installed HERE (not via the apt-packages list). Uses curl, which
// core-packages (an earlier phase) provides. Best-effort: a hiccup shouldn't
// abort the whole provision.
func (p *Provisioner) phaseEnpassRepo(*Provisioner) error {
	if _, err := os.Stat("/etc/apt/sources.list.d/enpass.list"); err == nil && p.installed("enpass") {
		return nil // already configured + installed
	}
	// The official Enpass method: the armored key goes straight into
	// /etc/apt/trusted.gpg.d/ (no gpg --dearmor, no signed-by) — simpler and
	// avoids a gpg dependency that isn't guaranteed on a minimal install.
	_, err := p.Runner.Run("", "bash", "-lc", `
set -e
echo "deb https://apt.enpass.io/ stable main" > /etc/apt/sources.list.d/enpass.list
curl -fsSL https://apt.enpass.io/keys/enpass-linux.key > /etc/apt/trusted.gpg.d/enpass.asc
apt-get update -qq
apt-get install -y enpass
`)
	if err != nil {
		log.Printf("  [WARN] enpass install failed: %v", err)
		return nil // best-effort
	}
	return nil
}

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

// proxySubscriptionFile is the gitignored file in the payload config dir
// holding the clash subscription URL (config/proxy-subscription.txt). Absent =>
// the proxy bootstrap is skipped and github-dependent installs fail (as before).
const proxySubscriptionFile = "proxy-subscription.txt"

// phaseProxySetup is an install-time-only bootstrap: it stands up a temporary
// headless mihomo core so the github-dependent installs (homebrew, mise,
// opencode, ...) can reach github from mainland China — without relying on any
// host proxy. The core comes from the clash-verge-rev deb on a China mirror;
// the subscription is the user's own (gitignored secret, fetched with a clash
// UA so it arrives as a complete config). http_proxy is set only after a node
// actually answers, so a failed bootstrap degrades to the old direct-connect
// failure rather than pointing every install at a dead port. proxy-teardown
// kills the temp core afterwards; the installed clash-verge GUI takes over on
// the same ports at login.
func (p *Provisioner) phaseProxySetup(*Provisioner) error {
	subFile := filepath.Join(paths.ConfigDir, proxySubscriptionFile)
	raw, err := os.ReadFile(subFile)
	sub := strings.TrimSpace(string(raw))
	if err != nil || sub == "" {
		log.Printf("  [WARN] %s missing — skipping proxy bootstrap (github installs will fail)", subFile)
		return nil
	}

	const (
		debURL    = "https://dl.p6p.net/clash-verge/v2.5.1/Clash.Verge_2.5.1_amd64.deb"
		core      = "/usr/bin/verge-mihomo"
		geoDir    = "/usr/lib/Clash Verge/resources" // geoip.dat/geosite.dat after the deb install
		cfgDir    = "/usr/local/lib/mihomo"
		proxyAddr = "http://127.0.0.1:7890"
		ua        = "clash-verge/v2.5.1" // subscription returns a full clash config only for a clash UA
	)

	// Install the deb (idempotent), fetch the subscription, start the temp core.
	if _, err := p.Runner.Run("", "bash", "-lc", `
set -e
if ! command -v `+core+` >/dev/null; then
  curl -fsSL -o /tmp/clash-verge.deb "`+debURL+`"
  dpkg -i /tmp/clash-verge.deb 2>/dev/null || apt-get -f install -y   # fix GUI lib deps if any
  rm -f /tmp/clash-verge.deb
fi
mkdir -p "`+cfgDir+`"
# Read the subscription URL from its file at runtime ($(cat)) rather than
# interpolating it into the shell string — it's user input and may contain
# shell metacharacters.
curl -fsSL -A "`+ua+`" "$(cat "`+subFile+`")" -o "`+cfgDir+`/config.yaml"
nohup `+core+` -f "`+cfgDir+`/config.yaml" -d "`+geoDir+`" > "`+cfgDir+`/mihomo.log" 2>&1 &
`); err != nil {
		return fmt.Errorf("proxy bootstrap: %w", err)
	}

	// Wait for the url-test group to pick a working node (worst case ~3 min:
	// 12 × (10s curl timeout + 5s sleep)).
	up := false
	for i := 0; i < 12; i++ {
		if out, err := p.Runner.Run("", "curl", "-x", proxyAddr, "-sSL", "--max-time", "10",
			"-o", "/dev/null", "-w", "%{http_code}", "https://www.gstatic.com/generate_204"); err == nil && strings.TrimSpace(out) == "204" {
			up = true
			break
		}
		time.Sleep(5 * time.Second)
	}
	if !up {
		p.Runner.Run("", "bash", "-lc", "pkill -x verge-mihomo; true")
		return fmt.Errorf("proxy verification failed — no subscription node answered")
	}

	// Register the teardown co-located with the setup: the temp core + proxy env
	// live for the whole provision, then RunAll's deferred cleanup kills the core
	// and unsets the env — so no later phase (e.g. fcitx5's apt) can hit a dead
	// proxy, on any exit path.
	os.Setenv("http_proxy", proxyAddr)
	os.Setenv("https_proxy", proxyAddr)
	os.Setenv("all_proxy", proxyAddr)
	p.cleanup = func() {
		os.Unsetenv("http_proxy")
		os.Unsetenv("https_proxy")
		os.Unsetenv("all_proxy")
		p.Runner.Run("", "bash", "-lc", "pkill -x verge-mihomo; true")
	}
	if _, err := p.Runner.Run("", "bash", "-lc",
		`echo 'Defaults env_keep += "http_proxy https_proxy all_proxy"' > /etc/sudoers.d/proxy && chmod 440 /etc/sudoers.d/proxy`); err != nil {
		return err
	}

	// Best-effort: seed the clash-verge GUI's profile so daily use works on
	// first login. If verge rejects the file, the user pastes the subscription
	// in the GUI — deliberately no fallback logic.
	if err := p.prefillVergeProfile(sub, cfgDir+"/config.yaml"); err != nil {
		log.Printf("  [WARN] prefill clash-verge profile: %v", err)
	}
	return nil
}

// prefillVergeProfile seeds ~/.config/clash-verge/profiles.yaml plus the
// already-fetched profile file, so the installed GUI shows the subscription on
// first login. cfgPath is the fetched subscription config (clash format).
func (p *Provisioner) prefillVergeProfile(subURL, cfgPath string) error {
	uid := "R" + randHex(8)
	dir := filepath.Join(p.Cfg.Home, ".config", "clash-verge")
	if err := os.MkdirAll(filepath.Join(dir, "profiles"), 0o755); err != nil {
		return err
	}
	cfg, err := os.ReadFile(cfgPath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "profiles", uid+".yaml"), cfg, 0o644); err != nil {
		return err
	}
	profiles := fmt.Sprintf("current: %s\nitems:\n  - uid: %s\n    itype: remote\n    file: %s.yaml\n    url: %q\n    name: subscription\n",
		uid, uid, uid, subURL)
	if err := os.WriteFile(filepath.Join(dir, "profiles.yaml"), []byte(profiles), 0o644); err != nil {
		return err
	}
	_, err = p.Runner.Run("", "chown", "-R", p.Cfg.User+":"+p.Cfg.User, dir)
	return err
}

// randHex returns n random hex chars (crypto-grade, for verge profile uids).
func randHex(n int) string {
	b := make([]byte, n/2+1)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("x%08x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)[:n]
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
