# Fixture provenance (worklog 2026-09-05-error-propagation, D14)

`insufficient_balance_402.json` is a **reconstruction, not a raw
capture**, of the live-probe evidence journaled 2026-09-05 ("Live
vendor probe"): deepseek answered `402` with
`message: "Insufficient Balance"`, `code: "invalid_request_error"` and
`type: "unknown_error"` — the code and type fields are useless for
decoding, so the decoder keys on the status and preserves the body as
facts.
