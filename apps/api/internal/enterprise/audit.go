package enterprise

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	maxAuditMetadataBytes = 64 << 10
	GenesisAuditHash      = "0000000000000000000000000000000000000000000000000000000000000000"
)

var auditHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type AuditOutcome string

const (
	AuditSucceeded AuditOutcome = "succeeded"
	AuditDenied    AuditOutcome = "denied"
	AuditFailed    AuditOutcome = "failed"
)

func (outcome AuditOutcome) Valid() bool {
	switch outcome {
	case AuditSucceeded, AuditDenied, AuditFailed:
		return true
	default:
		return false
	}
}

type AuditEvent struct {
	ID             string         `json:"id"`
	OrganizationID string         `json:"organizationId"`
	Sequence       uint64         `json:"sequence"`
	ActorID        string         `json:"actorId,omitempty"`
	Action         string         `json:"action"`
	ResourceType   string         `json:"resourceType"`
	ResourceID     string         `json:"resourceId"`
	Outcome        AuditOutcome   `json:"outcome"`
	RequestID      string         `json:"requestId,omitempty"`
	SourceIP       string         `json:"sourceIp,omitempty"`
	Metadata       map[string]any `json:"metadata"`
	OccurredAt     time.Time      `json:"occurredAt"`
	PreviousHash   string         `json:"previousHash"`
	Hash           string         `json:"hash"`
}

type AuditCheckpoint struct {
	OrganizationID string `json:"organizationId"`
	LastSequence   uint64 `json:"lastSequence"`
	LastHash       string `json:"lastHash"`
}

type AuditIntegrityError struct {
	Index   int
	EventID string
	Reason  string
}

func (e *AuditIntegrityError) Error() string {
	if e.EventID == "" {
		return fmt.Sprintf("audit chain integrity failure at index %d: %s", e.Index, e.Reason)
	}
	return fmt.Sprintf("audit chain integrity failure at index %d (event %s): %s", e.Index, e.EventID, e.Reason)
}

// AuditChain is an append-only in-memory chain builder. Persistence remains an
// adapter concern; each returned event contains everything needed for durable
// verification after it is written to audit_log.
type AuditChain struct {
	mutex          sync.RWMutex
	organizationID string
	events         []AuditEvent
	ids            map[string]struct{}
}

// SealAuditEvent advances a trusted checkpoint without loading the complete
// history. A persistence adapter can lock the tenant tail, call this function,
// insert the event and checkpoint atomically, and then release the lock.
func SealAuditEvent(checkpoint AuditCheckpoint, event AuditEvent) (AuditEvent, AuditCheckpoint, error) {
	if err := validateAuditCheckpoint(checkpoint); err != nil {
		return AuditEvent{}, AuditCheckpoint{}, err
	}
	if event.Sequence != 0 || event.PreviousHash != "" || event.Hash != "" {
		return AuditEvent{}, AuditCheckpoint{}, invalid("event", "sequence and hash fields are managed by the audit chain")
	}
	if event.OrganizationID != checkpoint.OrganizationID {
		return AuditEvent{}, AuditCheckpoint{}, invalid("organizationId", "does not match the audit checkpoint tenant")
	}
	if checkpoint.LastSequence == ^uint64(0) {
		return AuditEvent{}, AuditCheckpoint{}, invalid("lastSequence", "cannot be advanced")
	}
	normalized, _, err := normalizeAuditEvent(event)
	if err != nil {
		return AuditEvent{}, AuditCheckpoint{}, err
	}
	normalized.Sequence = checkpoint.LastSequence + 1
	normalized.PreviousHash = checkpoint.LastHash
	normalized.Hash, err = computeAuditHash(normalized)
	if err != nil {
		return AuditEvent{}, AuditCheckpoint{}, err
	}
	next := AuditCheckpoint{
		OrganizationID: normalized.OrganizationID,
		LastSequence:   normalized.Sequence,
		LastHash:       normalized.Hash,
	}
	return cloneAuditEvent(normalized), next, nil
}

func NewAuditChain(organizationID string) (*AuditChain, error) {
	if err := validateUUID("organizationId", organizationID, false); err != nil {
		return nil, err
	}
	return &AuditChain{
		organizationID: organizationID,
		events:         []AuditEvent{},
		ids:            map[string]struct{}{},
	}, nil
}

