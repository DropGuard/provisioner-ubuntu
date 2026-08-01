// Package config holds the typed first-boot provisioning configuration. The
// config/*.list + mise.toml files at the repo root are the single source of
// truth on a provisioned machine: Load reads them at runtime (they are copied
// into /usr/local/share/provisioner-ubuntu/config by the autoinstall
// late-commands) and Default supplies the built-in fallback when they are
// absent. Editing a list file is enough — no recompile required.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Provision configures the post-install provisioning run (provision.sh port).
type Provision struct {
	User string // target user, e.g. "dailyuser"
	Home string // target user home, e.g. "/home/dailyuser"

	// Phase 0 — base environment (root)
	CorePackages    []string // fail-fast if these fail
	ExtraPackages   []string // best-effort
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

		GnomeTheme: true,
		GPUDrivers: true,
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
			"50026B727200FDDC":    "KINGSTON 120G system disk",
			"38F6_0156_326B_257E": "PLEXTOR 1T NVMe — near-dead, leave alone",
		},

		OpenCode:   true,
		ClaudeCode: true,
	}
}

// Load returns the provisioning configuration for a machine. If dir exists
// (the late-commands copy the repo's config/ there), its apt-packages.list,
// brew-packages.list and mise.toml override the built-in defaults — they are
// the single source of truth and replace, not append. If dir is absent, the
// built-in defaults are returned unchanged. A missing individual file keeps
// its default value.
func Load(dir string) (Provision, error) {
	cfg := Default()
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return cfg, nil
	}
	if err := loadAptList(&cfg, filepath.Join(dir, "apt-packages.list")); err != nil {
		return cfg, err
	}
	if err := loadBrewList(&cfg, filepath.Join(dir, "brew-packages.list")); err != nil {
		return cfg, err
	}
	if err := loadMiseToml(&cfg, filepath.Join(dir, "mise.toml")); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// loadIfExists reads and applies a single config file; a missing file keeps
// the default (nil error), any other error is returned.
func loadIfExists(path string, apply func([]byte) error) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return apply(b)
}

// loadAptList splits apt-packages.list into fail-fast core packages (listed
// after a "# --- core" marker) and best-effort extras (everything else).
func loadAptList(cfg *Provision, path string) error {
	return loadIfExists(path, func(b []byte) error {
		var core, extra []string
		inCore := false
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			switch {
			case line == "":
				continue
			case strings.HasPrefix(line, "# --- core"):
				inCore = true
				continue
			case strings.HasPrefix(line, "# ---"):
				inCore = false
				continue
			case strings.HasPrefix(line, "#"):
				continue
			}
			if inCore {
				core = append(core, line)
			} else {
				extra = append(extra, line)
			}
		}
		cfg.CorePackages = core
		cfg.ExtraPackages = extra
		return nil
	})
}

// loadBrewList reads one package per line (comments/blank lines ignored).
func loadBrewList(cfg *Provision, path string) error {
	return loadIfExists(path, func(b []byte) error {
		var pkgs []string
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			pkgs = append(pkgs, line)
		}
		cfg.BrewPackages = pkgs
		return nil
	})
}

// loadMiseToml keeps the file verbatim (written to the user's mise config) and
// parses the [tools] section into MiseTools.
func loadMiseToml(cfg *Provision, path string) error {
	return loadIfExists(path, func(b []byte) error {
		cfg.MiseConfig = string(b)
		cfg.MiseTools = parseMiseTools(string(b))
		return nil
	})
}

// parseMiseTools extracts `key = "value"` pairs from the [tools] section.
func parseMiseTools(content string) map[string]string {
	tools := map[string]string{}
	inTools := false
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inTools = strings.Trim(line, "[]") == "tools"
			continue
		}
		if !inTools {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		tools[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(val), `"'`)
	}
	return tools
}

var (
	// Debian policy: package names start with a lowercase letter/digit and may
	// contain +.- . E.g. build-essential, libc6-dev, golang-1.26-go.
	aptPkgRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]*$`)
	brewPkgRe  = regexp.MustCompile(`^[a-z0-9][a-zA-Z0-9@+._/-]*$`) // formulas + tap "user/repo" + @version
	miseLineRe = regexp.MustCompile(`^[a-zA-Z0-9_.-]+\s*=\s*"[^"]+"$`)
)

// Validate checks the config directory (apt-packages.list, brew-packages.list,
// mise.toml) for syntax errors that would surface only on a provisioned
// machine: a typo'd package name breaks the apt install, an unparseable
// mise.toml leaves the toolchain empty. Called by build-payload so a bad
// config fails before it is burned into a USB/ISO, not after. Empty list = OK.
func Validate(dir string) []error {
	var errs []error
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return errs // nothing to validate; Load() falls back to defaults
	}
	errs = append(errs, validatePkgList(filepath.Join(dir, "apt-packages.list"), "apt", aptPkgRe)...)
	errs = append(errs, validatePkgList(filepath.Join(dir, "brew-packages.list"), "brew", brewPkgRe)...)
	errs = append(errs, validateMise(filepath.Join(dir, "mise.toml"))...)
	return errs
}

// validatePkgList checks every non-comment line is a well-formed package name.
func validatePkgList(path, what string, re *regexp.Regexp) []error {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil // missing file is legal (defaults apply)
	}
	var errs []error
	for i, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !re.MatchString(line) {
			errs = append(errs, fmt.Errorf("%s:%d: malformed %s package %q", path, i+1, what, line))
		}
	}
	return errs
}

// validateMise ensures mise.toml has a parseable [tools] section with at least
// one `key = "value"` entry — otherwise the toolchain phase would install
// nothing while reporting success.
func validateMise(path string) []error {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var errs []error
	inTools, sawKey := false, false
	for i, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]"):
			inTools = strings.Trim(line, "[]") == "tools"
		case inTools && strings.HasPrefix(line, "#"):
			continue
		case inTools:
			if !miseLineRe.MatchString(line) {
				errs = append(errs, fmt.Errorf("%s:%d: malformed mise [tools] entry %q (want `key = \"value\"`)", path, i+1, line))
			} else {
				sawKey = true
			}
		}
	}
	if !sawKey {
		errs = append(errs, fmt.Errorf("%s: no `key = \"value\"` entries under [tools]", path))
	}
	return errs
}
