package enterprise

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	maxDisplayNameLength = 120
	maxURLLength         = 2048
)

var (
	slugPattern        = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	domainLabelPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	sha256Pattern      = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Field + ": " + e.Message
}

func invalid(field, message string) error {
	return &ValidationError{Field: field, Message: message}
}

type OrganizationStatus string

const (
	OrganizationActive    OrganizationStatus = "active"
	OrganizationSuspended OrganizationStatus = "suspended"
)

func (status OrganizationStatus) Valid() bool {
	return status == OrganizationActive || status == OrganizationSuspended
}

type OrganizationRole string

const (
	OrganizationOwner   OrganizationRole = "owner"
	OrganizationAdmin   OrganizationRole = "admin"
	OrganizationMember  OrganizationRole = "member"
	OrganizationAuditor OrganizationRole = "auditor"
)

func (role OrganizationRole) Valid() bool {
	switch role {
	case OrganizationOwner, OrganizationAdmin, OrganizationMember, OrganizationAuditor:
		return true
	default:
		return false
	}
}

type MembershipStatus string

const (
	MembershipInvited   MembershipStatus = "invited"
	MembershipActive    MembershipStatus = "active"
	MembershipSuspended MembershipStatus = "suspended"
)

func (status MembershipStatus) Valid() bool {
	switch status {
	case MembershipInvited, MembershipActive, MembershipSuspended:
		return true
	default:
		return false
	}
}

type Organization struct {
	ID        string             `json:"id"`
	Slug      string             `json:"slug"`
	Name      string             `json:"name"`
	Status    OrganizationStatus `json:"status"`
	CreatedAt time.Time          `json:"createdAt"`
	UpdatedAt time.Time          `json:"updatedAt"`
}

func (organization Organization) Validate() error {
	if err := validateUUID("id", organization.ID, false); err != nil {
		return err
	}
	if !slugPattern.MatchString(organization.Slug) {
		return invalid("slug", "must be a lowercase DNS-style slug between 1 and 63 characters")
	}
	if err := validateDisplayName("name", organization.Name); err != nil {
		return err
	}
	if !organization.Status.Valid() {
		return invalid("status", "must be active or suspended")
	}
	return validateTimestamps(organization.CreatedAt, organization.UpdatedAt)
}

type OrganizationMembership struct {
	OrganizationID string           `json:"organizationId"`
	UserID         string           `json:"userId"`
	Role           OrganizationRole `json:"role"`
	Status         MembershipStatus `json:"status"`
	CreatedAt      time.Time        `json:"createdAt"`
	JoinedAt       *time.Time       `json:"joinedAt,omitempty"`
}

func (membership OrganizationMembership) Validate() error {
	if err := validateUUID("organizationId", membership.OrganizationID, false); err != nil {
		return err
	}
	if err := validateUUID("userId", membership.UserID, false); err != nil {
		return err
	}
	if !membership.Role.Valid() {
		return invalid("role", "must be owner, admin, member, or auditor")
	}
	if !membership.Status.Valid() {
		return invalid("status", "must be invited, active, or suspended")
	}
	if membership.CreatedAt.IsZero() {
		return invalid("createdAt", "is required")
	}
	if membership.Role == OrganizationOwner && membership.Status != MembershipActive {
		return invalid("status", "an owner membership must be active")
	}
	if membership.Status == MembershipActive && membership.JoinedAt == nil {
		return invalid("joinedAt", "is required for an active membership")
	}
	if membership.Status == MembershipInvited && membership.JoinedAt != nil {
		return invalid("joinedAt", "must be absent while a membership is invited")
	}
	if membership.JoinedAt != nil && membership.JoinedAt.Before(membership.CreatedAt) {
		return invalid("joinedAt", "must not be earlier than createdAt")
	}
	return nil
}

type SSOProtocol string

const (
	SSOProtocolOIDC SSOProtocol = "oidc"
	SSOProtocolSAML SSOProtocol = "saml"
)

func (protocol SSOProtocol) Valid() bool {
	return protocol == SSOProtocolOIDC || protocol == SSOProtocolSAML
}

// SSOConnection intentionally contains only public discovery metadata. Client
// secrets, signing keys, access tokens, and private certificates do not belong
// in this model or in its persistence table.
type SSOConnection struct {
	ID                     string      `json:"id"`
	OrganizationID         string      `json:"organizationId"`
	Name                   string      `json:"name"`
	Protocol               SSOProtocol `json:"protocol"`
	IssuerURL              string      `json:"issuerUrl,omitempty"`
	MetadataURL            string      `json:"metadataUrl,omitempty"`
	EntityID               string      `json:"entityId,omitempty"`
	SignInURL              string      `json:"signInUrl,omitempty"`
	CertificateFingerprint string      `json:"certificateFingerprint,omitempty"`
	Domains                []string    `json:"domains"`
	Enabled                bool        `json:"enabled"`
	CreatedAt              time.Time   `json:"createdAt"`
	UpdatedAt              time.Time   `json:"updatedAt"`
}

