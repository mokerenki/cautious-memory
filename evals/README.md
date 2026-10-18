# ARTEX evaluation harness

Automated benchmark for logic-flaw detection (this range) and technical vulns (OWASP Juice Shop).

## Business-logic range

Standalone Flask app under `evals/range/` with **exactly five** seeded bugs:

| id | class | endpoint |
|----|-------|----------|
| bola-documents | BOLA/IDOR | GET/PUT `/api/documents/<id>` |
| price-tamper-checkout | price tampering | POST `/api/checkout` |
| race-coupon-redeem | race condition | POST `/api/coupon/redeem` |
| workflow-skip-refund-approve | workflow skip | POST `/api/refund/approve` |
| quota-bypass-projects | quota bypass | POST `/api/projects` |

Accounts: `alice/alice`, `bob/bob` (free users), `staff/staff` (staff/pro).

### Run with Docker Compose

From repo root:

```bash
docker compose --profile eval up -d --build eval-range
# health: http://127.0.0.1:5050/health
```

Or:

```bash
./evals/run_eval.sh
```

The script starts the range, waits until healthy, and prints a ready-to-paste ARTEX task payload. **It does not launch ARTEX or attacks.**

### Ground truth

Machine-readable: `evals/range/groundtruth.json`.

### Scoring

```bash
python3 evals/score.py evals/samples/sample_findings.json evals/range/groundtruth.json
# append a metrics row for trend tracking:
python3 evals/score.py evals/samples/sample_findings.json evals/range/groundtruth.json --csv evals/results.csv
```

Matching is keyword/class based against ground-truth `id` / `class` / `endpoint` / description tokens. See `score.py`.

### Juice Shop (technical half)

See [juice-shop.md](juice-shop.md).
