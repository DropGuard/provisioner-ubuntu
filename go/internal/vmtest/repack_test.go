package vmtest

import (
	"os"
	"strings"
	"testing"
)

const testISO = "/home/dropguard/Downloads/ubuntu-26.04-desktop-amd64.iso"

// TestParseXorrisoReport is ISO-free: it feeds parseXorrisoReport a realistic
// `-report_el_torito as_mkisofs` transcript and checks every field, including
// that interval paths get their display quoting stripped (an absolute path
// passed back still-quoted fails to open) and that missing required fields
// produce an error instead of a half-filled struct.
func TestParseXorrisoReport(t *testing.T) {
	const report = `-V 'Ubuntu 26.04 amd64'
--modification-date='2026042302185100'
--grub2-mbr --interval:local_fs:0s-15s:zero_mbrpt,zero_gpt:'/data/ubuntu-26.04.iso'
--protective-msdos-label
-partition_cyl_align off
-append_partition 2 c8dca3c23d5c4b4d8a0e9f1a2b3c4d5e --interval:local_fs:12721412d-12731707d::'/data/ubuntu-26.04.iso'
-b '/boot/grub/i386-pc/eltorito.img'
-boot-load-size 4
-boot-info-table
-eltorito-alt-boot
-e --interval:appended_partition_2:all::
-no-emul-boot
-boot-load-size 10296
-iso_mbr_part_type a2a0d0ebe5b9334487c068b6b72699c7
`
	p, err := parseXorrisoReport(report)
	if err != nil {
		t.Fatalf("parseXorrisoReport: %v", err)
	}
	for _, tc := range []struct{ name, got, want string }{
		{"vol", p.vol, "Ubuntu 26.04 amd64"},
		{"modDate", p.modDate, "2026042302185100"},
		// Display quoting ('...') must be stripped from interval paths.
		{"grub2Mbr", p.grub2Mbr, "--interval:local_fs:0s-15s:zero_mbrpt,zero_gpt:/data/ubuntu-26.04.iso"},
		{"appendUUID", p.appendUUID, "c8dca3c23d5c4b4d8a0e9f1a2b3c4d5e"},
		{"appendInterval", p.appendInterval, "--interval:local_fs:12721412d-12731707d::/data/ubuntu-26.04.iso"},
		{"eltoritoImg", p.eltoritoImg, "/boot/grub/i386-pc/eltorito.img"},
		{"eltoritoBLS", p.eltoritoBLS, "4"},
		{"efiBLS", p.efiBLS, "10296"},
		{"isoMBR", p.isoMBR, "a2a0d0ebe5b9334487c068b6b72699c7"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}

	// Missing required fields must fail loudly, not silently zero-fill.
	if _, err := parseXorrisoReport("-V 'x'\n--modification-date='y'\n"); err == nil {
		t.Error("parseXorrisoReport with missing boot params should error")
	}
}

// TestDeriveXorrisoParams verifies we can read the boot parameters from the
// real Ubuntu desktop ISO (the same ones the original was built with), so the
// repack produces a valid boot structure across point releases.
func TestDeriveXorrisoParams(t *testing.T) {
	if _, err := os.Stat(testISO); err != nil {
		t.Skipf("ISO %s not present", testISO)
	}
	p, err := deriveXorrisoParams(testISO)
	if err != nil {
		t.Fatalf("deriveXorrisoParams: %v", err)
	}
	for _, tc := range []struct{ name, got, want string }{
		{"vol", p.vol, "Ubuntu 26.04 amd64"},
		{"modDate", p.modDate, "2026042302185100"},
		{"eltoritoImg", p.eltoritoImg, "/boot/grub/i386-pc/eltorito.img"},
		{"eltoritoBLS", p.eltoritoBLS, "4"},
		{"efiBLS", p.efiBLS, "10296"},
		{"isoMBR", p.isoMBR, "a2a0d0ebe5b9334487c068b6b72699c7"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
	if !strings.Contains(p.grub2Mbr, "--interval:local_fs:0s-15s") {
		t.Errorf("grub2Mbr unexpected: %q", p.grub2Mbr)
	}
	if len(p.appendUUID) != 32 {
		t.Errorf("appendUUID unexpected: %q", p.appendUUID)
	}
	if !strings.Contains(p.appendInterval, "--interval:local_fs:12721412d-12731707d") {
		t.Errorf("appendInterval unexpected: %q", p.appendInterval)
	}
}

// TestRepackArgsAssembles verifies the xorriso argument slice contains the
// seed path with the escaped semicolon and the appended-partition symbolic ref.
func TestRepackArgsAssembles(t *testing.T) {
	p := xorrisoParams{
		vol: "V", modDate: "M", grub2Mbr: "--interval:local_fs:0s-15s:zero_mbrpt,zero_gpt:'x.iso'",
		appendUUID:     "12345678901234567890123456789012",
		appendInterval: "--interval:local_fs:1d-2d::'x.iso'",
		eltoritoImg:    "/boot/grub/i386-pc/eltorito.img",
		eltoritoBLS:    "4", efiBLS: "10296", isoMBR: "abc",
	}
	args := xorrisoArgs(p, "/tmp/tree", "/tmp/out.iso")
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--grub2-mbr", "--interval:local_fs:0s-15s:zero_mbrpt,zero_gpt:'x.iso'",
		"-append_partition", "2", "12345678901234567890123456789012",
		"-e", "--interval:appended_partition_2:all::",
		"-o", "/tmp/out.iso", "/tmp/tree",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q\nargs: %s", want, joined)
		}
	}
}
