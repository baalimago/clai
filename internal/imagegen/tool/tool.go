// Package tool is the client-side generate_image tool: any model generates or edits
// images through the injected imagegen.ImageGenerator.
package tool

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/imagegen"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// NewGenerateImageTool returns the tool. cfg carries the model and config dir;
// newGen is the injected constructor (I9).
func NewGenerateImageTool(cfg imagegen.GeneratorConfig, newGen imagegen.NewGeneratorFunc, store asset.AssetStorer) pub_models.PartsCaller {
	return &generateImage{cfg: cfg, newGen: newGen, store: store}
}

// Specification
//  1. It returns the schema above.
//
// CallParts
//  1. For each generated image, in order, it returns
//     TextPart "generated image <handle> saved at <abs path>" (plus
//     "\nrevised prompt: <p>" when present), then
//     ImagePart{Asset: ref, MIME, Origin: generated}. Each image is stored with
//     store.Put, and its path read with store.Path, before CallParts returns (I6, I7).
//  2. An input_images entry that is handle-length lowercase hex is resolved with
//     store.Resolve, then read with store.Load (I1, I2). Any other entry is a path:
//     "~" is expanded, the file is read, and its type is asset.Sniff(bytes). A path
//     that is not an image fails with asset.ErrNotImage, and a file larger than the
//     README's largest-local-image parameter fails with *asset.ImageTooLargeError,
//     each naming the path. The path input is never written to the store.
//  3. A missing prompt, or a prompt that is only whitespace, fails with ErrPromptRequired.
//  4. A size outside the enum, which is the tools parameter "Image sizes a caller may
//     request", fails with *InvalidSizeError.
//  5. An unknown or ambiguous handle fails with the asset error, wrapped with the
//     handle, so the model can correct itself.
//  6. A failure from newGen (*imagegen.UnknownImageModelError, *MissingAPIKeyError, or
//     the refusing constructor in tests) is returned unchanged.
//  7. A Generate error is returned wrapped with "generate_image:". Nothing is stored
//     for a failed call.
//  8. newGen is called at most once per tool instance, on the first call.
//  9. Call returns the string form of CallParts (TL1).
// 10. The rebus input is for display only: CallParts neither reads nor validates it.
//
// Rebus is the call's rebus input, as the presented image line shows it.
//  1. It returns the value trimmed, with each run of whitespace collapsed to one space,
//     when it holds one to three emoji and nothing else. An emoji is a keycap, a flag,
//     or an emoji code point with an optional skin tone and variation selector (a
//     black flag may add a tag sequence); up to four may be chained by zero-width
//     joiners.
//  2. A missing rebus, or one that is only whitespace, fails with ErrNoRebus.
//  3. Any other value, including a non-string, fails with *InvalidRebusError.

var ErrPromptRequired = errors.New("generate_image: prompt is required")

var ErrNoRebus = errors.New("generate_image: no rebus")

type InvalidRebusError struct{ Rebus any }

func (e *InvalidRebusError) Error() string {
	return fmt.Sprintf("generate_image: rebus %v is not one to three emoji", e.Rebus)
}

const maxRebusEmoji emojiCount = 3

func Rebus(in pub_models.Input) (string, error) {
	raw, present := in["rebus"]
	if !present {
		return "", ErrNoRebus
	}
	text, ok := raw.(string)
	if !ok {
		return "", &InvalidRebusError{Rebus: raw}
	}
	rebus := strings.Join(strings.Fields(text), " ")
	if rebus == "" {
		return "", ErrNoRebus
	}
	if n, err := countEmoji(rebus); err != nil || n > maxRebusEmoji {
		return "", &InvalidRebusError{Rebus: raw}
	}
	return rebus, nil
}

type emojiCount int

var errNotEmoji = errors.New("not an emoji")

const (
	zwj        = '\u200d'
	vs15, vs16 = '\ufe0e', '\ufe0f'
	keycap     = '\u20e3'
	blackFlag  = '\U0001f3f4'
	cancelTag  = '\U000e007f'
	// maxJoined is how many emoji a zero-width-joiner sequence may chain.
	maxJoined = 4
	// maxTags is how many tag characters a subdivision flag may carry.
	maxTags = 8
)

