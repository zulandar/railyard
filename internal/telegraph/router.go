package telegraph

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"strings"
)

// commandPrefix is the prefix that triggers read-only command handling.
const commandPrefix = "!ry"

// Router classifies inbound chat messages and routes them to the
// appropriate handler: session manager for dispatch, command handler
// for read-only queries, or ignore for bot/unknown messages.
type Router struct {
	sessionMgr *SessionManager
	cmdHandler *CommandHandler
	adapter    Adapter
	botUserID  string // the bot's own user ID (to filter self-messages)
	out        io.Writer
}

// RouterOpts holds parameters for creating a Router.
type RouterOpts struct {
	SessionMgr *SessionManager
	CmdHandler *CommandHandler
	Adapter    Adapter
	BotUserID  string    // bot's user ID for self-message filtering
	Out        io.Writer // defaults to os.Stdout
}

// NewRouter creates a Router.
func NewRouter(opts RouterOpts) (*Router, error) {
	if opts.SessionMgr == nil {
		return nil, fmt.Errorf("telegraph: router: session manager is required")
	}
	if opts.CmdHandler == nil {
		return nil, fmt.Errorf("telegraph: router: command handler is required")
	}
	if opts.Adapter == nil {
		return nil, fmt.Errorf("telegraph: router: adapter is required")
	}
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}
	return &Router{
		sessionMgr: opts.SessionMgr,
		cmdHandler: opts.CmdHandler,
		adapter:    opts.Adapter,
		botUserID:  opts.BotUserID,
		out:        out,
	}, nil
}

// Handle classifies and routes a single inbound message. Routing paths:
//  1. Bot self-message → ignore
//  2. Command prefix "!ry" → command handler
//  3. Thread reply with active session → session manager Route()
//  4. Thread reply with historic session → session manager Resume()
//  5. @mention or new message → session manager NewSession()
//  6. Everything else → ignore
func (r *Router) Handle(ctx context.Context, msg InboundMessage) {
	// 1. Filter bot self-messages.
	if r.isSelfMessage(msg) {
		return
	}

	text := strings.TrimSpace(msg.Text)

	// 2. Command prefix ("!ry ...") or @mention with command ("@bot status").
	if isCommand(text) {
		r.handleCommand(ctx, msg, text)
		return
	}
	if mentionCmd := r.extractMentionCommand(text); mentionCmd != "" {
		r.handleCommand(ctx, msg, commandPrefix+" "+mentionCmd)
		return
	}

	// 3. Thread reply with active session.
	if msg.ThreadID != "" && r.sessionMgr.HasSession(msg.ChannelID, msg.ThreadID) {
		if err := r.sessionMgr.Route(ctx, msg.ChannelID, msg.ThreadID, msg.UserName, text); err != nil {
			log.Printf("telegraph: router: route to session: %v", err)
		}
		return
	}

	// 4. Thread reply with historic session → resume.
	if msg.ThreadID != "" && r.sessionMgr.HasHistoricSession(msg.ChannelID, msg.ThreadID) {
		_, err := r.sessionMgr.Resume(ctx, msg.ChannelID, msg.ThreadID, msg.UserName)
		if err != nil {
			log.Printf("telegraph: router: resume session: %v", err)
			return
		}
		// Route the message to the newly resumed session.
		if err := r.sessionMgr.Route(ctx, msg.ChannelID, msg.ThreadID, msg.UserName, text); err != nil {
			log.Printf("telegraph: router: route after resume: %v", err)
		}
		return
	}

	// 5. New message (non-thread or thread with no session history) → new session.
	if isMention(text) {
		threadID := msg.ThreadID
		if threadID == "" {
			threadID = msg.ChannelID // use channel as thread for top-level messages
		}
		_, err := r.sessionMgr.NewSession(ctx, "telegraph", msg.UserName, threadID, msg.ChannelID)
		if err != nil {
			log.Printf("telegraph: router: new session: %v", err)
			return
		}
		// Route the initial message.
		if err := r.sessionMgr.Route(ctx, msg.ChannelID, threadID, msg.UserName, text); err != nil {
			log.Printf("telegraph: router: route initial message: %v", err)
		}
		return
	}

	// 6. Unknown/unhandled message → ignore.
	fmt.Fprintf(r.out, "telegraph: router: ignoring message from %s in %s\n", msg.UserName, msg.ChannelID)
}

// handleCommand dispatches a "!ry" command and sends the response.
func (r *Router) handleCommand(ctx context.Context, msg InboundMessage, text string) {
	response := r.cmdHandler.Execute(text)
	threadID := msg.ThreadID
	if threadID == "" {
		threadID = msg.ChannelID
	}
	if err := r.adapter.Send(ctx, OutboundMessage{
		ChannelID: msg.ChannelID,
		ThreadID:  threadID,
		Text:      response,
	}); err != nil {
		log.Printf("telegraph: router: send command response: %v", err)
	}
}

// isSelfMessage returns true if the message is from the bot itself.
func (r *Router) isSelfMessage(msg InboundMessage) bool {
	return r.botUserID != "" && msg.UserID == r.botUserID
}

// isCommand returns true if the text starts with the command prefix.
func isCommand(text string) bool {
	return strings.HasPrefix(text, commandPrefix+" ") || text == commandPrefix
}

// discordMentionRe matches Discord mention formats: <@ID> or <@!ID>.
var discordMentionRe = regexp.MustCompile(`<@!?\d+>`)

// knownCommands is the set of top-level commands the CommandHandler supports.
var knownCommands = map[string]bool{
	"status": true,
	"car":    true,
	"engine": true,
	"help":   true,
}

// extractMentionCommand checks if the message is a bot @mention followed by
// a known command. Returns the command text (without the mention) if so,
// or empty string if not. Handles Discord <@ID> format and plain @name.
func (r *Router) extractMentionCommand(text string) string {
	// Strip Discord-style mentions: <@ID> or <@!ID>.
	stripped := discordMentionRe.ReplaceAllString(text, "")
	stripped = strings.TrimSpace(stripped)

	if stripped == "" {
		return ""
	}

	// Check if the first word is a known command.
	firstWord := strings.Fields(stripped)[0]
	if knownCommands[firstWord] {
		return stripped
	}

	return ""
}

// isMention returns true if the text contains an @mention pattern.
// This is a simple heuristic; platform-specific adapters may provide
// richer mention detection.
func isMention(text string) bool {
	return strings.Contains(text, "@")
}
