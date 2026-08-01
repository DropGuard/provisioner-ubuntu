package provision

import (
	"bytes"
	"fmt"
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
	if user != "" {
		full = append([]string{"sudo", "-u", user, "--"}, full...)
	}
	cmd := exec.Command(full[0], full[1:]...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	if err != nil {
		return buf.String(), fmt.Errorf("%s: %w", name, err)
	}
	return buf.String(), nil
}
