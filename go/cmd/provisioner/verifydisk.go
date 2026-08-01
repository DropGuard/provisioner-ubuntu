package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"provisioner-ubuntu/internal/vmtest"
)

// verifyDiskCmd asserts that an installed qcow2 (the output of `test-vm`)
// actually contains a successful autoinstall: correct partition layout plus
// the files the installer and late-commands must have produced. This closes
// the loop after a VM run — `test-vm` itself only reports disk growth and
// console output, while this command makes the verdict explicit.
func verifyDiskCmd() *cobra.Command {
	var disk string
	cmd := &cobra.Command{
		Use:   "verify-disk",
		Short: "Assert an installed qcow2 (test-vm output) is a successful autoinstall",
		RunE: func(cmd *cobra.Command, args []string) error {
			v, err := vmtest.OpenVerifier(disk)
			if err != nil {
				return err
			}
			defer v.Close()
			if err := vmtest.AssertInstall(v, vmtest.InstallChecks{}); err != nil {
				return fmt.Errorf("install not verified: %w", err)
			}
			fmt.Printf("install verified: %s is a successful autoinstall\n", disk)
			return nil
		},
	}
	cmd.Flags().StringVar(&disk, "disk", "", "installed qcow2 to verify (required)")
	_ = cmd.MarkFlagRequired("disk")
	return cmd
}
