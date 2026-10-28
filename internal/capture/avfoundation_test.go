package capture

import (
	"strings"
	"testing"
)

func TestSources_Darwin(t *testing.T) {
	list, err := parseAVFoundationDevices(`AVFoundation video devices:
[0] FaceTime HD Camera
AVFoundation audio devices:
[0] Built-in Microphone
[1] BlackHole 2ch
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("list = %+v, want a mic and a sink", list)
	}
	if list[0].Kind != Mic || !list[0].Default || list[0].Name != "0" {
		t.Fatalf("mic = %+v", list[0])
	}
	if list[1].Kind != Sink || !list[1].Default || list[1].Label != "BlackHole 2ch" {
		t.Fatalf("sink = %+v", list[1])
	}

	list, err = parseAVFoundationDevices("AVFoundation audio devices:\n[0] Built-in Microphone\n")
	if err != nil {
		t.Fatalf("parse without loopback: %v", err)
	}
	if len(list) != 1 || list[0].Kind != Mic {
		t.Fatalf("list without loopback = %+v, want one mic", list)
	}

	_, err = parseAVFoundationDevices("Unknown input format: 'avfoundation'")
	if err == nil || !strings.Contains(err.Error(), "avfoundation") {
		t.Fatalf("error = %v, want an avfoundation remedy", err)
	}
}
