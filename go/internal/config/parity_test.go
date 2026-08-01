package config

import (
	"fmt"
	"os"
	"testing"
)

// TestRepoConfigParity guards against drift between the repo's config/
// directory (the single source of truth) and the built-in Default() fallback.
// If they diverge, this test fails — update Default() or fix the list file.
func TestRepoConfigParity(t *testing.T) {
	repo := os.Getenv("REPO_ROOT")
	if repo == "" {
		t.Skip("REPO_ROOT not set (run with REPO_ROOT=/workspace)")
	}
	loaded, err := Load(repo + "/config")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	def := Default()
	if fmt.Sprint(loaded.CorePackages) != fmt.Sprint(def.CorePackages) {
		t.Errorf("core drifted:\n file=%v\n  def=%v", loaded.CorePackages, def.CorePackages)
	}
	if fmt.Sprint(loaded.ExtraPackages) != fmt.Sprint(def.ExtraPackages) {
		t.Errorf("extra drifted:\n file=%v\n  def=%v", loaded.ExtraPackages, def.ExtraPackages)
	}
	if fmt.Sprint(loaded.BrewPackages) != fmt.Sprint(def.BrewPackages) {
		t.Errorf("brew drifted:\n file=%v\n  def=%v", loaded.BrewPackages, def.BrewPackages)
	}
	if loaded.MiseConfig != def.MiseConfig {
		t.Errorf("mise drifted:\n file=%q\n  def=%q", loaded.MiseConfig, def.MiseConfig)
	}
}
