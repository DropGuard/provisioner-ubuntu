package vmtest

import (
	"os/exec"
	"path/filepath"
)

// CreateOverlay creates a qcow2 external snapshot (overlay) over base. Writes
// go to the overlay; reads fall through to base. Discarding the overlay file
// rolls back to the base state.
func CreateOverlay(base, overlay string) error {
	return exec.Command("qemu-img", "create", "-f", "qcow2", "-b", base, "-F", "qcow2", overlay).Run()
}

// SnapshotOverlayPath returns the overlay path for a phase.
func SnapshotOverlayPath(workDir, phase string) string {
	return filepath.Join(workDir, "phase-"+phase+".qcow2")
}
