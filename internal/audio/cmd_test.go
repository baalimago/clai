package audio

import (
	"testing"
)

func TestApplyFlagOverridesForAudio(t *testing.T) {
	fileConf := Configurations{
		Transcribe: TranscribeConfig{
			Model:        "from-file",
			OutputFormat: "text",
			Parallelism:  2,
		},
	}
	t.Run("default flags leave file values", func(t *testing.T) {
		conf := fileConf
		ApplyFlagOverrides(&conf, &Flags{})
		if conf != fileConf {
			t.Errorf("expected file config untouched, got: %+v", conf)
		}
	})
	t.Run("set flags beat file values", func(t *testing.T) {
		conf := fileConf
		f := &Flags{}
		for _, sv := range []struct {
			v   interface{ Set(string) error }
			val string
		}{{&f.Model, "from-flag"}, {&f.Format, "json"}, {&f.Parallelism, "7"}} {
			if err := sv.v.Set(sv.val); err != nil {
				t.Fatalf("Set(%q): %v", sv.val, err)
			}
		}
		ApplyFlagOverrides(&conf, f)
		if conf.Transcribe.Model != "from-flag" {
			t.Errorf("expected flag model, got: %v", conf.Transcribe.Model)
		}
		if conf.Transcribe.OutputFormat != "json" {
			t.Errorf("expected flag format, got: %v", conf.Transcribe.OutputFormat)
		}
		if conf.Transcribe.Parallelism != 7 {
			t.Errorf("expected flag parallelism, got: %v", conf.Transcribe.Parallelism)
		}
	})
}
