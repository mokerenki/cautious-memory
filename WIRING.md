# Manual wiring required (Phase 2–4)

The following one-line hooks must be present for full runtime integration.
They are intentionally small so they can be applied as a follow-up PR without
rewriting large files via the API.

## server/server.go — seed + routes

After `seedFindingRetester()`:

```go
if err := s.seedFindingVerifier(); err != nil {
    log.Printf("[verifier] seed: %v", err)
}
```

Next to retest routes:

```go
mux.HandleFunc("GET /api/exploration/findings/{id}/verifications", s.listFindingVerifications)
mux.HandleFunc("POST /api/exploration/findings/{id}/verify", s.startFindingVerification)
mux.HandleFunc("POST /api/tasks/{id}/report", s.taskClientReport)
```

## Auto-verify on create

Wherever `RecordFinding` / `report_finding` returns a new `finding_id`, call:

```go
s.maybeAutoVerify(findingID)
```

## Conversation finish

When a conversation with `agent_key == "verifier"` ends, call:

```go
s.finishVerification(verificationID, "completed"|"failed"|"stopped", reason)
```

(Mirror the existing retester finish path in the conversation runner.)

## Frontend badges

Map `verification_status`:

| status | badge |
|--------|-------|
| verified | green |
| false_positive | gray strikethrough |
| downgraded | amber |
| needs_human | red outline |
| unverified / verifying | muted spinner |
