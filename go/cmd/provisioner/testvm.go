package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"provisioner-ubuntu/internal/autoinstall"
	"provisioner-ubuntu/internal/vmtest"
)

func testvmCmd() *cobra.Command {
	var iso, work, target, payload, serial, aptProxy string
	var timeout time.Duration
	var repackOnly, golden bool
	cmd := &cobra.Command{
		Use:   "test-vm",
		Short: "Validate the autoinstall config end-to-end in a KVM VM",
		RunE: func(cmd *cobra.Command, args []string) error {
			if work == "" {
				home, _ := os.UserHomeDir()
				work = filepath.Join(home, ".cache", "vmtest-work")
			}

			// Golden mode: minimal install, cached by ISO hash.
			if golden {
				hash, err := vmtest.FileSha256(iso)
				if err != nil {
					return fmt.Errorf("hash ISO: %w", err)
				}
				cached := vmtest.GoldenImagePath(hash, autoinstall.GoldenVersion)
				if vmtest.GoldenImageCached(hash, autoinstall.GoldenVersion) {
					fmt.Printf("golden image cached: %s (%d bytes)\n", cached, fileSize(cached))
					return nil
				}
				fmt.Printf("building golden image (this will take ~10 min)...\n")
				cfg := autoinstall.Golden()
				if aptProxy != "" {
					cfg.AptProxy = aptProxy
				}
				if timeout == 0 {
					timeout = 30 * time.Minute
				}
				opts := vmtest.TestOptions{
					SourceISO:  iso,
					WorkDir:    work,
					DiskSerial: serial,
					Timeout:    timeout,
					Config:     cfg,
					// No payload — golden image is a clean install.
					ProgressFunc: func(p vmtest.ProgressReport) {
						fmt.Printf("  progress: %.1f GiB written (vm %s)\n", p.WrittenGiB, p.Status)
					},
					OnStall: func(p vmtest.ProgressReport) {
						fmt.Printf("  ⚠ SILENT HANG: target not written for a while (%.1f GiB, vm %s)\n", p.WrittenGiB, p.Status)
					},
				}
				res, err := vmtest.RunTest(opts)
				if err != nil {
					return err
				}
				if res.TimedOut {
					return fmt.Errorf("golden install timed out after %v — check %s", timeout, res.ConsoleLog)
				}
				targetDisk := target
				if targetDisk == "" {
					targetDisk = filepath.Join(work, "target.qcow2")
				}
				if err := vmtest.CacheGoldenImage(hash, autoinstall.GoldenVersion, targetDisk); err != nil {
					return fmt.Errorf("cache golden: %w", err)
				}
				fmt.Printf("golden image cached: %s (%d bytes)\n", cached, fileSize(cached))
				return nil
			}

			cfg := autoinstall.Default()
			if aptProxy != "" {
				cfg.AptProxy = aptProxy
			}
			opts := vmtest.TestOptions{
				SourceISO:  iso,
				WorkDir:    work,
				TargetDisk: target,
				DiskSerial: serial,
				Timeout:    timeout,
				Config:     cfg,
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
				fmt.Printf("repacked ISO: %s\n", p.Repacked)
				fmt.Printf("seed.iso:     %s\n", p.Seed)
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
	cmd.Flags().StringVar(&work, "work", "", "scratch dir (default $HOME/.cache/vmtest-work)")
	cmd.Flags().StringVar(&target, "target", "", "qcow2 target (default $work/target.qcow2)")
	cmd.Flags().StringVar(&payload, "payload", "", "dir copied into nocloud/ (provision, first-boot.service, config/, ...)")
	cmd.Flags().StringVar(&serial, "serial", "50026B727200FDDC", "target disk serial")
	cmd.Flags().DurationVar(&timeout, "timeout", 40*time.Minute, "qemu timeout")
	cmd.Flags().StringVar(&aptProxy, "apt-proxy", "", "apt proxy for the install (e.g. http://10.0.2.2:3142 for host apt-cacher-ng)")
	cmd.Flags().BoolVar(&repackOnly, "repack-only", false, "build the repacked ISO and stop (no VM)")
	cmd.Flags().BoolVar(&golden, "golden", false, "build the cached golden image (minimal install, Phase A)")
	_ = cmd.MarkFlagRequired("iso")
	return cmd
}

func fileSize(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return -1
	}
	return st.Size()
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
