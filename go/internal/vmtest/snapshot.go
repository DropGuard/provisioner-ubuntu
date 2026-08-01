package vmtest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// CreateOverlay creates a qcow2 external snapshot (overlay) over base. Writes
// go to the overlay; reads fall through to base. Discarding the overlay file
// rolls back to the base state.
func CreateOverlay(base, overlay string) error {
	return exec.Command("qemu-img", "create", "-f", "qcow2", "-b", base, "-F", "qcow2", overlay).Run()
}

// ValidateProvisionOptions drives the snapshot-based first-boot provision
// validation: for each phase, boot an overlay of the clean install, run the
// phase, assert via guest-exec, and roll back on failure.
type ValidateProvisionOptions struct {
	BaseDisk  string // clean installed system (the install output)
	WorkDir   string
	Serial    string
	Timeout   int    // per-phase boot timeout (seconds)
	PhaseUser string // "dailyuser"
}

// SnapshotOverlayPath returns the overlay path for a phase.
func SnapshotOverlayPath(workDir, phase string) string {
	return filepath.Join(workDir, "phase-"+phase+".qcow2")
}

// ensureOverlay recreates overlay over base (rollback = delete + recreate).
func ensureOverlay(base, overlay string) error {
	os.Remove(overlay)
	if err := CreateOverlay(base, overlay); err != nil {
		return fmt.Errorf("create overlay: %w", err)
	}
	return nil
}
