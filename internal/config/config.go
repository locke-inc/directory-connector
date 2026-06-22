package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	Locke   LockeConfig   `mapstructure:"locke"`
	LDAP    LDAPConfig    `mapstructure:"ldap"`
	Sync    SyncConfig    `mapstructure:"sync"`
	Mapping MappingConfig `mapstructure:"mapping"`
	State   StateConfig   `mapstructure:"state"`
	Logging LoggingConfig `mapstructure:"logging"`
	Relay   RelayConfig   `mapstructure:"relay"`
}

type RelayConfig struct {
	Enabled        bool   `mapstructure:"enabled"`
	StreamEndpoint string `mapstructure:"stream_endpoint"`
	ResultEndpoint string `mapstructure:"result_endpoint"`
}

type LockeConfig struct {
	APIURL    string `mapstructure:"api_url"`
	SCIMToken string `mapstructure:"scim_token"`
	OrgID     string `mapstructure:"org_id"`
}

type LDAPConfig struct {
	Host          string `mapstructure:"host"`
	Port          int    `mapstructure:"port"`
	TLS           bool   `mapstructure:"tls"`
	TLSSkipVerify bool   `mapstructure:"tls_skip_verify"`
	Plaintext     bool   `mapstructure:"plaintext"`
	CACert        string `mapstructure:"ca_cert"`
	BindDN        string `mapstructure:"bind_dn"`
	BindPassword  string `mapstructure:"bind_password"`
	BaseDN        string `mapstructure:"base_dn"`
}

type SyncConfig struct {
	Interval         string   `mapstructure:"interval"`
	FullSyncInterval string   `mapstructure:"full_sync_interval"`
	UserFilter       string   `mapstructure:"user_filter"`
	GroupFilter      string   `mapstructure:"group_filter"`
	UserSearchBase   string   `mapstructure:"user_search_base"`
	GroupSearchBase  string   `mapstructure:"group_search_base"`
	GroupInclude     []string `mapstructure:"group_include"`
	GroupExclude     []string `mapstructure:"group_exclude"`
}

type MappingConfig struct {
	UserID       string `mapstructure:"user_id"`
	UserIDFormat string `mapstructure:"user_id_format"`
	Username     string `mapstructure:"username"`
	Email        string `mapstructure:"email"`
	FirstName    string `mapstructure:"first_name"`
	LastName     string `mapstructure:"last_name"`
	MemberOf     string `mapstructure:"member_of"`
}

type StateConfig struct {
	Path string `mapstructure:"path"`
}

type LoggingConfig struct {
	Level     string `mapstructure:"level"`
	File      string `mapstructure:"file"`
	MaxSizeMB int    `mapstructure:"max_size_mb"`
}

func Load() (*Config, error) {
	cfg := &Config{}

	setDefaults()

	if err := viper.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	applyEnvOverrides(cfg)
	applyRelayDefaults(cfg)

	if err := validate(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

func applyRelayDefaults(cfg *Config) {
	base := strings.TrimRight(cfg.Locke.APIURL, "/")
	if cfg.Relay.StreamEndpoint == "" {
		cfg.Relay.StreamEndpoint = base + "/connector/auth-stream"
	}
	if cfg.Relay.ResultEndpoint == "" {
		cfg.Relay.ResultEndpoint = base + "/connector/auth-result"
	}
}

func setDefaults() {
	viper.SetDefault("locke.api_url", "https://api.locke.id")
	viper.SetDefault("ldap.port", 636)
	viper.SetDefault("ldap.tls", true)
	viper.SetDefault("sync.interval", "5m")
	viper.SetDefault("sync.full_sync_interval", "6h")
	viper.SetDefault("sync.user_filter", "(&(objectClass=user)(objectCategory=person))")
	viper.SetDefault("sync.group_filter", "(objectClass=group)")
	viper.SetDefault("mapping.user_id", "objectGUID")
	viper.SetDefault("mapping.user_id_format", "base64")
	viper.SetDefault("mapping.username", "sAMAccountName")
	viper.SetDefault("mapping.email", "mail")
	viper.SetDefault("mapping.first_name", "givenName")
	viper.SetDefault("mapping.last_name", "sn")
	viper.SetDefault("mapping.member_of", "memberOf")
	viper.SetDefault("state.path", "./locke-connector.db")
	viper.SetDefault("logging.level", "info")
	viper.SetDefault("logging.max_size_mb", 50)
	viper.SetDefault("relay.enabled", true)
}

func applyEnvOverrides(cfg *Config) {
	if token := os.Getenv("LOCKE_SCIM_TOKEN"); token != "" {
		cfg.Locke.SCIMToken = token
	}
	if pw := os.Getenv("LDAP_BIND_PASSWORD"); pw != "" {
		cfg.LDAP.BindPassword = pw
	}
	if host := os.Getenv("LDAP_HOST"); host != "" {
		cfg.LDAP.Host = host
	}
}

func validate(cfg *Config) error {
	if cfg.LDAP.Host == "" {
		return fmt.Errorf("ldap.host is required")
	}
	if cfg.LDAP.BindDN == "" {
		return fmt.Errorf("ldap.bind_dn is required")
	}
	if cfg.LDAP.BindPassword == "" {
		return fmt.Errorf("ldap.bind_password is required (set LDAP_BIND_PASSWORD env var)")
	}
	if cfg.LDAP.BaseDN == "" {
		return fmt.Errorf("ldap.base_dn is required")
	}
	if cfg.Locke.SCIMToken == "" {
		return fmt.Errorf("locke.scim_token is required (set LOCKE_SCIM_TOKEN env var)")
	}
	if !cfg.LDAP.TLS && cfg.LDAP.Port == 636 {
		return fmt.Errorf("ldap.tls is false but port is 636 (LDAPS port) — use port 389 for StartTLS")
	}
	return nil
}
