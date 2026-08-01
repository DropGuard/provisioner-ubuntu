package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"provisioner-ubuntu/internal/vmtest"
)

// testProvisionCmd validates the first-boot provisioning on a snapshot of the
// installed system: it creates a qcow2 external-snapshot overlay of the clean
// install, boots it (the installed system's first-boot.service runs the Go
// provisioner), then runs a guest-exec check inside the VM via the QMP guest
// agent and reports. Discarding the overlay rolls back to the clean install.
func testProvisionCmd() *cobra.Command {
	var base, work, check string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "test-provision",
		Short: "Run first-boot provision on a snapshot of the installed disk and assert via guest-exec",
		RunE: func(cmd *cobra.Command, args []string) error {
			if work == "" {
				work = "/tmp/vmtest-provision"
			}
			if check == "" {
				check = "ls -la /usr/local/bin/ /usr/local/share/provisioner-ubuntu/"
			}
			overlay := vmtest.SnapshotOverlayPath(work, "boot")
			if err := vmtest.CreateOverlay(base, overlay); err != nil {
				return fmt.Errorf("create overlay: %w", err)
			}
			fmt.Printf("snapshot overlay: %s (rollback = delete + recreate)\n", overlay)

			timedOut, err := vmtest.BootInstalled(vmtest.BootOptions{
				Disk: overlay, WorkDir: work, Timeout: timeout,
			})
			if err != nil {
				return err
			}
			if timedOut {
				fmt.Printf("boot timed out after %v — check %s/boot-console.log\n", timeout, work)
			}
			// After boot, the guest agent should be up: run the check inside the VM.
			q, err := vmtest.ConnectQMP(work + "/qmp.sock")
			if err != nil {
				return fmt.Errorf("connect qmp (guest may not have finished booting): %w", err)
			}
			defer q.Close()
			out, err := q.GuestExec("/bin/sh", []string{"-c", check}, 30*time.Second)
			fmt.Printf("guest-exec result:\n%s\n", out)
			if err != nil {
				return fmt.Errorf("guest-exec: %w", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&base, "base", "", "clean installed qcow2 (the install output) — required")
	cmd.Flags().StringVar(&work, "work", "/tmp/vmtest-provision", "scratch dir")
	cmd.Flags().StringVar(&check, "check", "", "shell command to run in the guest (default: list provision payload)")
	cmd.Flags().DurationVar(&timeout, "timeout", 25*time.Minute, "boot timeout")
	_ = cmd.MarkFlagRequired("base")
	return cmd
}
