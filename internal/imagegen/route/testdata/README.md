# Fixture provenance (worklog 2026-10-21-image-support, phase 8)

- `openai-images-generations.json`, `openai-images-edits.json`, `openrouter-images.json` and
  `probe-glyph-K7Q.png` are copies of the worklog's recorded fixtures (live captures,
  2026-10-21; see the worklog's vendor image probe note).
- `gemini-generate-content.json` is a **reconstruction**: the `generateContent` response shape
  that the tests of the former Gemini photo querier used (removed in phase 16), in its REST
  spelling (`inlineData.mimeType`), carrying the 64×64 PNG of the OpenAI generations fixture.
