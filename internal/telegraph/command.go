package telegraph

import (
	"fmt"
	"strings"

	"github.com/zulandar/railyard/internal/car"
	"github.com/zulandar/railyard/internal/models"
	"github.com/zulandar/railyard/internal/orchestration"
	"gorm.io/gorm"
)

// CommandHandler processes read-only "!ry" commands from chat.
// It does NOT acquire dispatch locks — all operations are read-only.
type CommandHandler struct {
	db             *gorm.DB
	statusProvider StatusProvider
}

// CommandHandlerOpts holds parameters for creating a CommandHandler.
type CommandHandlerOpts struct {
	DB             *gorm.DB
	StatusProvider StatusProvider // defaults to orchestration.Status()
}

// NewCommandHandler creates a CommandHandler.
func NewCommandHandler(opts CommandHandlerOpts) (*CommandHandler, error) {
	if opts.DB == nil {
		return nil, fmt.Errorf("telegraph: command handler: db is required")
	}
	sp := opts.StatusProvider
	if sp == nil {
		sp = &defaultStatusProvider{db: opts.DB, tmux: nil}
	}
	return &CommandHandler{
		db:             opts.DB,
		statusProvider: sp,
	}, nil
}

// Execute parses and executes a "!ry" command string. Returns the
// response text to send back to the chat channel.
func (ch *CommandHandler) Execute(text string) string {
	args := parseCommand(text)
	if len(args) == 0 {
		return ch.helpText()
	}

	switch args[0] {
	case "status":
		return ch.cmdStatus()
	case "car":
		return ch.cmdCar(args[1:])
	case "engine":
		return ch.cmdEngine(args[1:])
	case "help":
		return ch.helpText()
	default:
		return fmt.Sprintf("Unknown command: `%s`\n\n%s", args[0], ch.helpText())
	}
}

// parseCommand strips the "!ry" prefix and splits the remaining text.
func parseCommand(text string) []string {
	text = strings.TrimSpace(text)
	if text == commandPrefix {
		return nil
	}
	text = strings.TrimPrefix(text, commandPrefix+" ")
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	return strings.Fields(text)
}

// cmdStatus returns formatted railyard status.
func (ch *CommandHandler) cmdStatus() string {
	info, err := ch.statusProvider.Status()
	if err != nil {
		return fmt.Sprintf("Error getting status: %v", err)
	}
	return orchestration.FormatStatus(info)
}

// cmdCar handles "!ry car" subcommands.
func (ch *CommandHandler) cmdCar(args []string) string {
	if len(args) == 0 {
		return "Usage: `!ry car list [--track <track>] [--status <status>]` or `!ry car show <id>`"
	}

	switch args[0] {
	case "list":
		return ch.cmdCarList(args[1:])
	case "show":
		return ch.cmdCarShow(args[1:])
	default:
		return fmt.Sprintf("Unknown car subcommand: `%s`\nUsage: `!ry car list` or `!ry car show <id>`", args[0])
	}
}

// cmdCarList lists cars with optional filters.
func (ch *CommandHandler) cmdCarList(args []string) string {
	filters := car.ListFilters{}
	for i := 0; i < len(args)-1; i += 2 {
		switch args[i] {
		case "--track":
			filters.Track = args[i+1]
		case "--status":
			filters.Status = args[i+1]
		case "--type":
			filters.Type = args[i+1]
		}
	}

	cars, err := car.List(ch.db, filters)
	if err != nil {
		return fmt.Sprintf("Error listing cars: %v", err)
	}
	if len(cars) == 0 {
		return "No cars found."
	}

	return formatCarTable(cars)
}

// cmdCarShow shows details for a single car.
func (ch *CommandHandler) cmdCarShow(args []string) string {
	if len(args) == 0 {
		return "Usage: `!ry car show <car-id>`"
	}
	c, err := car.Get(ch.db, args[0])
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	return formatCarDetail(c)
}

// cmdEngine handles "!ry engine" subcommands.
func (ch *CommandHandler) cmdEngine(args []string) string {
	if len(args) == 0 || args[0] != "list" {
		return "Usage: `!ry engine list`"
	}

	var engines []models.Engine
	if err := ch.db.Where("status != ?", "dead").Order("track, id").Find(&engines).Error; err != nil {
		return fmt.Sprintf("Error listing engines: %v", err)
	}
	if len(engines) == 0 {
		return "No active engines."
	}

	return formatEngineTable(engines)
}

// helpText returns usage information for all commands.
func (ch *CommandHandler) helpText() string {
	return "**Railyard Commands**\n" +
		"`!ry status` — Railyard dashboard\n" +
		"`!ry car list [--track X] [--status X]` — List cars\n" +
		"`!ry car show <id>` — Car details\n" +
		"`!ry engine list` — List engines\n" +
		"`!ry help` — This message"
}

// formatCarTable formats a slice of cars as a markdown table.
func formatCarTable(cars []models.Car) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("**Cars** (%d)\n", len(cars)))
	b.WriteString(fmt.Sprintf("%-16s %-12s %-12s %-4s %s\n",
		"ID", "STATUS", "TRACK", "PRI", "TITLE"))
	for _, c := range cars {
		title := c.Title
		if len(title) > 40 {
			title = title[:37] + "..."
		}
		b.WriteString(fmt.Sprintf("%-16s %-12s %-12s %-4d %s\n",
			c.ID, c.Status, c.Track, c.Priority, title))
	}
	return b.String()
}

// formatCarDetail formats a single car with full details.
func formatCarDetail(c *models.Car) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("**%s** — %s\n", c.ID, c.Title))
	b.WriteString(fmt.Sprintf("Status: %s | Track: %s | Priority: %d\n", c.Status, c.Track, c.Priority))
	if c.Type != "" {
		b.WriteString(fmt.Sprintf("Type: %s\n", c.Type))
	}
	if c.Assignee != "" {
		b.WriteString(fmt.Sprintf("Assignee: %s\n", c.Assignee))
	}
	if c.Branch != "" {
		b.WriteString(fmt.Sprintf("Branch: %s\n", c.Branch))
	}
	if c.Description != "" {
		b.WriteString(fmt.Sprintf("\n%s\n", c.Description))
	}
	return b.String()
}

// formatEngineTable formats a slice of engines as a markdown table.
func formatEngineTable(engines []models.Engine) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("**Engines** (%d)\n", len(engines)))
	b.WriteString(fmt.Sprintf("%-14s %-12s %-10s %-14s\n",
		"ID", "TRACK", "STATUS", "CURRENT CAR"))
	for _, e := range engines {
		currentCar := e.CurrentCar
		if currentCar == "" {
			currentCar = "-"
		}
		b.WriteString(fmt.Sprintf("%-14s %-12s %-10s %-14s\n",
			e.ID, e.Track, e.Status, currentCar))
	}
	return b.String()
}
