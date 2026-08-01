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
		},
	}
	b, err := yaml.Marshal(&d)
	if err != nil {
		return "", fmt.Errorf("marshal user-data: %w", err)
	}
	return "#cloud-config\n" + string(b), nil
}

// aptConfFor builds the cloud-init apt config (mirror + optional proxy), or
// nil if no mirror is set.
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
