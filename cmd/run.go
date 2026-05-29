package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/locke-inc/directory-connector/internal/config"
	"github.com/locke-inc/directory-connector/internal/ldap"
	"github.com/locke-inc/directory-connector/internal/scim"
	"github.com/locke-inc/directory-connector/internal/state"
	"github.com/locke-inc/directory-connector/internal/sync"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run as a daemon, syncing on a schedule",
	RunE:  runDaemon,
}

func init() {
	rootCmd.AddCommand(runCmd)
}

func runDaemon(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	store, err := state.Open(cfg.State.Path)
	if err != nil {
		return err
	}
	defer store.Close()

	scimClient := scim.NewClient(cfg.Locke)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	incrementalInterval, err := time.ParseDuration(cfg.Sync.Interval)
	if err != nil {
		incrementalInterval = 5 * time.Minute
	}

	fullSyncInterval, err := time.ParseDuration(cfg.Sync.FullSyncInterval)
	if err != nil {
		fullSyncInterval = 6 * time.Hour
	}

	log.Info().
		Dur("incremental_interval", incrementalInterval).
		Dur("full_sync_interval", fullSyncInterval).
		Msg("daemon started")

	incrementalTicker := time.NewTicker(incrementalInterval)
	fullSyncTicker := time.NewTicker(fullSyncInterval)
	defer incrementalTicker.Stop()
	defer fullSyncTicker.Stop()

	runOnce := func(full bool) {
		ldapClient, err := ldap.NewClient(cfg.LDAP)
		if err != nil {
			log.Error().Err(err).Msg("failed to connect to LDAP")
			return
		}
		defer ldapClient.Close()

		engine := sync.NewEngine(ldapClient, scimClient, store, cfg.Sync, cfg.Mapping, false)

		var result *sync.Result
		if full {
			log.Info().Msg("starting scheduled full sync")
			result, err = engine.FullSync()
		} else {
			log.Debug().Msg("starting scheduled incremental sync")
			result, err = engine.IncrementalSync()
		}

		if err != nil {
			log.Error().Err(err).Bool("full", full).Msg("sync failed")
			return
		}

		log.Info().
			Bool("full", full).
			Int("created", result.Created).
			Int("updated", result.Updated).
			Int("disabled", result.Disabled).
			Int("deleted", result.Deleted).
			Int("errors", result.Errors).
			Msg("sync complete")
	}

	// Run an initial incremental sync immediately
	runOnce(false)

	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("daemon shutting down")
			return nil
		case <-sigCh:
			log.Info().Msg("received shutdown signal")
			cancel()
		case <-incrementalTicker.C:
			runOnce(false)
		case <-fullSyncTicker.C:
			runOnce(true)
		}
	}
}