// countEmoji counts the space-separated emoji in s; anything else fails with
// errNotEmoji.
func countEmoji(s string) (emojiCount, error) {
	rs := []rune(s)
	var n emojiCount
	for i := 0; i < len(rs); {
		if rs[i] == ' ' {
			i++
			continue
		}
		next, err := emojiAt(rs, i)
		if err != nil {
			return 0, err
		}
		n++
		i = next
	}
	return n, nil
}

// emojiAt returns the index just past the one emoji that starts at rs[i].
func emojiAt(rs []rune, i int) (int, error) {
	switch r := rs[i]; {
	case isKeycapBase(r):
		j := i + 1
		if j < len(rs) && rs[j] == vs16 {
			j++
		}
		if j < len(rs) && rs[j] == keycap {
			return j + 1, nil
		}
		return 0, errNotEmoji
	case isRegionalIndicator(r):
		if i+1 < len(rs) && isRegionalIndicator(rs[i+1]) {
			return i + 2, nil
		}
		return 0, errNotEmoji
	}
	j, err := modifiedEmojiAt(rs, i)
	for joined := 1; err == nil && j < len(rs) && rs[j] == zwj; joined++ {
		if joined == maxJoined || j+1 == len(rs) {
			return 0, errNotEmoji
		}
		j, err = modifiedEmojiAt(rs, j+1)
	}
	return j, err
}

// modifiedEmojiAt reads one emoji base and the modifiers that may follow it: a
// variation selector, a skin tone, and for a black flag a tag sequence.
func modifiedEmojiAt(rs []rune, i int) (int, error) {
	if !isEmojiBase(rs[i]) {
		return 0, errNotEmoji
	}
	j := i + 1
	if j < len(rs) && isSkinTone(rs[j]) {
		j++
	}
	if j < len(rs) && (rs[j] == vs15 || rs[j] == vs16) {
		j++
	}
	if rs[i] != blackFlag || j == len(rs) || !isTag(rs[j]) {
		return j, nil
	}
	for tags := 0; j < len(rs) && isTag(rs[j]) && rs[j] != cancelTag; tags++ {
		if tags == maxTags {
			return 0, errNotEmoji
		}
		j++
	}
	if j < len(rs) && rs[j] == cancelTag {
		return j + 1, nil
	}
	return 0, errNotEmoji
}

func isKeycapBase(r rune) bool { return r == '#' || r == '*' || (r >= '0' && r <= '9') }

func isRegionalIndicator(r rune) bool { return r >= 0x1f1e6 && r <= 0x1f1ff }

func isSkinTone(r rune) bool { return r >= 0x1f3fb && r <= 0x1f3ff }

func isTag(r rune) bool { return r >= 0xe0020 && r <= 0xe007f }

// isEmojiBase reports whether r is an emoji code point that can start an emoji.
func isEmojiBase(r rune) bool {
	for _, span := range emojiSpans() {
		if r >= span[0] && r <= span[1] {
			return true
		}
	}
	return false
}

