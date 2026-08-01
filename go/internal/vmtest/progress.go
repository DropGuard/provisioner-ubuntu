package vmtest

import (
	"os"
	"syscall"
	"time"
)

// ProgressReport is a snapshot of the install's write progress.
type ProgressReport struct {
	WrittenGiB float64
	Status     string // "running" or "exited"
}

// WatchProgress monitors the target qcow2's on-disk size via os.Stat — no
// external binary, no lock contention. It calls tick every interval with the
// current file size, and if the disk stops growing for stallThreshold while
// the qemu process is still alive, it calls stall. Returns when qemu exits.
func WatchProgress(qcow2Path string, qemuPID int, interval, stallThreshold time.Duration,
	tick func(ProgressReport), stall func(ProgressReport)) {

	var last int64
	quiet := time.Duration(0)
	for {
		alive := processAlive(qemuPID)

		var actual int64
		if st, err := os.Stat(qcow2Path); err == nil {
			actual = st.Size()
		}

		status := "running"
		if !alive {
			status = "exited"
		}
		p := ProgressReport{WrittenGiB: float64(actual) / (1 << 30), Status: status}
		if tick != nil {
			tick(p)
		}

		if !alive {
			return
		}

		if actual != last {
			last = actual
			quiet = 0
		} else {
			quiet += interval
			if quiet >= stallThreshold {
				if stall != nil {
					stall(p)
				}
				quiet = 0
			}
		}
		time.Sleep(interval)
	}
}

// processAlive returns true if the process with the given PID exists.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
