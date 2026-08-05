package enterprise

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const maxPluginCapabilities = 32

var (
	pluginKeyPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]{0,126}[a-z0-9])?$`)
	semverPattern    = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

	ErrPluginAlreadyRegistered = errors.New("plugin is already registered")
	ErrPluginNotFound          = errors.New("plugin is not registered")
	ErrPluginRevoked           = errors.New("revoked plugin registrations are immutable")
)

type PluginStatus string

const (
	PluginActive   PluginStatus = "active"
	PluginDisabled PluginStatus = "disabled"
	PluginRevoked  PluginStatus = "revoked"
)

func (status PluginStatus) Valid() bool {
	switch status {
	case PluginActive, PluginDisabled, PluginRevoked:
		return true
	default:
		return false
	}
}

type PluginRegistration struct {
	ID             string       `json:"id"`
	OrganizationID string       `json:"organizationId"`
	PluginKey      string       `json:"pluginKey"`
	Version        string       `json:"version"`
	Status         PluginStatus `json:"status"`
	SourceURL      string       `json:"sourceUrl"`
	Checksum       string       `json:"checksum"`
	Capabilities   []string     `json:"capabilities"`
	InstalledBy    string       `json:"installedBy,omitempty"`
	CreatedAt      time.Time    `json:"createdAt"`
	UpdatedAt      time.Time    `json:"updatedAt"`
}

func (registration PluginRegistration) Normalize() PluginRegistration {
	registration.PluginKey = strings.ToLower(strings.TrimSpace(registration.PluginKey))
	registration.Version = strings.TrimSpace(registration.Version)
	registration.SourceURL = strings.TrimSpace(registration.SourceURL)
	registration.Checksum = strings.ToLower(strings.TrimSpace(registration.Checksum))
	registration.Capabilities = canonicalStrings(registration.Capabilities, true)
	return registration
}

func (registration PluginRegistration) Validate() error {
	if err := validateUUID("id", registration.ID, false); err != nil {
		return err
	}
	if err := validateUUID("organizationId", registration.OrganizationID, false); err != nil {
		return err
	}
	if err := validateUUID("installedBy", registration.InstalledBy, true); err != nil {
		return err
	}
	if !pluginKeyPattern.MatchString(registration.PluginKey) {
		return invalid("pluginKey", "must be a lowercase namespaced plugin key")
	}
	if !validSemanticVersion(registration.Version) {
		return invalid("version", "must be a valid semantic version")
	}
	if !registration.Status.Valid() {
		return invalid("status", "must be active, disabled, or revoked")
	}
	if err := validatePluginSourceURL(registration.SourceURL); err != nil {
		return err
	}
	if !sha256Pattern.MatchString(registration.Checksum) {
		return invalid("checksum", "must be a lowercase sha256 digest")
	}
	if len(registration.Capabilities) > maxPluginCapabilities {
		return invalid("capabilities", fmt.Sprintf("must contain at most %d entries", maxPluginCapabilities))
	}
	if !equalStrings(registration.Capabilities, canonicalStrings(registration.Capabilities, true)) {
		return invalid("capabilities", "must be lowercase, unique, and sorted")
	}
	for index, capability := range registration.Capabilities {
		if err := validatePolicyLiteral(capability); err != nil {
			return invalid(fmt.Sprintf("capabilities[%d]", index), err.Error())
		}
	}
	return validateTimestamps(registration.CreatedAt, registration.UpdatedAt)
}

type PluginRegistry struct {
	mutex     sync.RWMutex
	byTenant  map[string]map[string]PluginRegistration
	locations map[string]string
}

func NewPluginRegistry() *PluginRegistry {
	return &PluginRegistry{
		byTenant:  map[string]map[string]PluginRegistration{},
		locations: map[string]string{},
	}
}

func (registry *PluginRegistry) Register(registration PluginRegistration) error {
	if registry == nil {
		return invalid("registry", "is required")
	}
	registration = registration.Normalize()
	if err := registration.Validate(); err != nil {
		return err
	}
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	registry.ensureMaps()
	if _, exists := registry.locations[registration.ID]; exists {
		return ErrPluginAlreadyRegistered
	}
	plugins := registry.byTenant[registration.OrganizationID]
	if plugins == nil {
		plugins = map[string]PluginRegistration{}
		registry.byTenant[registration.OrganizationID] = plugins
	}
	if _, exists := plugins[registration.PluginKey]; exists {
		return ErrPluginAlreadyRegistered
	}
	plugins[registration.PluginKey] = clonePluginRegistration(registration)
	registry.locations[registration.ID] = registration.OrganizationID + "\x00" + registration.PluginKey
	return nil
}

func (registry *PluginRegistry) Get(organizationID, pluginKey string) (PluginRegistration, bool) {
	if registry == nil {
		return PluginRegistration{}, false
	}
	pluginKey = strings.ToLower(strings.TrimSpace(pluginKey))
	registry.mutex.RLock()
	defer registry.mutex.RUnlock()
	registration, exists := registry.byTenant[organizationID][pluginKey]
	if !exists {
		return PluginRegistration{}, false
	}
	return clonePluginRegistration(registration), true
}

func (registry *PluginRegistry) List(organizationID string) []PluginRegistration {
	if registry == nil {
		return []PluginRegistration{}
	}
	registry.mutex.RLock()
	defer registry.mutex.RUnlock()
	plugins := registry.byTenant[organizationID]
	result := make([]PluginRegistration, 0, len(plugins))
	for _, registration := range plugins {
		result = append(result, clonePluginRegistration(registration))
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].PluginKey == result[right].PluginKey {
			return result[left].ID < result[right].ID
		}
		return result[left].PluginKey < result[right].PluginKey
	})
	return result
}

func (registry *PluginRegistry) SetStatus(organizationID, pluginKey string, status PluginStatus, updatedAt time.Time) error {
	if registry == nil {
		return invalid("registry", "is required")
	}
	if err := validateUUID("organizationId", organizationID, false); err != nil {
		return err
	}
	pluginKey = strings.ToLower(strings.TrimSpace(pluginKey))
	if !pluginKeyPattern.MatchString(pluginKey) {
		return invalid("pluginKey", "must be a lowercase namespaced plugin key")
	}
	if !status.Valid() {
		return invalid("status", "must be active, disabled, or revoked")
	}
	if updatedAt.IsZero() {
		return invalid("updatedAt", "is required")
	}

	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	registration, exists := registry.byTenant[organizationID][pluginKey]
	if !exists {
		return ErrPluginNotFound
	}
	if registration.Status == PluginRevoked {
		return ErrPluginRevoked
	}
	if updatedAt.Before(registration.UpdatedAt) {
		return invalid("updatedAt", "must not move backwards")
	}
	registration.Status = status
	registration.UpdatedAt = updatedAt.UTC()
	registry.byTenant[organizationID][pluginKey] = registration
	return nil
}

func (registry *PluginRegistry) ensureMaps() {
	if registry.byTenant == nil {
		registry.byTenant = map[string]map[string]PluginRegistration{}
	}
	if registry.locations == nil {
		registry.locations = map[string]string{}
	}
}

func clonePluginRegistration(registration PluginRegistration) PluginRegistration {
	registration.Capabilities = append([]string(nil), registration.Capabilities...)
	return registration
}

func validatePluginSourceURL(value string) error {
	if value == "" || len(value) > maxURLLength {
		return invalid("sourceUrl", "must be a valid HTTPS or OCI URL")
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "oci") || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" || parsed.RawQuery != "" {
		return invalid("sourceUrl", "must be an HTTPS or OCI URL without credentials, query parameters, or fragments")
	}
	if parsed.Path == "" || parsed.Path == "/" {
		return invalid("sourceUrl", "must identify a concrete plugin artifact")
	}
	return nil
}

func validSemanticVersion(value string) bool {
	if len(value) > 128 || !semverPattern.MatchString(value) {
		return false
	}
	withoutBuild := strings.SplitN(value, "+", 2)[0]
	parts := strings.SplitN(withoutBuild, "-", 2)
	if len(parts) == 1 {
		return true
	}
	for _, identifier := range strings.Split(parts[1], ".") {
		numeric := true
		for _, character := range identifier {
			if character < '0' || character > '9' {
				numeric = false
				break
			}
		}
		if numeric && len(identifier) > 1 && identifier[0] == '0' {
			return false
		}
	}
	return true
}
