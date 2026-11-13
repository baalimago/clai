# Fixture provenance (worklog 2026-09-05-error-propagation, D14)

`credits_402.json` is a **reconstruction, not a raw capture**, of the
live-probe evidence journaled 2026-09-05 ("Live vendor probe"):
openrouter answered `402` with `metadata.limit_source:
"openrouter_credits"` and a **numeric** `code`. The raw probe body was
not preserved; the message text is representative, the journal-recorded
`limit_source` value and numeric code are the load-bearing shape.

`scribe_v2_diarized.json` is a **raw capture** of a live
`POST /api/v1/audio/transcriptions` on 2026-11-13: multipart, model
`elevenlabs/scribe-v2`, `response_format=verbose_json`, `diarize=true`, a
21 s two-speaker TTS clip. The two names the recognizer heard were replaced
with `Alex`; nothing else was edited. The speaker sits only on `words[]`.

`diarize_unsupported_400.json` is the 400 body the same request got from
`deepgram/nova-3` (and six other models), with the `user_id` member removed.
