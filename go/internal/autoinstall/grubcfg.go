package autoinstall

import "fmt"

// RenderGrubCfg returns the boot/grub/grub.cfg for the repacked ISO. The
// default entry boots casper with `autoinstall ds=nocloud;s=SEED`.
//
// CRITICAL: the ';' in "ds=nocloud;s=..." is escaped as "\;" in the generated
// file. Grub treats an unescaped ';' as a command separator and truncates the
// kernel cmdline there, silently dropping the s=... seed path (verified in
// KVM — this was a real-machine root cause). serialConsole adds console=ttyS0
// so a headless VM run is observable on the serial log.
func RenderGrubCfg(seedPath string, serialConsole bool) string {
	// The `\\;` in the raw string below is a literal backslash-semicolon in the
	// generated grub.cfg — that is the point.
	console := ""
	if serialConsole {
		console = " console=ttyS0"
	}
	return fmt.Sprintf(`set timeout=0

serial --unit=0 --speed=115200
terminal_input serial
terminal_output serial

menuentry "Ubuntu autoinstall" {
    linux  /casper/vmlinuz%s autoinstall ds=nocloud\;s=%s ---
    initrd /casper/initrd
}
menuentry "Ubuntu (safe graphics)" {
    linux  /casper/vmlinuz%s nomodeset ---
    initrd /casper/initrd
}
`, console, seedPath, console)
}
