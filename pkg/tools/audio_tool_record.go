package tools

import (
	"context"
	"fmt"
	"math"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// AudioRecordCall is one parsed call. An empty or zero field takes the
// configured default.
type AudioRecordCall struct {
	// Devices records exactly these sources in this order and overrides Sinks
	// and Mic.
	Devices        []string
	MaxSeconds     int
	SilenceTimeout int
	NameSuffix     string
	Dir            string
	Sinks          string
	Mic            *bool
}

// AudioRecordEngine is injected by the clai runtime. It returns the text a
// model reads.
var AudioRecordEngine func(ctx context.Context, call AudioRecordCall) (string, error)

type AudioRecordTool pub_models.Specification

var audioRecordSinks = []string{"default", "all", "none"}

// AudioRecord records this machine's audio.
var AudioRecord = AudioRecordTool{
	Name:        "audio_record",
	Description: "Record this machine's microphone and/or system audio (what the speakers play) into one 16 kHz mono WAV file and return its path and times. The recording stops on Enter when the terminal is interactive, after the configured silence timeout, or at max_seconds. Call audio_sources first to learn the device names.",
	Inputs: &pub_models.InputSchema{
		Type: "object",
		Properties: map[string]pub_models.ParameterObject{
			"devices":                 {Type: "array", Description: "Exact device names from audio_sources, recorded in this order. When set, sinks and mic are ignored.", Items: &pub_models.ParameterObject{Type: "string"}},
			"max_seconds":             {Type: "integer", Description: "Hard stop after this many seconds. Omit to use the configured default."},
			"silence_timeout_seconds": {Type: "integer", Description: "Stop after this many seconds without sound above the threshold, once sound has been heard. Omit to use the configured default; the configured 0 disables the trigger."},
			"name_suffix":             {Type: "string", Description: "Short context appended to the file name, for example 'release-week'. Reduced to lowercase words joined by dashes."},
			"dir":                     {Type: "string", Description: "Directory for the recording. Omit to use the configured directory; a leading ~/ is expanded."},
			"sinks":                   {Type: "string", Description: "Which system outputs to record: 'default' (what you hear), 'all' (every output), or 'none'. Omit to use the configured default.", Enum: &audioRecordSinks},
			"mic":                     {Type: "boolean", Description: "Record the default microphone. Omit to use the configured default."},
		},
	},
}

func (a AudioRecordTool) Call(input pub_models.Input) (string, error) {
	return a.CallWithContext(context.Background(), input)
}

func (a AudioRecordTool) CallWithContext(ctx context.Context, input pub_models.Input) (string, error) {
	call, err := parseAudioRecordCall(input)
	if err != nil {
		return "", err
	}
	if AudioRecordEngine == nil {
		return "", fmt.Errorf("audio capture is not available in this runtime")
	}
	return AudioRecordEngine(ctx, call)
}

func (a AudioRecordTool) Specification() pub_models.Specification {
	return pub_models.Specification(a)
}

func parseAudioRecordCall(input pub_models.Input) (AudioRecordCall, error) {
	var call AudioRecordCall
	var err error
	if input == nil {
		return call, nil
	}

	if v, ok := input["devices"]; ok {
		arr, ok := v.([]any)
		if !ok {
			return AudioRecordCall{}, fmt.Errorf("devices must be an array")
		}
		call.Devices = make([]string, 0, len(arr))
		for i, raw := range arr {
			s, ok := raw.(string)
			if !ok || s == "" {
				return AudioRecordCall{}, fmt.Errorf("devices[%d] must be a non-empty string", i)
			}
			call.Devices = append(call.Devices, s)
		}
	}
	call.MaxSeconds, err = intArgument(input, "max_seconds")
	if err != nil {
		return AudioRecordCall{}, err
	}
	call.SilenceTimeout, err = intArgument(input, "silence_timeout_seconds")
	if err != nil {
		return AudioRecordCall{}, err
	}
	call.NameSuffix, err = stringArgument(input, "name_suffix")
	if err != nil {
		return AudioRecordCall{}, err
	}
	call.Dir, err = stringArgument(input, "dir")
	if err != nil {
		return AudioRecordCall{}, err
	}
	call.Sinks, err = stringArgument(input, "sinks")
	if err != nil {
		return AudioRecordCall{}, err
	}
	if v, ok := input["mic"]; ok {
		b, ok := v.(bool)
		if !ok {
			return AudioRecordCall{}, fmt.Errorf("mic must be a boolean")
		}
		call.Mic = &b
	}
	return call, nil
}

func intArgument(input pub_models.Input, name string) (int, error) {
	raw, ok := input[name]
	if !ok {
		return 0, nil
	}
	value, ok := raw.(float64)
	if !ok || value != math.Trunc(value) {
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	if value < math.MinInt32 || value > math.MaxInt32 {
		return 0, fmt.Errorf("%s is out of range", name)
	}
	return int(value), nil
}

func stringArgument(input pub_models.Input, name string) (string, error) {
	raw, ok := input[name]
	if !ok {
		return "", nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", name)
	}
	return value, nil
}
