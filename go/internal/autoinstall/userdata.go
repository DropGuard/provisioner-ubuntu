package autoinstall

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// The types below mirror the autoinstall YAML schema exactly, so marshalling
// with yaml.v3 always produces VALID YAML — including multi-line late-commands
// (a hand-written text/template silently emitted unindented continuation lines,
// which made subiquity choke and sit in state WAITING forever).

// doc is the root of the user-data document. `apt` is consumed by cloud-init's
// apt module (sets the primary mirror for both the live session and the target
// install); `autoinstall` is consumed by subiquity.
type doc struct {
	Autoinstall autoinstall `yaml:"autoinstall"`
	Apt         *aptConf    `yaml:"apt,omitempty"`
}

type aptConf struct {
	Proxy    string      `yaml:"proxy,omitempty"`
	Primary  []aptMirror `yaml:"primary"`
	Security []aptMirror `yaml:"security,omitempty"`
	// GeoIP + Conf are autoinstall.apt (target) fields, not cloud-init's.
	GeoIP *bool  `yaml:"geoip,omitempty"` // false = don't let the installer pick a geo mirror
	Conf  *string `yaml:"conf,omitempty"`  // apt.conf content (e.g. Acquire proxy)
}

type aptMirror struct {
	Arches []string `yaml:"arches"`
	URI    string   `yaml:"uri"`
}

type autoinstall struct {
	Version   int      `yaml:"version"`
	Identity  identity `yaml:"identity"`
	Locale    string   `yaml:"locale"`
	Timezone  string   `yaml:"timezone"`
	Keyboard  keyboard `yaml:"keyboard"`
	Storage   storage  `yaml:"storage"`
	Packages  []string `yaml:"packages"`
	Snaps     []snap   `yaml:"snaps"`
	SSH       sshConf  `yaml:"ssh"`
	EarlyCmds []string `yaml:"early-commands,omitempty"`
	LateCmds  []string `yaml:"late-commands"`
	Shutdown  string   `yaml:"shutdown"`
	Apt       *aptConf `yaml:"apt,omitempty"` // nested so the installer configures the target mirror
}

type identity struct {
	Hostname     string `yaml:"hostname"`
	Username     string `yaml:"username"`
	PasswordHash string `yaml:"password"`
}

type keyboard struct {
	Layout string `yaml:"layout"`
}

type storage struct {
	Layout layout `yaml:"layout"`
	Grub   grub   `yaml:"grub"`
	Swap   swap   `yaml:"swap"`
}

type layout struct {
	Name  string `yaml:"name"`
	Match match  `yaml:"match"`
}

type match struct {
	Serial string `yaml:"serial"`
}

type grub struct {
	ReorderUEFI bool `yaml:"reorder_uefi"`
}

type swap struct {
	Size int `yaml:"size"`
}

type snap struct {
	Name    string `yaml:"name"`
	Classic bool   `yaml:"classic,omitempty"`
}

type sshConf struct {
	InstallServer bool `yaml:"install-server"`
	AllowPW       bool `yaml:"allow-pw"`
}

// RenderUserData renders the user-data file (what cloud-init's ds=nocloud
// reads). The result always begins with EXACTLY "#cloud-config\n" — cloud-init
// does not recognize "# cloud-config" and would silently drop the config.
func RenderUserData(c Config) (string, error) {
	d := doc{
		Apt: aptConfFor(c.AptMirror, c.AptProxy),
		Autoinstall: autoinstall{
			Version:  1,
			Identity: identity{Hostname: c.Identity.Hostname, Username: c.Identity.Username, PasswordHash: c.Identity.PasswordHash},
			Locale:   c.Locale,
			Timezone: c.Timezone,
			Keyboard: keyboard{Layout: c.Keyboard},
			Storage: storage{
				Layout: layout{Name: "direct", Match: match{Serial: c.DiskSerial}},
				Grub:   grub{ReorderUEFI: false},
				Swap:   swap{Size: 0},
			},
			Packages:  c.Packages,
			Snaps:     makeSnaps(c.Snaps),
			SSH:       sshConf{InstallServer: true, AllowPW: c.SSHAllowPW},
			EarlyCmds: c.EarlyCommand,
			LateCmds:  c.LateCommand,
			Shutdown:  shutdownAction(c.Reboot),
			Apt:       targetAptConf(c.AptMirror, c.AptProxy),
		},
	}
	b, err := yaml.Marshal(&d)
	if err != nil {
		return "", fmt.Errorf("marshal user-data: %w", err)
	}
	return "#cloud-config\n" + string(b), nil
}

// aptConfFor builds the cloud-init apt config (live-session mirror + proxy),
// or nil if no mirror is set.
func aptConfFor(mirror, proxy string) *aptConf {
	if mirror == "" {
		return nil
	}
	return &aptConf{
		Proxy:    proxy,
		Primary:  []aptMirror{{Arches: []string{"default"}, URI: "http://" + mirror + "/ubuntu/"}},
		Security: []aptMirror{{Arches: []string{"default"}, URI: "http://" + mirror + "/ubuntu-security/"}},
	}
}

// targetAptConf builds the autoinstall.apt config for the INSTALLED system: the
// same mirror, but nested under autoinstall (where the 26.04 installer reads it
// from — a top-level `apt:` only configures the live session) with geoip
// disabled so the installer can't fall back to a geo-picked mirror. The apt
// proxy goes into `conf:` (subiquity's form), not cloud-init's `proxy:`.
func targetAptConf(mirror, proxy string) *aptConf {
	if mirror == "" {
		return nil
	}
	c := &aptConf{
		Primary:  []aptMirror{{Arches: []string{"default"}, URI: "http://" + mirror + "/ubuntu/"}},
		Security: []aptMirror{{Arches: []string{"default"}, URI: "http://" + mirror + "/ubuntu-security/"}},
	}
	geoip := false
	c.GeoIP = &geoip
	if proxy != "" {
		conf := fmt.Sprintf("Acquire::http::Proxy %q;\nAcquire::https::Proxy %q;\n", proxy, proxy)
		c.Conf = &conf
	}
	return c
}

func makeSnaps(snaps []Snap) []snap {
	out := make([]snap, 0, len(snaps))
	for _, s := range snaps {
		out = append(out, snap{Name: s.Name, Classic: s.Classic})
	}
	return out
}

func shutdownAction(reboot bool) string {
	if reboot {
		return "reboot"
	}
	return "poweroff"
}
