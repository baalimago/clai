# Fixture provenance

No live API keys were available during phase 1; all fixtures are authored from
vendor documentation per the worklog fixture-provenance rule
(worklogs/2026-08-26-audio-transcription-design/phase-1-segments-and-renderers.md).

- `openai_whisper1_verbose.json` — authored 2026-08-26 from the OpenAI API
  reference, "The transcription object (Verbose JSON)"
  (https://platform.openai.com/docs/api-reference/audio/verbose-json-object),
  model `whisper-1`.
- `openrouter_verbose.json` — authored 2026-08-26 from the OpenRouter audio
  transcriptions endpoint docs (https://openrouter.ai/docs/features/multimodal/audio),
  which mirror the OpenAI `verbose_json` shape; see the worklog README protocol
  survey (2026-08).
- `openai_diarized.json` — authored 2026-08-26 from the OpenAI
  `gpt-4o-transcribe-diarize` `diarized_json` documentation
  (https://platform.openai.com/docs/api-reference/audio/createTranscription);
  labels `A`/`B` per the worklog README protocol survey (2026-08).
