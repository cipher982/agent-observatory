package procenv

// Info is the command line and environment for a local process.
type Info struct {
	Command string
	Env     map[string]string
}

// Lookup returns best-effort command/env evidence for pid. Callers must treat
// missing or unreadable env as not trust-ready.
func Lookup(pid int) (Info, error) {
	info, err := lookup(pid)
	if info.Env == nil {
		info.Env = map[string]string{}
	}
	return info, err
}

func envMap(values []string) map[string]string {
	out := map[string]string{}
	for _, value := range values {
		for i := 0; i < len(value); i++ {
			if value[i] == '=' {
				if i > 0 {
					out[value[:i]] = value[i+1:]
				}
				break
			}
		}
	}
	return out
}
