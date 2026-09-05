# Fixture provenance (worklog 2026-09-05-error-propagation, D14)

These are **reconstructions, not raw captures**. The live probe
(worklog session journal, "Live vendor probe", 2026-09-05) could not
drain the openai account, so no live openai error body exists.

- `insufficient_quota_429.json` — the documented 429 quota-exhaustion
  envelope, reconstructed from OpenAI's error-codes guide
  (developers.openai.com/api/docs/guides/error-codes).
- `responses_error_event_insufficient_quota.json` — the top-level
  `error` Responses stream event, reconstructed from the Responses
  streaming-events API reference.
- `responses_failed_event_server_error.json` — the `response.failed`
  Responses stream event, reconstructed from the Responses
  streaming-events API reference (its documented example code is
  `server_error`).