// emojiSpans are the Emoji code points of Unicode emoji-data 15.1, without keycap
// bases, regional indicators and skin tones, plus the two blocks newer emoji go to.
func emojiSpans() [][2]rune {
	return [][2]rune{
		{0xa9, 0xa9},
		{0xae, 0xae},
		{0x203c, 0x203c},
		{0x2049, 0x2049},
		{0x2122, 0x2122},
		{0x2139, 0x2139},
		{0x2194, 0x2199},
		{0x21a9, 0x21aa},
		{0x231a, 0x231b},
		{0x2328, 0x2328},
		{0x23cf, 0x23cf},
		{0x23e9, 0x23f3},
		{0x23f8, 0x23fa},
		{0x24c2, 0x24c2},
		{0x25aa, 0x25ab},
		{0x25b6, 0x25b6},
		{0x25c0, 0x25c0},
		{0x25fb, 0x25fe},
		{0x2600, 0x2604},
		{0x260e, 0x260e},
		{0x2611, 0x2611},
		{0x2614, 0x2615},
		{0x2618, 0x2618},
		{0x261d, 0x261d},
		{0x2620, 0x2620},
		{0x2622, 0x2623},
		{0x2626, 0x2626},
		{0x262a, 0x262a},
		{0x262e, 0x262f},
		{0x2638, 0x263a},
		{0x2640, 0x2640},
		{0x2642, 0x2642},
		{0x2648, 0x2653},
		{0x265f, 0x2660},
		{0x2663, 0x2663},
		{0x2665, 0x2666},
		{0x2668, 0x2668},
		{0x267b, 0x267b},
		{0x267e, 0x267f},
		{0x2692, 0x2697},
		{0x2699, 0x2699},
		{0x269b, 0x269c},
		{0x26a0, 0x26a1},
		{0x26a7, 0x26a7},
		{0x26aa, 0x26ab},
		{0x26b0, 0x26b1},
		{0x26bd, 0x26be},
		{0x26c4, 0x26c5},
		{0x26c8, 0x26c8},
		{0x26ce, 0x26cf},
		{0x26d1, 0x26d1},
		{0x26d3, 0x26d4},
		{0x26e9, 0x26ea},
		{0x26f0, 0x26f5},
		{0x26f7, 0x26fa},
		{0x26fd, 0x26fd},
		{0x2702, 0x2702},
		{0x2705, 0x2705},
		{0x2708, 0x270d},
		{0x270f, 0x270f},
		{0x2712, 0x2712},
		{0x2714, 0x2714},
		{0x2716, 0x2716},
		{0x271d, 0x271d},
		{0x2721, 0x2721},
		{0x2728, 0x2728},
		{0x2733, 0x2734},
		{0x2744, 0x2744},
		{0x2747, 0x2747},
		{0x274c, 0x274c},
		{0x274e, 0x274e},
		{0x2753, 0x2755},
		{0x2757, 0x2757},
		{0x2763, 0x2764},
		{0x2795, 0x2797},
		{0x27a1, 0x27a1},
		{0x27b0, 0x27b0},
		{0x27bf, 0x27bf},
		{0x2934, 0x2935},
		{0x2b05, 0x2b07},
		{0x2b1b, 0x2b1c},
		{0x2b50, 0x2b50},
		{0x2b55, 0x2b55},
		{0x3030, 0x3030},
		{0x303d, 0x303d},
		{0x3297, 0x3297},
		{0x3299, 0x3299},
		{0x1f004, 0x1f004},
		{0x1f0cf, 0x1f0cf},
		{0x1f170, 0x1f171},
		{0x1f17e, 0x1f17f},
		{0x1f18e, 0x1f18e},
		{0x1f191, 0x1f19a},
		{0x1f201, 0x1f202},
		{0x1f21a, 0x1f21a},
		{0x1f22f, 0x1f22f},
		{0x1f232, 0x1f23a},
		{0x1f250, 0x1f251},
		{0x1f300, 0x1f321},
		{0x1f324, 0x1f393},
		{0x1f396, 0x1f397},
		{0x1f399, 0x1f39b},
		{0x1f39e, 0x1f3f0},
		{0x1f3f3, 0x1f3f5},
		{0x1f3f7, 0x1f3fa},
		{0x1f400, 0x1f4fd},
		{0x1f4ff, 0x1f53d},
		{0x1f549, 0x1f54e},
		{0x1f550, 0x1f567},
		{0x1f56f, 0x1f570},
		{0x1f573, 0x1f57a},
		{0x1f587, 0x1f587},
		{0x1f58a, 0x1f58d},
		{0x1f590, 0x1f590},
		{0x1f595, 0x1f596},
		{0x1f5a4, 0x1f5a5},
		{0x1f5a8, 0x1f5a8},
		{0x1f5b1, 0x1f5b2},
		{0x1f5bc, 0x1f5bc},
		{0x1f5c2, 0x1f5c4},
		{0x1f5d1, 0x1f5d3},
		{0x1f5dc, 0x1f5de},
		{0x1f5e1, 0x1f5e1},
		{0x1f5e3, 0x1f5e3},
		{0x1f5e8, 0x1f5e8},
		{0x1f5ef, 0x1f5ef},
		{0x1f5f3, 0x1f5f3},
		{0x1f5fa, 0x1f64f},
		{0x1f680, 0x1f6c5},
		{0x1f6cb, 0x1f6d2},
		{0x1f6d5, 0x1f6d7},
		{0x1f6dc, 0x1f6e5},
		{0x1f6e9, 0x1f6e9},
		{0x1f6eb, 0x1f6ec},
		{0x1f6f0, 0x1f6f0},
		{0x1f6f3, 0x1f6fc},
		{0x1f7e0, 0x1f7eb},
		{0x1f7f0, 0x1f7f0},
		{0x1f900, 0x1f9ff},
		{0x1fa70, 0x1faff},
	}
}

