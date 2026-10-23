package chat

import pub_models "github.com/baalimago/clai/pkg/text/models"

// ImagesDescribed returns a copy of messages in which every ImagePart is replaced
// by TextPart(Describe()), for surfaces that serialize a conversation as text.
// The caller's messages and parts are not modified.
func ImagesDescribed(messages []pub_models.Message) []pub_models.Message {
	out := make([]pub_models.Message, len(messages))
	for i, m := range messages {
		out[i] = m
		if len(m.Parts) == 0 {
			continue
		}
		out[i].Parts = make([]pub_models.Part, len(m.Parts))
		for j, p := range m.Parts {
			if img, ok := p.(pub_models.ImagePart); ok {
				p = pub_models.TextPart{Text: img.Describe()}
			}
			out[i].Parts[j] = p
		}
	}
	return out
}
