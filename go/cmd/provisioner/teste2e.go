package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"provisioner-ubuntu/internal/autoinstall"
	"provisioner-ubuntu/internal/vmtest"
)

// testE2ECmd implements Phase B of the e2e pipeline: snapshot-based testing
// using a cached golden image (Phase A) + SSH assertions.
//
// Each test case creates a qcow2 overlay of the golden image, boots it, runs
// an SSH command, asserts the output, and rolls back by discarding the
// overlay. Failures in one case don't affect others.
func testE2ECmd() *cobra.Command {
	var iso, serial string
	cmd := &cobra.Command{
		Use:   "test-e2e",
		Short: "Phase B: golden-image smoke tests via SSH",
		Long: `Boots a cached golden image (Phase A) on a fresh qcow2 overlay per test
case and runs an SSH assertion. Failures don't cascade — each case rolls
back by discarding its overlay.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// ── Ensure golden image exists ──────────────────────────
			hash, err := vmtest.FileSha256(iso)
			if err != nil {
				return fmt.Errorf("hash ISO: %w", err)
			}
			goldenPath := vmtest.GoldenImagePath(hash, autoinstall.GoldenVersion)
			if !vmtest.GoldenImageCached(hash, autoinstall.GoldenVersion) {
				return fmt.Errorf("golden image not found at %s — run `test-vm --golden --iso %s` first", goldenPath, iso)
			}
			fmt.Printf("golden image: %s (%d bytes)\n", goldenPath, fileSize(goldenPath))

			work, _ := os.MkdirTemp("", "e2e-*")
			defer os.RemoveAll(work)
			fmt.Printf("work dir: %s\n", work)

			// ── Run test cases ──────────────────────────────────────
			passed, failed := 0, 0
			for _, tc := range testCases {
				fmt.Printf("\n── test: %s ──\n", tc.name)
				err := runTestCase(work, goldenPath, serial, tc)
				if err != nil {
					fmt.Printf("  FAIL: %v\n", err)
					failed++
				} else {
					fmt.Printf("  PASS\n")
					passed++
				}
			}
			fmt.Printf("\n── %d passed, %d failed ──\n", passed, failed)
			if failed > 0 {
				return fmt.Errorf("%d test(s) failed", failed)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&iso, "iso", "", "original Ubuntu desktop ISO (required)")
	cmd.Flags().StringVar(&serial, "serial", "50026B727200FDDC", "disk serial")
	_ = cmd.MarkFlagRequired("iso")
	return cmd
}

// testCase is a single assertion run against a fresh golden overlay.
type testCase struct {
	name    string
	check   string // command run in the guest over SSH
	contain string // expected substring in stdout
	timeout time.Duration
}

var testCases = []testCase{
	{
		name:    "ssh exec responsive",
		check:   "echo ready",
		contain: "ready",
		timeout: 30 * time.Second,
	},
	{
		name:    "dailyuser exists",
		check:   "id dailyuser",
		contain: "uid=",
		timeout: 10 * time.Second,
	},
	{
		name:    "qemu-guest-agent installed",
		check:   "dpkg -l qemu-guest-agent 2>/dev/null | grep '^ii'",
		contain: "qemu-guest-agent",
		timeout: 10 * time.Second,
	},
	{
		name:    "openssh-server installed",
		check:   "dpkg -l openssh-server 2>/dev/null | grep '^ii'",
		contain: "openssh-server",
		timeout: 10 * time.Second,
	},
	{
		name:    "hostname is ubuntu",
		check:   "hostname",
		contain: "ubuntu",
		timeout: 10 * time.Second,
	},
	{
		name:    "locale is en_US.UTF-8",
		check:   "cat /etc/default/locale || cat /etc/locale.conf",
		contain: "en_US.UTF-8",
		timeout: 10 * time.Second,
	},
	{
		name:    "timezone is Asia_Shanghai",
		check:   "cat /etc/timezone 2>/dev/null || ls -l /etc/localtime",
		contain: "Shanghai",
		timeout: 10 * time.Second,
	},
	// NOTE: provision-phase outcomes (NVIDIA, kubuntu-ppa, dotfiles) are NOT
	// asserted here — Phase B boots the minimal unprovisioned golden. They are
	// machine-specific (nvidia tasks are gated on `lspci | grep nvidia` in
	// ansible/tasks/system.yml) and belong in Phase C / real-hardware runs.
}

// runTestCase creates an overlay of the golden image, boots it, runs a
// guest-exec check, and discards the overlay.
func runTestCase(work, golden, serial string, tc testCase) error {
	overlay := filepath.Join(work, tc.name+".qcow2")
	if err := vmtest.CreateOverlay(golden, overlay); err != nil {
		return fmt.Errorf("create overlay: %w", err)
	}
	defer os.Remove(overlay)

	bootWork := filepath.Join(work, tc.name)
	os.MkdirAll(bootWork, 0o755)

	// Boot the overlay; BootReady returns once the guest's sshd accepts password
	// auth (OS up) and leaves the VM running for SSH assertions. Kill tears it down.
	sess, err := vmtest.BootReady(vmtest.BootOptions{
		Disk:    overlay,
		WorkDir: bootWork,
		Serial:  serial,
		Timeout: 5 * time.Minute,
	})
	if err != nil {
		return fmt.Errorf("boot: %w", err)
	}
	defer sess.Kill()

	// Run the test check over SSH.
	out, err := vmtest.SshRun(sess.SSH, tc.check, tc.timeout)
	if err != nil {
		return fmt.Errorf("ssh %q: %w\n%s", tc.check, err, out)
	}
	if tc.contain != "" && !strings.Contains(out, tc.contain) {
		return fmt.Errorf("output does not contain %q\n%s", tc.contain, out)
	}
	return nil
}
