package cmd

import (
	"fmt"
	"time"

	"github.com/locke-inc/directory-connector/internal/config"
	"github.com/locke-inc/directory-connector/internal/state"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show sync status",
	RunE:  runStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

func runStatus(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	store, err := state.Open(cfg.State.Path)
	if err != nil {
		return err
	}
	defer store.Close()

	info, err := store.GetSyncInfo()
	if err != nil {
		return err
	}

	fmt.Printf("Locke Directory Connector Status\n")
	fmt.Printf("─────────────────────────────────\n")

	if info.LastSync.IsZero() {
		fmt.Printf("Last sync:       never\n")
	} else {
		ago := time.Since(info.LastSync).Round(time.Second)
		status := "success"
		if info.LastError != "" {
			status = fmt.Sprintf("failed: %s", info.LastError)
		}
		fmt.Printf("Last sync:       %s ago (%s)\n", ago, status)
	}

	fmt.Printf("Users synced:    %d\n", info.UserCount)
	fmt.Printf("Groups synced:   %d\n", info.GroupCount)
	fmt.Printf("High-water mark: %d\n", info.HighWaterMark)

	if !info.LastFullSync.IsZero() {
		ago := time.Since(info.LastFullSync).Round(time.Second)
		fmt.Printf("Last full sync:  %s ago\n", ago)
	}

	return nil
}
