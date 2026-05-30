package cmd

import (
	"fmt"

	"github.com/locke-inc/directory-connector/internal/service"
	"github.com/spf13/cobra"
)

var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: "Manage the Locke Directory Connector as a system service",
}

var serviceInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install as a system service (Windows Service or systemd unit)",
	RunE: func(cmd *cobra.Command, args []string) error {
		configPath, _ := cmd.Flags().GetString("config-path")
		if err := service.Install(configPath); err != nil {
			return err
		}
		fmt.Printf("Service %q installed successfully.\n", service.ServiceName)
		return nil
	},
}

var serviceUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove the system service",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := service.Uninstall(); err != nil {
			return err
		}
		fmt.Printf("Service %q removed.\n", service.ServiceName)
		return nil
	},
}

var serviceStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the system service",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := service.Start(); err != nil {
			return err
		}
		fmt.Printf("Service %q started.\n", service.ServiceName)
		return nil
	},
}

var serviceStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the system service",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := service.Stop(); err != nil {
			return err
		}
		fmt.Printf("Service %q stopped.\n", service.ServiceName)
		return nil
	},
}

func init() {
	serviceInstallCmd.Flags().String("config-path", "", "absolute path to config file (embedded in service definition)")
	serviceCmd.AddCommand(serviceInstallCmd)
	serviceCmd.AddCommand(serviceUninstallCmd)
	serviceCmd.AddCommand(serviceStartCmd)
	serviceCmd.AddCommand(serviceStopCmd)
	rootCmd.AddCommand(serviceCmd)
}
