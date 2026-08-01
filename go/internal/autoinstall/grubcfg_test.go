package autoinstall

import (
	"strings"
	"testing"
)

// TestGrubCfgSemicolonEscaping is the guard for the grub ';' truncation bug:
// grub treats an unescaped ';' in the linux line as a command separator, so
// `ds=nocloud;s=/cdrom/nocloud/` silently loses the seed path. The generated
// file MUST contain the escaped `\;` form and must NOT contain the bare one.
func TestGrubCfgSemicolonEscaping(t *testing.T) {
	seedPath := "/cdrom/nocloud/"
	out := RenderGrubCfg(seedPath, true)

	escaped := "ds=nocloud\\;s=" + seedPath
	if !strings.Contains(out, escaped) {
		t.Errorf("grub.cfg must contain escaped form %q, got:\n%s", escaped, out)
	}
	bare := "ds=nocloud;s=" + seedPath
	if strings.Contains(out, bare) {
		t.Errorf("grub.cfg must NOT contain unescaped %q (grub truncates at ';')", bare)
	}
	// The seed path itself must be present regardless of escaping form.
	if !strings.Contains(out, "s="+seedPath) {
		t.Errorf("grub.cfg missing seed path %q", seedPath)
	}
}

// TestGrubCfgAutoDetectSeed checks the VM form (empty seedPath): ds=nocloud with
// no s= clause and no ';' at all, so the grub ';' truncation bug cannot apply.
func TestGrubCfgAutoDetectSeed(t *testing.T) {
	out := RenderGrubCfg("", true)

	if !strings.Contains(out, "autoinstall ds=nocloud ---") {
		t.Errorf("empty seedPath must emit `autoinstall ds=nocloud` (auto-detect), got:\n%s", out)
	}
	// No source clause at all: no ';' anywhere (so the truncation bug cannot
	// apply), no /cdrom/nocloud reference. (A bare "s=" check would false-hit on
	// "ds=nocloud", so assert on the delimiter and the path instead.)
	if strings.Contains(out, ";") {
		t.Errorf("empty seedPath must not emit any ';' (no source clause), got:\n%s", out)
	}
	if strings.Contains(out, "cdrom/nocloud") {
		t.Errorf("empty seedPath must not reference /cdrom/nocloud:\n%s", out)
	}
}

// TestGrubCfgAutoinstallAndConsole checks the entry actually carries the
// autoinstall trigger and (when requested) the serial console.
func TestGrubCfgAutoinstallAndConsole(t *testing.T) {
	withConsole := RenderGrubCfg("/cdrom/nocloud/", true)
	noConsole := RenderGrubCfg("/cdrom/nocloud/", false)

	if !strings.Contains(withConsole, "autoinstall ") {
		t.Error("grub.cfg missing 'autoinstall' kernel param")
	}
	if !strings.Contains(withConsole, "console=ttyS0") {
		t.Error("with serialConsole=true, grub.cfg must include console=ttyS0")
	}
	if strings.Contains(noConsole, "console=ttyS0") {
		t.Error("with serialConsole=false, grub.cfg must not include console=ttyS0")
	}
	if !strings.Contains(withConsole, "linux  /casper/vmlinuz") {
		t.Error("grub.cfg missing casper vmlinuz kernel line")
	}
	if !strings.Contains(withConsole, "initrd /casper/initrd") {
		t.Error("grub.cfg missing initrd line")
	}
}
