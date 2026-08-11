package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"

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
	var checkOnly bool
	cmd := &cobra.Command{
		Use:   "test-provision",
		Short: "Phase C: run first-boot provision on a golden/installed snapshot and assert via SSH",
		RunE: func(cmd *cobra.Command, args []string) error {
			if work == "" {
				cacheDir, _ := os.UserCacheDir()
				work = filepath.Join(cacheDir, "vmtest-provision")
			}
			if err := os.MkdirAll(work, 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", work, err)
			}
			if check == "" {
				check = "ls -la /usr/local/bin/ /usr/local/share/provisioner-ubuntu/"
			}

			// check-only boots an already-provisioned base and runs --check
			// without re-provisioning — the fast iteration path once a
			// provisioned snapshot exists (playbook/payload unchanged).
			payloadISO := ""
			if !checkOnly {
				// Assemble the provision payload (scripts + config + dotfiles + ansible).
				// --binary is optional: provisioning is ansible-driven (bootstrap-provision.sh),
				// the Go binary is no longer placed or run.
				payloadDir := filepath.Join(work, "payload")
				if err := payload.Build(payload.Options{
					Out:      payloadDir,
					Binary:   binary,
					Scripts:  filepath.Join(repo, "scripts"),
					Config:   filepath.Join(repo, "config"),
					Dotfiles: filepath.Join(repo, "dotfiles"),
					Ansible:  filepath.Join(repo, "ansible"),
				}); err != nil {
					return err
				}
				// Delivery ISO: non-cidata label so cloud-init ignores it; the guest
				// placement script mounts it by label.
				payloadISO = filepath.Join(work, "provision-payload.iso")
				if err := vmtest.WritePayloadISO(payloadISO, "provision-payload", payloadDir); err != nil {
					return err
				}
			}

			overlay := vmtest.SnapshotOverlayPath(work, "boot")
			if err := vmtest.CreateOverlay(base, overlay); err != nil {
				return fmt.Errorf("create overlay: %w", err)
			}
			fmt.Printf("snapshot overlay: %s (rollback = delete + recreate)\n", overlay)

			// VM boot/SSH has its own, shorter ceiling than the provision run:
			// a stuck boot should fail fast instead of eating the whole timeout
			// silently. 10m covers slow Server-ISO boots; tune if needed.
			bootTimeout := 10 * time.Minute
			if timeout < bootTimeout {
				bootTimeout = timeout
			}
			sess, err := vmtest.BootReady(vmtest.BootOptions{
				Disk: overlay, WorkDir: work, Timeout: bootTimeout, Seed: payloadISO,
			})
			if err != nil {
				return err
			}
			defer sess.Kill()
			fmt.Printf("ssh up — console: %s/boot-console.log\n", work)

			if !checkOnly {
				// Deliver the payload + start first-boot provisioning (root via sudo).
				if err := deliverAndStartProvision(sess.SSH); err != nil {
					return err
				}

				// Wait for the provisioner (first-boot.service) to finish.
				// Progress every 60s so a stuck apt/debconf is visible, not silent.
				consoleLog := filepath.Join(work, "boot-console.log")
				state, err := waitProvisionDone(sess.SSH, timeout, 60*time.Second, consoleLog)
				if err != nil {
					return err
				}
				// first-boot.service streams journal+console, so ansible failures and
				// the play recap land in boot-console.log — surface them here instead
				// of forcing a manual grep of a second file.
				if failCtx := consoleFailures(consoleLog); failCtx != "" {
					fmt.Printf("ansible 失败/recap 摘要 (boot-console.log):\n%s\n", failCtx)
				}
				if state == "failed" {
					return fmt.Errorf("first-boot.service FAILED — provision did not complete\n调试: %s", debugBootHint(overlay, work))
				}
			} else {
				fmt.Println("check-only: 基于已 provision 的镜像,跳过重新 provision")
			}
			// --check 的超时跟随 --timeout: 长 check (如在 VM 里真装大包验证)
			// 不应被 30s 硬编码截断。给个 1m 下限防止 check 自身挂死流程。
			checkTimeout := timeout
			if checkTimeout < time.Minute {
				checkTimeout = time.Minute
			}
			out, err := vmtest.SshRun(sess.SSH, check, checkTimeout)
			fmt.Printf("guest check result:\n%s\n", out)
			if err != nil {
				return fmt.Errorf("ssh: %w\n%s\n调试: %s", err, out, debugBootHint(overlay, work))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&base, "base", "", "base qcow2 (golden image or provisioned snapshot) — required")
	cmd.Flags().StringVar(&binary, "binary", "", "provisioner binary (optional; unused since the ansible transition)")
	cmd.Flags().StringVar(&repo, "repo", ".", "repo root containing scripts/, config/, dotfiles/")
	cmd.Flags().StringVar(&work, "work", "", "scratch dir (default $HOME/.cache/vmtest-provision)")
	cmd.Flags().StringVar(&check, "check", "", "shell command to run in the guest (default: list provision payload)")
	cmd.Flags().DurationVar(&timeout, "timeout", 40*time.Minute, "provision timeout")
	cmd.Flags().BoolVar(&checkOnly, "check-only", false, "boot an already-provisioned base and run --check without re-provisioning")
	_ = cmd.MarkFlagRequired("base")
	return cmd
}

// provisionPlaceScript runs as root in the guest (via sudo): it mounts the
// payload ISO, copies the payload to the on-target locations (mirroring the
// autoinstall late-commands), and starts first-boot provisioning. set -e makes
// any failure (wrong label, missing file) surface as an error.
const provisionPlaceScript = `set -e
mkdir -p /mnt/payload /usr/local/share/provisioner-ubuntu/config /usr/local/share/provisioner-ubuntu/dotfiles /usr/local/share/provisioner-ubuntu/ansible
mount -t iso9660 -o ro /dev/disk/by-label/provision-payload /mnt/payload
cp /mnt/payload/first-boot.service /etc/systemd/system/first-boot.service
cp /mnt/payload/bootstrap-provision.sh /usr/local/bin/bootstrap-provision.sh
chmod +x /usr/local/bin/bootstrap-provision.sh
cp /mnt/payload/fav.sh /usr/local/bin/fav 2>/dev/null || true
cp -a /mnt/payload/config/. /usr/local/share/provisioner-ubuntu/config/
cp -a /mnt/payload/dotfiles/. /usr/local/share/provisioner-ubuntu/dotfiles/
cp -a /mnt/payload/ansible/. /usr/local/share/provisioner-ubuntu/ansible/
systemctl daemon-reload
systemctl enable --now --no-block first-boot.service
`

// consoleFailures extracts ansible failure context from a test VM console log:
// first-boot.service streams StandardOutput=journal+console, so fatal:/FAILED!/
// play-recap lines land here. Keeps the last 15 matches — enough to see the
// failing task and the recap without opening the file.
func consoleFailures(logPath string) string {
	b, err := os.ReadFile(logPath)
	if err != nil {
		return ""
	}
	var lines []string
	for _, ln := range strings.Split(string(b), "\n") {
		if strings.Contains(ln, "fatal:") || strings.Contains(ln, "FAILED!") ||
			strings.Contains(ln, "play recap") || strings.Contains(ln, "ERROR!") ||
			strings.Contains(ln, "failed=") || strings.Contains(ln, "ignored:") ||
			strings.Contains(ln, "FAILED]") {
			lines = append(lines, ln)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	if len(lines) > 15 {
		lines = lines[len(lines)-15:]
	}
	return strings.Join(lines, "\n")
}

// debugBootHint prints a ready-to-run qemu command to re-open the preserved
// overlay for interactive inspection (SSH: ssh -p 2222 dailyuser@localhost, pw 1).
// Flags mirror vmtest.BootReady so the session behaves like the harness's.
func debugBootHint(overlay, work string) string {
	vars := filepath.Join(work, "debug.vars")
	serial := "50026B727200FDDC" // project's target disk serial (storage.match.serial)
	return fmt.Sprintf(`overlay 保留在 %s,重开交互会话:
  cp /usr/share/OVMF/OVMF_VARS_4M.fd %s
  qemu-system-x86_64 -machine q35,accel=kvm -cpu host -m 4096 -smp 2 \
    -drive if=pflash,format=raw,readonly=on,file=/usr/share/OVMF/OVMF_CODE_4M.fd \
    -drive if=pflash,format=raw,file=%s \
    -drive file=%s,format=qcow2,if=none,id=target \
    -device virtio-blk-pci,drive=target,serial=%s \
    -netdev user,id=net0,hostfwd=tcp::2222-:22 -device virtio-net-pci,netdev=net0 \
    -nographic -serial file:%s -monitor none -no-reboot
  (SSH 进入: ssh -p 2222 dailyuser@localhost,密码 1)`, overlay, vars, vars, overlay, serial, filepath.Join(work, "debug-console.log"))
}

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
//
// It is NOT a black box: every progressInterval it prints the elapsed wait and
// the last ansible TASK seen in the console log, so a hung provision (e.g. apt
// stuck on a debconf prompt) is visible immediately instead of silently eating
// the whole timeout.
func waitProvisionDone(client *ssh.Client, timeout, progressInterval time.Duration, consoleLog string) (string, error) {
	deadline := time.Now().Add(timeout)
	nextProgress := time.Now()
	var lastTask string
	for time.Now().Before(deadline) {
		out, _ := vmtest.SshRun(client, "systemctl is-active first-boot.service", 10*time.Second)
		state := strings.TrimSpace(out)
		switch state {
		// "inactive"/"" can be observed before the start job lands (enable --now
		// --no-block returns once queued), so treat them as still-booting.
		case "activating", "reloading", "inactive", "":
			// still running or not yet started
		default:
			if strings.HasPrefix(state, "Failed to retrieve") || strings.Contains(state, "disconnected from message bus") {
				// dbus restarted during package installation (e.g. sddm/kde); transient error, keep waiting.
				break
			}
			// "active" (RemainAfterExit, finished) or "failed"
			fmt.Printf("first-boot.service state: %s\n", state)
			return state, nil
		}
		// Periodic progress: surface what ansible is doing right now.
		if time.Now().After(nextProgress) {
			cur := lastAnsibleTask(consoleLog)
			if cur != lastTask {
				lastTask = cur
			}
			aptLog, _ := vmtest.SshRun(client, "tail -n 1 /var/log/apt/term.log 2>/dev/null || true", 5*time.Second)
			aptLog = strings.TrimSpace(aptLog)
			if aptLog != "" {
				aptLog = " | apt: " + aptLog
			}
			fmt.Printf("[wait %s] provision running — last ansible task: %s%s\n",
				time.Until(deadline).Truncate(time.Second)*-1, orEllipsis(lastTask), aptLog)
			nextProgress = time.Now().Add(progressInterval)
		}
		time.Sleep(5 * time.Second)
	}
	return "", fmt.Errorf("first-boot.service still running after %v", timeout)
}

// lastAnsibleTask returns the most recent "TASK [..]" line from the console log,
// so progress output shows where provisioning is stuck.
func lastAnsibleTask(consoleLog string) string {
	b, err := os.ReadFile(consoleLog)
	if err != nil {
		return "(log unreadable)"
	}
	last := ""
	for _, ln := range strings.Split(string(b), "\n") {
		if strings.Contains(ln, "TASK [") {
			last = ln
		}
	}
	return last
}

func orEllipsis(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "(no task seen yet)"
	}
	if len(s) > 120 {
		return s[:117] + "..."
	}
	return s
}
