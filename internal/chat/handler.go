package chat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os/user"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/stdio"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/internal/vendors"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/dimensions"
)

const chatUsage = `clai - (c)ommand (l)ine (a)rtificial (i)ntelligence

Usage: clai [flags] chat <subcommand> <prompt/chatID>

You may start a new chat using the prompt-modification flags which
are normally used in query mode.

Commands:
  c|continue <chatID> <prompt>    Continue an existing chat with the given chat ID. Prompt is optional
  d|delete   <chatID>             Delete the chat with the given chat ID.
  l|list                          List all existing chats.
  dir                             Show legacy chat info for CWD (stable v1 output).
  dirv2                           Show chat info with total and recent token usage.
  s|summarize <window>            Generate a title and summary for conversations updated since the window (7d, 12h, 2026-09-01).

The chatID is the 5 first words of the prompt joined by underscores. Easiest
way to get the chatID is to list all chats with 'clai chat list'. You may also select
a chat by its index in the list of chats.

The chats are found in %v/.clai/conversations, here they may be manually edited
as JSON files.

Examples:
  - clai chat list
  - clai chat continue my_chat_id
  - clai chat continue 3
  - clai chat delete my_chat_id
  - clai chat dir
  - clai -r chat dirv2
  - clai chat summarize 7d
`

const (
	// index | role | length | summary
	editMessageTblFormat = "%-6v| %-10v| %-7v| %v"
)

// errListChats marks a chat-listing failure that is a real filesystem or
// permissions problem, distinct from "no chat found". It is package-internal:
// chat-listing state is not a provider meaning, so it does not join
// pkg/claierr (worklog 2026-09-05-error-propagation, phase 8).
var errListChats = errors.New("failed to list chats")

type ChatHandler struct {
	username string
	subCmd   string
	chat     pub_models.Chat
	prompt   string
	confDir  string
	convDir  string
	// profile is the -p override. Continuing a chat stamps it onto the
	// conversation so later -dre queries keep using it.
	profile string
	raw     bool
	// sendPrompt sends the prompt of `continue <chat> <prompt>`; nil only
	// previews it.
	sendPrompt func(ctx context.Context, req DirReplyRequest) error

	out    io.Writer
	input  io.Reader
	errOut io.Writer
	// selectPrompt shows a table's selection prompt; nil lets the table
	// write it to out.
	selectPrompt func(string) error
	// dims is the one terminal-dimensions snapshot of this chat command
	// invocation, bound to the handler's output writer. It is resolved once in
	// New and used by every width-aware render path of the handler. Tests
	// inject a snapshot to stay ambient-independent.
	dims dimensions.Dimensions

	// summarizer, summarizeOptions and upsertIndexBatch are set only for the
	// summarize verb; a nil upsertIndexBatch means UpsertChatIndexBatch.
	summarizer       models.Summarizer
	summarizeOptions summarizeOptions
	// forceLive draws the summarize board as on a terminal; boardWidth
	// fixes its width and now its clock (tests).
	forceLive        bool
	boardWidth       int
	now              func() time.Time
	upsertIndexBatch func(convDir string, chats []pub_models.Chat) error

	// foreignCache must stay a nil interface when there is none: a typed nil
	// would pass discovery's "always scan" guard. Tests assign it directly.
	foreignCache vendors.SourceCache
	// newForeignCache builds it lazily and at most once (D27): only list and
	// continue read the cache, while dir and dirv2 are shell-prompt hot paths.
	newForeignCache  func() vendors.SourceCache
	foreignCacheOnce sync.Once
	foreignCacheMu   sync.Mutex
	foreignWarnOnce  sync.Once
}

// foreignCacheOrNil resolves the index on first consultation. A construction
// that fails leaves a nil interface, read as "always scan" (D27).
func (cq *ChatHandler) foreignCacheOrNil() vendors.SourceCache {
	cq.foreignCacheOnce.Do(func() {
		if cq.newForeignCache == nil {
			return
		}
		if cache := cq.newForeignCache(); cache != nil {
			cq.foreignCacheMu.Lock()
			cq.foreignCache = cache
			cq.foreignCacheMu.Unlock()
		}
	})
	return cq.resolvedForeignCache()
}

// resolvedForeignCache returns the already-resolved index, or nil. It never
// constructs one, so a caller that only cleans up does not undo D27. It takes
// the lock because sync.Once's happens-before only reaches a goroutine that
// calls Do, and a deferred persist may run on another one (R3-02).
func (cq *ChatHandler) resolvedForeignCache() vendors.SourceCache {
	cq.foreignCacheMu.Lock()
	defer cq.foreignCacheMu.Unlock()
	return cq.foreignCache
}

func (q *ChatHandler) Query(ctx context.Context) error {
	return q.actOnSubCmd(ctx)
}

