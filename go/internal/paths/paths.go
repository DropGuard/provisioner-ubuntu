// Package paths centralizes the on-target deployment paths shared between the
// autoinstall late-commands (which copy the payload into the installed system)
// and the provisioner runtime (which reads that payload). Keeping them in one
// place means a move only has to be made once.
package paths

const (
	// ProvisionBin is where the Go provisioner lands in the installed system;
	// first-boot.service runs it.
	ProvisionBin = "/usr/local/bin/provision"

	// FavBin is a helper script copied alongside.
	FavBin = "/usr/local/bin/fav"

	// ShareDir holds the provision payload (config/ + dotfiles/) that the
	// provisioner reads on first boot.
	ShareDir    = "/usr/local/share/provisioner-ubuntu"
	ConfigDir   = ShareDir + "/config"
	DotfilesDir = ShareDir + "/dotfiles"

	// FirstBootUnit is the oneshot service that triggers provisioning.
	FirstBootUnit = "/etc/systemd/system/first-boot.service"
)
