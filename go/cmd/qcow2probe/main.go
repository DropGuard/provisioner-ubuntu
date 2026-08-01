// Command qcow2probe tests the fully-pure-Go path to read a qcow2 installed by
// the 26.04 autoinstall: github.com/lima-vm/go-qcow2reader exposes the qcow2 as
// an io.ReaderAt of the virtual disk; we adapt it into go-diskfs's backend so
// GPT + ext4 are read directly — no qemu-img, no temp files, no root.
package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"time"

	"github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/backend/file"
	"github.com/lima-vm/go-qcow2reader"
)

// readerAtFile adapts an io.ReaderAt (the qcow2 virtual disk) to fs.File so it
// can back go-diskfs via backend/file.New.
type readerAtFile struct {
	io.ReaderAt
	pos  int64
	size int64
}

func (f *readerAtFile) Read(p []byte) (int, error) {
	n, err := f.ReadAt(p, f.pos)
	f.pos += int64(n)
	return n, err
}
func (f *readerAtFile) Seek(off int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		f.pos = off
	case io.SeekCurrent:
		f.pos += off
	case io.SeekEnd:
		f.pos = f.size + off
	}
	return f.pos, nil
}
func (f *readerAtFile) Stat() (os.FileInfo, error) { return statInfo{f.size}, nil }
func (f *readerAtFile) Close() error               { return nil }

type statInfo struct{ size int64 }

func (statInfo) Name() string     { return "disk" }
func (s statInfo) Size() int64    { return s.size }
func (statInfo) Mode() fs.FileMode { return 0 }
func (statInfo) ModTime() time.Time { return time.Time{} }
func (statInfo) IsDir() bool      { return false }
func (statInfo) Sys() any         { return nil }

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: qcow2probe <disk.qcow2>")
		os.Exit(2)
	}
	qf, err := os.Open(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "open qcow2:", err)
		os.Exit(1)
	}
	defer qf.Close()
	img, err := qcow2reader.Open(qf)
	if err != nil {
		fmt.Fprintln(os.Stderr, "qcow2reader:", err)
		os.Exit(1)
	}
	defer img.Close()
	fmt.Printf("virtual disk size: %d bytes\n", img.Size())

	storage := file.New(&readerAtFile{ReaderAt: img, size: img.Size()}, true)
	disk, err := diskfs.OpenBackend(storage)
	if err != nil {
		fmt.Fprintln(os.Stderr, "diskfs.OpenBackend:", err)
		os.Exit(1)
	}
	pt, err := disk.GetPartitionTable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "partition table:", err)
		os.Exit(1)
	}
	for _, p := range pt.GetPartitions() {
		idx := p.GetIndex()
		fsys, err := disk.GetFilesystem(idx)
		if err != nil {
			fmt.Printf("  part %d: fs error: %v\n", idx, err)
			continue
		}
		fmt.Printf("  part %d: fs=%v start=%d size=%d\n", idx, fsys.Type(), p.GetStart(), p.GetSize())
		for _, f := range []string{"etc/passwd", "etc/locale.conf", "etc/hostname", "etc/localtime", "var/lib/dpkg/status"} {
			data, err := fsys.ReadFile(f)
			if err != nil {
				fmt.Printf("    read %s: %v\n", f, err)
				continue
			}
			head := data
			if len(head) > 60 {
				head = head[:60]
			}
			fmt.Printf("    %s: %d bytes | %q\n", f, len(data), head)
		}
	}
}
