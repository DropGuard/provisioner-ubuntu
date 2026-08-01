package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"provisioner-ubuntu/internal/autoinstall"
	"provisioner-ubuntu/internal/vmtest"
)

func testvmCmd() *cobra.Command {
	var iso, work, target, payload, serial string
	var timeout time.Duration
	var repackOnly bool
	cmd := &cobra.Command{
		Use:   "test-vm",
		Short: "Validate the autoinstall config end-to-end in a KVM VM",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := vmtest.TestOptions{
				SourceISO:  iso,
				WorkDir:    work,
				TargetDisk: target,
				DiskSerial: serial,
				Timeout:    timeout,
				Config:     autoinstall.Default(),
				PayloadDir: payload,
				ProgressFunc: func(p vmtest.ProgressReport) {
					fmt.Printf("  progress: %.1f GiB written (vm %s)\n", p.WrittenGiB, p.Status)
				},
				OnStall: func(p vmtest.ProgressReport) {
					fmt.Printf("  ⚠ SILENT HANG: target not written for a while (%.1f GiB, vm %s)\n", p.WrittenGiB, p.Status)
				},
			}
			if repackOnly {
				p, err := vmtest.PrepareTestISO(opts)
				if err != nil {
					return err
				}
				fmt.Printf("repacked ISO: %s\n", p)
				return nil
			}
			res, err := vmtest.RunTest(opts)
			if err != nil {
				return err
			}
			if res.TimedOut {
				fmt.Printf("qemu TIMED OUT after %s — check %s\n", timeout, res.ConsoleLog)
			} else {
				fmt.Printf("qemu exited (install finished). target: %s\n", humanSize(res.TargetSize))
			}
			fmt.Printf("console log: %s\n", res.ConsoleLog)
			return nil
		},
	}
	cmd.Flags().StringVar(&iso, "iso", "", "original Ubuntu desktop ISO (required)")
	cmd.Flags().StringVar(&work, "work", "/tmp/vmtest-work", "scratch dir")
	cmd.Flags().StringVar(&target, "target", "", "qcow2 target (default $work/target.qcow2)")
	cmd.Flags().StringVar(&payload, "payload", "", "dir copied into nocloud/ (provision, first-boot.service, config/, ...)")
	cmd.Flags().StringVar(&serial, "serial", "50026B727200FDDC", "target disk serial")
	cmd.Flags().DurationVar(&timeout, "timeout", 40*time.Minute, "qemu timeout")
	cmd.Flags().BoolVar(&repackOnly, "repack-only", false, "build the repacked ISO and stop (no VM)")
	_ = cmd.MarkFlagRequired("iso")
	return cmd
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}