func (q *ChatHandler) SuppressCompletionNotification() bool {
	return q.raw && (q.subCmd == "dir" || q.subCmd == "dirv2")
}

func (cq *ChatHandler) actOnSubCmd(ctx context.Context) error {
	runenv.Debugf(ctx, runenv.SubsystemChat, "chat: %+v", cq)
	switch cq.subCmd {
	case "continue", "c":
		return cq.cont(ctx)
	case "list", "l":
		return cq.handleListCmd(ctx)
	case "delete", "d":
		return cq.deleteFromPrompt(ctx)
	case "query", "q":
		return errors.New("not yet implemented")
	case "summarize", "s":
		return cq.handleSummarize(ctx)
	case "dir", "dirv2":
		var err error
		if cq.subCmd == "dirv2" {
			err = cq.dirInfoV2(ctx)
		} else {
			err = cq.dirInfo(ctx)
		}
		if errors.Is(err, fs.ErrNotExist) {
			return errors.New("failed to print any chat information as there was no bound chats found. This is unusual, check if replies are enabled")
		}
		return err
	case "help", "h":
		claiConfDir, _ := utils.GetClaiConfigDir()
		fmt.Fprintf(cq.out, chatUsage, claiConfDir)
		return nil
	default:
		return fmt.Errorf("unknown subcommand: '%s'\n%v", cq.subCmd, chatUsage)
	}
}

func (cq *ChatHandler) findChatByID(ctx context.Context, potentialChatIdx string, foreign foreignSelection) (pub_models.Chat, error) {
	split := strings.Split(potentialChatIdx, " ")
	firstToken := split[0]
	chatIdx, err := strconv.Atoi(firstToken)
	if err == nil {
		paginator, err := NewChatIndexPaginator(ctx, cq.convDir)
		if err != nil {
			return pub_models.Chat{}, diag.Errorf("failed to read chat index: %w", err)
		}
		var chat pub_models.Chat
		if len(paginator.rows) == 0 {
			chats, listErr := cq.list(ctx)
			if listErr != nil {
				return pub_models.Chat{}, diag.Errorf("%w: %w", errListChats, listErr)
			}
			if chatIdx < 0 || chatIdx >= len(chats) {
				return pub_models.Chat{}, errIndexOutOfRange
			}
			chat = chats[chatIdx]
		} else {
			chat, err = cq.listedChat(ctx, paginator, chatIdx, foreign)
			if err != nil {
				return pub_models.Chat{}, err
			}
		}
		// Reassemble the prompt from the split tokens, but without the index selecting the chat
		cq.prompt = strings.Join(split[1:], " ")
		return chat, nil
	}

	// Prefer exact ID match first.
	c, err := cq.getByID(ctx, firstToken)
	if err == nil {
		return c, nil
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return pub_models.Chat{}, diag.Errorf("load chat by id %q: %w", firstToken, err)
	}
	return pub_models.Chat{}, diag.Errorf("load chat by id %q: %w", firstToken, err)
}

func (cq *ChatHandler) printChat(chat pub_models.Chat) error {
	// New default behavior: fast, heavily obfuscated preview.
	// This avoids expensive glow rendering and avoids printing message bodies.
	if err := printChatObfuscated(cq.out, chat, cq.raw, cq.dims.Width); err != nil {
		return diag.Errorf("print obfuscated chat: %w", err)
	}
	return nil
}

