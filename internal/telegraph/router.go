package telegraph

import (
	"context"
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"regexp"
	"strings"
	"sync"
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

	ackMu   sync.Mutex
	ackDeck []string // shuffled phrases, popped from end
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
	fmt.Fprintf(r.out, "telegraph: router: recv [ch=%s thread=%s user=%s] %q\n",
		msg.ChannelID, msg.ThreadID, msg.UserName, truncate(text, 80))

	// 2. Command prefix ("!ry ...") or @mention with command ("@bot status").
	if isCommand(text) {
		fmt.Fprintf(r.out, "telegraph: router: → command\n")
		r.handleCommand(ctx, msg, text)
		return
	}
	if mentionCmd := r.extractMentionCommand(text); mentionCmd != "" {
		fmt.Fprintf(r.out, "telegraph: router: → mention-command %q\n", mentionCmd)
		r.handleCommand(ctx, msg, commandPrefix+" "+mentionCmd)
		return
	}

	// 3. Thread reply with active session.
	if msg.ThreadID != "" && r.sessionMgr.HasSession(msg.ChannelID, msg.ThreadID) {
		fmt.Fprintf(r.out, "telegraph: router: → active session [ch=%s thread=%s]\n", msg.ChannelID, msg.ThreadID)
		r.sendAck(ctx, msg.ChannelID, msg.ThreadID)
		if err := r.sessionMgr.Route(ctx, msg.ChannelID, msg.ThreadID, msg.UserName, text); err != nil {
			log.Printf("telegraph: router: route to session: %v", err)
		}
		return
	}

	// 4. Thread reply with historic session → resume.
	if msg.ThreadID != "" && r.sessionMgr.HasHistoricSession(msg.ChannelID, msg.ThreadID) {
		fmt.Fprintf(r.out, "telegraph: router: → resume session [ch=%s thread=%s]\n", msg.ChannelID, msg.ThreadID)
		r.sendAck(ctx, msg.ChannelID, msg.ThreadID)
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
		fmt.Fprintf(r.out, "telegraph: router: → new session [ch=%s thread=%s]\n", msg.ChannelID, threadID)
		r.sendAck(ctx, msg.ChannelID, threadID)
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
	fmt.Fprintf(r.out, "telegraph: router: → ignore (no mention, no thread session)\n")
}

// truncate returns s truncated to maxLen with "..." appended if needed.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// handleCommand dispatches a "!ry" command and sends the response.
func (r *Router) handleCommand(ctx context.Context, msg InboundMessage, text string) {
	response := r.cmdHandler.Execute(text)
	if err := r.adapter.Send(ctx, OutboundMessage{
		ChannelID: msg.ChannelID,
		ThreadID:  msg.ThreadID,
		Text:      response,
	}); err != nil {
		log.Printf("telegraph: router: send command response: %v", err)
	}
}

// ackPhrases are the random acknowledgment messages the bot sends when it
// starts working on a dispatch request.
var ackPhrases = []string{
	"On it, boss.",
	"Looking into it...",
	"Making some juicy stuff happen...",
	"Copy that, working on it now.",
	"Roger that. Give me a sec.",
	"Firing up the engines...",
	"Let me see what I can do.",
	"Already on it.",
	"Hold tight, working my magic...",
	"Consider it done. Well, almost.",
}

// sendAck sends a random acknowledgment message to the chat platform so the
// user knows the bot received their request and is working on it. It cycles
// through all phrases in shuffled order before repeating any.
func (r *Router) sendAck(ctx context.Context, channelID, threadID string) {
	phrase := r.nextAck()
	if err := r.adapter.Send(ctx, OutboundMessage{
		ChannelID: channelID,
		ThreadID:  threadID,
		Text:      phrase,
	}); err != nil {
		log.Printf("telegraph: router: send ack: %v", err)
	}
}

// nextAck returns the next ack phrase from the shuffled deck. When the deck
// is exhausted it reshuffles, guaranteeing every phrase is used before repeats.
func (r *Router) nextAck() string {
	r.ackMu.Lock()
	defer r.ackMu.Unlock()

	if len(r.ackDeck) == 0 {
		r.ackDeck = make([]string, len(ackPhrases))
		copy(r.ackDeck, ackPhrases)
		rand.Shuffle(len(r.ackDeck), func(i, j int) {
			r.ackDeck[i], r.ackDeck[j] = r.ackDeck[j], r.ackDeck[i]
		})
	}

	phrase := r.ackDeck[len(r.ackDeck)-1]
	r.ackDeck = r.ackDeck[:len(r.ackDeck)-1]
	return phrase
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
