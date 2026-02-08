package chat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path"
	"strconv"
	"strings"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

const chatUsage = `clai - (c)ommand (l)ine (a)rtificial (i)ntelligence

Usage: clai [flags] chat <subcommand> <prompt/chatID>

You may start a new chat using the prompt-modification flags which
are normally used in query mode.

Commands:
  c|continue <chatID> <prompt>    Continue an existing chat with the given chat ID. Prompt is optional
  d|delete   <chatID>             Delete the chat with the given chat ID.
  l|list                          List all existing chats.
  dir                             Show chat info for CWD (dir binding or global prevQuery).

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
`

const (
	// index | created | messages | tokens | prompt
	selectChatTblFormat        = "%-6s| %-20s| %-8v | %-6s | %v"
	selectChatTblChoicesFormat = "(page: (%v/%v). goto chat: [<num>], next: [<enter>]/[n]ext, [p]rev, [q]uit): "
	actOnChatFormat            = `=== Chat info ===

file path: %v
created_at: %v
am replies:
	user:   '%v'
	tool:   '%v'
	system: '%v'
	assistant: '%v'

%v

(make [p]revQuery (-re/-reply flag), go [b]ack to list, [e]dit messages, [d]elete messages, [q]uit, [<enter>] to continue): `

	// index | role | length | summary
	editMessageTblFormat        = "%-6v| %-10v| %-7v| %v"
	editMessageChoicesFormat    = `(page: (%v/%v). edit message: [<num>], next: [<enter>]/[n]ext, [p]rev, [q]uit): `
	deleteMessagesChoicesFormat = `(page: (%v/%v). delete message: [<num0>,<num1>,<num2>,...], next: [<enter>]/[n]ext, [p]rev, [q]uit): `
)

type NotCyclicalImport struct {
	UseTools   bool
	UseProfile string
	Model      string
}

type ChatHandler struct {
	q           models.ChatQuerier
	debug       bool
	username    string
	subCmd      string
	chat        pub_models.Chat
	preMessages []pub_models.Message
	prompt      string
	confDir     string
	convDir     string
	config      NotCyclicalImport
	raw         bool

	out io.Writer
}

func (q *ChatHandler) Query(ctx context.Context) error {
	return q.actOnSubCmd(ctx)
}

func (cq *ChatHandler) actOnSubCmd(ctx context.Context) error {
	if cq.debug {
		ancli.PrintOK(fmt.Sprintf("chat: %+v\n", cq))
	}
	switch cq.subCmd {
	case "continue", "c":
		return cq.cont(ctx)
	case "list", "l":
		return cq.handleListCmd(ctx)
	case "delete", "d":
		return cq.deleteFromPrompt()
	case "query", "q":
		return errors.New("not yet implemented")
	case "dir":
		err := cq.dirInfo()
		if errors.Is(err, fs.ErrNotExist) {
			return errors.New("failed to print any chat information as there was no bound chats found. This is unusual, check if replies are enabled")
		}
		return err
	case "help", "h":
		claiConfDir, _ := utils.GetClaiConfigDir()
		fmt.Printf(chatUsage, claiConfDir)
		return nil
	default:
		return fmt.Errorf("unknown subcommand: '%s'\n%v", cq.subCmd, chatUsage)
	}
}

func (cq *ChatHandler) findChatByID(potentialChatIdx string) (pub_models.Chat, error) {
	chats, err := cq.list()
	if err != nil {
		return pub_models.Chat{}, fmt.Errorf("failed to list chats: %w", err)
	}
	split := strings.Split(potentialChatIdx, " ")
	firstToken := split[0]
	chatIdx, err := strconv.Atoi(firstToken)
	if err == nil {
		if chatIdx < 0 || chatIdx >= len(chats) {
			return pub_models.Chat{}, fmt.Errorf("chat index out of range")
		}
		// Reassemble the prompt from the split tokens, but without the index selecting the chat
		cq.prompt = strings.Join(split[1:], " ")
		return chats[chatIdx], nil
	}

	// Prefer exact ID match first (covers continuing a hash-id conversation).
	c, err := cq.getByID(firstToken)
	if err == nil {
		return c, nil
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return pub_models.Chat{}, fmt.Errorf("load chat by id %q: %w", firstToken, err)
	}

	// Backwards compatible fallbacks: derived IDs.
	c, err = cq.getByID(IDFromPrompt(potentialChatIdx))
	if err == nil {
		return c, nil
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return pub_models.Chat{}, fmt.Errorf("load chat by legacy prompt id: %w", err)
	}

	c, err = cq.getByID(HashIDFromPrompt(potentialChatIdx))
	if err != nil {
		return pub_models.Chat{}, fmt.Errorf("load chat by hash-from-prompt id: %w", err)
	}
	return c, nil
}

func (cq *ChatHandler) printChat(chat pub_models.Chat) error {
	// New default behavior: fast, heavily obfuscated preview.
	// This avoids expensive glow rendering and avoids printing message bodies.
	if err := printChatObfuscated(cq.out, chat, cq.raw); err != nil {
		return fmt.Errorf("print obfuscated chat: %w", err)
	}
	return nil
}

