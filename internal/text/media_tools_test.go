package text

import (
	"context"
	"errors"
	"reflect"
	"strings"
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
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("settings = %+v, want %+v", got, want)
	}
}

func TestResolveRemoteAudioModel(t *testing.T) {
	ladder := []string{"gpt-4o-transcribe-diarize", "or:elevenlabs/scribe-v2+diarize"}
	resolveTo := func(models []string, err error) func(context.Context, string) ([]string, error) {
		return func(context.Context, string) ([]string, error) { return models, err }
	}
	t.Run("a remote list is resolved for the tool", func(t *testing.T) {
		s := internal.MediaToolSettings{AudioModel: "cchub:ada/diarize@1"}
		if err := resolveToolAudioModels(context.Background(), &s, resolveTo(ladder, nil)); err != nil {
			t.Fatalf("resolveToolAudioModels: %v", err)
		}
		if !reflect.DeepEqual(s.AudioModels, ladder) || s.AudioModel != "cchub:ada/diarize@1" {
			t.Fatalf("settings = %+v, want the target kept and its list attached", s)
		}
	})
	t.Run("a local name is left to the tool", func(t *testing.T) {
		s := internal.MediaToolSettings{AudioModel: "meeting"}
		called := false
		err := resolveToolAudioModels(context.Background(), &s, func(context.Context, string) ([]string, error) {
			called = true
			return nil, nil
		})
		if err != nil || called || s.AudioModels != nil {
			t.Fatalf("err = %v, called = %v, settings = %+v, want nothing resolved", err, called, s)
		}
	})
	t.Run("a failed fetch names the target", func(t *testing.T) {
		s := internal.MediaToolSettings{AudioModel: "cchub:ada/diarize@1"}
		err := resolveToolAudioModels(context.Background(), &s, resolveTo(nil, errors.New("hub down")))
		if err == nil || !strings.Contains(err.Error(), "cchub:ada/diarize@1") || !strings.Contains(err.Error(), "hub down") {
			t.Fatalf("err = %v, want the target and the cause", err)
		}
	})
	t.Run("no resolver is an error", func(t *testing.T) {
		s := internal.MediaToolSettings{AudioModel: "cchub:ada/diarize@1"}
		if err := resolveToolAudioModels(context.Background(), &s, nil); err == nil {
			t.Fatal("resolveToolAudioModels: no error, want remote targets unavailable")
		}
	})
}
