package generic

import (
	"encoding/base64"
	"fmt"
	"net/http"

	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// GeneratedImage decodes a model-generated image into a pending ImagePart whose MIME
// is sniffed from its bytes. A non-empty declaredMIME must agree with the sniffed one.
func GeneratedImage(b64, declaredMIME string) (pub_models.ImagePart, error) {
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return pub_models.ImagePart{}, UnexpectedImage(fmt.Sprintf("generated image is not valid base64: %v", err))
	}
	sniffed, err := asset.Sniff(data)
	if err != nil {
		return pub_models.ImagePart{}, fmt.Errorf("%w: %w", UnexpectedImage("generated image is not a supported image"), err)
	}
	if declaredMIME != "" && declaredMIME != sniffed {
		return pub_models.ImagePart{}, &asset.MIMEMismatchError{Declared: declaredMIME, Sniffed: sniffed}
	}
	return pub_models.ImagePart{MIME: sniffed, Origin: pub_models.OriginGenerated, Data: data}, nil
}

// UnexpectedImage is the vocabulary error for an image frame that arrived mid-stream
// in a shape clai cannot use.
func UnexpectedImage(message string) *claierr.UnexpectedProviderResponseError {
	return claierr.NewUnexpectedProviderResponseWithAPI(&claierr.APIError{StatusCode: http.StatusOK, Message: message})
}
