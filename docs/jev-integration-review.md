# Jev / System One Integration Review

Review date: 2026-11-03. Protocol basis: TypeSafe official `https://api.typesafe.ai/openapi.json` (OpenAPI 3.1.0, API schema 0.2.0).

## Requests and Configuration

- Uses a Bearer API Key, submitting `model`, `state`, and named `questions` via `POST /v1/systemone`. Interception decisions use `choice` questions with `allow / ask / deny` criteria.
- `GET /v1/models` returns `models[].name`, not OpenAI's `data[].id`. The model management page can load the account's available models, with the default alias being `jev-latest`.
- The Base URL can be the service root address, `/v1`, or the full `/v1/systemone` address; only HTTP(S) is accepted, and query parameters and fragments are not accepted.
- Uses an independent proxy from the configuration; an empty proxy connects directly and does not inherit the process proxy environment. The Jev configuration can only be used for interception judging and cannot be set as a chat or task model.

## Decisions and Failure Handling

- Validates the type of `answers.decision`, the decision value, the required confidence, and the three probability fields. Confidence and probabilities must be finite numbers in the range 0–1; an invalid response enters the existing retry/failure policy and is not treated as a valid allow result.
- Results below the configured threshold go to manual approval; when evidence is incomplete or truncated, Jev's `allow` is turned into `ask`.
- HTTP errors, malformed responses, and cancel/timeout are treated as call failures; production calls obey the judge's single timeout and retry configuration. Connection tests and model list queries are limited to 30 seconds.
- Accounting uses the official `usage.input_tokens / output_tokens`; the review record retains the input and configuration summary.

## Validation Scope

Automated tests cover the model list native protocol, proxy, URL normalization, normal decisions, abnormal responses, low confidence, evidence truncation, usage, and management interface wiring; frontend type checking, static export, and existing frontend API regression tests are also included in the release validation.

**No real Jev API Key was used to call the production service.** The VM does not store a Jev profile, and the currently enabled judge is another model. Protocol and mock-service tests cannot prove actual account permissions, balance, service reachability, or model judgment accuracy; before going live, a Jev configuration should still be saved and connection tests and business-sample acceptance performed.

This new version includes the Jev feature and the above fixes. The historical `v0.3.25` does not include the Jev integration; restoring the VM to that version will not deploy the Jev feature.
