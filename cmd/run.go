package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/locke-inc/directory-connector/internal/config"
	"github.com/locke-inc/directory-connector/internal/ldap"
	"github.com/locke-inc/directory-connector/internal/scim"
	"github.com/locke-inc/directory-connector/internal/service"
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
	if service.IsWindowsService() {
		return service.RunAsService(func(stop <-chan struct{}) error {
			return runDaemonLoop(stop)
		})
	}
	return runDaemonLoop(nil)
}

func runDaemonLoop(serviceStop <-chan struct{}) error {
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

	// Maintain a persistent LDAP connection with automatic reconnection
	var ldapClient *ldap.Client
	consecutiveFailures := 0

	ensureConnected := func() error {
		if ldapClient != nil && ldapClient.IsConnected() {
			return nil
		}

		if ldapClient != nil {
			log.Warn().Msg("LDAP connection lost, reconnecting...")
			if err := ldapClient.Reconnect(); err == nil {
				log.Info().Msg("LDAP reconnected successfully")
				consecutiveFailures = 0
				return nil
			}
			ldapClient.Close()
		}

		var err error
		ldapClient, err = ldap.NewClient(cfg.LDAP)
		if err != nil {
			consecutiveFailures++
			backoff := time.Duration(consecutiveFailures) * 30 * time.Second
			if backoff > 5*time.Minute {
				backoff = 5 * time.Minute
			}
			log.Error().Err(err).Int("consecutive_failures", consecutiveFailures).Dur("next_retry_backoff", backoff).Msg("LDAP connection failed")
			return err
		}
		consecutiveFailures = 0
		log.Info().Msg("LDAP connection established")
		return nil
	}

	incrementalTicker := time.NewTicker(incrementalInterval)
	fullSyncTicker := time.NewTicker(fullSyncInterval)
	defer incrementalTicker.Stop()
	defer fullSyncTicker.Stop()

	runOnce := func(full bool) {
		if err := ensureConnected(); err != nil {
			store.SetLastError(fmt.Sprintf("LDAP connection failed: %v", err))
			return
		}

		engine := sync.NewEngine(ldapClient, scimClient, store, cfg.Sync, cfg.Mapping, false)

		var result *sync.Result
		var syncErr error
		if full {
			log.Info().Msg("starting scheduled full sync")
			result, syncErr = engine.FullSync()
		} else {
			log.Debug().Msg("starting scheduled incremental sync")
			result, syncErr = engine.IncrementalSync()
		}

		if syncErr != nil {
			log.Error().Err(syncErr).Bool("full", full).Msg("sync failed")
			// If the sync failed due to a connection issue, invalidate the client
			// so the next cycle reconnects
			if ldapClient != nil && !ldapClient.IsConnected() {
				ldapClient.Close()
				ldapClient = nil
			}
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
			if ldapClient != nil {
				ldapClient.Close()
			}
			log.Info().Msg("daemon shutting down")
			return nil
		case <-sigCh:
			log.Info().Msg("received shutdown signal")
			cancel()
		case <-incrementalTicker.C:
			runOnce(false)
		case <-fullSyncTicker.C:
			runOnce(true)
		case <-stopChan(serviceStop):
			log.Info().Msg("service stop requested")
			cancel()
		}
	}
}

func stopChan(ch <-chan struct{}) <-chan struct{} {
	if ch == nil {
		return make(chan struct{})
	}
	return ch
}
