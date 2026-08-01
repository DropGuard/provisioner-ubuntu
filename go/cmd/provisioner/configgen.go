package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"provisioner-ubuntu/internal/autoinstall"
)

func configgenCmd() *cobra.Command {
	var serial, user, pass string
	cmd := &cobra.Command{
		Use:   "config-gen",
		Short: "Render user-data + grub.cfg from the typed config",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := autoinstall.Default()
			c.DiskSerial = serial
			if user != "" {
				c.Identity.Username = user
			}
			if pass != "" {
				c.Identity.PasswordHash = pass
			}
			ud, err := autoinstall.RenderUserData(c)
			if err != nil {
				return err
			}
			fmt.Println("=== user-data ===")
			fmt.Print(ud)
			fmt.Println("=== grub.cfg ===")
			fmt.Print(autoinstall.RenderGrubCfg("/cdrom/nocloud/", true))
			return nil
		},
	}
	cmd.Flags().StringVar(&serial, "serial", "50026B727200FDDC", "target disk serial")
	cmd.Flags().StringVar(&user, "user", "dailyuser", "created user")
	cmd.Flags().StringVar(&pass, "pass-hash", "", "sha512-crypt password hash (default: generated for '1')")
	return cmd
}
