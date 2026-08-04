package provision

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (p *Provisioner) phaseDockerGroup(*Provisioner) error {
	if !p.Cfg.AddUserToDocker {
		return nil
	}
	_, err := p.Runner.Run("", "usermod", "-aG", "docker", p.Cfg.User)
	return err
}

// phaseGPUDrivers installs the recommended NVIDIA driver if an NVIDIA GPU is
// detected. AMD/Intel use in-kernel drivers (amdgpu/i915) and need nothing.
func (p *Provisioner) phaseGPUDrivers(*Provisioner) error {
	if !p.Cfg.GPUDrivers {
		return nil
	}
	out, err := p.Runner.Run("", "lspci")
	if err != nil {
		return err
	}
	if !strings.Contains(strings.ToLower(out), "nvidia") {
		return nil // no NVIDIA GPU — kernel drivers cover AMD/Intel
	}
	// ubuntu-drivers (in ubuntu-drivers-common) detects and installs the
	// recommended driver + kernel modules. Best-effort: failures warn.
	if _, err := p.Runner.Run("", "ubuntu-drivers", "autoinstall"); err != nil {
		return fmt.Errorf("ubuntu-drivers autoinstall: %w", err)
	}
	return nil
}

// phaseShellEnv writes the global PATH via /etc/environment.d and appends the
// brew/mise activation block to the user's .bashrc.
func (p *Provisioner) phaseShellEnv(*Provisioner) error {
	envd := p.Cfg.EnvDPath
	if _, err := os.Stat(envd); os.IsNotExist(err) {
		os.MkdirAll(filepath.Dir(envd), 0o755)
		if err := os.WriteFile(envd, []byte(p.Cfg.EnvDConf), 0o644); err != nil {
			return err
		}
	}
	bashrc := filepath.Join(p.Cfg.Home, ".bashrc")
	data, err := os.ReadFile(bashrc)

	if err != nil {
		return err
	}
	if strings.Contains(string(data), "brew shellenv") {
		return nil // already added
	}
	f, err := os.OpenFile(bashrc, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString("\n" + p.Cfg.BashrcAdditions); err != nil {
		return err
	}
	_, err = p.Runner.Run("", "chown", p.Cfg.User+":"+p.Cfg.User, bashrc)
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
		mnt, line := fstabEntry(uuid, serial, ptype)
		os.MkdirAll(mnt, 0o755)
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
