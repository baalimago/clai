package models

import pub_models "github.com/baalimago/clai/pkg/text/models"

// ImageEvent carries one final image that a chat model produced as its output, on a
// completions channel. Image is pending: Data and MIME are set, and Origin is
// generated. It holds bytes, never base64 (I5).
type ImageEvent struct {
	Image pub_models.ImagePart
}

// ImageProgressEvent says an image is being generated and has not arrived yet.
type ImageProgressEvent struct{}
