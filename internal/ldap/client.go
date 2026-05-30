package ldap

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strconv"

	ber "github.com/go-asn1-ber/asn1-ber"
	"github.com/go-ldap/ldap/v3"
	"github.com/locke-inc/directory-connector/internal/config"
	"github.com/rs/zerolog/log"
)

type Client struct {
	conn   *ldap.Conn
	config config.LDAPConfig
}

func NewClient(cfg config.LDAPConfig) (*Client, error) {
	c := &Client{config: cfg}
	if err := c.connect(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) connect() error {
	address := fmt.Sprintf("%s:%d", c.config.Host, c.config.Port)

	if c.config.TLSSkipVerify {
		log.Warn().Msg("TLS certificate verification is DISABLED — vulnerable to MITM attacks. Only use for testing with self-signed certs.")
	}

	tlsConfig := &tls.Config{
		InsecureSkipVerify: c.config.TLSSkipVerify,
		ServerName:         c.config.Host,
	}

	if c.config.CACert != "" {
		caCert, err := os.ReadFile(c.config.CACert)
		if err != nil {
			return fmt.Errorf("failed to read CA cert: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caCert) {
			return fmt.Errorf("failed to parse CA cert")
		}
		tlsConfig.RootCAs = pool
	}

	var conn *ldap.Conn
	var err error

	if c.config.TLS {
		// LDAPS — TLS from the start (port 636)
		conn, err = ldap.DialTLS("tcp", address, tlsConfig)
	} else {
		// StartTLS — plain connect then upgrade (port 389)
		conn, err = ldap.Dial("tcp", address)
		if err == nil {
			err = conn.StartTLS(tlsConfig)
		}
	}

	if err != nil {
		return fmt.Errorf("failed to connect to LDAP server %s: %w", address, err)
	}

	// Simple bind with the service account
	if err := conn.Bind(c.config.BindDN, c.config.BindPassword); err != nil {
		conn.Close()
		return fmt.Errorf("LDAP bind failed (check service account credentials): %w", err)
	}

	log.Debug().Str("host", c.config.Host).Msg("LDAP connection established")
	c.conn = conn
	return nil
}

func (c *Client) Close() {
	if c.conn != nil {
		c.conn.Close()
	}
}

func (c *Client) Reconnect() error {
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	return c.connect()
}

func (c *Client) IsConnected() bool {
	if c.conn == nil {
		return false
	}
	// Attempt a lightweight RootDSE query to verify the connection is alive
	searchReq := ldap.NewSearchRequest(
		"",
		ldap.ScopeBaseObject,
		ldap.NeverDerefAliases,
		0, 0, false,
		"(objectClass=*)",
		[]string{"defaultNamingContext"},
		nil,
	)
	_, err := c.conn.Search(searchReq)
	return err == nil
}

func (c *Client) BaseDN() string {
	return c.config.BaseDN
}

func (c *Client) SearchUsers(baseDN, filter string, attributes []string) ([]*ldap.Entry, error) {
	return c.pagedSearch(baseDN, filter, attributes)
}

func (c *Client) SearchChangedUsers(baseDN, filter string, attributes []string, highWaterMark int64) ([]*ldap.Entry, error) {
	usnFilter := fmt.Sprintf("(&%s(uSNChanged>=%d))", filter, highWaterMark+1)
	return c.pagedSearch(baseDN, usnFilter, attributes)
}

func (c *Client) SearchDeletedObjects(baseDN string, highWaterMark int64) ([]*ldap.Entry, error) {
	filter := fmt.Sprintf("(&(isDeleted=TRUE)(uSNChanged>=%d))", highWaterMark+1)
	deletedDN := fmt.Sprintf("CN=Deleted Objects,%s", baseDN)

	searchReq := ldap.NewSearchRequest(
		deletedDN,
		ldap.ScopeWholeSubtree,
		ldap.DerefAlways,
		0, 0, false,
		filter,
		[]string{"objectGUID", "sAMAccountName", "uSNChanged"},
		[]ldap.Control{&showDeletedControl{}},
	)

	result, err := c.conn.Search(searchReq)
	if err != nil {
		return nil, fmt.Errorf("tombstone search failed: %w", err)
	}
	return result.Entries, nil
}

func (c *Client) SearchGroups(baseDN, filter string) ([]*ldap.Entry, error) {
	attributes := []string{"objectGUID", "cn", "member", "uSNChanged"}
	return c.pagedSearch(baseDN, filter, attributes)
}

func (c *Client) pagedSearch(baseDN, filter string, attributes []string) ([]*ldap.Entry, error) {
	const pageSize = 1000

	searchReq := ldap.NewSearchRequest(
		baseDN,
		ldap.ScopeWholeSubtree,
		ldap.DerefAlways,
		0, 0, false,
		filter,
		attributes,
		nil,
	)

	result, err := c.conn.SearchWithPaging(searchReq, uint32(pageSize))
	if err != nil {
		return nil, fmt.Errorf("LDAP search failed (base=%s, filter=%s): %w", baseDN, filter, err)
	}

	log.Debug().
		Str("base", baseDN).
		Int("results", len(result.Entries)).
		Msg("LDAP search complete")

	return result.Entries, nil
}

func (c *Client) GetHighestUSN(baseDN string) (int64, error) {
	// Query RootDSE for highestCommittedUSN
	searchReq := ldap.NewSearchRequest(
		"",
		ldap.ScopeBaseObject,
		ldap.NeverDerefAliases,
		0, 0, false,
		"(objectClass=*)",
		[]string{"highestCommittedUSN"},
		nil,
	)

	result, err := c.conn.Search(searchReq)
	if err != nil {
		return 0, fmt.Errorf("failed to query RootDSE: %w", err)
	}

	if len(result.Entries) == 0 {
		return 0, fmt.Errorf("RootDSE returned no entries")
	}

	usnStr := result.Entries[0].GetAttributeValue("highestCommittedUSN")
	if usnStr == "" {
		return 0, fmt.Errorf("highestCommittedUSN not found in RootDSE")
	}

	usn, err := strconv.ParseInt(usnStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid highestCommittedUSN value: %s", usnStr)
	}

	return usn, nil
}

// showDeletedControl returns the LDAP control for searching deleted objects (tombstones).
// OID: 1.2.840.113556.1.4.417
type showDeletedControl struct{}

func (c *showDeletedControl) GetControlType() string {
	return "1.2.840.113556.1.4.417"
}

func (c *showDeletedControl) Encode() *ber.Packet {
	p := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "Control")
	p.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "1.2.840.113556.1.4.417", "Control Type"))
	p.AppendChild(ber.NewBoolean(ber.ClassUniversal, ber.TypePrimitive, ber.TagBoolean, true, "Criticality"))
	return p
}

func (c *showDeletedControl) String() string {
	return "Show Deleted Objects Control (1.2.840.113556.1.4.417)"
}
