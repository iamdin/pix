//go:build !unix

package pix

import "os/exec"

func protectProcess(cmd *exec.Cmd) {}
