package vmtest

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/diskfs/go-diskfs/filesystem"
)

// TestVerifierInstalledDisk runs against a real autoinstall output qcow2 if one
// is present (produced by the VM integration test). It exercises the full
// pure-Go verifier -> AssertInstall path against a real installed system.
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
	if err := AssertInstall(v, InstallChecks{}); err != nil {
		t.Errorf("AssertInstall: %v", err)
	}
}

// fakeDisk implements InstalledDisk from in-memory state so AssertInstall can
// be tested without a real qcow2.
type fakeDisk struct {
	parts []Partition
	ext   int
	files map[string]string // path -> content (or missing if absent)
}

func (f *fakeDisk) Partitions() ([]Partition, error) { return f.parts, nil }
func (f *fakeDisk) FindPartition(filesystem.Type) (int, error) {
	return f.ext, nil
}
func (f *fakeDisk) ReadFile(part int, path string) ([]byte, error) {
	c, ok := f.files[path]
	if !ok {
		return nil, fmt.Errorf("no such file")
	}
	return []byte(c), nil
}

func goodDisk() *fakeDisk {
	return &fakeDisk{
		parts: []Partition{{Index: 1, Type: "vfat"}, {Index: 2, Type: "ext4"}},
		ext:   2,
		files: map[string]string{
			"etc/hostname":        "ubuntu\n",
			"etc/locale.conf":     "LANG=en_US.UTF-8\n",
			"etc/passwd":          "root:x:0:0:root:/root:/bin/bash\ndailyuser:x:1000:1000::/home/dailyuser:/bin/bash\n",
			"var/lib/dpkg/status": "Package: openssh-server\nStatus: install ok installed\n",
		},
	}
}

// TestAssertInstall exercises the install assertions end-to-end with a fake
// disk: pass on a correct install, fail on each broken invariant.
func TestAssertInstall(t *testing.T) {
	if err := AssertInstall(goodDisk(), InstallChecks{}); err != nil {
		t.Fatalf("good disk should pass: %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(*fakeDisk)
		wantErr string
	}{
		{"wrong partition count", func(d *fakeDisk) { d.parts = d.parts[:1] }, "expected 2 partitions"},
		{"no ext4 root", func(d *fakeDisk) { d.ext = -1 }, "no ext4 partition"},
		{"wrong hostname", func(d *fakeDisk) { d.files["etc/hostname"] = "other\n" }, "etc/hostname"},
		{"missing locale.conf", func(d *fakeDisk) { delete(d.files, "etc/locale.conf") }, "etc/locale.conf"},
		{"missing user in passwd", func(d *fakeDisk) { d.files["etc/passwd"] = "root:x:0:0:\n" }, "etc/passwd"},
		{"dpkg status without ssh", func(d *fakeDisk) { d.files["var/lib/dpkg/status"] = "Package: git\n" }, "var/lib/dpkg/status"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := goodDisk()
			tc.mutate(d)
			err := AssertInstall(d, InstallChecks{})
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}
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
