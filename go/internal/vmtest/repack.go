package vmtest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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

// RepackISO builds a bootable autoinstall ISO from the original Ubuntu desktop
// ISO. The repacked ISO carries ONLY the fixed grub.cfg (ds=nocloud
// auto-detect — the seed lives on a separate cidata disk, see WriteSeedISO) and
// the payload tree that the late-commands copy into the target. When payload is
// empty (golden mode) the output ISO is constant per ISO hash and cached, so
// editing user-data never re-extracts or re-runs xorriso; a payload-carrying
// repack extracts the ISO fresh into the scratch tree each time (the rare,
// legacy path).
func RepackISO(srcISO, tree, outISO string, payload map[string][]byte) error {
	hash, err := FileSha256(srcISO)
	if err != nil {
		return fmt.Errorf("hash ISO: %w", err)
	}
	// A payload-free repack depends only on the ISO itself: reuse the cached
	// output instead of extracting + repacking.
	if len(payload) == 0 {
		hit, err := outISOCached(hash, outISO)
		if err != nil {
			return fmt.Errorf("repack cache: %w", err)
		}
		if hit {
			return nil
		}
	}

	os.RemoveAll(tree)
	if err := extractISO(srcISO, tree); err != nil {
		return fmt.Errorf("extract ISO: %w", err)
	}
	if err := writeISOTree(tree, payload); err != nil {
		return fmt.Errorf("write tree: %w", err)
	}
	p, err := deriveXorrisoParams(srcISO)
	if err != nil {
		return err
	}
	if err := repackWithXorriso(p, tree, outISO); err != nil {
		return fmt.Errorf("repack: %w", err)
	}
	if len(payload) == 0 {
		if err := cacheOutISO(hash, outISO); err != nil {
			return fmt.Errorf("cache repacked ISO: %w", err)
		}
	}
	return nil
}

// sentinel is written into the repacked-ISO cache directory after a successful
// repack so a partial run (crash, disk full) is detected and retried.
const sentinel = ".cache-ok"

// FileSha256 returns the hex-encoded SHA-256 hash of the file at path.
func FileSha256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ── Repacked-ISO output cache ───────────────────────────────────────────

// outISOCacheDir returns the cache dir for the payload-free repack of an ISO
// hash. The output depends only on the ISO (grub.cfg is fixed), so config edits
// never invalidate it — one entry per ISO version.
func outISOCacheDir(hash string) string {
	dir, _ := os.UserCacheDir()
	return filepath.Join(dir, "vmtest-iso-out", hash)
}

const outISOName = "autoinstall.iso"

// outISOCached restores the cached payload-free repack to outISO. Returns false
// (no error) if the cache is absent/incomplete.
func outISOCached(hash, outISO string) (bool, error) {
	cacheDir := outISOCacheDir(hash)
	iso := filepath.Join(cacheDir, outISOName)
	if _, err := os.Stat(filepath.Join(cacheDir, sentinel)); err != nil {
		return false, nil
	}
	if _, err := os.Stat(iso); err != nil {
		return false, nil // sentinel but ISO missing — treat as a miss, rebuild
	}
	return true, linkOrCopy(iso, outISO)
}

// cacheOutISO stores a freshly repacked payload-free ISO keyed by ISO hash.
func cacheOutISO(hash, outISO string) error {
	cacheDir := outISOCacheDir(hash)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return err
	}
	if err := linkOrCopy(outISO, filepath.Join(cacheDir, outISOName)); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(cacheDir, sentinel), nil, 0o644)
}

// linkOrCopy hard-links src to dst (instant CoW on the same filesystem),
// falling back to a copy when the paths straddle filesystems.
func linkOrCopy(src, dst string) error {
	os.Remove(dst)
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	return copyFile(src, dst)
}

// ── ISO extraction (pure Go, no root) ───────────────────────────────────

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

// ── Seed writing ────────────────────────────────────────────────────────

// writeISOTree writes the fixed grub.cfg and the payload tree into the
// extracted ISO tree. user-data/meta-data are deliberately NOT here — they live
// on a separate cidata disk (WriteSeedISO) so the ISO never changes with config.
func writeISOTree(tree string, payload map[string][]byte) error {
	// ds=nocloud auto-detect: cloud-init finds the cidata-labeled seed disk.
	grub := autoinstall.RenderGrubCfg("", true)
	if err := os.WriteFile(tree+"/boot/grub/grub.cfg", []byte(grub), 0o644); err != nil {
		return err
	}
	seedDir := tree + "/nocloud"
	if len(payload) == 0 {
		os.RemoveAll(seedDir) // nothing to carry (golden mode) — drop the dir
	} else {
		if err := os.MkdirAll(seedDir, 0o755); err != nil {
			return err
		}
		for name, data := range payload {
			dst := filepath.Join(seedDir, name)
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", filepath.Dir(dst), err)
			}
			if err := os.WriteFile(dst, data, 0o755); err != nil {
				return fmt.Errorf("write %s: %w", dst, err)
			}
		}
	}
	// Drop a stale boot catalog — xorriso regenerates it via -c.
	os.Remove(tree + "/boot.catalog")
	return nil
}

// WriteSeedISO builds the tiny NoCloud config-drive (seed.iso): an ISO9660
// image labeled `cidata` carrying only user-data + meta-data. cloud-init in the
// live installer auto-detects it (ds=nocloud) and hands the autoinstall config
// to subiquity. A few KB, regenerated in ~0s — config edits never touch the
// bootable ISO. No -b flag: the seed has no boot record and can never be booted.
func WriteSeedISO(outPath, userData, metaData string) error {
	dir, err := os.MkdirTemp("", "seed-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, "user-data"), []byte(userData), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "meta-data"), []byte(metaData), 0o644); err != nil {
		return err
	}
	return writeISODir(outPath, "cidata", dir)
}

// WritePayloadISO builds a plain ISO carrying a payload directory's files at
// the root. Phase C uses it to deliver the provision payload into the booted
// guest: labeled non-cidata so cloud-init ignores it, and the placement script
// mounts it by label. (The autoinstall seed.iso stays tiny; this one is ~12MB
// because it carries the provisioner binary.)
func WritePayloadISO(outPath, label, payloadDir string) error {
	return writeISODir(outPath, label, payloadDir)
}

// writeISODir runs xorriso to build a non-bootable ISO from a directory's
// contents with the given volume label.
func writeISODir(outPath, label, dir string) error {
	var stderr bytes.Buffer
	cmd := exec.Command("xorriso", "-as", "mkisofs", "-r",
		"-volid", label,
		"-o", outPath, dir)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("xorriso %s: %w\n%s", label, err, stderr.String())
	}
	return nil
}

// ── xorriso repack ──────────────────────────────────────────────────────

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
