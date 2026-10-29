# Fixture provenance (worklog 2026-09-05-error-propagation, D14)

These are **reconstructions, not raw captures**. The live probe
(worklog session journal, "Live vendor probe", 2026-09-05) recorded no
anthropic error body; these bodies follow Anthropic's documented
`{"type": "error", "error": {"type", "message"}}` envelope. The
429 rate-limit facts under test travel in response headers
(`anthropic-ratelimit-*`), which the test sets alongside the body.

`credit_balance_400.json` is the one fixture that is a capture: the 400
Anthropic answered a paid account with when its credit ran out (28 October
2026), trimmed to the envelope.
