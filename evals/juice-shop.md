# Juice Shop (technical vulnerability half)

Use [OWASP Juice Shop](https://github.com/juice-shop/juice-shop) alongside the logic range so the harness covers both classic technical vulns and business-logic flaws.

```bash
docker run -d --name juice-shop -p 3000:3000 bkimminich/juice-shop
# → http://localhost:3000
```

Export ARTEX findings from a Juice Shop task the same way as a range task, then score with a separate ground-truth file (or a combined export). Append metrics to the same `evals/results.csv` with a `target=juice-shop` note in the run label column if you extend the CSV manually.

The default `score.py` is tuned for `evals/range/groundtruth.json`; for Juice Shop, supply a JSON ground-truth list with the same shape (`id`, `endpoint`, `class`, `severity`, `description`) or score manually against the official score board.
