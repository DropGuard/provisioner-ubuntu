package autoinstall

import "fmt"

// RenderGrubCfg returns the boot/grub/grub.cfg for the repacked ISO.
//
// seedPath selects how cloud-init's NoCloud datasource finds the seed:
//   - "" (VM tests): emit `autoinstall ds=nocloud` — NoCloud auto-detects a
//     block device labeled `cidata` (the tiny seed.iso, see WriteSeedISO). The
//     cmdline has no ';' at all, so the escaping bug below cannot apply.
//   - "/cdrom/nocloud/" (USB): emit `autoinstall ds=nocloud\;s=...` pointing at
//     the seed dir on the boot media.
//
// CRITICAL: when seedPath is non-empty, the ';' in "ds=nocloud;s=..." is
// escaped as "\;" in the generated file. Grub treats an unescaped ';' as a
// command separator and truncates the kernel cmdline there, silently dropping
// the s=... seed path (verified in KVM — this was a real-machine root cause).
// serialConsole adds console=ttyS0 so a headless VM run is observable on the
// serial log.
func RenderGrubCfg(seedPath string, serialConsole bool) string {
	// Auto-detect a cidata-labeled seed disk — no source clause at all.
	ds := "ds=nocloud"
	if seedPath != "" {
		// The `\\;` in the raw string below is a literal backslash-semicolon in
		// the generated grub.cfg — that is the point.
		ds = "ds=nocloud\\;s=" + seedPath
	}
	console := ""
	if serialConsole {
		console = " console=ttyS0"
	}
	return fmt.Sprintf(`set timeout=0

serial --unit=0 --speed=115200
terminal_input serial
terminal_output serial

menuentry "Ubuntu autoinstall" {
    linux  /casper/vmlinuz%s autoinstall %s ---
    initrd /casper/initrd
}
menuentry "Ubuntu (safe graphics)" {
    linux  /casper/vmlinuz%s nomodeset ---
    initrd /casper/initrd
}
`, console, ds, console)
}
