package telegraph

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/zulandar/railyard/internal/config"
	"gorm.io/gorm"
)

// Daemon is the main telegraph process. It connects to a chat platform via
// an Adapter, pumps inbound messages to a handler, and posts Railyard events
// to configured channels.
type Daemon struct {
	db      *gorm.DB
	cfg     *config.Config
	adapter Adapter
	out     io.Writer
}

// DaemonOpts holds parameters for creating a new Daemon.
type DaemonOpts struct {
	DB      *gorm.DB
	Config  *config.Config
	Adapter Adapter
	Out     io.Writer // defaults to os.Stdout
}

// NewDaemon creates a Daemon with the given options.
func NewDaemon(opts DaemonOpts) (*Daemon, error) {
	if opts.DB == nil {
		return nil, fmt.Errorf("telegraph: db is required")
	}
	if opts.Config == nil {
		return nil, fmt.Errorf("telegraph: config is required")
	}
	if opts.Adapter == nil {
		return nil, fmt.Errorf("telegraph: adapter is required")
	}
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}
	return &Daemon{
		db:      opts.DB,
		cfg:     opts.Config,
		adapter: opts.Adapter,
		out:     out,
	}, nil
}

// Run starts the telegraph daemon. It connects the adapter, starts listening
// for inbound messages, and blocks until the context is cancelled. On shutdown
// it closes the adapter gracefully.
func (d *Daemon) Run(ctx context.Context) error {
	fmt.Fprintf(d.out, "Telegraph connecting...\n")
	if err := d.adapter.Connect(ctx); err != nil {
		return fmt.Errorf("telegraph: connect: %w", err)
	}

	inbound, err := d.adapter.Listen(ctx)
	if err != nil {
		d.adapter.Close()
		return fmt.Errorf("telegraph: listen: %w", err)
	}

	fmt.Fprintf(d.out, "Telegraph online\n")

	// Post online status.
	if err := d.adapter.Send(ctx, OutboundMessage{
		Text: "Telegraph online",
	}); err != nil {
		log.Printf("telegraph: send online message: %v", err)
	}

	// Main event loop: pump inbound messages until context is cancelled.
	for {
		select {
		case <-ctx.Done():
			fmt.Fprintf(d.out, "Telegraph shutting down...\n")
			d.sendShutdown()
			if err := d.adapter.Close(); err != nil {
				log.Printf("telegraph: close adapter: %v", err)
			}
			fmt.Fprintf(d.out, "Telegraph stopped\n")
			return nil

		case msg, ok := <-inbound:
			if !ok {
				// Adapter closed the channel.
				fmt.Fprintf(d.out, "Telegraph inbound channel closed\n")
				return nil
			}
			d.handleInbound(msg)
		}
	}
}

// handleInbound processes a single inbound message from the chat platform.
func (d *Daemon) handleInbound(msg InboundMessage) {
	fmt.Fprintf(d.out, "Telegraph received: [%s] %s: %s\n", msg.ChannelID, msg.UserName, msg.Text)
}

// sendShutdown posts a shutdown message to the adapter (best-effort).
func (d *Daemon) sendShutdown() {
	ctx := context.Background()
	if err := d.adapter.Send(ctx, OutboundMessage{
		Text: "Telegraph shutting down",
	}); err != nil {
		log.Printf("telegraph: send shutdown message: %v", err)
	}
}
