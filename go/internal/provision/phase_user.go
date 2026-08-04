package provision

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

func (p *Provisioner) phaseGitConfig(*Provisioner) error {
	// Identity comes from env (GIT_USER_NAME / GIT_USER_EMAIL), set when running.
	if name := os.Getenv("GIT_USER_NAME"); name != "" {
		p.Runner.Run(p.Cfg.User, "git", "config", "--global", "user.name", name)
	}
	if email := os.Getenv("GIT_USER_EMAIL"); email != "" {
		p.Runner.Run(p.Cfg.User, "git", "config", "--global", "user.email", email)
	}
	// Sensible defaults regardless of identity.
	p.Runner.Run(p.Cfg.User, "git", "config", "--global", "init.defaultBranch", "main")
	p.Runner.Run(p.Cfg.User, "git", "config", "--global", "pull.rebase", "false")
	return nil
}

func (p *Provisioner) phaseDotfiles(*Provisioner) error {
	// Dotfiles ship alongside the binary under share/provisioner-ubuntu/dotfiles.
	dotfilesDir := os.Getenv("PROVISIONER_DOTFILES")
	if dotfilesDir == "" {
		return nil
	}
	if _, err := os.Stat(dotfilesDir); err != nil {
		return nil // no dotfiles dir — skip
	}
	copied := 0
	err := filepath.WalkDir(dotfilesDir, func(src string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dotfilesDir, src)
		if err != nil {
			return err
		}
		dst := filepath.Join(p.Cfg.Home, rel)
		if _, err := os.Stat(dst); err == nil {
			return nil // already exists — preserve user's local copy
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			target, err := os.Readlink(src)
			if err != nil {
				return fmt.Errorf("readlink %s: %w", rel, err)
			}
			if err := os.Symlink(target, dst); err != nil {
				return fmt.Errorf("symlink %s: %w", rel, err)
			}
		} else {
			info, err := d.Info()
			if err != nil {
				return err
			}
			if err := copyFile(src, dst, info.Mode()); err != nil {
				return fmt.Errorf("copy %s: %w", rel, err)
			}
		}
		copied++
		return nil
	})
	if err != nil {
		return err
	}
	// chown newly-copied files to the target user (no-op if nothing copied).
	if copied > 0 {
		p.Runner.Run("", "chown", "-R", p.Cfg.User+":"+p.Cfg.User, p.Cfg.Home)
	}
	return nil
}
