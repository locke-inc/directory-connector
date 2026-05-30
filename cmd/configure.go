package cmd

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/locke-inc/directory-connector/internal/config"
	"github.com/locke-inc/directory-connector/internal/ldap"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var configureCmd = &cobra.Command{
	Use:   "configure",
	Short: "Interactive setup wizard — configure LDAP and SCIM connection",
	RunE:  runConfigure,
}

func init() {
	rootCmd.AddCommand(configureCmd)
}

func runConfigure(cmd *cobra.Command, args []string) error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("┌──────────────────────────────────────────────────────┐")
	fmt.Println("│       Locke Directory Connector — Setup Wizard       │")
	fmt.Println("└──────────────────────────────────────────────────────┘")
	fmt.Println()

	// Step 1: LDAP configuration
	fmt.Println("── LDAP Configuration ──")
	fmt.Println()

	host := prompt(scanner, "LDAP host (domain controller)", "dc01.acme.local")
	portStr := prompt(scanner, "LDAP port", "636")
	port, err := strconv.Atoi(portStr)
	if err != nil {
		port = 636
	}

	useTLS := port == 636
	if port != 636 {
		tlsAnswer := prompt(scanner, "Use StartTLS? (y/n)", "y")
		useTLS = strings.ToLower(tlsAnswer) != "n"
	}

	caCert := prompt(scanner, "Path to CA certificate (leave empty for system CAs)", "")

	bindDN := prompt(scanner, "Bind DN (service account)", "CN=Locke Sync,OU=Service Accounts,DC=acme,DC=local")
	fmt.Print("  Bind password: ")
	bindPassword := readPassword(scanner)
	fmt.Println()

	baseDN := prompt(scanner, "Base DN", inferBaseDN(bindDN))

	// Step 2: Test LDAP connection
	fmt.Println()
	fmt.Println("── Testing LDAP Connection ──")
	fmt.Println()

	ldapCfg := config.LDAPConfig{
		Host:         host,
		Port:         port,
		TLS:          useTLS,
		CACert:       caCert,
		BindDN:       bindDN,
		BindPassword: bindPassword,
		BaseDN:       baseDN,
	}

	ldapClient, err := ldap.NewClient(ldapCfg)
	if err != nil {
		fmt.Printf("  ✗ LDAP connection failed: %v\n", err)
		fmt.Println()
		fmt.Println("  Check: host/port reachable? bind DN correct? password correct? TLS/certs valid?")
		return fmt.Errorf("LDAP connection test failed")
	}
	fmt.Printf("  ✓ Connected to %s:%d\n", host, port)

	// Test user search
	userSearchBase := prompt(scanner, "User search base (OU)", baseDN)
	userFilter := prompt(scanner, "User filter", "(&(objectClass=user)(objectCategory=person))")

	entries, err := ldapClient.SearchUsers(userSearchBase, userFilter, ldap.UserAttributes())
	if err != nil {
		fmt.Printf("  ✗ User search failed: %v\n", err)
		ldapClient.Close()
		return fmt.Errorf("LDAP user search test failed")
	}
	fmt.Printf("  ✓ Found %d users in %s\n", len(entries), userSearchBase)

	// Test tombstone access
	_, err = ldapClient.SearchDeletedObjects(baseDN, 0)
	tombstoneAccess := err == nil
	if tombstoneAccess {
		fmt.Println("  ✓ Deleted Objects container accessible (real-time delete detection enabled)")
	} else {
		fmt.Println("  ⚠ Cannot read Deleted Objects container. Hard-delete detection will rely on")
		fmt.Println("    full sync reconciliation (every 6h) instead of real-time tombstone polling.")
		fmt.Println("    To enable real-time delete detection, grant read access to:")
		fmt.Printf("    CN=Deleted Objects,%s\n", baseDN)
	}

	ldapClient.Close()

	// Step 3: Locke SCIM configuration
	fmt.Println()
	fmt.Println("── Locke SCIM Configuration ──")
	fmt.Println()

	apiURL := prompt(scanner, "Locke API URL", "https://api.locke.id")
	fmt.Print("  SCIM token: ")
	scimToken := readPassword(scanner)
	fmt.Println()

	// Extract org ID from token
	orgID := extractOrgID(scimToken)
	if orgID != "" {
		fmt.Printf("  ✓ Organization: %s\n", orgID)
	}

	// Step 4: Test SCIM connection
	fmt.Println()
	fmt.Println("── Testing SCIM Connection ──")
	fmt.Println()

	scimValid := testSCIMToken(apiURL, scimToken)
	if scimValid {
		fmt.Println("  ✓ SCIM token valid")
	} else {
		fmt.Println("  ✗ SCIM token validation failed")
		fmt.Println("    Check: token correct? API URL reachable? org exists?")
		return fmt.Errorf("SCIM token validation failed")
	}

	// Step 5: Sync settings
	fmt.Println()
	fmt.Println("── Sync Settings ──")
	fmt.Println()

	syncInterval := prompt(scanner, "Incremental sync interval", "5m")
	fullSyncInterval := prompt(scanner, "Full sync interval", "6h")
	groupSearchBase := prompt(scanner, "Group search base (OU, leave empty to use base DN)", "")
	statePath := prompt(scanner, "State database path", "./locke-connector.db")

	// Step 6: Write config
	fmt.Println()
	fmt.Println("── Writing Configuration ──")
	fmt.Println()

	cfg := buildConfigYAML(configParams{
		apiURL:           apiURL,
		scimToken:        "",
		orgID:            orgID,
		host:             host,
		port:             port,
		tls:              useTLS,
		caCert:           caCert,
		bindDN:           bindDN,
		baseDN:           baseDN,
		syncInterval:     syncInterval,
		fullSyncInterval: fullSyncInterval,
		userFilter:       userFilter,
		userSearchBase:   userSearchBase,
		groupSearchBase:  groupSearchBase,
		statePath:        statePath,
	})

	outputPath := "locke-connector.yaml"
	if err := os.WriteFile(outputPath, cfg, 0600); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}
	fmt.Printf("  ✓ Config written to %s (mode 0600)\n", outputPath)
	fmt.Println()
	fmt.Println("  ╭───────────────────────────────────────────────────────────────╮")
	fmt.Println("  │ Next steps:                                                   │")
	fmt.Println("  │   1. Set environment variables:                               │")
	fmt.Println("  │      export LOCKE_SCIM_TOKEN='your-token-here'                │")
	fmt.Println("  │      export LDAP_BIND_PASSWORD='your-password-here'           │")
	fmt.Println("  │   2. Test with dry run:   locke-connector sync --dry-run      │")
	fmt.Println("  │   3. Run first sync:      locke-connector sync --full         │")
	fmt.Println("  │   4. Start daemon:        locke-connector run                 │")
	fmt.Println("  ╰───────────────────────────────────────────────────────────────╯")
	fmt.Println()

	return nil
}