func (cq *ChatHandler) cont(ctx context.Context) error {
	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("prompt: %v", cq.prompt))
	}

	// Special case: `clai chat continue` with an empty string continues the chat
	// in the current directory. Fallback: globalScope.
	if strings.TrimSpace(cq.prompt) == "" {
		// 1) dir-scoped (CWD)
		if dsID, err := LoadDirScopeChatID(cq.confDir); err == nil && strings.TrimSpace(dsID) != "" {
			c, err := cq.getByID(dsID)
			if err != nil {
				// In the case that the linked dirscope chat for some reason doesnt exist
				// instead attempt to run global chat
				if errors.Is(err, fs.ErrNotExist) {
					goto global_scope
				}
				return fmt.Errorf("load dirscoped chat %q: %w", dsID, err)
			}
			if err := cq.printChat(c); err != nil {
				return fmt.Errorf("print dirscoped chat: %w", err)
			}
			return nil
		}

		// 2) globalScope
	global_scope:
		g, err := LoadGlobalScope(cq.confDir)
		if err != nil {
			return fmt.Errorf("load global scope chat: %w", err)
		}
		if strings.TrimSpace(g.ID) != "" {
			if err := cq.printChat(g); err != nil {
				return fmt.Errorf("print global scope chat: %w", err)
			}
			return nil
		}

		// 3) no chat found
		ancli.PrintErr("could not find chat with id: \"\"\n")
		return cq.handleListCmd(ctx)
	}

	chat, err := cq.findChatByID(cq.prompt)
	if err != nil {
		// If listing of chats failed, propagate error. This indicates a real filesystem or
		// permissions issue that should not be treated as "not found".
		if strings.Contains(err.Error(), "failed to list chats") {
			return fmt.Errorf("failed to get chat: %w", err)
		}

		// Otherwise, treat as a not-found case: inform the user and revert to the chat list UI.
		firstToken := strings.Split(cq.prompt, " ")[0]
		ancli.PrintErr(fmt.Sprintf("could not find chat with id: \"%v\"\n", firstToken))
		return cq.handleListCmd(ctx)
	}

	// If the conversation has a profile associated with it, prefer that when continuing.
	// This makes `chat continue <id-or-index>` use the profile last used for that chat.
	if chat.Profile != "" {
		cq.config.UseProfile = chat.Profile
	} else if cq.config.UseProfile != "" {
		// If no profile stored yet, stamp it so it persists going forward.
		chat.Profile = cq.config.UseProfile
	}

	if cq.prompt != "" {
		chat.Messages = append(chat.Messages, pub_models.Message{Role: "user", Content: cq.prompt})
	}
	if err := cq.printChat(chat); err != nil {
		return fmt.Errorf("failed to print chat: %w", err)
	}

	// New behavior: `clai chat continue` should not enter interactive loop.
	// Instead, bind the current working directory to this chat so that it can be
	// continued using directory-reply mode (-dre).
	if err := cq.UpdateDirScopeFromCWD(chat.ID); err != nil {
		return fmt.Errorf("failed to update directory-scoped binding: %w", err)
	}
	ancli.Noticef("chat %s is now replyable with flag \"clai -dre query <prompt>\"\n", chat.ID)
	return nil
}

func (cq *ChatHandler) deleteFromPrompt() error {
	c, err := cq.findChatByID(cq.prompt)
	if err != nil {
		return fmt.Errorf("failed to get chat to delete: %w", err)
	}
	err = os.Remove(path.Join(cq.convDir, fmt.Sprintf("%v.json", c.ID)))
	if err != nil {
		return fmt.Errorf("failed to delete chat: %w", err)
	}
	ancli.PrintOK(fmt.Sprintf("deleted chat '%v'\n", c.ID))
	return nil
}

func (cq *ChatHandler) getByID(ID string) (pub_models.Chat, error) {
	return FromPath(path.Join(cq.convDir, fmt.Sprintf("%v.json", ID)))
}

func New(q models.ChatQuerier,
	confDir,
	args string,
	preMessages []pub_models.Message,
	conf NotCyclicalImport,
	raw bool,
	out io.Writer,
) (*ChatHandler, error) {
	username := "user"
	debug := misc.Truthy(os.Getenv("DEBUG"))
	argsArr := strings.Split(args, " ")
	subCmd := argsArr[0]
	currentUser, err := user.Current()
	if err == nil {
		username = currentUser.Username
	}

	subPrompt := strings.Join(argsArr[1:], " ")
	claiDir, err := utils.GetClaiConfigDir()
	if err != nil {
		return nil, err
	}

	if out == nil {
		out = os.Stdout
	}
	return &ChatHandler{
		q:           q,
		username:    username,
		debug:       debug,
		subCmd:      subCmd,
		prompt:      subPrompt,
		preMessages: preMessages,
		confDir:     claiDir,
		convDir:     path.Join(claiDir, "conversations"),
		config:      conf,
		raw:         raw,
		out:         out,
	}, nil
}
