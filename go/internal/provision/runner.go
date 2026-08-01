package provision

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
)

// Runner executes commands. The production implementation shells out via
// os/exec; tests substitute a fake to assert which commands a phase would run
// (idempotency + argument correctness) without touching the system.
type Runner interface {
	// Run executes name with args, optionally as user ("" = current user),
	// and returns combined stdout+stderr plus any error.
	Run(user string, name string, args ...string) (string, error)
}

// ExecRunner is the production Runner backed by os/exec.
type ExecRunner struct{}

func (ExecRunner) Run(user, name string, args ...string) (string, error) {
	full := []string{name}
	full = append(full, args...)
	// Skip sudo inside a provision-user re-exec: that process is ALREADY the
	// target user (marked by PROVISION_USER_REEXEC), so a second
	// `sudo -u dailyuser` would prompt for a password and fail non-interactively.
	if needsSudo(user, os.Getenv(ProvisionUserReexecEnv) != "") {
		full = append([]string{"sudo", "-u", user, "--"}, full...)
	}
	cmd := exec.Command(full[0], full[1:]...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	if err != nil {
		// Carry the command's own output in the error so a phase WARN shows WHY
		// it failed ("Unable to locate package", "dpkg was interrupted", ...),
		// not just the exit status. Truncated to the tail where errors land.
		return buf.String(), fmt.Errorf("%s: %w\n%s", name, err, tail(buf.String(), 2048))
	}
	return buf.String(), nil
}

// tail returns the last n bytes of s, prefixed with a truncation marker when s
// was longer.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "...(truncated)...\n" + s[len(s)-n:]
}

// ProvisionUserReexecEnv is set by the provision-user subcommand (the re-exec
// half that runs a user-owned phase as the target user); Runner sees it and
// skips the redundant sudo. An env-var marker beats an os/user lookup — no
// /etc/passwd dependency in the static binary.
const ProvisionUserReexecEnv = "PROVISION_USER_REEXEC"

// needsSudo reports whether a command for user must run via sudo: only when a
// target user is given AND this process is not the provision-user re-exec
// (which already runs as the target user). Pure — unit-tested.
func needsSudo(user string, reexec bool) bool {
	return user != "" && !reexec
}
