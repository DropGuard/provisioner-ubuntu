package vmtest

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/crypto/ssh"
)

// Phase B runs assertions in the booted guest over SSH instead of the QEMU
// guest agent: QEMU 10.x removed the guest-* commands from QMP (the agent is
// now reachable only via its virtio-serial chardev), while the golden image
// already installs openssh-server with allow-pw: true. SSH gives structured
// stdout/stderr/exit-code assertions with no hand-rolled protocol.

// The golden image's autoinstall identity — the build-time `openssl passwd -6 1`
// in PrepareTestISO, exposed over the forwarded port by ssh: allow-pw: true.
const (
	testSSHUser     = "dailyuser"
	testSSHPassword = "1"
)

// sshConfig returns the client config for the throwaway test VM: password auth
// with the golden's known password, host key verification skipped.
func sshConfig() *ssh.ClientConfig {
	return &ssh.ClientConfig{
		User:            testSSHUser,
		Auth:            []ssh.AuthMethod{ssh.Password(testSSHPassword)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}
}

// waitForSSH waits until the guest's sshd accepts password auth on the
// forwarded port (127.0.0.1:port), then returns an authenticated client. The
// wait is event-driven: context bounds the total time, a ticker paces the
// retries, and select delivers whichever fires first.
func waitForSSH(port int, timeout time.Duration) (*ssh.Client, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	cfg := sshConfig()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	var lastErr error
	for {
		c, err := ssh.Dial("tcp", addr, cfg)
		if err == nil {
			return c, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("ssh %s not ready within %v (last: %w)", addr, timeout, lastErr)
		case <-ticker.C:
		}
	}
}

// SshRun runs cmd inside the guest via SSH and returns combined stdout+stderr.
// The returned error is non-nil if the remote command exited non-zero or the
// deadline elapsed.
func SshRun(client *ssh.Client, cmd string, timeout time.Duration) (string, error) {
	sess, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("ssh session: %w", err)
	}
	defer sess.Close()
	type result struct {
		out []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		out, err := sess.CombinedOutput(cmd)
		ch <- result{out, err}
	}()
	select {
	case r := <-ch:
		return string(r.out), r.err
	case <-time.After(timeout):
		return "", fmt.Errorf("ssh command %q timed out after %v", cmd, timeout)
	}
}
