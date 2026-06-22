package cmd

import (
	"github.com/locke-inc/directory-connector/internal/config"
	"github.com/locke-inc/directory-connector/internal/ldap"
	"github.com/locke-inc/directory-connector/internal/scim"
	"github.com/locke-inc/directory-connector/internal/state"
	"github.com/locke-inc/directory-connector/internal/sync"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Run a one-shot sync from Active Directory to Locke",
	RunE:  runSync,
}

var (
	fullSync bool
	dryRun   bool
)

func init() {
	syncCmd.Flags().BoolVar(&fullSync, "full", false, "run a full reconciliation instead of incremental")
	syncCmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would happen without making SCIM calls")
	rootCmd.AddCommand(syncCmd)
}

func runSync(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	store, err := state.Open(cfg.State.Path)
	if err != nil {
		return err
	}
	defer store.Close()

	ldapClient, err := ldap.NewClient(cfg.LDAP)
	if err != nil {
		return err
	}
	defer ldapClient.Close()

	scimClient := scim.NewClient(cfg.Locke)

	engine := sync.NewEngine(ldapClient, scimClient, store, cfg.Sync, cfg.Mapping, dryRun)

	var result *sync.Result
	if fullSync {
		log.Info().Msg("starting full sync")
		result, err = engine.FullSync()
	} else {
		log.Info().Msg("starting incremental sync")
		result, err = engine.IncrementalSync()
	}

	if err != nil {
		log.Error().Err(err).Msg("sync failed")
		return err
	}

	log.Info().
		Int("created", result.Created).
		Int("updated", result.Updated).
		Int("disabled", result.Disabled).
		Int("deleted", result.Deleted).
		Int("skipped", result.Skipped).
		Int("errors", result.Errors).
		Msg("sync complete")

	// Report skipped users to API (triggers admin email notification)
	if len(result.SkippedUsers) > 0 {
		var reportUsers []scim.SyncReportUser
		for _, su := range result.SkippedUsers {
			reportUsers = append(reportUsers, scim.SyncReportUser{
				Username: su.Username,
				Email:    su.Email,
				Reason:   su.Reason,
			})
		}
		report := &scim.SyncReport{
			Created:      result.Created,
			Updated:      result.Updated,
			Disabled:     result.Disabled,
			Deleted:      result.Deleted,
			Skipped:      result.Skipped,
			Errors:       result.Errors,
			SkippedUsers: reportUsers,
		}
		if err := scimClient.ReportSyncResult(report); err != nil {
			log.Warn().Err(err).Msg("failed to send sync report to API")
		} else {
			log.Info().Int("skipped_users", len(reportUsers)).Msg("sync report sent — admin email triggered")
		}
	}

	return nil
}