func (chain *AuditChain) Append(event AuditEvent) (AuditEvent, error) {
	if chain == nil {
		return AuditEvent{}, invalid("chain", "is required")
	}
	if event.Sequence != 0 || event.PreviousHash != "" || event.Hash != "" {
		return AuditEvent{}, invalid("event", "sequence and hash fields are managed by the audit chain")
	}
	if event.OrganizationID != chain.organizationID {
		return AuditEvent{}, invalid("organizationId", "does not match the audit chain tenant")
	}
	normalized, _, err := normalizeAuditEvent(event)
	if err != nil {
		return AuditEvent{}, err
	}

	chain.mutex.Lock()
	defer chain.mutex.Unlock()
	if _, exists := chain.ids[normalized.ID]; exists {
		return AuditEvent{}, invalid("id", "audit event IDs must be unique within a chain")
	}
	normalized.Sequence = uint64(len(chain.events)) + 1
	normalized.PreviousHash = GenesisAuditHash
	if len(chain.events) > 0 {
		normalized.PreviousHash = chain.events[len(chain.events)-1].Hash
	}
	normalized.Hash, err = computeAuditHash(normalized)
	if err != nil {
		return AuditEvent{}, err
	}
	chain.events = append(chain.events, cloneAuditEvent(normalized))
	chain.ids[normalized.ID] = struct{}{}
	return cloneAuditEvent(normalized), nil
}

func (chain *AuditChain) Events() []AuditEvent {
	if chain == nil {
		return []AuditEvent{}
	}
	chain.mutex.RLock()
	defer chain.mutex.RUnlock()
	result := make([]AuditEvent, len(chain.events))
	for index, event := range chain.events {
		result[index] = cloneAuditEvent(event)
	}
	return result
}

func (chain *AuditChain) Checkpoint() AuditCheckpoint {
	if chain == nil {
		return AuditCheckpoint{LastHash: GenesisAuditHash}
	}
	chain.mutex.RLock()
	defer chain.mutex.RUnlock()
	checkpoint := AuditCheckpoint{OrganizationID: chain.organizationID, LastHash: GenesisAuditHash}
	if len(chain.events) > 0 {
		last := chain.events[len(chain.events)-1]
		checkpoint.LastSequence = last.Sequence
		checkpoint.LastHash = last.Hash
	}
	return checkpoint
}

func VerifyAuditChain(events []AuditEvent) error {
	if len(events) == 0 {
		return nil
	}
	organizationID := events[0].OrganizationID
	if err := validateUUID("organizationId", organizationID, false); err != nil {
		return integrityFailure(0, events[0].ID, err.Error())
	}
	previousHash := GenesisAuditHash
	seenIDs := make(map[string]struct{}, len(events))
	for index, event := range events {
		if event.OrganizationID != organizationID {
			return integrityFailure(index, event.ID, "tenant changed inside the chain")
		}
		if _, exists := seenIDs[event.ID]; exists {
			return integrityFailure(index, event.ID, "duplicate event ID")
		}
		seenIDs[event.ID] = struct{}{}
		if event.Sequence != uint64(index)+1 {
			return integrityFailure(index, event.ID, "non-contiguous sequence")
		}
		if event.PreviousHash != previousHash {
			return integrityFailure(index, event.ID, "previous hash does not match")
		}
		if !auditHashPattern.MatchString(event.Hash) {
			return integrityFailure(index, event.ID, "hash is not a canonical SHA-256 digest")
		}
		if _, _, err := normalizeAuditEvent(event); err != nil {
			return integrityFailure(index, event.ID, err.Error())
		}
		expected, err := computeAuditHash(event)
		if err != nil {
			return integrityFailure(index, event.ID, err.Error())
		}
		if subtle.ConstantTimeCompare([]byte(event.Hash), []byte(expected)) != 1 {
			return integrityFailure(index, event.ID, "event hash does not match its content")
		}
		previousHash = event.Hash
	}
	return nil
}

// VerifyAuditChainAgainst also detects a valid-looking truncated tail by
// comparing the chain with a separately retained checkpoint.
func VerifyAuditChainAgainst(events []AuditEvent, checkpoint AuditCheckpoint) error {
	if err := VerifyAuditChain(events); err != nil {
		return err
	}
	if err := validateAuditCheckpoint(checkpoint); err != nil {
		return integrityFailure(len(events), "", err.Error())
	}
	if len(events) == 0 {
		if checkpoint.LastSequence != 0 || checkpoint.LastHash != GenesisAuditHash {
			return integrityFailure(0, "", "chain does not reach the checkpoint")
		}
		return nil
	}
	last := events[len(events)-1]
	if last.OrganizationID != checkpoint.OrganizationID || last.Sequence != checkpoint.LastSequence || last.Hash != checkpoint.LastHash {
		return integrityFailure(len(events), last.ID, "chain does not reach the checkpoint")
	}
	return nil
}

