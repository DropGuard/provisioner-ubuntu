// Package provision ports scripts/provision.sh to Go. Phases that must run as
// the target user (brew, mise, gsettings, ...) are executed via binary
// self re-exec: the root run invokes `sudo -u USER provisioner-ubuntu
// provision-user <phase>`, and the re-entered process runs that phase with
// HOME/USER set correctly. The Runner interface makes each phase's logic
// unit-testable without touching the system.
package provision

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"provisioner-ubuntu/internal/config"
)

// Phase name constants for the phases users actually re-run with --phase.
const (
	PhaseDotfiles = "dotfiles"
	PhaseFlatpaks = "flatpaks"
	PhaseSnaps    = "snaps"
)

// Phase is one provisioning step.
type Phase struct {
	Name string // short name, used for the provision-user subcommand
	User bool   // true => runs as the target user via self re-exec
	// FailFast aborts the whole run (and the process exits non-zero) if the
	// phase errors. Only reserved for the critical path — everything else is
	// best-effort with a warning.
	FailFast bool
	Run      func(*Provisioner) error
}

// Provisioner carries the config + runner used by the phases.
type Provisioner struct {
	Cfg    config.Provision
	Runner Runner
	Phase  string // if set, RunAll only runs this phase

	// cleanup is registered by phases that start a transient resource (e.g. the
	// install-time proxy core) so it is torn down when RunAll returns — on any
	// exit path. Co-located with the start so it can't be forgotten.
	cleanup func()
}

// Phases returns the full phase list in execution order.
func (p *Provisioner) Phases() []Phase {
	return []Phase{
		{Name: "apt-mirror", Run: p.phaseAptMirror},
		{Name: "apt-update", Run: p.phaseAptUpdate},
		{Name: "core-packages", FailFast: true, Run: p.phaseCorePackages},
		{Name: PhaseSnaps, Run: p.phaseSnaps},
		{Name: PhaseFlatpaks, Run: p.phaseFlatpaks},
		// enpass-repo after core-packages so curl is available (it isn't on the
		// minimal golden image before then).
		{Name: "enpass-repo", Run: p.phaseEnpassRepo},
		{Name: "docker-group", Run: p.phaseDockerGroup},
		{Name: "gpu-drivers", Run: p.phaseGPUDrivers},
		{Name: "cc-switch", Run: p.phaseCCSwitch},
		{Name: "proxy-setup", Run: p.phaseProxySetup},
		{Name: "homebrew-dir", Run: p.phaseBrewDir},
		{Name: "homebrew", User: true, Run: p.phaseHomebrew},
		{Name: "mise", User: true, Run: p.phaseMise},
		{Name: "brew-packages", User: true, Run: p.phaseBrewPackages},
		{Name: "mise-tools", User: true, Run: p.phaseMiseTools},
		{Name: "mise-shims", User: true, Run: p.phaseMiseShims},
		{Name: "npm-globals", User: true, Run: p.phaseNPMGlobals},
		{Name: "claude-code", User: true, Run: p.phaseClaudeCode},
		{Name: "gnome-theme", User: true, Run: p.phaseGnomeTheme},
		{Name: "gnome-dock", User: true, Run: p.phaseGnomeDock},
		{Name: "gnome-shortcuts", User: true, Run: p.phaseGnomeShortcuts},
		{Name: "fcitx5", Run: p.phaseFcitx5},
		{Name: "shell-env", Run: p.phaseShellEnv},
		{Name: "default-apps", Run: p.phaseDefaultApps},
		{Name: "git-config", User: true, Run: p.phaseGitConfig},
		{Name: PhaseDotfiles, User: true, Run: p.phaseDotfiles},
		{Name: "mount-data-disks", Run: p.phaseMountDataDisks},
		{Name: "disable-service", Run: p.phaseDisableService},
	}
}

// RunAll runs every phase (or a single phase when Phase is set). selfPath is the
// binary to re-exec for user phases. A FailFast phase error aborts the run
// immediately and is returned, so the caller exits non-zero and systemd marks the
// oneshot failed (retryable).
func (p *Provisioner) RunAll(selfPath string) error {
	// Tear down any transient resources (install-time proxy core) no matter how
	// the run ends — a FailFast abort included.
	defer func() {
		if p.cleanup != nil {
			p.cleanup()
		}
	}()
	matched := false
	for _, ph := range p.Phases() {
		if p.Phase != "" && ph.Name != p.Phase {
			continue
		}
		matched = true
		p.logPhase(ph.Name)
		var err error
		if ph.User {
			err = p.runUserPhase(selfPath, ph.Name)
		} else {
			err = ph.Run(p)
		}
		if err != nil {
			if ph.FailFast {
				return fmt.Errorf("phase %s: %w", ph.Name, err)
			}
			log.Printf("  [WARN] phase %s: %v", ph.Name, err)
		}
	}
	if p.Phase != "" && !matched {
		return fmt.Errorf("unknown phase %q", p.Phase)
	}
	return nil
}

// RunUserPhaseByName runs a single user-owned phase (from the provision-user
// subcommand, where we ARE the target user).
func (p *Provisioner) RunUserPhaseByName(name string) error {
	for _, ph := range p.Phases() {
		if ph.Name == name {
			if !ph.User {
				return fmt.Errorf("phase %q is not a user phase", name)
			}
			return ph.Run(p)
		}
	}
	return fmt.Errorf("unknown user phase %q", name)
}

func (p *Provisioner) runUserPhase(selfPath, name string) error {
	_, err := p.Runner.Run("", "sudo", "-u", p.Cfg.User, "--", selfPath, "provision-user", name)
	return err
}

func (p *Provisioner) logPhase(name string) {
	fmt.Printf("==> phase %s\n", name)
}

// SelfExec returns this binary's path for self re-exec.
func SelfExec() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	p, err := filepath.EvalSymlinks(exe)
	if err == nil {
		return p, nil
	}
	return exe, nil
}
