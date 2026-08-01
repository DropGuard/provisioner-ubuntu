package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"provisioner-ubuntu/internal/config"
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
			cfg := config.Default()
			cfg.Fcitx5SetupPath = "/usr/local/bin/setup-fcitx5-chinese.sh"
			if dotfiles := "/usr/local/share/provisioner-ubuntu/dotfiles"; dirExists(dotfiles) {
				os.Setenv("PROVISIONER_DOTFILES", dotfiles)
			}
			p := &provision.Provisioner{Cfg: cfg, Runner: provision.ExecRunner{}}
			p.RunAll(self)
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
			cfg := config.Default()
			// Make brew/mise/npm resolvable for user phases.
			os.Setenv("PATH", userToolPath(cfg)+":"+os.Getenv("PATH"))
			if dotfiles := "/usr/local/share/provisioner-ubuntu/dotfiles"; dirExists(dotfiles) {
				os.Setenv("PROVISIONER_DOTFILES", dotfiles)
			}
			p := &provision.Provisioner{Cfg: cfg, Runner: provision.ExecRunner{}}
			return p.RunUserPhaseByName(args[0])
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