type InvalidSizeError struct{ Size string }

func (e *InvalidSizeError) Error() string {
	return fmt.Sprintf("generate_image: size %q is not one of %s", e.Size, strings.Join(imageSizes(), ", "))
}

// SavedText is the first line of CallParts.1's TextPart for one image.
func SavedText(handle, path string) string { return "generated image " + handle + " saved at " + path }

// Name is the tool's name in a tool selection and on the wire.
const Name = "generate_image"

var (
	errInputImages = errors.New("generate_image: input_images must be an array of strings")
	errNoGenerator = errors.New("generate_image: no image generator is configured")
)

// imageSizes is the tools parameter "Image sizes a caller may request".
func imageSizes() []string {
	return []string{"auto", "1024x1024", "1536x1024", "1024x1536"}
}

type generateImage struct {
	cfg    imagegen.GeneratorConfig
	newGen imagegen.NewGeneratorFunc
	store  asset.AssetStorer

	once   sync.Once
	gen    imagegen.ImageGenerator
	genErr error
}

func (g *generateImage) Specification() pub_models.Specification {
	sizes := imageSizes()
	return pub_models.Specification{
		Name:        Name,
		Description: "Generate a new image, or edit existing ones. To edit or use images as references, pass their handles (shown as [image <handle>] in the conversation) or local file paths in input_images.",
		Inputs: &pub_models.InputSchema{
			Type:     "object",
			Required: []string{"prompt", "rebus"},
			Properties: map[string]pub_models.ParameterObject{
				"prompt": {Type: "string", Description: "What to generate, or how to change the input images."},
				"rebus":  {Type: "string", Description: "Exactly three emoji, separated by spaces, that summarise the image. Shown to the user beside it."},
				"input_images": {
					Type:        "array",
					Items:       &pub_models.ParameterObject{Type: "string"},
					Description: "Image handles or local file paths.",
				},
				"size": {Type: "string", Enum: &sizes, Description: "Output size. Default auto."},
			},
		},
	}
}

func (g *generateImage) Call(in pub_models.Input) (string, error) {
	parts, err := g.CallParts(context.Background(), in)
	if err != nil {
		return "", err
	}
	return pub_models.Message{Parts: parts}.Describe(), nil
}

func (g *generateImage) CallParts(ctx context.Context, in pub_models.Input) ([]pub_models.Part, error) {
	req, err := g.request(in)
	if err != nil {
		return nil, err
	}
	gen, err := g.generator()
	if err != nil {
		return nil, err
	}
	images, err := gen.Generate(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", Name, err)
	}
	if len(images) == 0 {
		return nil, fmt.Errorf("%s: %w", Name, imagegen.ErrNoImage)
	}
	return g.save(images)
}

func (g *generateImage) generator() (imagegen.ImageGenerator, error) {
	g.once.Do(func() {
		if g.newGen == nil {
			g.genErr = errNoGenerator
			return
		}
		g.gen, g.genErr = g.newGen(g.cfg)
		if g.gen == nil && g.genErr == nil {
			g.genErr = errNoGenerator
		}
	})
	return g.gen, g.genErr
}

