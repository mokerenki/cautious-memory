package server

import (
	"strings"
	"testing"

	"github.com/mokerenki/cautious-memory/db"
)

func TestRenderClientReportHTML_Empty(t *testing.T) {
	html := renderClientReportHTML("Brand", "Op", "T1", "goal", "desc", nil, nil, map[string]int{})
	if !strings.Contains(html, "No verified findings") && !strings.Contains(html, "No independently verified") {
		t.Fatal("empty report missing empty message")
	}
	if !strings.Contains(html, "Excluded appendix") {
		t.Fatal("missing excluded appendix")
	}
	if !strings.Contains(html, "AI-assisted, human-verified") {
		t.Fatal("missing methodology statement")
	}
}

func TestRenderClientReportHTML_IncludesOnlyVerified(t *testing.T) {
	included := []*db.DBFinding{{
		Name: "IDOR", VulnClass: "bola_idor", Severity: "high",
		Summary: "cross user read", Evidence: "GET /api/documents/2",
	}}
	excluded := []*db.DBFinding{{
		Name: "XSS noise", Status: db.FindingFalsePositive,
	}}
	html := renderClientReportHTML("Brand", "Op", "T1", "g", "", included, excluded, map[string]int{"high": 1})
	if !strings.Contains(html, "IDOR") {
		t.Fatal("missing included finding")
	}
	if !strings.Contains(html, "Independently verified") {
		t.Fatal("missing badge")
	}
	if !strings.Contains(html, "XSS noise") {
		t.Fatal("excluded should appear in appendix")
	}
	// Main section should not treat excluded as a finding heading with verified badge adjacent incorrectly —
	// basic check: only one Independently verified badge
	if strings.Count(html, "Independently verified") != 1 {
		t.Fatal("unexpected verified badges")
	}
}