func prompt(scanner *bufio.Scanner, label, defaultVal string) string {
	if defaultVal != "" {
		fmt.Printf("  %s [%s]: ", label, defaultVal)
	} else {
		fmt.Printf("  %s: ", label)
	}
	scanner.Scan()
	val := strings.TrimSpace(scanner.Text())
	if val == "" {
		return defaultVal
	}
	return val
}

func readPassword(scanner *bufio.Scanner) string {
	scanner.Scan()
	return strings.TrimSpace(scanner.Text())
}

func inferBaseDN(bindDN string) string {
	parts := strings.Split(bindDN, ",")
	var dcParts []string
	for _, p := range parts {
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(p)), "DC=") {
			dcParts = append(dcParts, strings.TrimSpace(p))
		}
	}
	if len(dcParts) > 0 {
		return strings.Join(dcParts, ",")
	}
	return ""
}

func extractOrgID(token string) string {
	// Token format: locke_scim_{org_id}_{secret}
	parts := strings.Split(token, "_")
	if len(parts) >= 4 && parts[0] == "locke" && parts[1] == "scim" {
		return parts[2]
	}
	return ""
}

func testSCIMToken(apiURL, token string) bool {
	url := strings.TrimRight(apiURL, "/") + "/scim/v2/Users?count=0"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/scim+json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()

	return resp.StatusCode == 200
}

type configParams struct {
	apiURL           string
	scimToken        string
	orgID            string
	host             string
	port             int
	tls              bool
	caCert           string
	bindDN           string
	baseDN           string
	syncInterval     string
	fullSyncInterval string
	userFilter       string
	userSearchBase   string
	groupSearchBase  string
	statePath        string
}

func buildConfigYAML(p configParams) []byte {
	cfg := map[string]interface{}{
		"locke": map[string]interface{}{
			"api_url":    p.apiURL,
			"scim_token": "",
			"org_id":     p.orgID,
		},
		"ldap": map[string]interface{}{
			"host":            p.host,
			"port":            p.port,
			"tls":             p.tls,
			"tls_skip_verify": false,
			"ca_cert":         p.caCert,
			"bind_dn":         p.bindDN,
			"bind_password":   "",
			"base_dn":         p.baseDN,
		},
		"sync": map[string]interface{}{
			"interval":           p.syncInterval,
			"full_sync_interval": p.fullSyncInterval,
			"user_filter":        p.userFilter,
			"group_filter":       "(objectClass=group)",
			"user_search_base":   p.userSearchBase,
			"group_search_base":  p.groupSearchBase,
			"group_include":      []string{},
			"group_exclude":      []string{"CN=Domain Controllers,*", "CN=Domain Computers,*"},
		},
		"mapping": map[string]interface{}{
			"user_id":        "objectGUID",
			"user_id_format": "base64",
			"username":       "sAMAccountName",
			"email":          "mail",
			"first_name":     "givenName",
			"last_name":      "sn",
			"member_of":      "memberOf",
		},
		"state": map[string]interface{}{
			"path": p.statePath,
		},
		"logging": map[string]interface{}{
			"level":       "info",
			"file":        "",
			"max_size_mb": 50,
		},
	}

	out, _ := yaml.Marshal(cfg)

	header := "# Locke Directory Connector configuration\n" +
		"# Generated by: locke-connector configure\n" +
		"# Secrets: set LOCKE_SCIM_TOKEN and LDAP_BIND_PASSWORD as environment variables\n\n"

	return []byte(header + string(out))
}
