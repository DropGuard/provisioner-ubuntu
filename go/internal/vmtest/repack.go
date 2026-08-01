package vmtest

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/filesystem"

	"provisioner-ubuntu/internal/autoinstall"
)

// xorrisoParams holds the boot-structure parameters of the original Ubuntu
// desktop ISO, derived from `xorriso -indev ISO -report_el_torito as_mkisofs`.
// Reusing the original's own parameters makes the repack work across point
// releases without hardcoding offsets.
type xorrisoParams struct {
	vol            string
	modDate        string
	grub2Mbr       string // e.g. --interval:local_fs:0s-15s:zero_mbrpt,zero_gpt:'PATH'
	appendUUID     string
	appendInterval string // e.g. --interval:local_fs:12721412d-12731707d::'PATH'
	eltoritoImg    string
	eltoritoBLS    string
	efiBLS         string
	isoMBR         string
}

var (
	reVol      = regexp.MustCompile(`^\-V '([^']+)'`)
	reMod      = regexp.MustCompile(`^--modification-date='([^']+)'`)
	reGrub2    = regexp.MustCompile(`^--grub2-mbr (--interval:local_fs:\S+)`)
	reAppend   = regexp.MustCompile(`^-append_partition 2 ([0-9a-f]+) (--interval:local_fs:\S+)`)
	reEltorito = regexp.MustCompile(`^\-b '([^']+)'`)
	reBLS      = regexp.MustCompile(`^\-boot-load-size (\d+)`)
	reISOMBR   = regexp.MustCompile(`^\-iso_mbr_part_type ([0-9a-f]+)`)
)

// deriveXorrisoParams reads the boot parameters from the original ISO.
func deriveXorrisoParams(isoPath string) (xorrisoParams, error) {
	out, err := exec.Command("xorriso", "-indev", isoPath, "-report_el_torito", "as_mkisofs").Output()
	if err != nil {
		return xorrisoParams{}, fmt.Errorf("xorriso report: %w", err)
	}
	return parseXorrisoReport(string(out))
}

// parseXorrisoReport parses the output of `xorriso -indev ISO
// -report_el_torito as_mkisofs`. Pure function (unit-testable without an ISO);
// deriveXorrisoParams shells out and delegates here.
func parseXorrisoReport(report string) (xorrisoParams, error) {
	var p xorrisoParams
	bls := []string{}
	for _, line := range strings.Split(report, "\n") {
		line = strings.TrimRight(line, "\r")
		if m := reVol.FindStringSubmatch(line); m != nil {
			p.vol = m[1]
		} else if m := reMod.FindStringSubmatch(line); m != nil {
			p.modDate = m[1]
		} else if m := reGrub2.FindStringSubmatch(line); m != nil {
			p.grub2Mbr = stripIntervalQuotes(m[1])
		} else if m := reAppend.FindStringSubmatch(line); m != nil {
			p.appendUUID = m[1]
			p.appendInterval = stripIntervalQuotes(m[2])
		} else if m := reEltorito.FindStringSubmatch(line); m != nil {
			p.eltoritoImg = m[1]
		} else if m := reBLS.FindStringSubmatch(line); m != nil {
			bls = append(bls, m[1])
		} else if m := reISOMBR.FindStringSubmatch(line); m != nil {
			p.isoMBR = m[1]
		}
	}
	if len(bls) >= 2 {
		p.eltoritoBLS, p.efiBLS = bls[0], bls[1]
	} else if len(bls) == 1 {
		p.eltoritoBLS = bls[0]
	}
	if p.vol == "" || p.grub2Mbr == "" || p.appendUUID == "" || p.efiBLS == "" {
		return xorrisoParams{}, fmt.Errorf("could not parse boot parameters")
	}
	return p, nil
}

// stripIntervalQuotes removes xorriso's display quoting ('...') from an
// interval argument. xorriso prints interval paths quoted; passing an absolute
// path back still-quoted fails to open ("No such file or directory").
func stripIntervalQuotes(s string) string {
	return strings.ReplaceAll(s, "'", "")
}

// SeedConfig is the nocloud seed planted on the repacked ISO.
type SeedConfig struct {
	UserData string            // rendered user-data (autoinstall.RenderUserData output)
	MetaData string            // the (possibly empty) meta-data file
	Payload  map[string][]byte // extra files copied under nocloud/, e.g. "provision.sh"
}

