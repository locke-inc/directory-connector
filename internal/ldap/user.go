package ldap

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"

	goldap "github.com/go-ldap/ldap/v3"
)

type ADUser struct {
	ObjectGUID         string
	SAMAccountName     string
	Email              string
	FirstName          string
	LastName           string
	Disabled           bool
	MemberOf           []string
	USNChanged         int64
	DistinguishedName  string
}

func ExtractUser(entry *goldap.Entry, idFormat string) *ADUser {
	user := &ADUser{
		SAMAccountName:    entry.GetAttributeValue("sAMAccountName"),
		Email:             entry.GetAttributeValue("mail"),
		FirstName:         entry.GetAttributeValue("givenName"),
		LastName:          entry.GetAttributeValue("sn"),
		MemberOf:          entry.GetAttributeValues("memberOf"),
		DistinguishedName: entry.DN,
	}

	// Extract objectGUID (raw binary)
	guidBytes := entry.GetRawAttributeValue("objectGUID")
	if len(guidBytes) == 16 {
		user.ObjectGUID = FormatObjectGUID(guidBytes, idFormat)
	}

	// Parse userAccountControl to detect disabled accounts
	uacStr := entry.GetAttributeValue("userAccountControl")
	if uacStr != "" {
		uac, err := strconv.ParseInt(uacStr, 10, 64)
		if err == nil {
			user.Disabled = (uac & 0x2) != 0 // bit 2 = ACCOUNTDISABLE
		}
	}

	// Parse uSNChanged
	usnStr := entry.GetAttributeValue("uSNChanged")
	if usnStr != "" {
		user.USNChanged, _ = strconv.ParseInt(usnStr, 10, 64)
	}

	return user
}

func FormatObjectGUID(raw []byte, format string) string {
	if len(raw) != 16 {
		return base64.StdEncoding.EncodeToString(raw)
	}

	switch strings.ToLower(format) {
	case "uuid":
		// Microsoft byte-order: first 3 groups are little-endian
		return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
			binary.LittleEndian.Uint32(raw[0:4]),
			binary.LittleEndian.Uint16(raw[4:6]),
			binary.LittleEndian.Uint16(raw[6:8]),
			binary.BigEndian.Uint16(raw[8:10]),
			raw[10:16])
	default: // "base64"
		return base64.StdEncoding.EncodeToString(raw)
	}
}

func UserAttributes() []string {
	return []string{
		"objectGUID",
		"sAMAccountName",
		"mail",
		"givenName",
		"sn",
		"userAccountControl",
		"memberOf",
		"uSNChanged",
		"distinguishedName",
	}
}
