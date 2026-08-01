package vmtest

import (
	"fmt"
	"time"
)

// ProgressReport is a snapshot of the install's write progress.
type ProgressReport struct {
	WrittenGiB float64
	Status     string // qemu runstate, e.g. "running"
}

// WatchProgress monitors a running install via QMP query-blockstats. It calls
// tick every interval with current progress, and if the target disk stops being
// written for stallThreshold while the VM is running (a silent hang — the disk
// wrote ~14 GiB then stopped, VM still "running"), it calls stall and returns.
// Returns when qemu exits (QMP socket closes) or a stall is detected.
func WatchProgress(qmpPath string, interval, stallThreshold time.Duration,
	tick func(ProgressReport), stall func(ProgressReport)) error {

	q, err := ConnectQMP(qmpPath)
	if err != nil {
		return fmt.Errorf("connect qmp: %w", err)
	}
	defer q.Close()

	var last int64
	quiet := time.Duration(0)
	for {
		status, _ := q.Status()
		w, err := q.TargetBytesWritten("target")
		if err != nil {
			// QMP socket closed => qemu exited. Not an error to the caller.
			return nil
		}
		p := ProgressReport{WrittenGiB: float64(w) / (1 << 30), Status: status}
		if tick != nil {
			tick(p)
		}
		if w != last {
			last = w
			quiet = 0
		} else if status == "running" {
			quiet += interval
			if quiet >= stallThreshold {
				if stall != nil {
					stall(p)
				}
				return nil
			}
		}
		time.Sleep(interval)
	}
}
