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
- `responses_error_event_credit_balance_exhausted.json` — a **live
  capture** (2026-09-28, `DEBUG_OPENAI=1 clai -cm gpt-6-luna q ...`): the
  `error` stream event at HTTP 200 on an empty credit balance. Note the
  nested `error` envelope with `insufficient_quota` as the *type* and
  `credit_balance_exhausted` as the *code*, unlike the two reconstructions
  above.
