// Package autoinstall builds the cloud-init seed artifacts (user-data and
// grub.cfg) for the Ubuntu desktop autoinstall flow, in a strongly-typed and
// unit-testable way.
//
// The two most history-laden bugs live here and are guarded by table tests:
//   - user-data must start with EXACTLY "#cloud-config" (a space breaks
//     cloud-init's content-type detection and silently drops the config).
//   - grub.cfg's kernel line must escape the ';' in "ds=nocloud;s=..." as
//     "\;" — an unescaped ';' makes grub truncate the cmdline.
package autoinstall

import (
	"fmt"

	"provisioner-ubuntu/internal/paths"
)

// Identity configures the user created by the installer.
type Identity struct {
	Hostname     string
	Username     string
	PasswordHash string // from `openssl passwd -6` (or argon2) at build time
}

// Snap is a snap to install during autoinstall. Code-style snaps need Classic.
type Snap struct {
	Name    string
	Classic bool
}

// Config is the typed autoinstall configuration. It mirrors the fields of
// autoinstall/autoinstall.yaml.
type Config struct {
	Identity     Identity
	Locale       string
	Timezone     string
	Keyboard     string
	DiskSerial   string
	Packages     []string
	Snaps        []Snap
	SSHAllowPW   bool
	AptMirror    string   // primary apt mirror for the install (cloud-init apt: module)
	AptProxy     string   // apt proxy (e.g. host apt-cacher-ng at 10.0.2.2:3142); empty = none
	EarlyCommand []string // run in the live session BEFORE the install starts
	LateCommand  []string // run chrooted to /target during the install
	Reboot       bool     // shutdown action after install
}

// Default returns a Config matching the project's current autoinstall.yaml,
// including the late-commands that had to be added because the 26.04 desktop
// installer's Locale/TimeZone controllers are no-ops for the target, and the
// payload copy + first-boot service registration.
func Default() Config {
	return Config{
		Identity: Identity{
			Hostname: "ubuntu",
			Username: "dailyuser",
		},
		Locale:     "en_US.UTF-8",
		Timezone:   "Asia/Shanghai",
		Keyboard:   "us",
		DiskSerial: "50026B727200FDDC",
		Packages: []string{
			"openssh-server", "curl", "git", "build-essential",
			"qemu-guest-agent", // enables QMP guest-exec on the installed system
		},
		// The install's primary mirror is set via cloud-init's `apt:` module
		// (rendered into user-data). archive.ubuntu.com is ~90KB/s via this
		// host's proxy vs ~1.6MB/s for the China mirror.
		AptMirror: "mirrors.ustc.edu.cn",
		Snaps: []Snap{
			{Name: "firefox"},
			{Name: "obsidian"},
			{Name: "code", Classic: true},
		},
		SSHAllowPW: true,
		Reboot:     true,
		LateCommand: []string{
			// Ubuntu 26.04 desktop installer BUG: Locale/TimeZone controllers are
			// no-ops for the target (verified in KVM). Apply explicitly.
			"echo 'LANG=en_US.UTF-8' > /target/etc/locale.conf",
			"sed -i 's/^# en_US.UTF-8/en_US.UTF-8/' /target/etc/locale.gen",
			"curtin in-target --target=/target -- locale-gen",
			"ln -sf /usr/share/zoneinfo/Asia/Shanghai /target/etc/localtime",
			`curtin in-target --target=/target -- bash -c 'printf "tzdata tzdata/Areas select Asia\ntzdata tzdata/Zones/Asia select Shanghai\n" | debconf-set-selections'`,
			// GDM auto-login (sudo still needs the password).
			"cat > /target/etc/gdm3/custom.conf << 'EOF'\n[daemon]\nAutomaticLoginEnable=true\nAutomaticLogin=dailyuser\nEOF",
			// Copy payload into the installed system. `provision` is the Go
			// provisioner binary; first-boot.service runs it as /usr/local/bin/provision.
			fmt.Sprintf("mkdir -p %s %s", "/target"+paths.ConfigDir, "/target"+paths.DotfilesDir),
			fmt.Sprintf("cp /cdrom/nocloud/provision %s", "/target"+paths.ProvisionBin),
			fmt.Sprintf("cp /cdrom/nocloud/fav.sh %s 2>/dev/null || true", "/target"+paths.FavBin),
			fmt.Sprintf("cp /cdrom/nocloud/setup-fcitx5-chinese.sh %s", "/target"+paths.Fcitx5Script),
			fmt.Sprintf("cp -a /cdrom/nocloud/config/. %s/ 2>/dev/null || true", "/target"+paths.ConfigDir),
			fmt.Sprintf("cp -a /cdrom/nocloud/dotfiles/. %s/ 2>/dev/null || true", "/target"+paths.DotfilesDir),
			fmt.Sprintf("chmod +x %s %s %s 2>/dev/null || true",
				"/target"+paths.ProvisionBin,
				"/target"+paths.FavBin, "/target"+paths.Fcitx5Script),
			// First-boot provisioning service.
			fmt.Sprintf("cp /cdrom/nocloud/first-boot.service %s", "/target"+paths.FirstBootUnit),
			"curtin in-target --target=/target -- systemctl enable first-boot.service",
		},
	}
}
// GoldenVersion is bumped manually whenever Golden()'s base config changes
// (packages, user, mirror, ...). The golden image cache key includes it, so
// bumping forces a clean rebuild instead of silently reusing a stale image.
const GoldenVersion = "v1"

// Golden returns the minimal config for building the cached golden image
// (Phase A of the e2e pipeline). The image is the smallest thing Phase B
// needs: a user to SSH into, openssh-server for the SSH channel, and
// qemu-guest-agent for future QEMU-level control. No snaps, no provision
// payload, no locale/timezone late-commands — the installer's default base is
// fine for a test image. The install finishes in ~10 minutes instead of 40.
// Phase B provisions on overlays of this image via SSH.
func Golden() Config {
	return Config{
		Identity: Identity{
			Hostname: "ubuntu",
			Username: "dailyuser",
		},
		Locale:     "en_US.UTF-8",
		Timezone:   "Asia/Shanghai",
		Keyboard:   "us",
		DiskSerial: "50026B727200FDDC",
		Packages: []string{
			"openssh-server", // SSH channel for Phase B assertions
			"qemu-guest-agent",
		},
		AptMirror:  "mirrors.ustc.edu.cn",
		SSHAllowPW: true,
		Reboot:     false, // poweroff = clean VM exit signal
	}
}

