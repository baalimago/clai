package models

import "errors"

type ImageInput int

const (
	ImageInputNone   ImageInput = iota // the route accepts no image anywhere
	ImageInputInline                   // images travel inline as base64
)

type ToolResultImages int

const (
	ToolResultFallback ToolResultImages = iota // tool images must be relocated (I3)
	ToolResultNative                           // tool messages may carry images
)

type ImageCapabilities struct {
	Input      ImageInput
	ToolResult ToolResultImages
	MIMEs      []string // accepted MIME types, a subset of the README MIME-to-extension keys
}

// ImageEncoder is a StreamCompleter that declares and honours image capabilities.
type ImageEncoder interface {
	StreamCompleter
	// ImageCapabilities returns the capabilities of the route this completer will
	// call, per Local fact P5-1.
	//  1. It is constant for a configured completer, and it can be called before Setup.
	//  2. For the OpenAI completer it reflects the selected route: Responses gives
	//     ToolResultNative, and chat-completions gives ToolResultFallback.
	ImageCapabilities() ImageCapabilities
	// StreamCompletions, and CountInputTokens where the completer implements it
	// (added clauses):
	//  E1. A message without Parts is encoded byte-identically to the pre-worklog
	//      request (I10).
	//  E2. Each TextPart and ImagePart is encoded in order, in the vendor's own shape.
	//      The shape is built in the vendor's package, and the generic completer emits
	//      only the chat-completions shape (I8):
	//      chat-completions vendors (generic): {"type":"text"} and
	//        {"type":"image_url","image_url":{"url":"data:<MIME>;base64,<b64>"}};
	//      OpenAI Responses: input_text and {"type":"input_image","image_url":"data:…"};
	//      Anthropic: text blocks and
	//        {"type":"image","source":{"type":"base64","media_type":<MIME>,"data":<b64>}}.
	//  E3. On a ToolResultNative route, a "tool" message with Parts is encoded as the
	//      vendor's array tool result: a chat-completions tool content array, Responses
	//      function_call_output.output [input_text, input_image], or Anthropic
	//      tool_result.content [text, image].
	//  E4. An ImagePart with no Data fails with ErrImageNotLoaded before any HTTP.
	//  E5. Before any HTTP, *claierr.ImageUnsupportedError is returned for any of:
	//      an image on an ImageInputNone route; an image in an assistant message (I3); a
	//      tool image on a ToolResultFallback route (I3); a MIME type not in MIMEs (I4).
	//  E6. Base64 is produced only while the request body is built (I5).
	//  E7. Mistral's merge of consecutive assistant messages keeps their Parts, in order.
	//  E8. vendors.NormalizeToolCallSequence treats a user message that has Parts as a
	//      non-empty user message. A message that is only an image therefore still ends
	//      a tool-call sequence.
}

var ErrImageNotLoaded = errors.New("image part reached the encoder without bytes")
