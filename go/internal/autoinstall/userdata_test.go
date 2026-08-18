package autoinstall

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// -update regenerates the golden user-data snapshot (see TestUserDataGolden).
var update = flag.Bool("update", false, "rewrite golden files")

func goldenPath(t *testing.T) string {
	t.Helper()
	return filepath.Join("testdata", "user-data.golden")
}

// TestUserDataGolden is a full-output snapshot test: the entire rendered
// user-data must byte-match the committed golden file. A configuration change
// that alters the installer input shows up here on every CI run instead of
// surfacing only in a VM install. Regenerate intentionally with -update.
func TestUserDataGolden(t *testing.T) {
	c := Default()
	c.Identity.PasswordHash = "$6$testsalt$hash" // placeholder hash, stable across runs
	got, err := RenderUserData(c)
	if err != nil {
		t.Fatalf("RenderUserData: %v", err)
	}
	p := goldenPath(t)
	if *update {
		if err := os.WriteFile(p, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("golden updated: %s", p)
		return
	}
	want, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			t.Fatalf("golden file missing (%s); run `go test ./internal/autoinstall -run TestUserDataGolden -update` to generate", p)
		}
		t.Fatalf("read golden: %v", err)
	}
	if got != string(want) {
		t.Errorf("user-data drifted from golden (%s). If the change is intentional, regenerate with -update.\n--- golden ---\n%s\n--- got ---\n%s", p, want, got)
	}
}

func testConfig() Config {
	c := Default()
	c.Identity.PasswordHash = "$6$testsalt$hash" // placeholder hash
	c.LateCommand = []string{
		"echo 'LANG=en_US.UTF-8' > /target/etc/locale.conf",
		"ln -sf /usr/share/zoneinfo/Asia/Shanghai /target/etc/localtime",
	}
	return c
}

// TestUserDataHeader is the guard for the #cloud-config space bug: cloud-init
// only recognizes a leading "#cloud-config" (no space). A space makes it treat
// the file as unhandled user-data and drop the whole config silently.
func TestUserDataHeader(t *testing.T) {
	tests := []struct {
		name     string
		config   Config
		wantHead string // what the first line must be
		bug      string // the accidental variant that must NOT appear
	}{
		{"default", testConfig(), "#cloud-config\n", "# cloud-config\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := RenderUserData(tt.config)
			if err != nil {
				t.Fatalf("RenderUserData error: %v", err)
			}
			if !strings.HasPrefix(out, tt.wantHead) {
				t.Errorf("user-data must start with %q, got:\n%q", tt.wantHead, firstLine(out))
			}
			if strings.HasPrefix(out, tt.bug) {
				t.Errorf("user-data accidentally starts with buggy header %q", tt.bug)
			}
		})
	}
}

// TestUserDataContents checks the rendered config carries the values that must
// reach the installer.
func TestUserDataContents(t *testing.T) {
	c := testConfig()
	out, err := RenderUserData(c)
	if err != nil {
		t.Fatalf("RenderUserData error: %v", err)
	}
	for _, want := range []string{
		"autoinstall:",
		"hostname: ubuntu",
		"username: dailyuser",
		"password: $6$testsalt$hash",
		"locale: en_US.UTF-8",
		"timezone: Asia/Shanghai",
		"serial: 50026B727200FDDC",
		"- openssh-server",
		"- git",
		"allow-pw: true",
		"shutdown: reboot",
		"ln -sf /usr/share/zoneinfo/Asia/Shanghai /target/etc/localtime",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("user-data missing %q", want)
		}
	}
}

// TestUserDataIsValidYAML guards against the multi-line late-command bug: a
// hand-written template emitted continuation lines without indentation, which
// broke YAML parsing and left subiquity stuck in state WAITING (install never
// started). The output must always parse as YAML and carry late-commands.
func TestUserDataIsValidYAML(t *testing.T) {
	// Default() carries the full late-commands, including the multi-line gdm
	// autologin command that broke a hand-written template.
	c := Default()
	c.Identity.PasswordHash = "$6$testsalt$hash"
	out, err := RenderUserData(c)
	if err != nil {
		t.Fatalf("RenderUserData: %v", err)
	}
	var m map[string]any
	if err := yaml.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("user-data is not valid YAML: %v\n%s", err, out)
	}
	ai, ok := m["autoinstall"].(map[string]any)
	if !ok {
		t.Fatalf("missing autoinstall key in:\n%s", out)
	}
	lc, ok := ai["late-commands"].([]any)
	if !ok || len(lc) == 0 {
		t.Fatalf("late-commands missing or empty in:\n%s", out)
	}
	// The multi-line sddm autologin command must survive the YAML round-trip
	// (exact bytes may differ by a trailing newline from block-scalar folding).
	foundSDDM := false
	for _, item := range lc {
		s, ok := item.(string)
		if !ok {
			continue
		}
		if strings.Contains(s, "sddm.conf.d/autologin.conf") && strings.Contains(s, "User=dailyuser") {
			foundSDDM = true
		}
	}
	if !foundSDDM {
		t.Fatalf("sddm autologin late-command lost in YAML round-trip:\n%s", out)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