func normalizeAuditEvent(event AuditEvent) (AuditEvent, []byte, error) {
	if err := validateUUID("id", event.ID, false); err != nil {
		return AuditEvent{}, nil, err
	}
	if err := validateUUID("organizationId", event.OrganizationID, false); err != nil {
		return AuditEvent{}, nil, err
	}
	if err := validateUUID("actorId", event.ActorID, true); err != nil {
		return AuditEvent{}, nil, err
	}
	if err := validatePolicyLiteral(event.Action); err != nil {
		return AuditEvent{}, nil, invalid("action", err.Error())
	}
	if err := validatePolicyLiteral(event.ResourceType); err != nil {
		return AuditEvent{}, nil, invalid("resourceType", err.Error())
	}
	if event.ResourceID == "" || len(event.ResourceID) > maxPolicyValueSize || !utf8.ValidString(event.ResourceID) || strings.TrimSpace(event.ResourceID) != event.ResourceID || containsControl(event.ResourceID) {
		return AuditEvent{}, nil, invalid("resourceId", "must be trimmed and contain between 1 and 512 characters")
	}
	if !event.Outcome.Valid() {
		return AuditEvent{}, nil, invalid("outcome", "must be succeeded, denied, or failed")
	}
	if len(event.RequestID) > 100 || !utf8.ValidString(event.RequestID) || strings.TrimSpace(event.RequestID) != event.RequestID || containsControl(event.RequestID) {
		return AuditEvent{}, nil, invalid("requestId", "must be trimmed and contain at most 100 characters")
	}
	if event.SourceIP != "" {
		parsed := net.ParseIP(event.SourceIP)
		if parsed == nil {
			return AuditEvent{}, nil, invalid("sourceIp", "must be a valid IPv4 or IPv6 address")
		}
		event.SourceIP = parsed.String()
	}
	if event.OccurredAt.IsZero() {
		return AuditEvent{}, nil, invalid("occurredAt", "is required")
	}
	// PostgreSQL timestamptz persists microsecond precision. Hash that durable
	// representation so a database round-trip never invalidates the chain.
	event.OccurredAt = event.OccurredAt.UTC().Truncate(time.Microsecond)
	metadata, metadataJSON, err := canonicalAuditMetadata(event.Metadata)
	if err != nil {
		return AuditEvent{}, nil, err
	}
	event.Metadata = metadata
	return event, metadataJSON, nil
}

