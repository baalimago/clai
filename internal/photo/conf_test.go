package photo

import "testing"

func TestValidateOutputType(t *testing.T) {
	valid := []OutputType{LOCAL, URL, UNSET}
	for _, v := range valid {
		if err := ValidateOutputType(v); err != nil {
			t.Errorf("expected no error for %v", v)
		}
	}
	if err := ValidateOutputType(OutputType("bad")); err == nil {
		t.Error("expected error for invalid output type")
	}
}
