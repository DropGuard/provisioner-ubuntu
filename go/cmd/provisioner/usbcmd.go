package main

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"provisioner-ubuntu/internal/usb"
)

// usbCmd builds a bootable autoinstall USB (port of prepare-usb.sh).
func usbCmd() *cobra.Command {
	var iso, disk, serial, payload string
	cmd := &cobra.Command{
		Use:   "usb",
		Short: "Build a bootable autoinstall USB (needs root — WIPES the disk)",
		RunE: func(cmd *cobra.Command, args []string) error {
			payloadMap, err := payloadFromDir(payload)
			if err != nil {
				return err
			}
			return usb.Build(usb.Options{
				ISO:     iso,
				Disk:    disk,
				Serial:  serial,
				Payload: payloadMap,
			})
		},
	}
	cmd.Flags().StringVar(&iso, "iso", "", "original Ubuntu desktop ISO (required)")
	cmd.Flags().StringVar(&disk, "disk", "", "USB block device, e.g. /dev/sdb (WILL BE WIPED)")
	cmd.Flags().StringVar(&serial, "serial", "50026B727200FDDC", "target system disk serial")
	cmd.Flags().StringVar(&payload, "payload", "", "dir copied into nocloud/ (provision, first-boot.service, config/, ...)")
	_ = cmd.MarkFlagRequired("iso")
	_ = cmd.MarkFlagRequired("disk")
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
