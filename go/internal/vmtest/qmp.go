package vmtest

import (
	"encoding/json"
	"fmt"
	"net"
	"sync"
)

// QMP is a minimal QEMU Machine Protocol client (QMP over a unix socket).
// It is used by the harness to query VM state directly from the host — e.g.
// query-blockstats (bytes written to the target disk) and query-status —
// instead of polling the qcow2 file size.
type QMP struct {
	conn   net.Conn
	dec    *json.Decoder
	mu     sync.Mutex
	nextID int64
}

// ConnectQMP dials the QMP unix socket and completes capability negotiation.
func ConnectQMP(path string) (*QMP, error) {
	conn, err := net.Dial("unix", path)
	if err != nil {
		return nil, fmt.Errorf("qmp dial: %w", err)
	}
	q := &QMP{conn: conn, dec: json.NewDecoder(conn)}
	// QMP greeting: {"QMP": {...}}
	var greeting map[string]any
	if err := q.dec.Decode(&greeting); err != nil {
		conn.Close()
		return nil, fmt.Errorf("qmp greeting: %w", err)
	}
	// Enable capabilities so commands are accepted.
	if _, err := q.command("qmp_capabilities", nil); err != nil {
		conn.Close()
		return nil, fmt.Errorf("qmp capabilities: %w", err)
	}
	return q, nil
}

// Close closes the QMP connection.
func (q *QMP) Close() error { return q.conn.Close() }

// command sends a QMP command and waits for its response (matched by id).
func (q *QMP) command(method string, args map[string]any) (map[string]any, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.nextID++
	id := q.nextID
	msg := map[string]any{"execute": method, "id": id}
	if args != nil {
		msg["arguments"] = args
	}
	if err := json.NewEncoder(q.conn).Encode(msg); err != nil {
		return nil, err
	}
	for {
		var resp map[string]any
		if err := q.dec.Decode(&resp); err != nil {
			return nil, err
		}
		if rid, ok := resp["id"].(float64); ok && int64(rid) == id {
			if resp["error"] != nil {
				return nil, fmt.Errorf("qmp %s: %v", method, resp["error"])
			}
			if data, ok := resp["return"].(map[string]any); ok {
				return data, nil
			}
			return resp, nil
		}
		// otherwise: an event or another command's response — ignore
	}
}

// Status returns the VM runstate, e.g. "running" or "paused".
func (q *QMP) Status() (string, error) {
	resp, err := q.command("query-status", nil)
	if err != nil {
		return "", err
	}
	s, _ := resp["status"].(string)
	return s, nil
}

// TargetBytesWritten returns the total bytes written to the disk with the
// given -device id (e.g. "target"), from query-blockstats.
func (q *QMP) TargetBytesWritten(deviceID string) (int64, error) {
	resp, err := q.command("query-blockstats", nil)
	if err != nil {
		return 0, err
	}
	devs, ok := resp["return"].([]any)
	if !ok {
		return 0, fmt.Errorf("query-blockstats: unexpected shape")
	}
	for _, d := range devs {
		dev, ok := d.(map[string]any)
		if !ok {
			continue
		}
		if dev["device"] == deviceID {
			stats, _ := dev["stats"].(map[string]any)
			if wb, ok := stats["wr_bytes"].(float64); ok {
				return int64(wb), nil
			}
		}
	}
	return 0, fmt.Errorf("query-blockstats: device %q not found", deviceID)
}
