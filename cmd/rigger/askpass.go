package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/k0nsta/rigger/internal/askpass"
)

// askpass is the SUDO_ASKPASS helper side: sudo runs it with the prompt as argument and reads
// the password from stdout. It asks the rigger run that started the child.
func (c *cli) askpass(args []string) error {
	socket := os.Getenv(askpass.EnvSocket)
	if socket == "" {
		return errors.New("askpass: not started by rigger")
	}
	pw, err := askpass.Request(socket, strings.Join(args, " "))
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(c.out, pw)
	return err
}
