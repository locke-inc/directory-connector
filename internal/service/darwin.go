//go:build darwin

package service

import "fmt"

func Install(configPath string) error {
	return fmt.Errorf("service install is not supported on macOS — use systemd (Linux) or Windows Service")
}

func Uninstall() error {
	return fmt.Errorf("service uninstall is not supported on macOS")
}

func Start() error {
	return fmt.Errorf("service start is not supported on macOS — run the daemon directly: locke-connector run")
}

func Stop() error {
	return fmt.Errorf("service stop is not supported on macOS")
}

func IsWindowsService() bool {
	return false
}

func RunAsService(daemonFn func(stop <-chan struct{}) error) error {
	return fmt.Errorf("RunAsService is only supported on Windows")
}
