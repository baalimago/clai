package audio

import "sync"

// transcribeOverride holds run-scoped configuration for the
// audio_transcribe tool, so a normal query or chat can pick the
// transcription model its tools use (the tool engine otherwise reads
// audioConfig.json). Same pattern as pkgtools.SetCmdBanList: CLI flags
// configure the tool runtime for the duration of the run.
var (
	transcribeOverrideMu sync.RWMutex
	transcribeOverride   struct {
		model  string
		format string
	}
)

// SetTranscribeOverrides sets the model and transcript format the
// audio_transcribe tool uses, overriding audioConfig.json and the tool
// call's own output_format. An empty string leaves that setting alone. The
// format is validated here so a bad value fails the run immediately rather
// than mid-conversation, inside a tool call.
func SetTranscribeOverrides(model, format string) error {
	if format != "" {
		if _, err := ParseOutputFormat(format); err != nil {
			return err
		}
	}
	transcribeOverrideMu.Lock()
	defer transcribeOverrideMu.Unlock()
	transcribeOverride.model = model
	transcribeOverride.format = format
	return nil
}

// ResetTranscribeOverrides clears the overrides; the tool falls back to
// audioConfig.json and the tool call's own format.
func ResetTranscribeOverrides() {
	transcribeOverrideMu.Lock()
	defer transcribeOverrideMu.Unlock()
	transcribeOverride.model = ""
	transcribeOverride.format = ""
}

func transcribeOverrides() (model, format string) {
	transcribeOverrideMu.RLock()
	defer transcribeOverrideMu.RUnlock()
	return transcribeOverride.model, transcribeOverride.format
}

// applyTranscribeOverrides layers the run-scoped overrides onto the
// file-loaded config and the tool call's requested format.
func applyTranscribeOverrides(aConf *Configurations, outputFormat string) string {
	model, format := transcribeOverrides()
	if model != "" {
		aConf.Transcribe.Model = model
	}
	if format != "" {
		return format
	}
	return outputFormat
}
