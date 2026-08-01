// Package payload assembles the seed payload — the tree planted under the
// nocloud/ directory of the repacked ISO or the USB, which the autoinstall
// late-commands copy into the installed system. It closes the loop between the
// repo (scripts/, config/, dotfiles/) and the VM/USB harness: one command
// produces exactly the directory `test-vm --payload` and `usb --payload`
// expect, instead of hand-assembling it.
package payload

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"provisioner-ubuntu/internal/config"
)

// scriptFiles maps a repo scripts/ filename to the payload filename expected
// under nocloud/ (the late-commands reference these exact names). required is
// true for scripts the autoinstall late-commands copy unconditionally — a
// missing one silently breaks provisioning after install, so the build must
// fail instead of emitting a payload without it.
var scriptFiles = []struct {
	src, dst string
	required bool
}{
	{"first-boot.service", "first-boot.service", true},
	{"setup-fcitx5-chinese.sh", "setup-fcitx5-chinese.sh", true},
	{"test-env-loading.sh", "test-env-loading.sh", true},
	// fav.sh is copied with `|| true` tolerance in the late-commands, so a
	// missing script is non-fatal and the build keeps going.
	{"fav.sh", "fav.sh", false},
}

// Options configures the payload build.
type Options struct {
	Out      string // output dir (created if needed)
	Binary   string // provisioner binary, copied as "provision" ("" = skip)
	Scripts  string // repo scripts/ dir
	Config   string // repo config/ dir
	Dotfiles string // repo dotfiles/ dir
}

// Build assembles the payload tree into Out. All inputs are optional except
// Out; missing dirs/files are skipped (so a minimal build with just the
// binary works, and a repo build with everything works).
func Build(o Options) error {
	if o.Out == "" {
		return fmt.Errorf("Out required")
	}
	if err := os.MkdirAll(o.Out, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", o.Out, err)
	}

	if o.Binary != "" {
		if err := copyMode(o.Binary, filepath.Join(o.Out, "provision"), 0o755); err != nil {
			return fmt.Errorf("provision binary: %w", err)
		}
	} else {
		// The late-commands copy nocloud/provision unconditionally; a payload
		// without it produces an installed system that never provisions.
		return fmt.Errorf("Binary required: the autoinstall late-commands copy nocloud/provision and first-boot.service runs it")
	}
	if o.Scripts != "" {
		for _, sf := range scriptFiles {
			src := filepath.Join(o.Scripts, sf.src)
			if _, err := os.Stat(src); err != nil {
				if sf.required {
					return fmt.Errorf("scripts: required file %s missing (autoinstall late-commands reference it)", sf.src)
				}
				continue // optional script
			}
			if err := copyMode(src, filepath.Join(o.Out, sf.dst), 0o755); err != nil {
				return fmt.Errorf("%s: %w", sf.src, err)
			}
		}
	}
	if o.Config != "" {
		if errs := config.Validate(o.Config); len(errs) > 0 {
			msgs := make([]string, len(errs))
			for i, e := range errs {
				msgs[i] = e.Error()
			}
			return fmt.Errorf("config validation failed:\n\t%s", strings.Join(msgs, "\n\t"))
		}
		if err := copyTree(o.Config, filepath.Join(o.Out, "config")); err != nil {
			return fmt.Errorf("config: %w", err)
		}
	}
	if o.Dotfiles != "" {
		if err := copyTree(o.Dotfiles, filepath.Join(o.Out, "dotfiles")); err != nil {
			return fmt.Errorf("dotfiles: %w", err)
		}
	}
	return nil
}

// copyTree recursively copies src to dst (shallow dirs preserved).
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyMode(path, target, info.Mode())
	})
}

// copyMode copies src to dst with the given mode, creating parent dirs.
func copyMode(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
