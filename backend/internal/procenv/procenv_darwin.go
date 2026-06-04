//go:build darwin

package procenv

import (
	"encoding/binary"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

const (
	ctlKern         = 1
	kernProcargs2ID = 49
)

func lookup(pid int) (Info, error) {
	buf, err := readKernProcargs2(pid)
	if err == nil {
		if info, parseErr := parseProcargs2(buf); parseErr == nil {
			return info, nil
		}
	}
	return lookupViaPS(pid)
}

func readKernProcargs2(pid int) ([]byte, error) {
	argmax, err := syscall.SysctlUint32("kern.argmax")
	if err != nil || argmax == 0 {
		argmax = 256 * 1024
	}
	buf := make([]byte, int(argmax))
	n := uintptr(len(buf))
	mib := [...]int32{ctlKern, kernProcargs2ID, int32(pid)}
	_, _, errno := syscall.Syscall6(
		syscall.SYS___SYSCTL,
		uintptr(unsafe.Pointer(&mib[0])),
		uintptr(len(mib)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&n)),
		0,
		0,
	)
	if errno != 0 {
		return nil, errno
	}
	return buf[:n], nil
}

func parseProcargs2(buf []byte) (Info, error) {
	if len(buf) < 4 {
		return Info{}, errors.New("procargs2 buffer too short")
	}
	argc := int(binary.LittleEndian.Uint32(buf[:4]))
	rest := buf[4:]
	exe, i, ok := nextCString(rest, 0)
	if !ok {
		return Info{}, errors.New("procargs2 missing executable")
	}
	i = skipNUL(rest, i)

	args := make([]string, 0, argc)
	for len(args) < argc && i < len(rest) {
		var arg string
		arg, i, ok = nextCString(rest, i)
		if !ok {
			break
		}
		if arg == "" {
			i = skipNUL(rest, i)
			continue
		}
		args = append(args, arg)
	}
	i = skipNUL(rest, i)

	env := make([]string, 0)
	for i < len(rest) {
		var value string
		value, i, ok = nextCString(rest, i)
		if !ok {
			break
		}
		if value != "" {
			env = append(env, value)
		}
		i = skipNUL(rest, i)
	}

	cmd := strings.Join(args, " ")
	if cmd == "" {
		cmd = exe
	}
	return Info{Command: cmd, Env: envMap(env)}, nil
}

func nextCString(buf []byte, start int) (string, int, bool) {
	if start >= len(buf) {
		return "", start, false
	}
	for i := start; i < len(buf); i++ {
		if buf[i] == 0 {
			return string(buf[start:i]), i + 1, true
		}
	}
	return "", len(buf), false
}

func skipNUL(buf []byte, i int) int {
	for i < len(buf) && buf[i] == 0 {
		i++
	}
	return i
}

func lookupViaPS(pid int) (Info, error) {
	out, err := exec.Command("ps", "eww", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return Info{}, err
	}
	cmd := strings.TrimSpace(string(out))
	return Info{Command: cmd, Env: map[string]string{}}, nil
}
