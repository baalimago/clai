package text

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/models/modelstest"
	"github.com/baalimago/clai/internal/tools/toolstest"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// turnModel runs the route-neutral rows of TestImageEncoderContract on every kind of route.
func TestTurnFakes_ImageEncoderContract(t *testing.T) {
	data := pngOf(t, 90)
	sum := sha256.Sum256(data)
	img := pub_models.ImagePart{Asset: hex.EncodeToString(sum[:]) + ".png", MIME: "image/png", Origin: pub_models.OriginUser, Data: data}
	none := models.ImageCapabilities{Input: models.ImageInputNone, ToolResult: models.ToolResultFallback}
	for _, row := range []struct {
		name string
		caps models.ImageCapabilities
	}{{"native", nativeCaps()}, {"fallback", fallbackCaps()}, {"none", none}} {
		t.Run(row.name, func(t *testing.T) {
			modelstest.RunEncoderCases(t, modelstest.EncoderSubject{
				Vendor: "turn",
				Model:  "turn",
				Caps:   row.caps,
				Image:  img,
				New:    func() models.ImageEncoder { return &turnModel{caps: row.caps} },
				Send: func(t *testing.T, c pub_models.Chat) modelstest.Exchange {
					m := &turnModel{caps: row.caps}
					ch, err := m.StreamCompletions(t.Context(), c)
					if err == nil {
						for range ch {
						}
					}
					return modelstest.Exchange{Err: err, Requests: len(m.received())}
				},
			})
		})
	}
}

// partsTool, on its success and its failure path, runs the PartsCaller contract suite.
func TestTurnFakes_PartsCallerContract(t *testing.T) {
	parts := []pub_models.Part{
		pub_models.TextPart{Text: "before"},
		pub_models.ImagePart{Origin: pub_models.OriginTool, MIME: "image/png", Data: pngOf(t, 91)},
		pub_models.TextPart{Text: "after"},
	}
	broke := errors.New("snap broke")
	toolstest.RunPartsCallerContract(t, toolstest.PartsSubject{
		Caller: partsTool{name: "snap", parts: parts, fail: func(in pub_models.Input) error {
			if in["fail"] == true {
				return broke
			}
			return nil
		}},
		ImageIn:   pub_models.Input{},
		WantParts: parts,
		FailIn:    pub_models.Input{"fail": true},
		FailErr:   broke,
	})
}
