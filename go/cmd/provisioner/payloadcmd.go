package main

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"provisioner-ubuntu/internal/payload"
)

// buildPayloadCmd assembles the seed payload directory (the tree planted under
// nocloud/ and later copied into the installed system by the autoinstall
// late-commands). Its output is what `test-vm --payload` and `usb --payload`
// consume — one command replaces the manual directory assembly.
func buildPayloadCmd() *cobra.Command {
	var binary, repo, out string
	cmd := &cobra.Command{
		Use:   "build-payload",
		Short: "Assemble the nocloud seed payload from the repo",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := payload.Build(payload.Options{
				Out:      out,
				Binary:   binary,
				Scripts:  filepath.Join(repo, "scripts"),
				Config:   filepath.Join(repo, "config"),
					Dotfiles: filepath.Join(repo, "dotfiles"),
					Ansible:  filepath.Join(repo, "ansible"),
			}); err != nil {
				return err
			}
			fmt.Printf("payload built in %s\n", out)
			return nil
		},
	}
	cmd.Flags().StringVar(&out, "out", "build/payload", "output dir")
	cmd.Flags().StringVar(&binary, "binary", "", "provisioner binary, copied as nocloud/provision (optional)")
	cmd.Flags().StringVar(&repo, "repo", ".", "repo root containing scripts/, config/, dotfiles/")
	return cmd
}
