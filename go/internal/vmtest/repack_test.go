package vmtest

import (
	"os"
	"strings"
	"testing"
)

const testISO = "/home/dropguard/Downloads/ubuntu-26.04-desktop-amd64.iso"

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
		appendUUID: "12345678901234567890123456789012",
		appendInterval: "--interval:local_fs:1d-2d::'x.iso'",
		eltoritoImg: "/boot/grub/i386-pc/eltorito.img",
		eltoritoBLS: "4", efiBLS: "10296", isoMBR: "abc",
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
