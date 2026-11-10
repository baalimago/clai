package tools

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/baalimago/clai/internal/runenv"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
)

type contextualTool interface {
	CallWithContext(context.Context, pub_models.Input) (string, error)
}

// ToolBox is the model's side of an offer: the tools its requests name.
type ToolBox interface {
	RegisterTool(pub_models.LLMTool)
}

// Offered is the set of tools one run has offered its model, and the only
// table a call from that model resolves through. Offering a tool and making
// it callable are one operation, so a model can never reach a tool its
// requests did not name. It is owned by one run and is not safe for
// concurrent use.
type Offered struct {
	tools   map[string]pub_models.LLMTool
	aliases map[string]string
}

// NewOffered returns an empty set.
func NewOffered() *Offered {
	return &Offered{tools: map[string]pub_models.LLMTool{}, aliases: map[string]string{}}
}

// Offer names tool in box's requests and makes it callable. A name is
// registered on box once; offering it again only replaces the instance its
// calls reach.
func (o *Offered) Offer(box ToolBox, tool pub_models.LLMTool) {
	name := tool.Specification().Name
	if _, offered := o.tools[name]; !offered {
		box.RegisterTool(tool)
	}
	o.tools[name] = tool
}

// Alias lets a call of alias reach the tool offered as canonical. It offers
// nothing: while canonical is not offered, alias is unknown too.
func (o *Offered) Alias(alias, canonical string) {
	o.aliases[alias] = canonical
}

// Get returns the tool a call of name reaches. A nil set offers nothing.
func (o *Offered) Get(name string) (pub_models.LLMTool, bool) {
	if o == nil {
		return nil, false
	}
	if tool, ok := o.tools[name]; ok {
		return tool, true
	}
	canonical, ok := o.aliases[name]
	if !ok {
		return nil, false
	}
	tool, ok := o.tools[canonical]
	return tool, ok
}

// Names returns the offered tool names, sorted. Aliases are not offers.
func (o *Offered) Names() []string {
	if o == nil {
		return nil
	}
	return slices.Sorted(maps.Keys(o.tools))
}

// Clone returns an independent copy, so runs built from one configuration do
// not share offers.
func (o *Offered) Clone() *Offered {
	if o == nil {
		return nil
	}
	return &Offered{tools: maps.Clone(o.tools), aliases: maps.Clone(o.aliases)}
}

// Invoke runs call when its tool is offered and returns UnknownToolCall's
// text when it is not.
func (o *Offered) Invoke(ctx context.Context, call pub_models.Call) string {
	tool, ok := o.Get(call.Name)
	if !ok {
		return UnknownToolCall(call.Name)
	}
	return Run(ctx, tool, call)
}

// UnknownToolCall is the tool result of a call naming a tool that was not
// offered.
func UnknownToolCall(name string) string {
	return "ERROR: unknown tool call: " + name
}

// Run calls tool with call's inputs and gathers both error and output in the
// same string.
func Run(ctx context.Context, tool pub_models.LLMTool, call pub_models.Call) string {
	if runenv.DebugEnabled(ctx, runenv.SubsystemCall) {
		runenv.Debugf(ctx, runenv.SubsystemCall, "Invoke call: %v", debug.IndentedJsonFmt(call))
	}
	// Arguments that were no JSON object run nothing: the model is told what
	// was wrong and can send the call again.
	if call.ArgumentsErr != "" {
		return "ERROR: " + call.ArgumentsErr
	}
	inp := pub_models.Input{}
	if call.Inputs != nil {
		inp = *call.Inputs
	}
	var out string
	var err error
	if ct, ok := tool.(contextualTool); ok {
		out, err = ct.CallWithContext(ctx, inp)
	} else {
		out, err = tool.Call(inp)
	}
	if err != nil {
		return fmt.Sprintf("ERROR: failed to run tool: %v, error: %v", call.Name, err)
	}
	return out
}
