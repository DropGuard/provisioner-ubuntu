package provision

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// filepathWalkCopy recursively copies src to dst.
func filepathWalkCopy(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// phaseCCSwitch installs the cc-switch .deb if a path is configured. Optional.
func (p *Provisioner) phaseCCSwitch(*Provisioner) error {
	if p.Cfg.CCSwitchDeb == "" {
		return nil
	}
	if _, err := os.Stat(p.Cfg.CCSwitchDeb); err != nil {
		return fmt.Errorf("cc-switch deb not found: %s", p.Cfg.CCSwitchDeb)
	}
	_, err := p.Runner.Run("", "apt-get", "install", "-y", p.Cfg.CCSwitchDeb)
	return err
}

// phaseMountDataDisks mounts the user's existing data disks under /mnt,
// non-destructively, by UUID. Ported from provision.sh phase_43.
func (p *Provisioner) phaseMountDataDisks(*Provisioner) error {
	out, err := p.Runner.Run("", "lsblk", "-d", "-n", "-o", "NAME,SERIAL,FSTYPE")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name, serial := fields[0], fields[1]
		fstype := ""
		if len(fields) >= 3 {
			fstype = fields[2]
		}
		if serial == "" {
			continue
		}
		if _, excluded := p.Cfg.ExcludedSerials[serial]; excluded {
			continue
		}
		part := "/dev/" + name
		if fstype == "" {
			// Find the first partition with a filesystem on this disk.
			po, err := p.Runner.Run("", "lsblk", "-r", "-n", "-o", "PATH,FSTYPE", part)
			if err != nil {
				continue
			}
			found := ""
			for _, pl := range strings.Split(po, "\n") {
				pf := strings.Fields(pl)
				if len(pf) == 2 && pf[1] != "" {
					found = pf[0]
					break
				}
			}
			if found == "" {
				continue // no filesystem — skip, don't format
			}
			part = found
		}
		// Get UUID + type.
		bo, err := p.Runner.Run("", "blkid", "-o", "export", part)
		if err != nil {
			continue
		}
		uuid, ptype := "", ""
		for _, l := range strings.Split(bo, "\n") {
			if v, ok := strings.CutPrefix(l, "UUID="); ok {
				uuid = v
			} else if v, ok := strings.CutPrefix(l, "TYPE="); ok {
				ptype = v
			}
		}
		if uuid == "" {
			continue
		}
		// Idempotent: already in fstab?
		fstab, err := os.ReadFile("/etc/fstab")
		if err != nil {
			return err
		}
		if strings.Contains(string(fstab), "UUID="+uuid) {
			continue
		}
		label := sanitize(serial)
		if len(label) > 8 {
			label = label[len(label)-8:]
		}
		mnt := "/mnt/data-" + label
		os.MkdirAll(mnt, 0o755)
		opts := "defaults,nofail,uid=1000,gid=1000,x-systemd.automount"
		if ptype == "ntfs" {
			opts = "defaults,nofail,uid=1000,gid=1000,umask=022,windows_names,x-systemd.automount"
		}
		line := fmt.Sprintf("UUID=%s %s %s %s 0 2\n", uuid, mnt, ptype, opts)
		f, err := os.OpenFile("/etc/fstab", os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		_, werr := f.WriteString(line)
		f.Close()
		if werr != nil {
			return werr
		}
	}
	return nil
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}
