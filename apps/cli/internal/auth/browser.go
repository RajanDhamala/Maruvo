package auth

import (
	"fmt"
	"os/exec"
	"runtime"
)

func OpenBrowser(link string) error {
	var command *exec.Cmd

	switch runtime.GOOS {
	case "linux":
		command = exec.Command("xdg-open", link)
	case "darwin":
		command = exec.Command("open", link)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", link)
	default:
		return fmt.Errorf("open the login URL in your browser")
	}

	if err := command.Start(); err != nil {
		return err
	}
	go command.Wait()

	return nil
}
