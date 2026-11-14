package notify

import "testing"

func TestParseFilterMalformedFallsBackToMatchAll(t *testing.T) {
	// Malformed JSON, empty input, wrongly-typed fields—all must degrade to the zero-value Filter,
	// i.e. "don't filter". This invariant is the landing spot of "better to over-push than under-push":
	// once this becomes an error or partial parse, a user mistyping one character silently drops all high-severity notifications.
	cases := []struct {
		name string
		raw  string
	}{
		{"empty input", ""},
		{"invalid JSON", `{not json`},
		{"truncated JSON", `{"min_severity":`},
		{"type mismatch", `{"min_severity": 123, "task_ids": "abc"}`},
		{"top-level array", `[1,2,3]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := ParseFilter([]byte(tc.raw))
			if f.MinSeverity != "" || len(f.TaskIDs) != 0 || len(f.AssetIDs) != 0 {
				t.Fatalf("malformed config should degrade to zero-value Filter, got %+v", f)
			}
			// Zero-value Filter must match any event.
			ev := Snapshot{Kind: EventFindingCreated, Severity: "low", VulnClass: "XSS"}
			if !Match(f, ev) {
				t.Fatal("zero-value Filter should match all events")
			}
		})
	}
}

func TestMatchSeverityThreshold(t *testing.T) {
	ev := func(sev string) Snapshot {
		return Snapshot{Kind: EventFindingCreated, Severity: sev}
	}
	cases := []struct {
		min    string
		sev    string
		expect bool
	}{
		{"", "low", true},
		{"", "critical", true},
		{"high", "critical", true},
		{"high", "high", true},
		{"high", "medium", false},
		{"high", "low", false},
		{"critical", "high", false},
		{"critical", "critical", true},
		// Unknown level order is 0, should be blocked by any non-empty threshold (when in doubt, don't push).
		{"low", "", false},
		{"low", "unknown", false},
		{"", "", true},
	}
	for _, tc := range cases {
		got := Match(Filter{MinSeverity: tc.min}, ev(tc.sev))
		if got != tc.expect {
			t.Errorf("min=%q sev=%q: expected %v got %v", tc.min, tc.sev, tc.expect, got)
		}
	}
}

func TestMatchScopeRestrictions(t *testing.T) {
	ev := Snapshot{
		Kind:      EventFindingCreated,
		Severity:  "high",
		TaskID:    7,
		AssetIDs:  []int64{10, 20},
		VulnClass: "SQL注入",
	}
	cases := []struct {
		name   string
		filter Filter
		expect bool
	}{
		{"empty scope = no limit", Filter{}, true},
		{"task hit", Filter{TaskIDs: []int64{7}}, true},
		{"task missed", Filter{TaskIDs: []int64{8}}, false},
		{"task multi-select contains hit", Filter{TaskIDs: []int64{8, 7}}, true},
		{"asset intersection", Filter{AssetIDs: []int64{20, 99}}, true},
		{"asset no intersection", Filter{AssetIDs: []int64{99}}, false},
		{"task and asset both hit", Filter{TaskIDs: []int64{7}, AssetIDs: []int64{10}}, true},
		{"task hit but asset missed", Filter{TaskIDs: []int64{7}, AssetIDs: []int64{99}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Match(tc.filter, ev); got != tc.expect {
				t.Errorf("expected %v got %v", tc.expect, got)
			}
		})
	}
}

func TestMatchVulnClassKeywords(t *testing.T) {
	ev := func(class string) Snapshot {
		return Snapshot{Kind: EventFindingCreated, Severity: "high", VulnClass: class}
	}
	cases := []struct {
		name   string
		filter Filter
		class  string
		expect bool
	}{
		{"include empty = accept all", Filter{}, "任意类型", true},
		{"include hit", Filter{VulnClassInclude: []string{"SQL"}}, "SQL注入", true},
		{"include missed", Filter{VulnClassInclude: []string{"命令执行"}}, "SQL注入", false},
		{"include multi-word any hit", Filter{VulnClassInclude: []string{"命令执行", "SQL"}}, "SQL注入", true},
		{"case insensitive", Filter{VulnClassInclude: []string{"sql"}}, "SQL注入", true},
		{"exclude hit => excluded", Filter{VulnClassExclude: []string{"信息泄露"}}, "信息泄露", false},
		{"exclude missed => pass", Filter{VulnClassExclude: []string{"信息泄露"}}, "SQL注入", true},
		// Exclusion takes precedence over inclusion: when both hit, it's out.
		{"exclusion takes precedence over inclusion", Filter{
			VulnClassInclude: []string{"SQL"},
			VulnClassExclude: []string{"注入"},
		}, "SQL注入", false},
		// Pure blank keywords should be ignored, otherwise they degenerate to "matching all strings containing spaces".
		{"blank keywords ignored", Filter{VulnClassInclude: []string{"", "  "}}, "SQL注入", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Match(tc.filter, ev(tc.class)); got != tc.expect {
				t.Errorf("expected %v got %v", tc.expect, got)
			}
		})
	}
}

func TestMatchStatusChangeRequiresOptIn(t *testing.T) {
	ev := Snapshot{Kind: EventFindingStatusChanged, Severity: "critical", FromStatus: "pending", ToStatus: "fixed"}
	// Default off: most people mean "push new vulnerabilities" when they say "push vulnerabilities", not a status change log.
	if Match(Filter{MinSeverity: "low"}, ev) {
		t.Fatal("status change events should be skipped when not enabled")
	}
	if !Match(Filter{OnStatusChange: true}, ev) {
		t.Fatal("status change events should match after enabling on_status_change")
	}
	// Create events are not affected by on_status_change.
	created := Snapshot{Kind: EventFindingCreated, Severity: "critical"}
	if !Match(Filter{MinSeverity: "low"}, created) {
		t.Fatal("create events should not depend on on_status_change")
	}
}
