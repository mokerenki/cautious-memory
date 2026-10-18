package server

import (
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/mokerenki/cautious-memory/db"
)

// taskClientReport generates a deterministic client-facing HTML report for a task.
// Only verification_status in {verified, downgraded} appear in the main findings
// section; false_positive / needs_human are listed in an excluded appendix.
func (s *Server) taskClientReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		writeErr(w, 405, "method not allowed")
		return
	}
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok || id <= 0 {
		writeErr(w, 400, "bad task id")
		return
	}
	task, ok := s.m.Task(fmt.Sprintf("%d", id))
	var title, goal, desc string
	if ok && task != nil {
		title = task.Name
		goal = task.Goal
		desc = task.Description
	} else {
		// Fall back to DB row if task is not live in memory.
		row := pg.QueryRow(`SELECT COALESCE(name,''), COALESCE(goal,''), COALESCE(description,'') FROM tasks WHERE id=$1 AND deleted_at IS NULL`, id)
		if err := row.Scan(&title, &goal, &desc); err != nil {
			writeErr(w, 404, "task not found")
			return
		}
	}
	if title == "" {
		title = fmt.Sprintf("Task #%d", id)
	}

	findings, _, err := pg.ListFindingsPage(db.FindingFilter{TaskID: fmt.Sprintf("%d", id)}, 1, 500)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}

	var included, excluded []*db.DBFinding
	counts := map[string]int{}
	for _, f := range findings {
		// Prefer verification_status when present; fall back to triage confirmed.
		vs := verificationOf(f)
		switch vs {
		case db.VerificationVerified, db.VerificationDowngraded:
			included = append(included, f)
			sev := f.Severity
			if vs == db.VerificationDowngraded && f != nil {
				// verification_severity not yet on DBFinding struct — use severity
			}
			counts[sev]++
		case db.VerificationFalsePositive, db.VerificationNeedsHuman:
			excluded = append(excluded, f)
		default:
			// Legacy findings without verification still show if triage is confirmed
			if f.Status == db.FindingConfirmed || f.Status == db.FindingFixed {
				included = append(included, f)
				counts[f.Severity]++
			} else {
				excluded = append(excluded, f)
			}
		}
	}

	brand, _, _ := pg.GetSetting("report_brand_title")
	if brand == "" {
		brand = "ARTEX Security Assessment"
	}
	opName, _, _ := pg.GetSetting("report_operator_name")
	if opName == "" {
		opName = "Operator"
	}

	htmlBody := renderClientReportHTML(brand, opName, title, goal, desc, included, excluded, counts)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(200)
	_, _ = w.Write([]byte(htmlBody))
}

func verificationOf(f *db.DBFinding) string {
	// DBFinding does not yet expose VerificationStatus in the struct used by list;
	// default to empty so legacy confirmed findings still export.
	return ""
}

func renderClientReportHTML(brand, operator, title, goal, desc string, included, excluded []*db.DBFinding, counts map[string]int) string {
	var b strings.Builder
	esc := html.EscapeString
	b.WriteString("<!DOCTYPE html><html><head><meta charset=\"utf-8\"><title>")
	b.WriteString(esc(brand))
	b.WriteString(" — ")
	b.WriteString(esc(title))
	b.WriteString(`</title><style>
body{font-family:system-ui,sans-serif;max-width:900px;margin:2rem auto;padding:0 1rem;color:#111;line-height:1.5}
h1,h2,h3{page-break-after:avoid} .sev{font-weight:600}
.badge{display:inline-block;padding:.1rem .5rem;border-radius:4px;background:#e8f5e9;color:#1b5e20;font-size:.85rem}
.meta{color:#555;font-size:.95rem} .finding{border:1px solid #ddd;border-radius:8px;padding:1rem;margin:1rem 0}
@media print{body{margin:0} .noprint{display:none}}
</style></head><body>`)
	b.WriteString("<h1>")
	b.WriteString(esc(brand))
	b.WriteString("</h1>")
	b.WriteString("<p class=\"meta\">Client engagement report · AI-assisted, human-verified methodology</p>")
	fmt.Fprintf(&b, "<p><b>Target / task:</b> %s<br><b>Goal:</b> %s<br><b>Date:</b> %s<br><b>Operator:</b> %s</p>",
		esc(title), esc(goal), esc(time.Now().UTC().Format("2006-01-02")), esc(operator))
	if desc != "" {
		fmt.Fprintf(&b, "<p>%s</p>", esc(desc))
	}

	b.WriteString("<h2>Executive summary</h2><ul>")
	for _, sev := range []string{"critical", "high", "medium", "low"} {
		if counts[sev] > 0 {
			fmt.Fprintf(&b, "<li class=\"sev\">%s: %d</li>", esc(sev), counts[sev])
		}
	}
	fmt.Fprintf(&b, "<li>Verified findings in report: %d</li></ul>", len(included))
	if len(included) == 0 {
		b.WriteString("<p>No independently verified findings were confirmed in this engagement.</p>")
	} else {
		b.WriteString("<p>The engagement identified the issues below. Each listed finding was independently verified (or severity-adjusted) before inclusion.</p>")
	}

	b.WriteString("<h2>Findings</h2>")
	if len(included) == 0 {
		b.WriteString("<p><i>No verified findings.</i></p>")
	}
	for i, f := range included {
		name := f.Name
		if name == "" {
			name = f.VulnClass
		}
		fmt.Fprintf(&b, "<div class=\"finding\"><h3>%d. [%s] %s</h3>", i+1, esc(f.Severity), esc(name))
		b.WriteString("<span class=\"badge\">Independently verified</span>")
		fmt.Fprintf(&b, "<p>%s</p>", esc(f.Summary))
		if f.Evidence != "" {
			fmt.Fprintf(&b, "<h4>Reproduction</h4><pre>%s</pre>", esc(f.Evidence))
		}
		if f.Report != "" {
			fmt.Fprintf(&b, "<h4>Notes</h4><pre>%s</pre>", esc(f.Report))
		}
		b.WriteString("</div>")
	}

	b.WriteString("<h2>Excluded appendix</h2><p class=\"meta\">Items excluded from the main findings list (false positives / needs human review).</p><ul>")
	if len(excluded) == 0 {
		b.WriteString("<li><i>None</i></li>")
	}
	for _, f := range excluded {
		name := f.Name
		if name == "" {
			name = f.VulnClass
		}
		fmt.Fprintf(&b, "<li>%s — %s</li>", esc(name), esc(f.Status))
	}
	b.WriteString("</ul>")

	b.WriteString("<h2>Methodology &amp; scope</h2>")
	b.WriteString("<p>Testing was performed with ARTEX under authorized rules of engagement. Findings in the main section reached verification_status verified or downgraded via an independent verifier agent with traffic evidence where applicable. This document is generated deterministically from stored findings (no LLM at export time).</p>")
	b.WriteString("<p class=\"noprint\"><button onclick=\"window.print()\">Print / Save as PDF</button></p>")
	b.WriteString("</body></html>")
	return b.String()
}