func (connection SSOConnection) Normalize() SSOConnection {
	connection.Name = strings.TrimSpace(connection.Name)
	connection.IssuerURL = strings.TrimSpace(connection.IssuerURL)
	connection.MetadataURL = strings.TrimSpace(connection.MetadataURL)
	connection.EntityID = strings.TrimSpace(connection.EntityID)
	connection.SignInURL = strings.TrimSpace(connection.SignInURL)
	connection.CertificateFingerprint = strings.ToLower(strings.TrimSpace(connection.CertificateFingerprint))
	connection.Domains = canonicalStrings(connection.Domains, true)
	return connection
}

func (connection SSOConnection) Validate() error {
	if err := validateUUID("id", connection.ID, false); err != nil {
		return err
	}
	if err := validateUUID("organizationId", connection.OrganizationID, false); err != nil {
		return err
	}
	if err := validateDisplayName("name", connection.Name); err != nil {
		return err
	}
	if !connection.Protocol.Valid() {
		return invalid("protocol", "must be oidc or saml")
	}
	if len(connection.Domains) == 0 || len(connection.Domains) > 50 {
		return invalid("domains", "must contain between 1 and 50 domains")
	}
	if !equalStrings(connection.Domains, canonicalStrings(connection.Domains, true)) {
		return invalid("domains", "must be lowercase, unique, and sorted")
	}
	for index, domain := range connection.Domains {
		if err := validateDomain(domain); err != nil {
			return invalid(fmt.Sprintf("domains[%d]", index), err.Error())
		}
	}
	if connection.MetadataURL != "" {
		if err := validateHTTPSMetadataURL("metadataUrl", connection.MetadataURL); err != nil {
			return err
		}
	}
	switch connection.Protocol {
	case SSOProtocolOIDC:
		if err := validateHTTPSMetadataURL("issuerUrl", connection.IssuerURL); err != nil {
			return err
		}
		if connection.EntityID != "" || connection.SignInURL != "" || connection.CertificateFingerprint != "" {
			return invalid("protocol", "OIDC connections must not contain SAML-only metadata")
		}
	case SSOProtocolSAML:
		if connection.IssuerURL != "" {
			return invalid("issuerUrl", "must be absent for a SAML connection")
		}
		if err := validateEntityID(connection.EntityID); err != nil {
			return err
		}
		if err := validateHTTPSMetadataURL("signInUrl", connection.SignInURL); err != nil {
			return err
		}
		if !sha256Pattern.MatchString(connection.CertificateFingerprint) {
			return invalid("certificateFingerprint", "must be a lowercase sha256 fingerprint")
		}
	}
	return validateTimestamps(connection.CreatedAt, connection.UpdatedAt)
}

func validateUUID(field, value string, optional bool) error {
	if optional && value == "" {
		return nil
	}
	parsed, err := uuid.Parse(value)
	if err != nil || parsed.String() != strings.ToLower(value) {
		return invalid(field, "must be a canonical UUID")
	}
	return nil
}

func validateDisplayName(field, value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed != value || !utf8.ValidString(value) || len([]rune(value)) > maxDisplayNameLength {
		return invalid(field, fmt.Sprintf("must be trimmed and contain between 1 and %d characters", maxDisplayNameLength))
	}
	return nil
}

func validateTimestamps(createdAt, updatedAt time.Time) error {
	if createdAt.IsZero() {
		return invalid("createdAt", "is required")
	}
	if updatedAt.IsZero() {
		return invalid("updatedAt", "is required")
	}
	if updatedAt.Before(createdAt) {
		return invalid("updatedAt", "must not be earlier than createdAt")
	}
	return nil
}

func validateHTTPSMetadataURL(field, value string) error {
	if value == "" || len(value) > maxURLLength || !utf8.ValidString(value) {
		return invalid(field, "must be a valid HTTPS URL")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" || parsed.RawQuery != "" {
		return invalid(field, "must be an HTTPS URL without credentials, query parameters, or fragments")
	}
	return nil
}

func validateEntityID(value string) error {
	if value == "" || len(value) > 512 || !utf8.ValidString(value) || strings.TrimSpace(value) != value || containsControl(value) {
		return invalid("entityId", "must be a trimmed URI no longer than 512 characters")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.User != nil || parsed.Fragment != "" || parsed.RawQuery != "" {
		return invalid("entityId", "must be an absolute URI without credentials, query parameters, or fragments")
	}
	return nil
}

func validateDomain(value string) error {
	if value == "" || len(value) > 253 || value != strings.ToLower(value) || strings.HasSuffix(value, ".") {
		return fmt.Errorf("must be a canonical lowercase DNS name")
	}
	labels := strings.Split(value, ".")
	if len(labels) < 2 {
		return fmt.Errorf("must contain at least two DNS labels")
	}
	for _, label := range labels {
		if !domainLabelPattern.MatchString(label) {
			return fmt.Errorf("contains an invalid DNS label")
		}
	}
	return nil
}

func canonicalStrings(values []string, lowercase bool) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if lowercase {
			value = strings.ToLower(value)
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	if result == nil {
		return []string{}
	}
	return result
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func containsControl(value string) bool {
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}
