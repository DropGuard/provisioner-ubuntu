// Command config-gen renders the autoinstall seed artifacts (user-data and
// grub.cfg) from the typed config. It is the proof-of-concept for the Go
// rewrite: the two content bugs that cost hours of KVM debugging are now
// impossible to reintroduce (the header and the ';' escaping are literal in the
// generators) and are enforced by unit tests.
package main

import (
	"fmt"
	"os"

	"provisioner-ubuntu/internal/autoinstall"
)

func main() {
	c := autoinstall.Default()

	ud, err := autoinstall.RenderUserData(c)
	if err != nil {
		fmt.Fprintln(os.Stderr, "user-data:", err)
		os.Exit(1)
	}
	fmt.Println("=== user-data ===")
	fmt.Print(ud)

	fmt.Println("=== grub.cfg ===")
	fmt.Print(autoinstall.RenderGrubCfg("/cdrom/nocloud/", true))
}
