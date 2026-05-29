package tests

import (
	"encoding/base64"
	"testing"

	"github.com/locke-inc/directory-connector/internal/ldap"
)

func TestFormatObjectGUID(t *testing.T) {
	// Known test vector: a 16-byte GUID
	raw := []byte{
		0x01, 0x02, 0x03, 0x04, // first group (LE uint32 = 0x04030201)
		0x05, 0x06, // second group (LE uint16 = 0x0605)
		0x07, 0x08, // third group (LE uint16 = 0x0807)
		0x09, 0x0a, // fourth group (BE uint16 = 0x090a)
		0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10, // last 6 bytes
	}

	t.Run("base64 format", func(t *testing.T) {
		result := ldap.FormatObjectGUID(raw, "base64")
		expected := base64.StdEncoding.EncodeToString(raw)
		if result != expected {
			t.Errorf("got %q, want %q", result, expected)
		}
	})

	t.Run("uuid format", func(t *testing.T) {
		result := ldap.FormatObjectGUID(raw, "uuid")
		expected := "04030201-0605-0807-090a-0b0c0d0e0f10"
		if result != expected {
			t.Errorf("got %q, want %q", result, expected)
		}
	})

	t.Run("empty guid falls back to base64", func(t *testing.T) {
		result := ldap.FormatObjectGUID([]byte{0x01, 0x02}, "uuid")
		expected := base64.StdEncoding.EncodeToString([]byte{0x01, 0x02})
		if result != expected {
			t.Errorf("got %q, want %q", result, expected)
		}
	})
}
