// Package config holds the typed first-boot provisioning configuration (the
// data previously spread across config/*.list + hardcoded in provision.sh).
package config

// Provision configures the post-install provisioning run (provision.sh port).
type Provision struct {
	User string // target user, e.g. "dailyuser"
	Home string // target user home, e.g. "/home/dailyuser"

	// Phase 0 — base environment (root)
	CorePackages  []string // fail-fast if these fail
	ExtraPackages []string // best-effort
	AddUserToDocker bool

	// Phase 1 — dev toolchain (user)
	BrewPrefix   string
	BrewPackages []string
	MiseTools    map[string]string // tool -> version (from mise.toml)
	MiseConfig   string            // mise.toml content written to user home

	// Phase 2 — CLI tools (user)
	NPMGlobals []string

	// Phase 3 — GNOME desktop
	GnomeTheme      bool // dark theme
	DockFavorites   []string
	Fcitx5Packages  []string
	Fcitx5SetupPath string // setup-fcitx5-chinese.sh next to the binary

	// GPU drivers (root): install the recommended NVIDIA driver if an NVIDIA
	// GPU is present. AMD/Intel use in-kernel drivers and need nothing.
	GPUDrivers bool

	// Phase 4 — shell environment
	BashrcAdditions string // lines appended to the user's .bashrc
	EnvDPath        string // path to the global environment.d file
	EnvDConf        string // content of EnvDPath
	GitUserDefault  bool   // set init.defaultBranch + pull.rebase

	// Phase 4 — data disks (root, non-destructive)
	ExcludedSerials map[string]string // serial -> reason, NEVER mounted/formatted

	// Repo app installs (best-effort, user)
	OpenCode    bool
	ClaudeCode  bool
	CCSwitchDeb string // path to cc-switch .deb if present
	ClashVergeDeb string
}

// Default returns the provisioning configuration matching the current bash
// scripts (config/*.list + provision.sh defaults).
func Default() Provision {
	return Provision{
		User: "dailyuser",
		Home: "/home/dailyuser",

		CorePackages: []string{"build-essential", "curl", "git", "docker.io"},
		ExtraPackages: []string{
			"ripgrep", "eza", "unzip", "zip", "protobuf-compiler", "jq", "htop",
			"ncdu", "bat", "fd-find",
			"gnome-tweaks", "gnome-shell-extensions", "chrome-gnome-shell",
			"vlc", "qimgv", "flameshot", "enpass", "gh",
		},
		AddUserToDocker: true,

		BrewPrefix:   "/home/linuxbrew/.linuxbrew",
		BrewPackages: []string{"yazi", "tokei", "gh"},
		MiseTools: map[string]string{
			"node": "lts", "python": "latest", "go": "latest", "rust": "stable",
			"java": "latest", "bun": "latest", "pnpm": "latest", "maven": "latest",
			"uv": "latest", "ruff": "latest",
		},
		MiseConfig: "[tools]\nnode = \"lts\"\npython = \"latest\"\ngo = \"latest\"\nrust = \"stable\"\njava = \"latest\"\nbun = \"latest\"\n\npnpm = \"latest\"\nmaven = \"latest\"\nuv = \"latest\"\nruff = \"latest\"\n",

		NPMGlobals: []string{"reasonix"},

		GnomeTheme:  true,
		GPUDrivers:  true,
		DockFavorites: []string{
			"firefox_firefox.desktop", "code_code.desktop",
			"org.gnome.Terminal.desktop", "org.gnome.Nautilus.desktop",
			"obsidian_obsidian.desktop", "discord_discord.desktop",
			"spotify_spotify.desktop",
		},
		Fcitx5Packages: []string{
			"fcitx5", "fcitx5-config-gui", "fcitx5-chinese-addons",
			"fcitx5-frontend-gtk3", "fcitx5-frontend-gtk2", "fcitx5-frontend-gtk4",
			"fcitx5-frontend-qt5", "fcitx5-frontend-qt6",
		},

		BashrcAdditions: `# >>> provisioner-ubuntu: brew & mise activation <<<
eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"
eval "$($HOME/.local/bin/mise activate bash)"

# >>> Proxy toggles <<<
alias proxy='export http_proxy="http://127.0.0.1:7897" https_proxy="http://127.0.0.1:7897" all_proxy="socks5://127.0.0.1:7897"; echo "Terminal proxy ON"'
alias unproxy='unset http_proxy https_proxy all_proxy; echo "Terminal proxy OFF"'
`,
		EnvDPath: "/etc/environment.d/zz-provisioner.conf",
		EnvDConf: `PATH="/home/linuxbrew/.linuxbrew/bin:/home/dailyuser/.local/bin:/home/dailyuser/.local/share/mise/shims:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/snap/bin"
`,
		GitUserDefault: true,

		ExcludedSerials: map[string]string{
			"50026B727200FDDC":   "KINGSTON 120G system disk",
			"38F6_0156_326B_257E": "PLEXTOR 1T NVMe — near-dead, leave alone",
		},

		OpenCode:    true,
		ClaudeCode:  true,
	}
}
