package anthropic

import "testing"

func Test_Setup(t *testing.T) {
	c := Claude{}

	t.Run("it should load environment variable from ANTHROPIC_API_KEY", func(t *testing.T) {
		want := "some-key"
		t.Setenv("ANTHROPIC_API_KEY", want)
		err := c.Setup()
		if err != nil {
			t.Fatalf("failed to run setup: %v", err)
		}
		got := c.apiKey
		if got != want {
			t.Fatalf("expected: %v, got: %v", want, got)
		}
	})
}
