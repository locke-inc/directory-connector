//go:build linux

package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"
)

const unitTemplate = `[Unit]
Description={{.Description}}
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart={{.ExecStart}}
Restart=on-failure
RestartSec=30
User={{.User}}
Group={{.Group}}
WorkingDirectory={{.WorkDir}}
Environment="LOCKE_SCIM_TOKEN={{.SCIMToken}}"
Environment="LDAP_BIND_PASSWORD={{.LDAPPassword}}"

# Hardening
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths={{.WorkDir}}
PrivateTmp=true

[Install]
WantedBy=multi-user.target
`

type unitParams struct {
	Description  string
	ExecStart    string
	User         string
	Group        string
	WorkDir      string
	SCIMToken    string
	LDAPPassword string
}

const unitPath = "/etc/systemd/system/" + ServiceName + ".service"

func Install(configPath string) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	execStart := exePath + " run"
	if configPath != "" {
		execStart += " --config " + configPath
	}

	workDir := filepath.Dir(exePath)
	if configPath != "" {
		workDir = filepath.Dir(configPath)
	}

	params := unitParams{
		Description: ServiceDescription,
		ExecStart:   execStart,
		User:        "locke-connector",
		Group:       "locke-connector",
		WorkDir:     workDir,
	}

	tmpl, err := template.New("unit").Parse(unitTemplate)
	if err != nil {
		return fmt.Errorf("failed to parse unit template: %w", err)
	}

	f, err := os.Create(unitPath)
	if err != nil {
		return fmt.Errorf("failed to write unit file (run as root): %w", err)
	}
	defer f.Close()

	if err := tmpl.Execute(f, params); err != nil {
		return fmt.Errorf("failed to render unit file: %w", err)
	}

	fmt.Printf("Unit file written to %s\n", unitPath)
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  1. Create service user:  useradd --system --no-create-home locke-connector")
	fmt.Println("  2. Set env vars in unit: systemctl edit " + ServiceName)
	fmt.Println("     [Service]")
	fmt.Println("     Environment=\"LOCKE_SCIM_TOKEN=your-token\"")
	fmt.Println("     Environment=\"LDAP_BIND_PASSWORD=your-password\"")
	fmt.Println("  3. Reload and start:")
	fmt.Println("     systemctl daemon-reload")
	fmt.Println("     systemctl enable --now " + ServiceName)

	return exec.Command("systemctl", "daemon-reload").Run()
}

func Uninstall() error {
	// Stop if running
	exec.Command("systemctl", "stop", ServiceName).Run()
	exec.Command("systemctl", "disable", ServiceName).Run()

	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove unit file: %w", err)
	}

	return exec.Command("systemctl", "daemon-reload").Run()
}

func Start() error {
	return exec.Command("systemctl", "start", ServiceName).Run()
}

func Stop() error {
	return exec.Command("systemctl", "stop", ServiceName).Run()
}

func IsWindowsService() bool {
	return false
}

func RunAsService(daemonFn func(stop <-chan struct{}) error) error {
	return fmt.Errorf("RunAsService is only supported on Windows")
}
