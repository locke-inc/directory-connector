package cmd

import (
	"io"
	"os"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/natefinch/lumberjack.v2"
)

var cfgFile string

var rootCmd = &cobra.Command{
	Use:   "locke-connector",
	Short: "Locke Directory Connector — sync Active Directory users to Locke via SCIM",
	Long: `A lightweight agent that reads users and groups from on-premises Active Directory
via LDAP and provisions them to Locke's SCIM endpoints. No cloud intermediary required.`,
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	cobra.OnInitialize(initConfig)
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default ./locke-connector.yaml)")
	rootCmd.PersistentFlags().String("log-level", "", "log level override (debug, info, warn, error)")
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		viper.SetConfigName("locke-connector")
		viper.SetConfigType("yaml")
		viper.AddConfigPath(".")
		viper.AddConfigPath("/etc/locke-connector/")
	}

	viper.SetEnvPrefix("LOCKE_CONNECTOR")
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			log.Fatal().Err(err).Msg("failed to read config file")
		}
	}

	// Determine log level: CLI flag > config file > default "info"
	level, _ := rootCmd.PersistentFlags().GetString("log-level")
	if level == "" {
		level = viper.GetString("logging.level")
	}
	if level == "" {
		level = "info"
	}

	logFile := viper.GetString("logging.file")
	maxSizeMB := viper.GetInt("logging.max_size_mb")
	if maxSizeMB == 0 {
		maxSizeMB = 50
	}

	setupLogging(level, logFile, maxSizeMB)
}

func setupLogging(level, file string, maxSizeMB int) {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnixMs

	switch level {
	case "debug":
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	case "warn":
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	case "error":
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	default:
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}

	var writer io.Writer = os.Stdout

	if file != "" {
		fileWriter := &lumberjack.Logger{
			Filename:   file,
			MaxSize:    maxSizeMB,
			MaxBackups: 5,
			MaxAge:     30,
			Compress:   true,
		}
		// Write to both file and stdout for daemon visibility
		writer = io.MultiWriter(os.Stdout, fileWriter)
	}

	log.Logger = zerolog.New(writer).With().Timestamp().Str("component", "locke-connector").Logger()
}
