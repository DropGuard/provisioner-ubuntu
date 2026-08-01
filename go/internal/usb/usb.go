// Package usb builds a bootable autoinstall USB drive (port of
// prepare-usb.sh): GPT + FAT32 partition, the ISO tree, the nocloud seed
// (user-data + payload), and the grub.cfg carrying the autoinstall kernel line.
// Requires root (parted, mkfs.fat, mount).
package usb

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/filesystem"

	"provisioner-ubuntu/internal/autoinstall"
)

// Options for building the USB.
type Options struct {
	ISO     string            // original Ubuntu desktop ISO
	Disk    string            // block device, e.g. /dev/sdb (WILL BE WIPED)
	Serial  string            // target system disk serial (autoinstall match)
	User    string            // created user
	Payload map[string][]byte // files under nocloud/: "provision", "first-boot.service", "setup-fcitx5-chinese.sh", "config/...", "dotfiles/..."
}

// Build wipes and populates the USB drive.
func Build(opts Options) error {
	if opts.Serial == "" {
		opts.Serial = "50026B727200FDDC"
	}
	if opts.User == "" {
		opts.User = "dailyuser"
	}
	part := opts.Disk + "1"
	if err := run("parted", "-s", opts.Disk, "mklabel", "gpt"); err != nil {
		return err
	}
	if err := run("parted", "-s", opts.Disk, "mkpart", "primary", "fat32", "0%", "100%"); err != nil {
		return err
	}
	if err := run("parted", "-s", opts.Disk, "set", "1", "boot", "on"); err != nil {
		return err
	}
	if err := run("mkfs.fat", "-F", "32", part); err != nil {
		return err
	}
	mnt, err := os.MkdirTemp("", "usb-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(mnt)
	if err := run("mount", part, mnt); err != nil {
		return err
	}
	defer run("umount", mnt)

	if err := extractISO(opts.ISO, mnt); err != nil {
		return err
	}
	return writeSeed(mnt, opts)
}

// writeSeed plants user-data + meta-data + payload + grub.cfg.
func writeSeed(root string, opts Options) error {
	hash, err := passwdHash()
	if err != nil {
		return err
	}
	cfg := autoinstall.Default()
	cfg.Identity.Username = opts.User
	cfg.Identity.PasswordHash = hash
	cfg.DiskSerial = opts.Serial
	ud, err := autoinstall.RenderUserData(cfg)
	if err != nil {
		return err
	}
	seedDir := filepath.Join(root, "nocloud")
	os.MkdirAll(seedDir+"/config", 0o755)
	os.MkdirAll(seedDir+"/dotfiles", 0o755)
	os.WriteFile(seedDir+"/user-data", []byte(ud), 0o644)
	os.WriteFile(seedDir+"/autoinstall.yaml", []byte(ud), 0o644)
	os.WriteFile(seedDir+"/meta-data", []byte("# cloud-init meta-data — intentionally empty.\n"), 0o644)
	for name, data := range opts.Payload {
		dst := filepath.Join(seedDir, name)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0o755); err != nil {
			return err
		}
	}
	// grub.cfg with the autoinstall kernel line (escaped semicolon).
	grub := autoinstall.RenderGrubCfg("/cdrom/nocloud/", false)
	grubPath := filepath.Join(root, "boot", "grub", "grub.cfg")
	if _, err := os.Stat(grubPath); err != nil {
		grubPath = filepath.Join(root, "EFI", "BOOT", "grub.cfg")
	}
	return os.WriteFile(grubPath, []byte(grub), 0o644)
}

func extractISO(iso, dest string) error {
	d, err := diskfs.Open(iso)
	if err != nil {
		return err
	}
	defer d.Close()
	fsys, err := d.GetFilesystem(0)
	if err != nil {
		return err
	}
	var walk func(dir string) error
	walk = func(dir string) error {
		ents, err := fsys.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, e := range ents {
			src := filepath.Join(dir, e.Name())
			dst := filepath.Join(dest, dir, e.Name())
			if e.IsDir() {
				os.MkdirAll(dst, 0o755)
				if err := walk(src); err != nil {
					return err
				}
				continue
			}
			if err := copyFromFS(fsys, src, dst); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(".")
}

func copyFromFS(fsys filesystem.FileSystem, src, dst string) error {
	f, err := fsys.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, f)
	return err
}

func passwdHash() (string, error) {
	out, err := exec.Command("openssl", "passwd", "-6", "1").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
