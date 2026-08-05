package enterprise

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestAuditChainSealsAndVerifiesEvents(t *testing.T) {
	chain, err := NewAuditChain(testOrganizationID)
	if err != nil {
		t.Fatal(err)
	}
	firstDraft := auditDraft("aaaaaaaa-1111-4111-8111-111111111111", "flows.create")
	firstDraft.OccurredAt = firstDraft.OccurredAt.Add(789)
	firstDraft.Metadata = map[string]any{
		"z":      "last",
		"nested": map[string]any{"count": 2, "enabled": true},
	}
	first, err := chain.Append(firstDraft)
	if err != nil {
		t.Fatal(err)
	}
	second, err := chain.Append(auditDraft("aaaaaaaa-2222-4222-8222-222222222222", "flows.publish"))
	if err != nil {
		t.Fatal(err)
	}

	if first.Sequence != 1 || first.PreviousHash != GenesisAuditHash || !auditHashPattern.MatchString(first.Hash) {
		t.Fatalf("invalid first seal: %#v", first)
	}
	if first.OccurredAt.Nanosecond()%1000 != 0 {
		t.Fatalf("timestamp was not normalized to PostgreSQL precision: %s", first.OccurredAt)
	}
	if second.Sequence != 2 || second.PreviousHash != first.Hash || second.Hash == first.Hash {
		t.Fatalf("invalid second seal: %#v", second)
	}
	events := chain.Events()
	if err := VerifyAuditChain(events); err != nil {
		t.Fatalf("valid chain failed verification: %v", err)
	}
	checkpoint := chain.Checkpoint()
	if checkpoint.OrganizationID != testOrganizationID || checkpoint.LastSequence != 2 || checkpoint.LastHash != second.Hash {
		t.Fatalf("checkpoint = %#v", checkpoint)
	}
	if err := VerifyAuditChainAgainst(events, checkpoint); err != nil {
		t.Fatalf("valid checkpoint failed: %v", err)
	}

	// Caller and returned snapshots cannot mutate the sealed chain.
	firstDraft.Metadata["z"] = "tampered"
	events[0].Metadata["z"] = "tampered"
	nested := events[0].Metadata["nested"].(map[string]any)
	nested["count"] = 999
	if err := VerifyAuditChain(chain.Events()); err != nil {
		t.Fatalf("external mutation reached the chain: %v", err)
	}
}

func TestAuditHashCanonicalizesMetadataKeyOrder(t *testing.T) {
	left := auditDraft("bbbbbbbb-1111-4111-8111-111111111111", "flows.read")
	left.Sequence, left.PreviousHash = 1, GenesisAuditHash
	left.Metadata = map[string]any{"alpha": json.Number("1e3"), "nested": map[string]any{"x": true, "y": "value"}}
	right := left
	right.Metadata = map[string]any{"nested": map[string]any{"y": "value", "x": true}, "alpha": 1000}

	leftHash, err := computeAuditHash(left)
	if err != nil {
		t.Fatal(err)
	}
	rightHash, err := computeAuditHash(right)
	if err != nil {
		t.Fatal(err)
	}
	if leftHash != rightHash {
		t.Fatalf("canonical metadata hashes differ: %s != %s", leftHash, rightHash)
	}
}

