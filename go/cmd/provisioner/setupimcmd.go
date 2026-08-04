package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"provisioner-ubuntu/internal/fcitx"
)

func setupIMCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "setup-im",
		Hidden: true,
		Short:  "Configure Rime via D-Bus (called via autostart)",
		Run: func(cmd *cobra.Command, args []string) {
			if err := fcitx.ConfigureRimeViaDBus(); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		},
	}
}
