package vmtest

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/diskfs/go-diskfs/filesystem"
)

// TestVerifierInstalledDisk runs against a real autoinstall output qcow2 if one
// is present (produced by the VM integration test). It verifies the pure-Go
// verifier reads the installed system the way we need it to.
func TestVerifierInstalledDisk(t *testing.T) {
	const disk = "/home/dropguard/vmtest-repack/target.qcow2"
	if _, err := os.Stat(disk); err != nil {
		t.Skipf("installed disk %s not present (run the VM test first)", disk)
	}
	v, err := OpenVerifier(disk)
	if err != nil {
		t.Fatalf("OpenVerifier: %v", err)
	}
	defer v.Close()

	parts, err := v.Partitions()
	if err != nil {
		t.Fatalf("Partitions: %v", err)
	}
	if len(parts) != 2 {
		t.Errorf("expected 2 partitions (ESP + ext4 root), got %d", len(parts))
	}
	ext, err := v.FindPartition(filesystem.TypeExt4)
	if err != nil {
		t.Fatalf("FindPartition: %v", err)
	}
	if ext < 0 {
		t.Fatal("no ext4 partition found")
	}

	for _, tc := range []struct{ path, want string }{
		{"etc/hostname", "ubuntu"},
		{"etc/locale.conf", "LANG="},
		{"etc/passwd", "dailyuser"},
		{"var/lib/dpkg/status", "Package: openssh-server"},
	} {
		b, err := v.ReadFile(ext, tc.path)
		if err != nil {
			t.Errorf("ReadFile(%s): %v", tc.path, err)
			continue
		}
		if !strings.Contains(string(b), tc.want) {
			t.Errorf("%s does not contain %q (got %q)", tc.path, tc.want, b)
		}
	}
}

// TestReaderAtFileAdapter exercises the io.ReaderAt -> fs.File adapter that
// backs go-diskfs (Read/Seek/Stat semantics), independent of any disk.
func TestReaderAtFileAdapter(t *testing.T) {
	src := []byte("0123456789")
	raf := &readerAtFile{ReaderAt: bytes.NewReader(src), size: int64(len(src))}

	buf := make([]byte, 4)
	n, err := raf.Read(buf)
	if err != nil || n != 4 || string(buf) != "0123" {
		t.Fatalf("initial Read: n=%d err=%v buf=%q", n, err, buf)
	}
	if pos, err := raf.Seek(5, io.SeekStart); err != nil || pos != 5 {
		t.Fatalf("SeekStart: pos=%d err=%v", pos, err)
	}
	n, _ = raf.Read(buf)
	if string(buf[:n]) != "5678" {
		t.Fatalf("read after SeekStart: %q", buf[:n])
	}
	if _, err := raf.Seek(-2, io.SeekCurrent); err != nil {
		t.Fatalf("SeekCurrent: %v", err)
	}
	if pos, _ := raf.Seek(0, io.SeekCurrent); pos != 7 {
		t.Fatalf("SeekCurrent(0) pos=%d want 7", pos)
	}
	if pos, _ := raf.Seek(0, io.SeekEnd); pos != 10 {
		t.Fatalf("SeekEnd pos=%d want 10", pos)
	}
	if fi, err := raf.Stat(); err != nil || fi.Size() != 10 {
		t.Fatalf("Stat: %v size=%d", err, fi.Size())
	}
}
