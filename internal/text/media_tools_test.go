package text

import (
	"testing"

	"github.com/baalimago/clai/internal"
)

func TestApplyProfileOverrides_audioModel(t *testing.T) {
	t.Run("the flag overrules the profile", func(t *testing.T) {
		conf := Default
		conf.AudioModel = "profile-model"
		ApplyProfileOverrides(&conf, parsedQueryFlags(t, "-am", "flag-model", "hi"))
		if conf.AudioModel != "flag-model" {
			t.Fatalf("AudioModel = %q, want the flag's model", conf.AudioModel)
		}
	})
	t.Run("no flag keeps the profile's model", func(t *testing.T) {
		conf := Default
		conf.AudioModel = "profile-model"
		ApplyProfileOverrides(&conf, parsedQueryFlags(t, "hi"))
		if conf.AudioModel != "profile-model" {
			t.Fatalf("AudioModel = %q, want the profile's model", conf.AudioModel)
		}
	})
}

func TestMediaToolSettings_resolvesProfileModelAndFormatFlag(t *testing.T) {
	conf := Default
	conf.AudioModel = "profile-model"
	got := mediaToolSettings(parsedQueryFlags(t, "-af", "json", "hi"), &conf)
	want := internal.MediaToolSettings{AudioModel: "profile-model", AudioFormat: "json"}
	if got != want {
		t.Fatalf("settings = %+v, want %+v", got, want)
	}
}
