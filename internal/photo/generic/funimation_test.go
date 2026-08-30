package generic

import "testing"

func TestFunimation(t *testing.T) {
	if funimation(0) != "🕛" {
		t.Errorf("unexpected image for 0")
	}
	if funimation(43478260) != "🕧" {
		t.Errorf("unexpected image for step")
	}
}

func TestStartAnimation(t *testing.T) {
	stop := StartAnimation()
	stop()
}
