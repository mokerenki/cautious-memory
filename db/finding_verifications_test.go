package db

import "testing"

func TestApplyTrafficGate(t *testing.T) {
	if got := ApplyTrafficGate(VerificationVerified, 0); got != VerificationNeedsHuman {
		t.Fatalf("verified+0 traffic → needs_human, got %s", got)
	}
	if got := ApplyTrafficGate(VerificationVerified, 2); got != VerificationVerified {
		t.Fatalf("verified+traffic stays verified, got %s", got)
	}
	if got := ApplyTrafficGate(VerificationFalsePositive, 0); got != VerificationFalsePositive {
		t.Fatalf("false_positive unaffected, got %s", got)
	}
}

func TestCanTransitionVerification(t *testing.T) {
	allow := [][2]string{
		{VerificationUnverified, VerificationVerifying},
		{VerificationVerifying, VerificationVerified},
		{VerificationVerifying, VerificationFalsePositive},
		{VerificationVerifying, VerificationDowngraded},
		{VerificationVerifying, VerificationNeedsHuman},
		{VerificationVerified, VerificationVerifying},
	}
	for _, p := range allow {
		if !CanTransitionVerification(p[0], p[1]) {
			t.Fatalf("expected allow %s → %s", p[0], p[1])
		}
	}
	deny := [][2]string{
		{VerificationUnverified, VerificationVerified},
		{VerificationVerified, VerificationFalsePositive},
		{VerificationFalsePositive, VerificationVerified},
		{"bogus", VerificationVerified},
	}
	for _, p := range deny {
		if CanTransitionVerification(p[0], p[1]) {
			t.Fatalf("expected deny %s → %s", p[0], p[1])
		}
	}
}

func TestValidVerificationStatus(t *testing.T) {
	for _, s := range []string{
		VerificationUnverified, VerificationVerifying, VerificationVerified,
		VerificationFalsePositive, VerificationDowngraded, VerificationNeedsHuman,
	} {
		if !ValidVerificationStatus(s) {
			t.Fatal(s)
		}
	}
	if ValidVerificationStatus("confirmed") {
		t.Fatal("triage status must not pass verification whitelist")
	}
}
