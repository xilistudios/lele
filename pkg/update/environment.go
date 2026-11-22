package update

import (
	"os"
	"strings"
)

type UnsupportedEnvironmentError struct {
	Reason string
}

func (e *UnsupportedEnvironmentError) Error() string {
	return e.Reason
}

func CheckEnvironment() error {
	if isDocker() {
		return &UnsupportedEnvironmentError{
			Reason: "running inside Docker; update the image instead (docker pull / compose up)",
		}
	}
	return nil
}

func isDocker() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	data, err := os.ReadFile("/proc/1/cgroup")
	if err == nil {
		s := string(data)
		if strings.Contains(s, "docker") || strings.Contains(s, "containerd") || strings.Contains(s, "kubepods") {
			return true
		}
	}
	if os.Getenv("container") == "docker" || os.Getenv("container") == "podman" {
		return true
	}
	return false
}

func IsDevBuild(version string) bool {
	v := strings.TrimSpace(version)
	return v == "" || v == "dev" || strings.HasPrefix(v, "dev-")
}
