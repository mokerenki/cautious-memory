package server

import (
	"testing"

	"github.com/mokerenki/cautious-memory/db"
)

func TestTrafficGateLogic(t *testing.T) {
	// Mirrors db.ApplyTrafficGate — server must never mark verified without traffic.
	if db.ApplyTrafficGate(db.VerificationVerified, 0) != db.VerificationNeedsHuman {
		t.Fatal("server traffic gate broken")
	}
}

func TestVerifierAgentKey(t *testing.T) {
	if db.FindingVerifierAgentKey != "verifier" {
		t.Fatal(db.FindingVerifierAgentKey)
	}
}
