package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"provisioner-ubuntu/internal/payload"
	"provisioner-ubuntu/internal/usb"
)

type USBConfigFile struct {
	ISO    string `yaml:"iso"`
	Disk   string `yaml:"disk"`
	Serial string `yaml:"serial"`
	Repo   string `yaml:"repo"`
}

func loadUSBConfig(configFile string) (USBConfigFile, error) {
	var cfg USBConfigFile
	candidates := []string{
		configFile,
		"config/provisioner.yaml",
		"../config/provisioner.yaml",
		"provisioner.yaml",
		"../provisioner.yaml",
	}
	for _, p := range candidates {
		if p == "" {
			continue
		}
		if data, err := os.ReadFile(p); err == nil {
			_ = yaml.Unmarshal(data, &cfg)
			return cfg, nil
		}
	}
	return cfg, nil
}

func findRepoRoot() string {
	for _, dir := range []string{".", "..", "../.."} {
		if _, err := os.Stat(filepath.Join(dir, "ansible")); err == nil {
			return dir
		}
	}
	return "."
}

func findDefaultISO() string {
	for _, dir := range []string{".", ".."} {
		matches, _ := filepath.Glob(filepath.Join(dir, "ubuntu-*.iso"))
		if len(matches) > 0 {
			return matches[0]
		}
	}
	return "ubuntu-26.04-live-server-amd64.iso"
}

// usbCmd builds a bootable autoinstall USB (port of prepare-usb.sh).
func usbCmd() *cobra.Command {
	var iso, disk, serial, payloadDir, repo, configFile string
	cmd := &cobra.Command{
		Use:   "usb",
		Short: "Build a bootable autoinstall USB (needs root — WIPES the disk)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _ := loadUSBConfig(configFile)

			// Resolve parameters: CLI Flag > Config File > Smart Default
			if serial == "" {
				if cfg.Serial != "" {
					serial = cfg.Serial
				} else {
					serial = "50026B727200FDDC"
				}
			}
			if iso == "" {
				if cfg.ISO != "" {
					iso = cfg.ISO
				} else {
					iso = findDefaultISO()
				}
			}
			if disk == "" {
				disk = cfg.Disk
			}
			if repo == "" {
				if cfg.Repo != "" {
					repo = cfg.Repo
				} else {
					repo = findRepoRoot()
				}
			}

			if disk == "" {
				return fmt.Errorf("target disk is required: specify via --disk (e.g. --disk /dev/sdb) or set 'disk' in config/provisioner.yaml")
			}

			// If payload dir is not provided, auto-build payload in temporary directory
			var payloadMap map[string][]byte
			if payloadDir != "" {
				var err error
				payloadMap, err = payloadFromDir(payloadDir)
				if err != nil {
					return err
				}
			} else {
				tmpPayload, err := os.MkdirTemp("", "provision-payload-*")
				if err != nil {
					return fmt.Errorf("create temp payload dir: %w", err)
				}
				defer os.RemoveAll(tmpPayload)

				fmt.Printf("auto-building seed payload from %s...\n", repo)
				if err := payload.Build(payload.Options{
					Out:      tmpPayload,
					Scripts:  filepath.Join(repo, "scripts"),
					Config:   filepath.Join(repo, "config"),
					Dotfiles: filepath.Join(repo, "dotfiles"),
					Ansible:  filepath.Join(repo, "ansible"),
				}); err != nil {
					return fmt.Errorf("build payload: %w", err)
				}
				payloadMap, err = payloadFromDir(tmpPayload)
				if err != nil {
					return err
				}
			}

			fmt.Printf("building USB on %s (target SSD serial: %s, iso: %s)...\n", disk, serial, iso)
			return usb.Build(usb.Options{
				ISO:     iso,
				Disk:    disk,
				Serial:  serial,
				Payload: payloadMap,
			})
		},
	}
	cmd.Flags().StringVar(&configFile, "config", "", "provisioner config file path (default: config/provisioner.yaml)")
	cmd.Flags().StringVar(&iso, "iso", "", "original Ubuntu desktop ISO (default: auto-detect)")
	cmd.Flags().StringVar(&disk, "disk", "", "USB block device, e.g. /dev/sdb (WILL BE WIPED)")
	cmd.Flags().StringVar(&serial, "serial", "", "target system disk serial (default from config or built-in)")
	cmd.Flags().StringVar(&repo, "repo", "", "repository root containing ansible/, config/, dotfiles/ (default: auto-detect)")
	cmd.Flags().StringVar(&payloadDir, "payload", "", "explicit pre-built payload dir (optional; auto-builds if omitted)")
	return cmd
}

// payloadFromDir reads every file under dir into a map keyed by relative path.
func payloadFromDir(dir string) (map[string][]byte, error) {
	payload := map[string][]byte{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		payload[rel] = data
		return nil
	})
	return payload, err
}