func (cq *ChatHandler) cont(ctx context.Context) error {
	runenv.Debugf(ctx, runenv.SubsystemChat, "prompt: %v", cq.prompt)

	// Special case: `clai chat continue` with an empty string continues the chat
	// in the current directory. Fallback: globalScope.
	if strings.TrimSpace(cq.prompt) == "" {
		// 1) dir-scoped (CWD)
		if dsID, err := LoadDirScopeChatID(cq.confDir); err == nil && strings.TrimSpace(dsID) != "" {
			c, err := cq.getByID(ctx, dsID)
			if err != nil {
				// In the case that the linked dirscope chat for some reason doesnt exist
				// instead attempt to run global chat
				if errors.Is(err, fs.ErrNotExist) {
					goto global_scope
				}
				return diag.Errorf("load dirscoped chat %q: %w", dsID, err)
			}
			if err := cq.printChat(c); err != nil {
				return diag.Errorf("print dirscoped chat: %w", err)
			}
			return nil
		}

		// 2) globalScope
	global_scope:
		g, err := LoadGlobalScope(ctx, cq.confDir)
		if err != nil {
			return diag.Errorf("load global scope chat: %w", err)
		}
		if strings.TrimSpace(g.ID) != "" {
			if err := cq.printChat(g); err != nil {
				return diag.Errorf("print global scope chat: %w", err)
			}
			return nil
		}

		// 3) no chat found
		runenv.Failuref(ctx, runenv.SubsystemChat, "could not find chat with id: \"\"")
		return cq.handleListCmd(ctx)
	}

	chat, err := cq.findChatByID(ctx, cq.prompt, foreignClone)
	if err != nil {
		// If listing of chats failed, propagate error. This indicates a real filesystem or
		// permissions issue that should not be treated as "not found".
		if errors.Is(err, errListChats) {
			return diag.Errorf("failed to get chat: %w", err)
		}

		// Otherwise, treat as a not-found case: inform the user and revert to the chat list UI.
		firstToken, _, _ := strings.Cut(cq.prompt, " ")
		runenv.Failuref(ctx, runenv.SubsystemChat, "could not find chat with id: \"%v\"", firstToken)
		return cq.handleListCmd(ctx)
	}

	// If the conversation has a profile associated with it, prefer that when continuing.
	// This makes `chat continue <id-or-index>` use the profile last used for that chat.
	if chat.Profile != "" {
		cq.profile = chat.Profile
	} else if cq.profile != "" {
		// If no profile stored yet, stamp it so it persists going forward.
		chat.Profile = cq.profile
	}

	if cq.prompt != "" {
		chat.Messages = append(chat.Messages, pub_models.Message{Role: "user", Content: cq.prompt})
	}
	if err := cq.printChat(chat); err != nil {
		return diag.Errorf("failed to print chat: %w", err)
	}

	// New behavior: `clai chat continue` should not enter interactive loop.
	// Instead, bind the current working directory to this chat so that it can be
	// continued using directory-reply mode (-dre).
	if err := cq.UpdateDirScopeFromCWD(ctx, chat.ID); err != nil {
		return diag.Errorf("failed to update directory-scoped binding: %w", err)
	}
	if cq.prompt != "" && cq.sendPrompt != nil {
		if err := cq.sendPrompt(ctx, DirReplyRequest{Prompt: cq.prompt, Profile: cq.profile, Raw: cq.raw}); err != nil {
			return diag.Errorf("failed to send prompt to chat %s: %w", chat.ID, err)
		}
		return nil
	}
	runenv.Noticef(ctx, runenv.SubsystemChat, "chat %s is now replyable with flag \"clai -dre query <prompt>\"", chat.ID)
	return nil
}

func (cq *ChatHandler) deleteFromPrompt(ctx context.Context) error {
	c, err := cq.findChatByID(ctx, cq.prompt, foreignRefuse)
	if err != nil {
		return diag.Errorf("failed to get chat to delete: %w", err)
	}
	_, err = convPersister(cq.convDir).Delete(c.ID)
	var sidecar *sidecarNotRemovedError
	var sweep *SweepFailedError
	if errors.As(err, &sidecar) && !errors.As(err, &sweep) {
		runenv.Warnf(ctx, runenv.SubsystemChat, "failed to remove reasoning sidecar for chat %q: %v", c.ID, sidecar.Cause)
		err = nil
	}
	if err != nil {
		return diag.Errorf("failed to delete chat: %w", err)
	}
	runenv.Noticef(ctx, runenv.SubsystemChat, "deleted chat '%v'", c.ID)
	return nil
}

func (cq *ChatHandler) getByID(ctx context.Context, ID string) (pub_models.Chat, error) {
	return convPersister(cq.convDir).Load(ctx, conversationPathFromDir(cq.convDir, ID))
}

// New builds the chat handler. args is the dispatched "<verb> <rest...>"
// string: the verb selects the action, the rest is the chat id or index.
// The handler holds no model querier — every chat subcommand reads stored
// transcripts and writes conversation state, so none of them query a
// vendor. A nil out is the run's stdout on ctx.
func New(ctx context.Context, confDir, args, profile string, raw bool, out io.Writer) (*ChatHandler, error) {
	if confDir == "" {
		return nil, errors.New("chat handler requires a config dir")
	}
	username := "user"
	argsArr := strings.Split(args, " ")
	subCmd := argsArr[0]
	if currentUser, err := user.Current(); err == nil {
		username = currentUser.Username
	}

	env := runenv.From(ctx)
	if out == nil {
		out = env.Stdout()
	}

	ch := &ChatHandler{
		username: username,
		subCmd:   subCmd,
		prompt:   strings.Join(argsArr[1:], " "),
		confDir:  confDir,
		convDir:  conversationsDir(confDir),
		profile:  profile,
		raw:      raw,
		out:      out,
		input:    env.Stdin(),
		errOut:   env.Stderr(),
		dims:     utils.SessionDimensions(out),
		// The tables write to out, which is the run's stdout under -json.
		selectPrompt: env.TablePrompter(stdio.Stdout),
	}

	// Macro mode: extra positional args after "chat list" become table inputs.
	if (subCmd == "list" || subCmd == "l") && len(argsArr) > 1 {
		ch.input = utils.NewMacroReader(argsArr[1:])
	}

	return ch, nil
}
