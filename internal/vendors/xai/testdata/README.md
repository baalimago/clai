# Fixture provenance (worklog 2026-09-05-error-propagation, D14)

`drained_credits_403.json` is a **reconstruction, not a raw capture**,
of the live-probe evidence journaled 2026-09-05 ("Live vendor probe"):
xAI answers **403** for a drained account with a body saying the team
has "used all available credits". The raw probe body was not
preserved; the journal transcription of that phrase is the recognition
signal the decoder keys on (D11 — the baseline's auth guess must not
fire for this shape).

`forbidden_403.json` is a plain-auth counterexample: a 403 body
without the drained-credits phrase, for which the decoder stays
silent and the baseline's `ErrAuthFailed` stands.
