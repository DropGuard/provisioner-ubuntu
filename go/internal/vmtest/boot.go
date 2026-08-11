package vmtest

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

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

	// ProgressFunc (if set) is called periodically during the install with the
	// target write progress; OnStall fires when the disk stops being written
	// for a while (silent hang).
	ProgressFunc func(ProgressReport)
	OnStall      func(ProgressReport)
}

// TestResult reports what happened.
type TestResult struct {
	TargetSize int64
	ConsoleLog string
	TimedOut   bool
}

const defaultDiskSerial = "50026B727200FDDC"

// PreparedISO is the output of PrepareTestISO: the bootable repacked ISO plus
// the tiny cidata seed disk carrying user-data/meta-data.
type PreparedISO struct {
	Repacked string // bootable autoinstall ISO (fixed grub.cfg, payload tree)
	Seed     string // cidata seed.iso (user-data + meta-data), 2nd drive
}

// PrepareTestISO builds the repacked ISO + seed.iso (no VM run). The seed is on
// its own cidata disk, so user-data edits only regenerate the few-KB seed.iso —
// never xorriso. It is also used to iterate on the repack in isolation.
func PrepareTestISO(opts TestOptions) (*PreparedISO, error) {
	if opts.SourceISO == "" {
		return nil, fmt.Errorf("SourceISO required")
	}
	if opts.WorkDir == "" {
		home, _ := os.UserHomeDir()
		opts.WorkDir = filepath.Join(home, ".cache", "vmtest-work")
	}
	if opts.DiskSerial == "" {
		opts.DiskSerial = defaultDiskSerial
	}
	// Password hash is build-time injected, like the bash `openssl passwd -6`.
	if opts.Config.Identity.PasswordHash == "" {
		hash, err := opensslPasswd()
		if err != nil {
			return nil, fmt.Errorf("generate password hash: %w", err)
		}
		opts.Config.Identity.PasswordHash = hash
	}

	tree := filepath.Join(opts.WorkDir, "iso-tree")
	repacked := filepath.Join(opts.WorkDir, "ubuntu-autoinstall.iso")
	seedISO := filepath.Join(opts.WorkDir, "seed.iso")

	var payload map[string][]byte
	if opts.PayloadDir == "" {
		payload = map[string][]byte{} // golden mode — no payload
	} else {
		var err error
		payload, err = seedPayloadFromDir(opts.PayloadDir)
		if err != nil {
			return nil, fmt.Errorf("seed payload: %w", err)
		}
	}
	userData, err := autoinstall.RenderUserData(opts.Config)
	if err != nil {
		return nil, err
	}
	metaData := "# cloud-init meta-data — intentionally empty.\n"

	if err := RepackISO(opts.SourceISO, tree, repacked, payload); err != nil {
		return nil, err
	}
	if err := WriteSeedISO(seedISO, userData, metaData); err != nil {
		return nil, err
	}
	return &PreparedISO{Repacked: repacked, Seed: seedISO}, nil
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
	prep, err := PrepareTestISO(opts)
	if err != nil {
		return nil, err
	}
	consoleLog := filepath.Join(opts.WorkDir, "console.log")
	os.Remove(consoleLog)
	os.Remove(opts.TargetDisk)
	if err := qemuImgCreate(opts.TargetDisk, "120G"); err != nil {
		return nil, err
	}

	done, pid, kill, err := launchQEMU(qemuOptions{
		cdrom: prep.Repacked, seed: prep.Seed,
		target: opts.TargetDisk, serial: opts.DiskSerial,
		console: consoleLog,
	})
	if err != nil {
		return nil, fmt.Errorf("qemu: %w", err)
	}

	timedOut := false
	if opts.ProgressFunc != nil || opts.OnStall != nil {
		// Watch write progress via qemu-img info and detect silent hangs in the background.
		go func() {
			WatchProgress(opts.TargetDisk, pid, 30*time.Second, 5*time.Minute, opts.ProgressFunc, opts.OnStall)
		}()
	}
	select {
	case <-done:
		// qemu exited on its own (install finished and rebooted).
	case <-time.After(opts.Timeout):
		kill()
		<-done
		timedOut = true
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
	cdrom, seed, target, serial, console string
}

// launchQEMU starts qemu and returns a channel delivering cmd.Wait()'s error
// when the VM exits, plus a kill func for timeout handling.
func launchQEMU(o qemuOptions) (waitCh <-chan error, pid int, kill func(), err error) {
	ovmfCode := "/usr/share/OVMF/OVMF_CODE_4M.fd"
	ovmfVars := o.target + ".vars"
	if err := copyFile("/usr/share/OVMF/OVMF_VARS_4M.fd", ovmfVars); err != nil {
		return nil, 0, nil, err
	}
	args := []string{
		"-machine", "q35,accel=kvm", "-cpu", "host",
		"-m", "6144", "-smp", "2",
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
	if o.seed != "" {
		// NoCloud config-drive: cloud-init in the live installer auto-detects the
		// cidata label and reads user-data/meta-data. Read-only media, no boot
		// record, and storage.match.serial ignores it — the installer never
		// touches this disk.
		args = append(args, "-drive", "file="+o.seed+",media=cdrom,readonly=on")
	}
	cmd := exec.Command("qemu-system-x86_64", args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, 0, nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	return done, cmd.Process.Pid, func() { cmd.Process.Kill() }, nil
}

// BootOptions for BootReady.
type BootOptions struct {
	Disk    string // the installed qcow2 (or an overlay) to boot
	WorkDir string
	Serial  string
	Seed    string        // optional payload ISO (non-cidata label) mounted by the guest placement script
	Timeout time.Duration // max wait for the guest's sshd to accept auth
}

// BootSession is a booted installed system still running: an authenticated SSH
// client for guest assertions, a Kill func to tear the VM down (idempotent),
// and a Done channel that receives if qemu exits on its own.
type BootSession struct {
	SSH  *ssh.Client
	Kill func()
	Done <-chan error
}

// BootReady boots an existing installed system (a golden overlay) and returns
// once the guest's sshd accepts password auth — i.e. the OS is up and Phase B
// can run assertions over SSH. It deliberately does NOT wait for qemu to exit:
// a booted desktop runs until killed (the install's poweroff only happens
// during the install), so the caller drives the session and calls Kill when
// done.
//
// SSH is the assertion channel rather than the QEMU guest agent: QEMU 10.x
// removed the guest-* commands from QMP (the agent is now only reachable via
// its virtio-serial chardev), while the golden already installs openssh-server
// with allow-pw: true.
func BootReady(o BootOptions) (*BootSession, error) {
	if o.Disk == "" {
		return nil, fmt.Errorf("Disk required")
	}
	if o.WorkDir == "" {
		home, _ := os.UserHomeDir()
		o.WorkDir = filepath.Join(home, ".cache", "vmtest-boot")
	}
	if o.Serial == "" {
		o.Serial = defaultDiskSerial
	}
	if o.Timeout == 0 {
		o.Timeout = 5 * time.Minute
	}
	ovmfCode := "/usr/share/OVMF/OVMF_CODE_4M.fd"
	ovmfVars := filepath.Join(o.WorkDir, "boot.vars")
	os.Remove(ovmfVars)
	if err := copyFile("/usr/share/OVMF/OVMF_VARS_4M.fd", ovmfVars); err != nil {
		return nil, err
	}
	console := filepath.Join(o.WorkDir, "boot-console.log")
	os.Remove(console)
	args := []string{
		"-machine", "q35,accel=kvm", "-cpu", "host",
		"-m", "6144", "-smp", "2",
		"-drive", "if=pflash,format=raw,readonly=on,file=" + ovmfCode,
		"-drive", "if=pflash,format=raw,file=" + ovmfVars,
		"-drive", "file=" + o.Disk + ",format=qcow2,if=none,id=target",
		"-device", "virtio-blk-pci,drive=target,serial=" + o.Serial,
		"-netdev", "user,id=net0,hostfwd=tcp::2222-:22",
		"-device", "virtio-net-pci,netdev=net0",
		"-nographic", "-serial", "file:" + console, "-monitor", "none", "-no-reboot",
	}
	if o.Seed != "" {
		// Phase C payload delivery: a non-bootable, non-cidata ISO the guest
		// mounts by label. cloud-init ignores it; the placement script uses it.
		args = append(args, "-drive", "file="+o.Seed+",media=cdrom,readonly=on")
	}

	cmd := exec.Command("qemu-system-x86_64", args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var killOnce sync.Once
	kill := func() {
		killOnce.Do(func() {
			cmd.Process.Kill()
			<-done
		})
	}

	// Wait for sshd (the guest OS booted) while watching for qemu exiting on
	// its own. select lets us fail fast if the VM dies before sshd comes up,
	// instead of burning the rest of the readiness timeout.
	type sshResult struct {
		client *ssh.Client
		err    error
	}
	sshCh := make(chan sshResult, 1)
	go func() {
		c, err := waitForSSH(2222, o.Timeout)
		sshCh <- sshResult{c, err}
	}()
	select {
	case r := <-sshCh:
		if r.err != nil {
			kill()
			return nil, r.err
		}
		return &BootSession{SSH: r.client, Kill: kill, Done: done}, nil
	case err := <-done:
		// qemu already exited — this branch consumed the only value on done, so
		// calling kill() (which re-reads done) would block forever.
		return nil, fmt.Errorf("qemu exited before sshd was up: %v", err)
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

// GoldenImagePath returns the cached golden image path for a given ISO hash and
// golden version (autoinstall.GoldenVersion). Bump the version to force a
// rebuild after changing Golden()'s base config — the golden is otherwise
// deliberately minimal and stable, so the cache only really turns over on an
// Ubuntu ISO version change.
func GoldenImagePath(isoHash, version string) string {
	dir, _ := os.UserCacheDir()
	return filepath.Join(dir, "vmtest-golden", isoHash+"-"+version+".qcow2")
}

// GoldenImageCached returns true if a cached golden image exists for the given
// ISO hash + golden version.
func GoldenImageCached(isoHash, version string) bool {
	_, err := os.Stat(GoldenImagePath(isoHash, version))
	return err == nil
}

// CacheGoldenImage copies src (a successfully installed qcow2) to the golden
// cache, creating parent directories as needed.
func CacheGoldenImage(isoHash, version, src string) error {
	dst := GoldenImagePath(isoHash, version)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("mkdir golden cache: %w", err)
	}
	return copyFile(src, dst)
}