func TestCanonicalJSONNumberUsesDatabaseStableDecimalForm(t *testing.T) {
	tests := map[string]string{
		"1e3":       "1000",
		"1.2300":    "1.23",
		"1.20e-2":   "0.012",
		"-0.000":    "0",
		"123000e-2": "1230",
	}
	for input, want := range tests {
		got, err := canonicalJSONNumber(input)
		if err != nil {
			t.Fatalf("canonicalJSONNumber(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("canonicalJSONNumber(%q) = %q, want %q", input, got, want)
		}
	}
	if _, err := canonicalJSONNumber("1e999999"); err == nil {
		t.Fatal("unbounded numeric exponent was accepted")
	}
}

func TestSealAuditEventAdvancesDurableCheckpoint(t *testing.T) {
	checkpoint := AuditCheckpoint{OrganizationID: testOrganizationID, LastHash: GenesisAuditHash}
	first, next, err := SealAuditEvent(checkpoint, auditDraft("bbbbbbbb-2222-4222-8222-222222222222", "flows.create"))
	if err != nil {
		t.Fatal(err)
	}
	second, final, err := SealAuditEvent(next, auditDraft("bbbbbbbb-3333-4333-8333-333333333333", "flows.publish"))
	if err != nil {
		t.Fatal(err)
	}
	if first.Sequence != 1 || second.Sequence != 2 || second.PreviousHash != first.Hash || final.LastHash != second.Hash {
		t.Fatalf("checkpoint progression failed: first=%#v second=%#v final=%#v", first, second, final)
	}
	if err := VerifyAuditChainAgainst([]AuditEvent{first, second}, final); err != nil {
		t.Fatalf("sealed segment failed verification: %v", err)
	}
	bad := checkpoint
	bad.LastHash = strings.Repeat("f", 64)
	if _, _, err := SealAuditEvent(bad, auditDraft("bbbbbbbb-4444-4444-8444-444444444444", "flows.read")); err == nil {
		t.Fatal("invalid genesis checkpoint was accepted")
	}
}

func TestAuditVerificationDetectsManipulation(t *testing.T) {
	chain, err := NewAuditChain(testOrganizationID)
	if err != nil {
		t.Fatal(err)
	}
	for index, action := range []string{"flows.create", "flows.update", "flows.publish"} {
		event := auditDraft(fmt.Sprintf("cccccccc-cccc-4ccc-8ccc-%012x", index+1), action)
		event.Metadata = map[string]any{"ordinal": index + 1}
		if _, err := chain.Append(event); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name   string
		mutate func([]AuditEvent) []AuditEvent
	}{
		{name: "content", mutate: func(events []AuditEvent) []AuditEvent { events[1].Action = "flows.delete"; return events }},
		{name: "metadata", mutate: func(events []AuditEvent) []AuditEvent { events[1].Metadata["ordinal"] = 200; return events }},
		{name: "previous hash", mutate: func(events []AuditEvent) []AuditEvent { events[1].PreviousHash = GenesisAuditHash; return events }},
		{name: "event hash", mutate: func(events []AuditEvent) []AuditEvent { events[1].Hash = strings.Repeat("f", 64); return events }},
		{name: "sequence", mutate: func(events []AuditEvent) []AuditEvent { events[1].Sequence = 8; return events }},
		{name: "tenant", mutate: func(events []AuditEvent) []AuditEvent { events[1].OrganizationID = testOtherTenantID; return events }},
		{name: "reordering", mutate: func(events []AuditEvent) []AuditEvent { events[0], events[1] = events[1], events[0]; return events }},
		{name: "middle deletion", mutate: func(events []AuditEvent) []AuditEvent { return append(events[:1], events[2:]...) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			events := test.mutate(chain.Events())
			err := VerifyAuditChain(events)
			var integrity *AuditIntegrityError
			if !errors.As(err, &integrity) {
				t.Fatalf("error = %T %v, want AuditIntegrityError", err, err)
			}
		})
	}

	prefix := chain.Events()[:2]
	if err := VerifyAuditChain(prefix); err != nil {
		t.Fatalf("a prefix should remain internally valid: %v", err)
	}
	if err := VerifyAuditChainAgainst(prefix, chain.Checkpoint()); err == nil {
		t.Fatal("checkpoint did not detect tail truncation")
	}
}

func TestAuditChainRejectsInvalidOrPresealedEvents(t *testing.T) {
	chain, err := NewAuditChain(testOrganizationID)
	if err != nil {
		t.Fatal(err)
	}
	valid := auditDraft("dddddddd-1111-4111-8111-111111111111", "flows.create")
	if _, err := chain.Append(valid); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(*AuditEvent)
		field  string
	}{
		{name: "wrong tenant", mutate: func(event *AuditEvent) { event.OrganizationID = testOtherTenantID }, field: "organizationId"},
		{name: "duplicate ID", mutate: func(event *AuditEvent) { event.ID = valid.ID }, field: "id"},
		{name: "invalid actor", mutate: func(event *AuditEvent) { event.ActorID = "system" }, field: "actorId"},
		{name: "invalid outcome", mutate: func(event *AuditEvent) { event.Outcome = "unknown" }, field: "outcome"},
		{name: "invalid IP", mutate: func(event *AuditEvent) { event.SourceIP = "999.1.1.1" }, field: "sourceIp"},
		{name: "presealed sequence", mutate: func(event *AuditEvent) { event.Sequence = 1 }, field: "event"},
		{name: "presealed hash", mutate: func(event *AuditEvent) { event.Hash = strings.Repeat("a", 64) }, field: "event"},
		{name: "oversized metadata", mutate: func(event *AuditEvent) {
			event.Metadata = map[string]any{"payload": strings.Repeat("x", maxAuditMetadataBytes)}
		}, field: "metadata"},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := auditDraft(fmt.Sprintf("dddddddd-dddd-4ddd-8ddd-%012x", index+2), "flows.update")
			test.mutate(&candidate)
			_, appendErr := chain.Append(candidate)
			assertValidationField(t, appendErr, test.field)
		})
	}
}

func TestAuditChainSupportsConcurrentAppendWithoutSequenceGaps(t *testing.T) {
	chain, err := NewAuditChain(testOrganizationID)
	if err != nil {
		t.Fatal(err)
	}
	const workers = 32
	start := make(chan struct{})
	errorsChannel := make(chan error, workers)
	var wait sync.WaitGroup
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			event := auditDraft(fmt.Sprintf("eeeeeeee-eeee-4eee-8eee-%012x", index+1), "flows.read")
			event.OccurredAt = event.OccurredAt.AddDate(0, 0, index)
			_, appendErr := chain.Append(event)
			errorsChannel <- appendErr
		}(index)
	}
	close(start)
	wait.Wait()
	close(errorsChannel)
	for appendErr := range errorsChannel {
		if appendErr != nil {
			t.Fatal(appendErr)
		}
	}
	events := chain.Events()
	if len(events) != workers {
		t.Fatalf("event count = %d, want %d", len(events), workers)
	}
	if err := VerifyAuditChain(events); err != nil {
		t.Fatalf("concurrent chain failed verification: %v", err)
	}
	sequences := make([]uint64, len(events))
	for index, event := range events {
		sequences[index] = event.Sequence
	}
	want := make([]uint64, workers)
	for index := range want {
		want[index] = uint64(index) + 1
	}
	if !reflect.DeepEqual(sequences, want) {
		t.Fatalf("sequences = %#v, want %#v", sequences, want)
	}
}

func auditDraft(id, action string) AuditEvent {
	return AuditEvent{
		ID: id, OrganizationID: testOrganizationID, ActorID: testUserID,
		Action: action, ResourceType: "flow", ResourceID: "flow:orders",
		Outcome: AuditSucceeded, RequestID: "request-123", SourceIP: "2001:db8::1",
		Metadata: map[string]any{}, OccurredAt: testTimestamp,
	}
}
