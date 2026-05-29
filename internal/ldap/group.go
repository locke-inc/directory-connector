package ldap

import (
	"strconv"

	goldap "github.com/go-ldap/ldap/v3"
)

type ADGroup struct {
	ObjectGUID        string
	CN                string
	Members           []string
	USNChanged        int64
	DistinguishedName string
}

func ExtractGroup(entry *goldap.Entry, idFormat string) *ADGroup {
	group := &ADGroup{
		CN:                entry.GetAttributeValue("cn"),
		Members:           entry.GetAttributeValues("member"),
		DistinguishedName: entry.DN,
	}

	guidBytes := entry.GetRawAttributeValue("objectGUID")
	if len(guidBytes) == 16 {
		group.ObjectGUID = FormatObjectGUID(guidBytes, idFormat)
	}

	usnStr := entry.GetAttributeValue("uSNChanged")
	if usnStr != "" {
		group.USNChanged, _ = strconv.ParseInt(usnStr, 10, 64)
	}

	return group
}