func (g *generateImage) request(in pub_models.Input) (imagegen.ImageRequest, error) {
	prompt, _ := in["prompt"].(string)
	if strings.TrimSpace(prompt) == "" {
		return imagegen.ImageRequest{}, ErrPromptRequired
	}
	size, err := requestedSize(in)
	if err != nil {
		return imagegen.ImageRequest{}, err
	}
	entries, err := inputEntries(in["input_images"])
	if err != nil {
		return imagegen.ImageRequest{}, err
	}
	inputs := make([]asset.Image, 0, len(entries))
	for _, entry := range entries {
		img, err := g.input(entry)
		if err != nil {
			return imagegen.ImageRequest{}, err
		}
		inputs = append(inputs, img)
	}
	if len(inputs) == 0 {
		inputs = nil
	}
	return imagegen.ImageRequest{Prompt: prompt, Inputs: inputs, Size: size}, nil
}

func requestedSize(in pub_models.Input) (string, error) {
	raw, given := in["size"]
	if !given || raw == nil {
		return "", nil
	}
	size, ok := raw.(string)
	if !ok || !slices.Contains(imageSizes(), size) {
		return "", &InvalidSizeError{Size: fmt.Sprint(raw)}
	}
	return size, nil
}

func inputEntries(raw any) ([]string, error) {
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case []string:
		return v, nil
	case []any:
		entries := make([]string, 0, len(v))
		for i, el := range v {
			s, ok := el.(string)
			if !ok {
				return nil, fmt.Errorf("%w: entry %d is %T", errInputImages, i, el)
			}
			entries = append(entries, s)
		}
		return entries, nil
	}
	return nil, fmt.Errorf("%w, got %T", errInputImages, raw)
}

func (g *generateImage) input(entry string) (asset.Image, error) {
	if !isHandle(entry) {
		return readLocalImage(entry)
	}
	ref, err := g.store.Resolve(entry)
	if err != nil {
		return asset.Image{}, fmt.Errorf("%s: input image %s: %w", Name, entry, err)
	}
	img, err := g.store.Load(ref)
	if err != nil {
		return asset.Image{}, fmt.Errorf("%s: input image %s: %w", Name, entry, err)
	}
	return img, nil
}

func isHandle(entry string) bool {
	if len(entry) != pub_models.HandleLength {
		return false
	}
	return strings.Trim(entry, "0123456789abcdef") == ""
}

// readLocalImage reads a path input; asset.ReadLocalImage names the path in every failure.
func readLocalImage(entry string) (asset.Image, error) {
	path, err := utils.AbsHomePath(entry)
	if err != nil {
		return asset.Image{}, fmt.Errorf("%s: input image %s: %w", Name, entry, err)
	}
	img, err := asset.ReadLocalImage(path)
	if err != nil {
		return asset.Image{}, fmt.Errorf("%s: input %w", Name, err)
	}
	return img, nil
}

func (g *generateImage) save(images []imagegen.GeneratedImage) ([]pub_models.Part, error) {
	parts := make([]pub_models.Part, 0, 2*len(images))
	for i, generated := range images {
		ref, err := g.store.Put(generated.Image.Data, generated.Image.MIME)
		if err != nil {
			return nil, fmt.Errorf("%s: store image %d: %w", Name, i+1, err)
		}
		path, err := g.store.Path(ref)
		if err != nil {
			return nil, fmt.Errorf("%s: store image %d: %w", Name, i+1, err)
		}
		img := pub_models.ImagePart{Asset: ref, MIME: generated.Image.MIME, Origin: pub_models.OriginGenerated}
		handle, err := img.Handle()
		if err != nil {
			return nil, fmt.Errorf("%s: store image %d: %w", Name, i+1, err)
		}
		text := SavedText(handle, path)
		if generated.RevisedPrompt != "" {
			text += "\nrevised prompt: " + generated.RevisedPrompt
		}
		parts = append(parts, pub_models.TextPart{Text: text}, img)
	}
	return parts, nil
}
