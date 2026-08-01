// Package vmtest validates the autoinstall configuration end-to-end in a KVM
// VM: it builds a repacked ISO carrying the autoinstall seed, boots it the way
// a real USB would, and inspects the installed disk — all verifiable without
// root.
package vmtest

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"time"

	"github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/backend/file"
	"github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/filesystem"
	"github.com/lima-vm/go-qcow2reader"
	"github.com/lima-vm/go-qcow2reader/image"
)

// readerAtFile adapts the qcow2 virtual-disk io.ReaderAt (from
// go-qcow2reader) to fs.File so go-diskfs can use it as a backend via
// backend/file.New. This is what lets us read the installed qcow2 directly in
// pure Go — no qemu-img convert, no root, no temp files.
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

func (f *readerAtFile) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		f.pos = offset
	case io.SeekCurrent:
		f.pos += offset
	case io.SeekEnd:
		f.pos = f.size + offset
	}
	return f.pos, nil
}

func (f *readerAtFile) Stat() (os.FileInfo, error) { return statInfo{f.size}, nil }
func (f *readerAtFile) Close() error               { return nil }

type statInfo struct{ size int64 }

func (statInfo) Name() string              { return "disk" }
func (s statInfo) Size() int64             { return s.size }
func (statInfo) Mode() fs.FileMode         { return 0 }
func (statInfo) ModTime() time.Time        { return time.Time{} }
func (statInfo) IsDir() bool               { return false }
func (statInfo) Sys() any                  { return nil }

// Partition describes one partition of the installed disk.
type Partition struct {
	Index int   // 1-based, as go-diskfs expects
	Type  string // filesystem type name, e.g. "ext4"
	Start int64 // byte offset on the virtual disk
	Size  int64
}

// Verifier reads an installed autoinstall disk (qcow2) without root.
type Verifier struct {
	disk *disk.Disk
	img  image.Image
}

// OpenVerifier opens a qcow2 disk image for reading. Call Close when done.
func OpenVerifier(qcow2Path string) (*Verifier, error) {
	f, err := os.Open(qcow2Path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", qcow2Path, err)
	}
	img, err := qcow2reader.Open(f)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("qcow2reader %s: %w", qcow2Path, err)
	}
	storage := file.New(&readerAtFile{ReaderAt: img, size: img.Size()}, true)
	disk, err := diskfs.OpenBackend(storage)
	if err != nil {
		img.Close()
		return nil, fmt.Errorf("open backend: %w", err)
	}
	return &Verifier{disk: disk, img: img}, nil
}

// Close releases the underlying image.
func (v *Verifier) Close() error { return v.img.Close() }

// Partitions returns the disk's partition table.
func (v *Verifier) Partitions() ([]Partition, error) {
	pt, err := v.disk.GetPartitionTable()
	if err != nil {
		return nil, fmt.Errorf("partition table: %w", err)
	}
	parts := pt.GetPartitions()
	out := make([]Partition, 0, len(parts))
	for _, p := range parts {
		out = append(out, Partition{
			Index: p.GetIndex(),
			Start: p.GetStart(),
			Size:  p.GetSize(),
		})
	}
	return out, nil
}

// FindPartition returns the index of the first partition whose filesystem type
// matches typ (e.g. filesystem.TypeExt4), or -1 if none matches.
func (v *Verifier) FindPartition(typ filesystem.Type) (int, error) {
	parts, err := v.Partitions()
	if err != nil {
		return -1, err
	}
	for _, p := range parts {
		fsys, err := v.disk.GetFilesystem(p.Index)
		if err != nil {
			continue
		}
		if fsys.Type() == typ {
			return p.Index, nil
		}
	}
	return -1, nil
}

// ReadFile reads a file from the given partition. path is io/fs-relative
// (e.g. "etc/passwd", NOT "/etc/passwd" — a leading slash is rejected).
func (v *Verifier) ReadFile(partIndex int, path string) ([]byte, error) {
	fsys, err := v.disk.GetFilesystem(partIndex)
	if err != nil {
		return nil, fmt.Errorf("filesystem on part %d: %w", partIndex, err)
	}
	b, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil, fmt.Errorf("read %q on part %d: %w", path, partIndex, err)
	}
	return b, nil
}
