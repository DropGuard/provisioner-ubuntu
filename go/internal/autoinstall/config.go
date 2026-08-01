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
	Identity    Identity
	Locale      string
	Timezone    string
	Keyboard    string
	DiskSerial  string
	Packages    []string
	Snaps       []Snap
	SSHAllowPW  bool
	EarlyCommand []string // run in the live session BEFORE the install starts
	LateCommand []string  // run chrooted to /target during the install
	Reboot      bool      // shutdown action after install
}

// AptMirror is the apt mirror used during the install (early-command rewrites
// the live session's sources). Empty means "use the ISO's default"
// (archive.ubuntu.com, which is very slow through this host's proxy path —
// ~90KB/s vs ~1.6MB/s for mirrors.ustc.edu.cn).
const AptMirror = "mirrors.ustc.edu.cn"

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
		// The live session ships NO apt sources (verified: /etc/apt/sources.list.d
		// is empty); subiquity generates the target's sources, defaulting to the
		// slow archive.ubuntu.com (~90KB/s via this host's proxy vs ~1.6MB/s for
		// the China mirror). Write the sources file up front so subiquity carries
		// the mirror into the target's package phase.
		EarlyCommand: []string{
			"mkdir -p /etc/apt/sources.list.d",
			`cat > /etc/apt/sources.list.d/ubuntu.sources << 'EOF'
Types: deb
URIs: http://` + AptMirror + `/ubuntu/
Suites: resolute resolute-updates resolute-backports
Components: main universe restricted multiverse
Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg

Types: deb
URIs: http://` + AptMirror + `/ubuntu-security/
Suites: resolute-security
Components: main universe restricted multiverse
Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg
EOF
# Verify the mirror actually works; roll back to the (empty) default sources
# if apt-get update fails, so we never break the install on a bad mirror.
if ! apt-get update -qq; then
  rm -f /etc/apt/sources.list.d/ubuntu.sources
  echo "provisioner-ubuntu: mirror apt-get update failed — rolled back to default sources"
fi
`,
		},
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
			"mkdir -p /target/usr/local/share/provisioner-ubuntu/config /target/usr/local/share/provisioner-ubuntu/dotfiles",
			"cp /cdrom/nocloud/provision /target/usr/local/bin/provision",
			"cp /cdrom/nocloud/test-env-loading.sh /target/usr/local/bin/test-env-loading",
			"cp /cdrom/nocloud/fav.sh /target/usr/local/bin/fav 2>/dev/null || true",
			"cp /cdrom/nocloud/setup-fcitx5-chinese.sh /target/usr/local/bin/setup-fcitx5-chinese.sh",
			"cp -a /cdrom/nocloud/config/. /target/usr/local/share/provisioner-ubuntu/config/ 2>/dev/null || true",
			"cp -a /cdrom/nocloud/dotfiles/. /target/usr/local/share/provisioner-ubuntu/dotfiles/ 2>/dev/null || true",
			"chmod +x /target/usr/local/bin/provision /target/usr/local/bin/test-env-loading /target/usr/local/bin/fav /target/usr/local/bin/setup-fcitx5-chinese.sh 2>/dev/null || true",
			// First-boot provisioning service.
			"cp /cdrom/nocloud/first-boot.service /target/etc/systemd/system/first-boot.service",
			"curtin in-target --target=/target -- systemctl enable first-boot.service",
		},
	}
}
