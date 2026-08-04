package provision

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"provisioner-ubuntu/internal/paths"
	"regexp"
	"strings"
	"time"
)

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

// phaseUFW enables the uncomplicated firewall to secure the machine on public Wi-Fi.
func (p *Provisioner) phaseUFW(*Provisioner) error {
	if !p.installed("ufw") {
		if _, err := p.Runner.Run("", "apt-get", "install", "-y", "ufw"); err != nil {
			return fmt.Errorf("ufw apt install: %w", err)
		}
	}
	cmds := [][]string{
		{"default", "deny", "incoming"},
		{"default", "allow", "outgoing"},
		{"limit", "22/tcp"}, // allow SSH but throttle brute-force attempts
		{"--force", "enable"},
	}
	for _, args := range cmds {
		if _, err := p.Runner.Run("", "ufw", args...); err != nil {
			return fmt.Errorf("ufw %v: %w", args, err)
		}
	}
	return nil
}
