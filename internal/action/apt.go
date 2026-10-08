package action

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/k0nsta/rigger/internal/sh"
)

// Apt is a list of Debian/Ubuntu packages.
type Apt struct {
	Packages []string
}

// AptProbe is the probed state of an apt list.
type AptProbe struct {
	Status  Status
	Detail  string
	Missing []string
}

// Probe asks dpkg which packages are installed.
func (a Apt) Probe(ctx context.Context, r Runner) (AptProbe, error) {
	if len(a.Packages) == 0 {
		return AptProbe{Status: OK}, nil
	}
	args := append([]string{"-W", "-f=${Package} ${db:Status-Abbrev}\n"}, a.Packages...)
	res, err := r.Run(ctx, sh.Cmd{Path: "dpkg-query", Args: args})
	if err != nil {
		return AptProbe{}, err
	}
	installed := map[string]bool{}
	for _, line := range sh.Lines(res.Output) {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.HasPrefix(fields[1], "ii") {
			installed[strings.SplitN(fields[0], ":", 2)[0]] = true
		}
	}
	var p AptProbe
	for _, pkg := range a.Packages {
		if !installed[pkg] {
			p.Missing = append(p.Missing, pkg)
		}
	}
	if len(p.Missing) == 0 {
		p.Status = OK
	} else {
		p.Status = Todo
		p.Detail = fmt.Sprintf("%d to install", len(p.Missing))
	}
	return p, nil
}

// AsRoot reports whether rigger runs as root (containers): apt then runs without sudo.
var AsRoot = func() bool { return os.Geteuid() == 0 }

// AptInstall installs packages in one batch; sudo asks through SUDO_ASKPASS (see internal/askpass).
func AptInstall(ctx context.Context, r Runner, packages []string, onLine func(string)) error {
	if len(packages) == 0 {
		return nil
	}
	env := []string{"DEBIAN_FRONTEND=noninteractive"}
	for _, step := range [][]string{
		{"apt-get", "update", "-q"},
		append([]string{"apt-get", "install", "-y", "-q"}, packages...),
	} {
		c := sh.Cmd{Path: "sudo", Args: step, Env: env, OnLine: onLine}
		if AsRoot() {
			c = sh.Cmd{Path: step[0], Args: step[1:], Env: env, OnLine: onLine}
		}
		res, err := r.Run(ctx, c)
		if err != nil {
			return err
		}
		if res.Code != 0 {
			return fmt.Errorf("%s failed (exit %d)", strings.Join(step[:2], " "), res.Code)
		}
	}
	return nil
}
