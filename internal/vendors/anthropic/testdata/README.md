# Fixture provenance (worklog 2026-09-05-error-propagation, D14)

These are **reconstructions, not raw captures**. The live probe
(worklog session journal, "Live vendor probe", 2026-09-05) recorded no
anthropic error body; these bodies follow Anthropic's documented
`{"type": "error", "error": {"type", "message"}}` envelope. The
429 rate-limit facts under test travel in response headers
(`anthropic-ratelimit-*`), which the test sets alongside the body.
