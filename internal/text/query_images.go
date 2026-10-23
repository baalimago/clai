package text

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/asset"
	"github.com/baalimago/clai/internal/board"
	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/imagegen"
	"github.com/baalimago/clai/internal/imagegen/route"
	imagegentool "github.com/baalimago/clai/internal/imagegen/tool"
	"github.com/baalimago/clai/internal/imagenorm"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/present"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/table"
)

// attachImageTurn hands the query command's ImageTurn to the querier (query.2).
func attachImageTurn(confDir string, tConf *Configurations, tf internal.TextFlags, deps QueryCommandDeps, q models.Querier) error {
	setter, ok := q.(interface {
		setImageTurn(*ImageTurn)
		imageVendor() route.ChatVendor
	})
	if !ok {
		return fmt.Errorf("attach image turn: %T is not a text querier", q)
	}
	turn, err := newImageTurn(confDir, tConf, tf, deps, setter.imageVendor())
	if err != nil {
		return err
	}
	setter.setImageTurn(turn)
	return nil
}

func newImageTurn(confDir string, tConf *Configurations, tf internal.TextFlags, deps QueryCommandDeps, vendor route.ChatVendor) (*ImageTurn, error) {
	gen, err := imageGenSetup(confDir, tConf, tf, deps, vendor)
	if err != nil {
		return nil, err
	}
	out := tConf.Out
	if out == nil {
		out = os.Stdout
	}
	terminal := utils.IsTerminalWriter(out)
	store := asset.NewStore(chat.AssetsDir(confDir))
	return &ImageTurn{
		Store:      store,
		Normalizer: imagenorm.New(store),
		Presenter: present.New(present.Options{
			Out:      out,
			Terminal: terminal,
			Color:    terminal && !table.NoColor() && !tConf.Raw,
			AutoOpen: tConf.AutoOpenImages,
			Opener:   present.NewCommandOpener(tConf.ImageOpener),
		}),
		Persister:    chat.NewPersister(confDir, store),
		Stderr:       os.Stderr,
		Generation:   gen,
		NewGenerator: deps.NewImageGenerator,
		Progress:     board.NewStatus(out, terminal, imageProgressTitle),
	}, nil
}

// imageGenSetup resolves the generate_image model only when the run selects the tool
// (query.2): -pm, else route.ImageModelFor the chat vendor and the photoConfig model.
// An implicit all-tools run (use-tools with no list) does not select it.
func imageGenSetup(confDir string, tConf *Configurations, tf internal.TextFlags, deps QueryCommandDeps, vendor route.ChatVendor) (ImageGenSetup, error) {
	gen := ImageGenSetup{
		Requested: selectsGenerateImage(strings.Split(tf.AgentText.UseTools.Value(), ",")...) ||
			(tConf.UseTools && (tConf.explicitAllTools || selectsGenerateImage(tConf.RequestedToolGlobs...))),
		Config: imagegen.GeneratorConfig{ConfigDir: confDir},
	}
	if !gen.Requested {
		return gen, nil
	}
	if gen.Config.Model = tf.AgentText.MediaTools.PhotoModel.Value(); gen.Config.Model != "" {
		return gen, nil
	}
	photoModel := ""
	if deps.PhotoModel != nil {
		model, err := deps.PhotoModel(confDir)
		if err != nil {
			return ImageGenSetup{}, fmt.Errorf("load the generate_image model: %w", err)
		}
		photoModel = model
	}
	gen.Config.Model = route.ImageModelFor(vendor, photoModel)
	return gen, nil
}

// selectsGenerateImage reports whether a tool selection entry matches generate_image
// as tools.WildcardMatch matches any tool.
func selectsGenerateImage(entries ...string) bool {
	return slices.ContainsFunc(entries, func(entry string) bool {
		return tools.WildcardMatch(strings.TrimSpace(entry), imagegentool.Name)
	})
}
