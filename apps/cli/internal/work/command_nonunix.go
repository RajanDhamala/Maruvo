//go:build !unix

package work

import "os/exec"

func configureHarness(command *exec.Cmd) {}
