package main

import (
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
)

func TestCreatePoolSessionBead_SetsPendingCreateClaim(t *testing.T) {
	store := beads.NewMemStore()
	now := time.Date(2026, 5, 1, 9, 15, 0, 0, time.UTC)

	bead, err := createPoolSessionBead(store, "gascity/claude", nil, now, poolSessionCreateIdentity{})
	if err != nil {
		t.Fatalf("createPoolSessionBead: %v", err)
	}

	if got := bead.Metadata["pending_create_claim"]; got != "true" {
		t.Fatalf("pending_create_claim = %q, want true", got)
	}
	if got, want := bead.Metadata["pending_create_started_at"], pendingCreateStartedAtNow(now); got != want {
		t.Fatalf("pending_create_started_at = %q, want %q", got, want)
	}

	stored, err := store.Get(bead.ID)
	if err != nil {
		t.Fatalf("store.Get(%s): %v", bead.ID, err)
	}
	if got := stored.Metadata["pending_create_claim"]; got != "true" {
		t.Fatalf("stored pending_create_claim = %q, want true", got)
	}
	if got, want := stored.Metadata["pending_create_started_at"], pendingCreateStartedAtNow(now); got != want {
		t.Fatalf("stored pending_create_started_at = %q, want %q", got, want)
	}
}

func TestResolvedTemplateForIdentity_ResolvesUniqueInBoundsLegacyLocalPoolIdentity(t *testing.T) {
	cfg := &config.City{
		Agents: []config.Agent{
			{Name: "worker", Dir: "frontend", MaxActiveSessions: intPtr(5)},
			{Name: "worker", Dir: "backend", MaxActiveSessions: intPtr(1)},
		},
	}

	if got := resolvedTemplateForIdentity("worker-5", cfg); got != "frontend/worker" {
		t.Fatalf("resolvedTemplateForIdentity(worker-5) = %q, want %q", got, "frontend/worker")
	}
}

func TestResolvedTemplateForIdentity_DoesNotResolveAmbiguousLegacyLocalPoolIdentity(t *testing.T) {
	cfg := &config.City{
		Agents: []config.Agent{
			{Name: "worker", Dir: "frontend", MaxActiveSessions: intPtr(5)},
			{Name: "worker", Dir: "backend", MaxActiveSessions: intPtr(5)},
		},
	}

	if got := resolvedTemplateForIdentity("worker-7", cfg); got != "" {
		t.Fatalf("resolvedTemplateForIdentity(worker-7) = %q, want unresolved ambiguity", got)
	}
}

func TestResolvedTemplateForIdentity_DoesNotResolveZeroCapacityLocalIdentity(t *testing.T) {
	cfg := &config.City{
		Agents: []config.Agent{
			{Name: "worker", Dir: "frontend", MaxActiveSessions: intPtr(0)},
		},
	}

	if got := resolvedTemplateForIdentity("worker-1", cfg); got != "" {
		t.Fatalf("resolvedTemplateForIdentity(worker-1) = %q, want zero-capacity template to stay unresolved", got)
	}
}

func TestResolvedTemplateForIdentity_DoesNotResolveOutOfBoundsQualifiedPoolIdentity(t *testing.T) {
	cfg := &config.City{
		Agents: []config.Agent{
			{Name: "worker", Dir: "frontend", MaxActiveSessions: intPtr(5)},
		},
	}

	if got := resolvedTemplateForIdentity("frontend/worker-7", cfg); got != "" {
		t.Fatalf("resolvedTemplateForIdentity(frontend/worker-7) = %q, want unresolved out-of-bounds identity", got)
	}
}

