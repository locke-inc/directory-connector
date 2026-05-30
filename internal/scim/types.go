package scim

import "fmt"

type SCIMUser struct {
	Schemas    []string   `json:"schemas"`
	UserName   string     `json:"userName"`
	ExternalID string     `json:"externalId,omitempty"`
	Name       SCIMName   `json:"name"`
	Emails     []SCIMEmail `json:"emails"`
	Active     bool       `json:"active"`
}

type SCIMName struct {
	GivenName  string `json:"givenName"`
	FamilyName string `json:"familyName"`
}

type SCIMEmail struct {
	Value   string `json:"value"`
	Primary bool   `json:"primary"`
}

type SCIMPatchOp struct {
	Schemas    []string        `json:"schemas"`
	Operations []SCIMOperation `json:"Operations"`
}

type SCIMOperation struct {
	Op    string      `json:"op"`
	Path  string      `json:"path,omitempty"`
	Value interface{} `json:"value"`
}

type SCIMGroupMember struct {
	Value string `json:"value"`
}

type SCIMError struct {
	StatusCode int
	Body       string
}

func (e *SCIMError) Error() string {
	return fmt.Sprintf("SCIM error %d: %s", e.StatusCode, e.Body)
}

type SCIMGroup struct {
	Schemas     []string          `json:"schemas,omitempty"`
	ID          string            `json:"id,omitempty"`
	DisplayName string            `json:"displayName"`
	ExternalID  string            `json:"externalId,omitempty"`
	Members     []SCIMGroupMember `json:"members,omitempty"`
}

type SCIMListResponse struct {
	TotalResults int         `json:"totalResults"`
	Resources    []SCIMGroup `json:"Resources"`
}

const (
	SchemaUser    = "urn:ietf:params:scim:schemas:core:2.0:User"
	SchemaGroup   = "urn:ietf:params:scim:schemas:core:2.0:Group"
	SchemaPatchOp = "urn:ietf:params:scim:api:messages:2.0:PatchOp"
)
