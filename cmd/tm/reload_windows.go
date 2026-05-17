//go:build windows

package main

import (
	"fmt"
	"os"
)

func runReload() error {
	if maybePrintCmdHelp("reload", os.Args[2:]) {
		return nil
	}
	return fmt.Errorf("reload is not supported on Windows; stop and restart the daemon instead")
}
