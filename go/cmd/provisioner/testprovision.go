package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"github.com/spf13/cobra"

	"provisioner-ubuntu/internal/payload"
	"provisioner-ubuntu/internal/vmtest"
)

// testProvisionCmd implements Phase C of the e2e pipeline: it validates the
// first-boot provisioner on a snapshot of a golden/installed system — the half
// of the old bash e2e pipeline that the golden architecture (Phase A + B) did
// not absorb. It boots an overlay, delivers the provision payload via a
// non-cidata ISO, starts first-boot.service (the real provisioner), waits for
// it to finish, and asserts. Discarding the overlay rolls back.
func testProvisionCmd() *cobra.Command {
	var base, work, check, repo, binary string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "test-provision",
		Short: "Phase C: run first-boot provision on a golden/installed snapshot and assert via SSH",
		RunE: func(cmd *cobra.Command, args []string) error {
			if work == "" {
				home, _ := os.UserHomeDir()
				work = filepath.Join(home, ".cache", "vmtest-provision")
			}
			if err := os.MkdirAll(work, 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", work, err)
			}
			if check == "" {
				check = "ls -la /usr/local/bin/ /usr/local/share/provisioner-ubuntu/"
			}

			// Assemble the provision payload (binary + scripts + config + dotfiles).
			payloadDir := filepath.Join(work, "payload")
			if err := payload.Build(payload.Options{
				Out:      payloadDir,
				Binary:   binary,
				Scripts:  filepath.Join(repo, "scripts"),
				Config:   filepath.Join(repo, "config"),
				Dotfiles: filepath.Join(repo, "dotfiles"),
			}); err != nil {
				return err
			}
			// Delivery ISO: non-cidata label so cloud-init ignores it; the guest
			// placement script mounts it by label.
			payloadISO := filepath.Join(work, "provision-payload.iso")
			if err := vmtest.WritePayloadISO(payloadISO, "provision-payload", payloadDir); err != nil {
				return err
			}

			overlay := vmtest.SnapshotOverlayPath(work, "boot")
			if err := vmtest.CreateOverlay(base, overlay); err != nil {
				return fmt.Errorf("create overlay: %w", err)
			}
			fmt.Printf("snapshot overlay: %s (rollback = delete + recreate)\n", overlay)

			sess, err := vmtest.BootReady(vmtest.BootOptions{
				Disk: overlay, WorkDir: work, Timeout: timeout, Seed: payloadISO,
			})
			if err != nil {
				return err
			}
			defer sess.Kill()
			fmt.Printf("ssh up — console: %s/boot-console.log\n", work)

			// Deliver the payload + start first-boot provisioning (root via sudo).
			if err := deliverAndStartProvision(sess.SSH); err != nil {
				return err
			}

			// Wait for the provisioner (first-boot.service) to finish.
			if err := waitProvisionDone(sess.SSH, timeout); err != nil {
				return err
			}
			out, err := vmtest.SshRun(sess.SSH, check, 30*time.Second)
			fmt.Printf("guest check result:\n%s\n", out)
			if err != nil {
				return fmt.Errorf("ssh: %w", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&base, "base", "", "base qcow2 (golden image or installed system) — required")
	cmd.Flags().StringVar(&binary, "binary", "", "provisioner binary, copied as payload/provision (required)")
	cmd.Flags().StringVar(&repo, "repo", ".", "repo root containing scripts/, config/, dotfiles/")
	cmd.Flags().StringVar(&work, "work", "", "scratch dir (default $HOME/.cache/vmtest-provision)")
	cmd.Flags().StringVar(&check, "check", "", "shell command to run in the guest (default: list provision payload)")
	cmd.Flags().DurationVar(&timeout, "timeout", 40*time.Minute, "provision timeout")
	_ = cmd.MarkFlagRequired("base")
	_ = cmd.MarkFlagRequired("binary")
	return cmd
}

// provisionPlaceScript runs as root in the guest (via sudo): it mounts the
// payload ISO, copies the payload to the on-target locations (mirroring the
// autoinstall late-commands), and starts first-boot provisioning. set -e makes
// any failure (wrong label, missing file) surface as an error.
const provisionPlaceScript = `set -e
mkdir -p /mnt/payload /usr/local/share/provisioner-ubuntu/config /usr/local/share/provisioner-ubuntu/dotfiles
mount -t iso9660 -o ro /dev/disk/by-label/provision-payload /mnt/payload
cp /mnt/payload/provision /usr/local/bin/provision
cp /mnt/payload/first-boot.service /etc/systemd/system/first-boot.service
cp /mnt/payload/setup-fcitx5-chinese.sh /usr/local/bin/setup-fcitx5-chinese.sh
cp /mnt/payload/fav.sh /usr/local/bin/fav 2>/dev/null || true
cp -a /mnt/payload/config/. /usr/local/share/provisioner-ubuntu/config/
cp -a /mnt/payload/dotfiles/. /usr/local/share/provisioner-ubuntu/dotfiles/
chmod +x /usr/local/bin/provision /usr/local/bin/setup-fcitx5-chinese.sh /usr/local/bin/fav
systemctl daemon-reload
systemctl enable --now --no-block first-boot.service
`

// deliverAndStartProvision runs the placement script as root via sudo. The
// golden image's dailyuser is in the sudo group with the build-time password
// "1" (matching the autoinstall identity).
func deliverAndStartProvision(client *ssh.Client) error {
	cmd := "echo '1' | sudo -S -p '' bash -c '" + provisionPlaceScript + "'"
	out, err := vmtest.SshRun(client, cmd, 60*time.Second)
	if err != nil {
		return fmt.Errorf("deliver payload + start provision: %w\n%s", err, out)
	}
	return nil
}

// waitProvisionDone polls the first-boot.service state (a RemainAfterExit
// oneshot) until it leaves the running states — "active" (finished), "inactive"
// or "failed" — so the assertion runs against the provisioned system.
// systemctl exits non-zero for non-active states, so the output is read
// regardless of SshRun's error.
func waitProvisionDone(client *ssh.Client, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, _ := vmtest.SshRun(client, "systemctl is-active first-boot.service", 10*time.Second)
		switch strings.TrimSpace(out) {
		// "inactive"/"" can be observed before the start job lands (enable --now
		// --no-block returns once queued), so treat them as still-booting.
		case "activating", "reloading", "inactive", "":
			// still running or not yet started
		default: // "active" (RemainAfterExit, finished) or "failed"
			fmt.Printf("first-boot.service state: %s\n", strings.TrimSpace(out))
			return nil
		}
		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("first-boot.service still running after %v", timeout)
}
