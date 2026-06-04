//go:build !darwin

package procenv

import (
	"os/exec"
	"strconv"
	"strings"
)

func lookup(pid int) (Info, error) {
	out, err := exec.Command("ps", "eww", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return Info{}, err
	}
	cmd := strings.TrimSpace(string(out))
	return Info{Command: cmd, Env: map[string]string{}}, nil
}
