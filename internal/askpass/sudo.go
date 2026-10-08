package askpass

import (
	"bytes"
	"os"
	"os/exec"
	"syscall"
)

// SudoValidate checks a password with `sudo -S -k -v` (stdin, no cached ticket, no terminal).
func SudoValidate(password string) bool {
	cmd := exec.Command("/usr/bin/sudo", "-S", "-k", "-v", "-p", "")
	cmd.Stdin = bytes.NewBufferString(password + "\n")
	cmd.Env = append(os.Environ(), "SUDO_ASKPASS=")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Run() == nil
}