// RepackISO builds a bootable autoinstall ISO from the original Ubuntu desktop
// ISO: it extracts the tree, writes grub.cfg + the nocloud seed, and repacks
// with xorriso using the original's boot parameters.
//
// The extraction uses mount + rsync (needs root, matching the old test-vm.sh);
// the xorriso repack and everything after is root-free.
func RepackISO(srcISO, tree, outISO string, seed SeedConfig) error {
	if err := extractISO(srcISO, tree); err != nil {
		return fmt.Errorf("extract ISO: %w", err)
	}
	if err := writeSeed(tree, seed); err != nil {
		return fmt.Errorf("write seed: %w", err)
	}
	p, err := deriveXorrisoParams(srcISO)
	if err != nil {
		return err
	}
	if err := repackWithXorriso(p, tree, outISO); err != nil {
		return fmt.Errorf("repack: %w", err)
	}
	return nil
}

// extractISO copies the ISO's ISO9660 tree to tree using go-diskfs in pure Go —
// no mount, no root. Large files (the squashfs) are streamed via Open+io.Copy.
func extractISO(iso, tree string) error {
	d, err := diskfs.Open(iso)
	if err != nil {
		return fmt.Errorf("open ISO: %w", err)
	}
	defer d.Close()
	fsys, err := d.GetFilesystem(0)
	if err != nil {
		return fmt.Errorf("ISO filesystem: %w", err)
	}
	var walk func(dir, dest string) error
	walk = func(dir, dest string) error {
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return err
		}
		ents, err := fsys.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, e := range ents {
			src := path.Join(dir, e.Name())
			dst := filepath.Join(dest, e.Name())
			if e.IsDir() {
				if err := walk(src, dst); err != nil {
					return err
				}
				continue
			}
			if err := copyFileFromFS(fsys, src, dst); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(".", tree)
}

func copyFileFromFS(fsys filesystem.FileSystem, src, dst string) error {
	in, err := fsys.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// writeSeed writes grub.cfg and the nocloud seed into the extracted tree.
func writeSeed(tree string, seed SeedConfig) error {
	grub := autoinstall.RenderGrubCfg("/cdrom/nocloud/", true)
	if err := os.WriteFile(tree+"/boot/grub/grub.cfg", []byte(grub), 0o644); err != nil {
		return err
	}
	seedDir := tree + "/nocloud"
	if err := os.MkdirAll(seedDir+"/config", 0o755); err != nil {
		return err
	}
	os.MkdirAll(seedDir+"/dotfiles", 0o755)
	if err := os.WriteFile(seedDir+"/user-data", []byte(seed.UserData), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(seedDir+"/autoinstall.yaml", []byte(seed.UserData), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(seedDir+"/meta-data", []byte(seed.MetaData), 0o644); err != nil {
		return err
	}
	for name, data := range seed.Payload {
		if err := os.WriteFile(seedDir+"/"+name, data, 0o755); err != nil {
			return err
		}
	}
	// Drop a stale boot catalog — xorriso regenerates it via -c.
	os.Remove(tree + "/boot.catalog")
	return nil
}

// xorrisoArgs builds the xorriso argument slice for the repack. The interval
// args are passed verbatim (single-quoted path, as xorriso wants).
func xorrisoArgs(p xorrisoParams, tree, outISO string) []string {
	return []string{
		"-as", "mkisofs", "-r",
		"-V", p.vol,
		"--modification-date=" + p.modDate,
		"--grub2-mbr", p.grub2Mbr,
		"--protective-msdos-label",
		"-partition_cyl_align", "off",
		"-partition_offset", "16",
		"--mbr-force-bootable",
		"-append_partition", "2", p.appendUUID, p.appendInterval,
		"-appended_part_as_gpt",
		"-iso_mbr_part_type", p.isoMBR,
		"-c", "/boot.catalog",
		"-b", p.eltoritoImg,
		"-no-emul-boot", "-boot-load-size", p.eltoritoBLS, "-boot-info-table",
		"--grub2-boot-info",
		"-eltorito-alt-boot",
		"-e", "--interval:appended_partition_2:all::",
		"-no-emul-boot", "-boot-load-size", p.efiBLS,
		"-o", outISO, tree,
	}
}

// repackWithXorriso runs the repack using the original ISO's boot parameters.
func repackWithXorriso(p xorrisoParams, tree, outISO string) error {
	args := xorrisoArgs(p, tree, outISO)
	cmd := exec.Command("xorriso", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("xorriso: %w\n%s", err, stderr.String())
	}
	return nil
}
