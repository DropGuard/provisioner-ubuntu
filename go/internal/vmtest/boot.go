package vmtest

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"provisioner-ubuntu/internal/autoinstall"
)

// TestOptions configures one full VM autoinstall validation run.
type TestOptions struct {
	SourceISO  string        // the original Ubuntu desktop ISO
	WorkDir    string        // scratch dir (tree, repacked ISO, console.log)
	TargetDisk string        // qcow2 written by the installer
	DiskSerial string        // serial matched by storage.match.serial
	Timeout    time.Duration // overall qemu timeout
	Config     autoinstall.Config
	PayloadDir string // copied into nocloud/ (provision, first-boot.service, config/, ...)
}

// TestResult reports what happened.
type TestResult struct {
	TargetSize int64
	ConsoleLog string
	TimedOut   bool
}

const defaultDiskSerial = "50026B727200FDDC"

// PrepareTestISO builds the seed + repacked ISO (no VM run). It is also used
// to iterate on the repack in isolation.
func PrepareTestISO(opts TestOptions) (repacked string, err error) {
	if opts.SourceISO == "" {
		return "", fmt.Errorf("SourceISO required")
	}
	if opts.WorkDir == "" {
		opts.WorkDir = "/tmp/vmtest-work"
	}
	if opts.DiskSerial == "" {
		opts.DiskSerial = defaultDiskSerial
	}
	// Password hash is build-time injected, like the bash `openssl passwd -6`.
	if opts.Config.Identity.PasswordHash == "" {
		hash, err := opensslPasswd()
		if err != nil {
			return "", fmt.Errorf("generate password hash: %w", err)
		}
		opts.Config.Identity.PasswordHash = hash
	}

	tree := filepath.Join(opts.WorkDir, "iso-tree")
	repacked = filepath.Join(opts.WorkDir, "ubuntu-autoinstall.iso")

	payload, err := seedPayloadFromDir(opts.PayloadDir)
	if err != nil {
		return "", fmt.Errorf("seed payload: %w", err)
	}
	userData, err := autoinstall.RenderUserData(opts.Config)
	if err != nil {
		return "", err
	}
	seed := SeedConfig{
		UserData: userData,
		MetaData: "# cloud-init meta-data — intentionally empty.\n",
		Payload:  payload,
	}

	os.RemoveAll(tree)
	os.Remove(repacked)
	if err := RepackISO(opts.SourceISO, tree, repacked, seed); err != nil {
		return "", err
	}
	return repacked, nil
}

// RunTest builds the seed + repacked ISO, boots it in a KVM VM the way a real
// USB boots, and waits for the install to finish (or timeout). Fully no-root:
// ISO extraction (go-diskfs) and verification (go-qcow2reader + go-diskfs) are
// pure Go; xorriso and qemu run as the current user.
func RunTest(opts TestOptions) (*TestResult, error) {
	if opts.TargetDisk == "" {
		opts.TargetDisk = filepath.Join(opts.WorkDir, "target.qcow2")
	}
	if opts.Timeout == 0 {
		opts.Timeout = 40 * time.Minute
	}
	repacked, err := PrepareTestISO(opts)
	if err != nil {
		return nil, err
	}
	consoleLog := filepath.Join(opts.WorkDir, "console.log")
	os.Remove(consoleLog)
	os.Remove(opts.TargetDisk)
	if err := qemuImgCreate(opts.TargetDisk, "120G"); err != nil {
		return nil, err
	}

	timedOut, err := launchQEMU(qemuOptions{
		cdrom: repacked, target: opts.TargetDisk, serial: opts.DiskSerial,
		console: consoleLog, timeout: opts.Timeout,
		qmp: filepath.Join(opts.WorkDir, "qmp.sock"),
	})
	if err != nil {
		return nil, fmt.Errorf("qemu: %w", err)
	}
	st, err := os.Stat(opts.TargetDisk)
	if err != nil {
		return nil, fmt.Errorf("stat target: %w", err)
	}
	return &TestResult{TargetSize: st.Size(), ConsoleLog: consoleLog, TimedOut: timedOut}, nil
}

// seedPayloadFromDir reads every file under dir into a map keyed by its
// relative path, for planting under nocloud/.
func seedPayloadFromDir(dir string) (map[string][]byte, error) {
	payload := map[string][]byte{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		payload[rel] = data
		return nil
	})
	return payload, err
}

type qemuOptions struct {
	cdrom, target, serial, console, qmp string
	timeout                             time.Duration
}

func launchQEMU(o qemuOptions) (timedOut bool, err error) {
	ovmfCode := "/usr/share/OVMF/OVMF_CODE_4M.fd"
	ovmfVars := o.target + ".vars"
	if err := copyFile("/usr/share/OVMF/OVMF_VARS_4M.fd", ovmfVars); err != nil {
		return false, err
	}
	args := []string{
		"-machine", "q35,accel=kvm", "-cpu", "host",
		"-m", "4096", "-smp", "2",
		"-drive", "if=pflash,format=raw,readonly=on,file=" + ovmfCode,
		"-drive", "if=pflash,format=raw,file=" + ovmfVars,
		"-boot", "order=d,menu=off",
		"-cdrom", o.cdrom,
		"-drive", "file=" + o.target + ",format=qcow2,if=none,id=target",
		"-device", "virtio-blk-pci,drive=target,serial=" + o.serial,
		"-netdev", "user,id=net0,hostfwd=tcp::2222-:22",
		"-device", "virtio-net-pci,netdev=net0",
		"-nographic", "-serial", "file:" + o.console, "-monitor", "none", "-no-reboot",
	}
	if o.qmp != "" {
		args = append(args, "-qmp", "unix:"+o.qmp+",server=on,wait=off")
	}
	cmd := exec.Command("qemu-system-x86_64", args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return false, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return false, err
	case <-time.After(o.timeout):
		cmd.Process.Kill()
		<-done
		return true, nil
	}
}

func qemuImgCreate(path, size string) error {
	return exec.Command("qemu-img", "create", "-f", "qcow2", path, size).Run()
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// opensslPasswd returns an sha512-crypt hash for the default password "1"
// (matching the project's VM_USER_PASSWORD default).
func opensslPasswd() (string, error) {
	out, err := exec.Command("openssl", "passwd", "-6", "1").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
