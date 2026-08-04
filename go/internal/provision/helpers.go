package provision

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
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

// proxySubscriptionFile is the gitignored file in the payload config dir
// holding the clash subscription URL (config/proxy-subscription.txt). Absent =>
// the proxy bootstrap is skipped and github-dependent installs fail (as before).
const proxySubscriptionFile = "proxy-subscription.txt"

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

// gnomeSet runs a gsettings command as the target user. Without a live session
// the change lands in the user's dconf and applies at next login.
func (p *Provisioner) gnomeSet(schema, key, value string) error {
	_, err := p.Runner.Run(p.Cfg.User, "gsettings", "set", schema, key, value)
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

// --- helpers ---

func mapSlice[T any, U any](in []T, f func(T) U) []U {
	out := make([]U, len(in))
	for i, v := range in {
		out[i] = f(v)
	}
	return out
}

// copyFile copies a single regular file from src to dst.
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// fstabEntry builds the mount point and fstab line for one data disk. Pure
// function (unit-testable): the mountpoint is derived from the last 8 chars of
// the sanitized serial, and NTFS gets windows-friendly options.
func fstabEntry(uuid, serial, ptype string) (mnt, line string) {
	label := sanitize(serial)
	if len(label) > 8 {
		label = label[len(label)-8:]
	}
	mnt = "/mnt/data-" + label
	opts := "defaults,nofail,uid=1000,gid=1000,x-systemd.automount"
	if ptype == "ntfs" {
		opts = "defaults,nofail,uid=1000,gid=1000,umask=022,windows_names,x-systemd.automount"
	}
	return mnt, fmt.Sprintf("UUID=%s %s %s %s 0 2\n", uuid, mnt, ptype, opts)
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}
