package main

import (
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/spf13/cobra"

	"provisioner-ubuntu/internal/config"
	"provisioner-ubuntu/internal/paths"
	"provisioner-ubuntu/internal/provision"
)

// provisionCmd runs first-boot provisioning as root. User-owned phases are run
// by re-executing this binary as the target user (provision-user subcommand).
func provisionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "provision",
		Short: "Run first-boot provisioning (needs root)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if os.Geteuid() != 0 {
				return errors.New("provision must run as root")
			}
			self, err := provision.SelfExec()
			if err != nil {
				return err
			}
			cfg, err := config.Load(paths.ConfigDir)
			if err != nil {
				log.Printf("  [WARN] loading %s: %v (using built-in defaults)", paths.ConfigDir, err)
				cfg = config.Default()
			}
			cfg.Fcitx5SetupPath = paths.Fcitx5Script
			if dotfiles := paths.DotfilesDir; dirExists(dotfiles) {
				os.Setenv("PROVISIONER_DOTFILES", dotfiles)
			}
			p := &provision.Provisioner{Cfg: cfg, Runner: provision.ExecRunner{}}
			// A FailFast phase error propagates here, so the oneshot service is
			// marked failed and can be retried with `systemctl restart
			// first-boot.service` after fixing the cause.
			if err := p.RunAll(self); err != nil {
				return err
			}
			return nil
		},
	}
}

// provisionUserCmd is the re-entered half: it runs a single user-owned phase
// as the target user (invoked by the root provision run via
// `sudo -u USER provisioner-ubuntu provision-user <phase>`).
func provisionUserCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "provision-user <phase>",
		Short: "Run a user-owned provisioning phase (internal, via self re-exec)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if os.Geteuid() == 0 {
				return errors.New("provision-user must NOT run as root")
			}
			cfg, err := config.Load(paths.ConfigDir)
			if err != nil {
				log.Printf("  [WARN] loading %s: %v (using built-in defaults)", paths.ConfigDir, err)
				cfg = config.Default()
			}
			// This process IS the target user (spawned by the root run via sudo -u).
			// Mark it so Runner skips redundant sudo inside the phases.
			os.Setenv(provision.ProvisionUserReexecEnv, "1")
			// Make brew/mise/npm resolvable for user phases.
			os.Setenv("PATH", userToolPath(cfg)+":"+os.Getenv("PATH"))
			if dotfiles := paths.DotfilesDir; dirExists(dotfiles) {
				os.Setenv("PROVISIONER_DOTFILES", dotfiles)
			}
			p := &provision.Provisioner{Cfg: cfg, Runner: provision.ExecRunner{}}
			// Surface the phase's error on stderr so the root run's `sudo -u ...`
			// captures it and the WARN shows WHY the user phase failed, not just
			// the re-exec's exit status.
			if err := p.RunUserPhaseByName(args[0]); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return err
			}
			return nil
		},
	}
}

func userToolPath(cfg config.Provision) string {
	return fmt.Sprintf("%s/bin:%s/.local/bin:%s/.local/share/mise/shims",
		cfg.BrewPrefix, cfg.Home, cfg.Home)
}

func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
