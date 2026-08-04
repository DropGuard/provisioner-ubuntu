package fcitx

import (
	"fmt"
	"time"

	"github.com/godbus/dbus/v5"
)

type IMItem struct {
	Name   string
	Layout string
}

func ConfigureRimeViaDBus() error {
	conn, err := dbus.SessionBus()
	if err != nil {
		return fmt.Errorf("connect to SessionBus: %w", err)
	}
	defer conn.Close()

	obj := conn.Object("org.fcitx.Fcitx5", "/controller")
	ready := false
	for i := 0; i < 30; i++ {
		call := obj.Call("org.fcitx.Fcitx.Controller1.State", 0)
		if call.Err == nil {
			ready = true
			break
		}
		time.Sleep(1 * time.Second)
	}

	if !ready {
		return fmt.Errorf("fcitx5 not responding after 30 seconds")
	}

	imList := []IMItem{
		{Name: "keyboard-us", Layout: ""},
		{Name: "rime", Layout: ""},
	}

	if err := obj.Call("org.fcitx.Fcitx.Controller1.SetInputMethodGroupInfo", 0, "Default", "rime", imList).Err; err != nil {
		return fmt.Errorf("SetInputMethodGroupInfo: %w", err)
	}

	if err := obj.Call("org.fcitx.Fcitx.Controller1.Save", 0).Err; err != nil {
		return fmt.Errorf("Save: %w", err)
	}

	if err := obj.Call("org.fcitx.Fcitx.Controller1.SetCurrentIM", 0, "rime").Err; err != nil {
		// Non-fatal, just log
		fmt.Printf("Warning: SetCurrentIM failed: %v\n", err)
	}

	fmt.Println("[OK] Rime-Ice configured via D-Bus as default.")
	return nil
}
