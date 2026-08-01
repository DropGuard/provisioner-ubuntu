// Command provisioner-ubuntu is the Go rewrite of the project's bash scripts:
// seed/config generation, USB build, first-boot provisioning, and the VM test
// harness. Subcommands:
//
//	config-gen    render user-data + grub.cfg from the typed config
//	test-vm       validate the autoinstall config end-to-end in a KVM VM
//	usb           build a bootable autoinstall USB (prepare-usb port)
//	provision     run first-boot provisioning (provision.sh port, root)
//	provision-user  run a user-owned provisioning phase (self re-exec)
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:   "provisioner-ubuntu",
		Short: "Ubuntu desktop provisioning tool (Go)",
	}
	root.AddCommand(configgenCmd())
	root.AddCommand(testvmCmd())
	root.AddCommand(usbCmd())
	root.AddCommand(provisionCmd())
	root.AddCommand(provisionUserCmd())
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
