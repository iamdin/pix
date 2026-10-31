//go:build !unix

package pitch

import "os/exec"

func protectProcess(cmd *exec.Cmd) {}