func computeAuditHash(event AuditEvent) (string, error) {
	normalized, metadataJSON, err := normalizeAuditEvent(event)
	if err != nil {
		return "", err
	}
	payload := struct {
		ID             string          `json:"id"`
		OrganizationID string          `json:"organizationId"`
		Sequence       uint64          `json:"sequence"`
		ActorID        string          `json:"actorId"`
		Action         string          `json:"action"`
		ResourceType   string          `json:"resourceType"`
		ResourceID     string          `json:"resourceId"`
		Outcome        AuditOutcome    `json:"outcome"`
		RequestID      string          `json:"requestId"`
		SourceIP       string          `json:"sourceIp"`
		Metadata       json.RawMessage `json:"metadata"`
		OccurredAt     string          `json:"occurredAt"`
		PreviousHash   string          `json:"previousHash"`
	}{
		ID: normalized.ID, OrganizationID: normalized.OrganizationID, Sequence: normalized.Sequence,
		ActorID: normalized.ActorID, Action: normalized.Action, ResourceType: normalized.ResourceType,
		ResourceID: normalized.ResourceID, Outcome: normalized.Outcome, RequestID: normalized.RequestID,
		SourceIP: normalized.SourceIP, Metadata: metadataJSON,
		OccurredAt: normalized.OccurredAt.Format(time.RFC3339Nano), PreviousHash: normalized.PreviousHash,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", invalid("event", "could not be encoded for hashing")
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func canonicalAuditMetadata(metadata map[string]any) (map[string]any, []byte, error) {
	if metadata == nil {
		metadata = map[string]any{}
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return nil, nil, invalid("metadata", "must contain JSON-compatible values")
	}
	if len(raw) > maxAuditMetadataBytes {
		return nil, nil, invalid("metadata", fmt.Sprintf("must not exceed %d bytes", maxAuditMetadataBytes))
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var normalized map[string]any
	if err := decoder.Decode(&normalized); err != nil || normalized == nil {
		return nil, nil, invalid("metadata", "must be a JSON object")
	}
	canonicalValue, err := canonicalizeJSONValue(normalized)
	if err != nil {
		return nil, nil, invalid("metadata", err.Error())
	}
	normalized = canonicalValue.(map[string]any)
	canonical, err := json.Marshal(normalized)
	if err != nil {
		return nil, nil, invalid("metadata", "must contain JSON-compatible values")
	}
	if len(canonical) > maxAuditMetadataBytes {
		return nil, nil, invalid("metadata", fmt.Sprintf("must not exceed %d bytes", maxAuditMetadataBytes))
	}
	return normalized, canonical, nil
}

func cloneAuditEvent(event AuditEvent) AuditEvent {
	metadata, _, err := canonicalAuditMetadata(event.Metadata)
	if err == nil {
		event.Metadata = metadata
	}
	return event
}

func integrityFailure(index int, eventID, reason string) error {
	return &AuditIntegrityError{Index: index, EventID: eventID, Reason: reason}
}

func validateAuditCheckpoint(checkpoint AuditCheckpoint) error {
	if err := validateUUID("organizationId", checkpoint.OrganizationID, false); err != nil {
		return err
	}
	if !auditHashPattern.MatchString(checkpoint.LastHash) {
		return invalid("lastHash", "must be a canonical SHA-256 digest")
	}
	if checkpoint.LastSequence == 0 && checkpoint.LastHash != GenesisAuditHash {
		return invalid("lastHash", "an empty checkpoint must use the genesis hash")
	}
	if checkpoint.LastSequence > 0 && checkpoint.LastHash == GenesisAuditHash {
		return invalid("lastHash", "a non-empty checkpoint must not use the genesis hash")
	}
	return nil
}

func canonicalizeJSONValue(value any) (any, error) {
	switch typed := value.(type) {
	case nil, bool, string:
		return typed, nil
	case json.Number:
		canonical, err := canonicalJSONNumber(string(typed))
		if err != nil {
			return nil, err
		}
		return json.Number(canonical), nil
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			canonical, err := canonicalizeJSONValue(item)
			if err != nil {
				return nil, err
			}
			result[index] = canonical
		}
		return result, nil
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			canonical, err := canonicalizeJSONValue(item)
			if err != nil {
				return nil, err
			}
			result[key] = canonical
		}
		return result, nil
	default:
		return nil, fmt.Errorf("contains a non-JSON value")
	}
}

func canonicalJSONNumber(value string) (string, error) {
	sign := ""
	if strings.HasPrefix(value, "-") {
		sign = "-"
		value = value[1:]
	}
	exponent := 0
	if position := strings.IndexAny(value, "eE"); position >= 0 {
		parsed, err := strconv.Atoi(value[position+1:])
		if err != nil || parsed > maxAuditMetadataBytes || parsed < -maxAuditMetadataBytes {
			return "", fmt.Errorf("contains a number with an unsupported exponent")
		}
		exponent = parsed
		value = value[:position]
	}
	integer, fraction := value, ""
	if position := strings.IndexByte(value, '.'); position >= 0 {
		integer, fraction = value[:position], value[position+1:]
	}
	digits := strings.TrimLeft(integer+fraction, "0")
	if digits == "" {
		return "0", nil
	}
	scale := len(fraction) - exponent
	for scale > 0 && strings.HasSuffix(digits, "0") {
		digits = strings.TrimSuffix(digits, "0")
		scale--
	}
	var canonical string
	switch {
	case scale <= 0:
		if len(digits)-scale > maxAuditMetadataBytes {
			return "", fmt.Errorf("contains a number that is too large")
		}
		canonical = digits + strings.Repeat("0", -scale)
	case scale >= len(digits):
		if scale+2 > maxAuditMetadataBytes {
			return "", fmt.Errorf("contains a number that is too precise")
		}
		canonical = "0." + strings.Repeat("0", scale-len(digits)) + digits
	default:
		position := len(digits) - scale
		canonical = digits[:position] + "." + digits[position:]
	}
	if len(canonical) > maxAuditMetadataBytes {
		return "", fmt.Errorf("contains a number that is too large")
	}
	return sign + canonical, nil
}