func TestLookupPoolSessionNameCandidates_RanksExplicitActiveCandidate(t *testing.T) {
	store := beads.NewMemStore()
	agentCfg := config.Agent{Name: "worker", Dir: "frontend", MaxActiveSessions: intPtr(2)}
	cfg := &config.City{Agents: []config.Agent{agentCfg}}

	createPoolLookupBead(t, store, "weak", map[string]string{
		"agent_name":     "frontend/worker-1",
		"session_name":   "session-weak",
		"template":       "frontend/worker",
		"state":          "creating",
		"session_origin": "ephemeral",
	})
	createPoolLookupBead(t, store, "strong", map[string]string{
		"agent_name":     "frontend/worker",
		"session_name":   "session-strong",
		"template":       "frontend/worker",
		"pool_slot":      "1",
		"state":          "active",
		"session_origin": "ephemeral",
	})
	createPoolLookupBead(t, store, "out-of-bounds", map[string]string{
		"agent_name":     "frontend/worker",
		"session_name":   "session-out-of-bounds",
		"template":       "frontend/worker",
		"pool_slot":      "9",
		"state":          "active",
		"session_origin": "ephemeral",
	})

	candidates, err := lookupPoolSessionNameCandidates(store, "frontend/worker", cfg, &agentCfg)
	if err != nil {
		t.Fatalf("lookupPoolSessionNameCandidates: %v", err)
	}
	ranked := candidates["frontend/worker-1"]
	if len(ranked) != 2 {
		t.Fatalf("ranked candidates = %#v, want two in-bounds candidates", ranked)
	}
	if got := ranked[0].sessionName; got != "session-strong" {
		t.Fatalf("top ranked session = %q, want explicit active pool-slot candidate", got)
	}

	names, err := lookupPoolSessionNames(store, cfg, &agentCfg)
	if err != nil {
		t.Fatalf("lookupPoolSessionNames: %v", err)
	}
	if got := names["frontend/worker-1"]; got != "session-strong" {
		t.Fatalf("resolved pool session = %q, want strongest candidate", got)
	}
	if _, ok := candidates["frontend/worker-9"]; ok {
		t.Fatalf("out-of-bounds slot was returned: %#v", candidates["frontend/worker-9"])
	}
}

func TestLookupPoolSessionNames_DropsAmbiguousEquivalentCandidates(t *testing.T) {
	store := beads.NewMemStore()
	agentCfg := config.Agent{Name: "worker", Dir: "frontend", MaxActiveSessions: intPtr(2)}
	cfg := &config.City{Agents: []config.Agent{agentCfg}}

	createPoolLookupBead(t, store, "first", map[string]string{
		"agent_name":     "frontend/worker-1",
		"session_name":   "session-first",
		"template":       "frontend/worker",
		"state":          "active",
		"session_origin": "ephemeral",
	})
	createPoolLookupBead(t, store, "second", map[string]string{
		"agent_name":     "frontend/worker-1",
		"session_name":   "session-second",
		"template":       "frontend/worker",
		"state":          "active",
		"session_origin": "ephemeral",
	})

	candidates, err := lookupPoolSessionNameCandidates(store, "frontend/worker", cfg, &agentCfg)
	if err != nil {
		t.Fatalf("lookupPoolSessionNameCandidates: %v", err)
	}
	if got := len(candidates["frontend/worker-1"]); got != 2 {
		t.Fatalf("candidate count = %d, want ambiguity to remain visible to resolver", got)
	}

	names, err := lookupPoolSessionNames(store, cfg, &agentCfg)
	if err != nil {
		t.Fatalf("lookupPoolSessionNames: %v", err)
	}
	if got := names["frontend/worker-1"]; got != "" {
		t.Fatalf("ambiguous equivalent pool session resolved to %q, want no resolution", got)
	}
}

func createPoolLookupBead(t *testing.T, store beads.Store, id string, metadata map[string]string) {
	t.Helper()
	if _, ok := metadata[poolManagedMetadataKey]; !ok {
		metadata[poolManagedMetadataKey] = boolMetadata(true)
	}
	if _, err := store.Create(beads.Bead{
		ID:       id,
		Title:    id,
		Type:     sessionBeadType,
		Labels:   []string{sessionBeadLabel},
		Metadata: metadata,
	}); err != nil {
		t.Fatalf("create pool lookup bead %s: %v", id, err)
	}
}
