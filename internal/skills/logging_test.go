package skills

import "testing"

func TestDebugSkills(t *testing.T) {
	for _, env := range []string{"DEBUG", "DEBUG_SKILLS"} {
		t.Run(env, func(t *testing.T) {
			t.Setenv(env, "true")
			if !debugSkills() {
				t.Fatalf("expected %s to enable skill debugging", env)
			}
		})
	}
}
