package bull

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/zulandar/railyard/internal/config"
	"gorm.io/gorm"
)

// StartOpts holds parameters for starting the Bull daemon.
type StartOpts struct {
	ConfigPath   string
	Config       *config.Config
	DB           *gorm.DB
	PollInterval time.Duration // default 60s
	Out          io.Writer     // default os.Stdout
}

// Start launches the bull daemon loop. It validates options, constructs
// dependencies, and delegates to RunDaemon.
func Start(ctx context.Context, opts StartOpts) error {
	if opts.Config == nil {
		return fmt.Errorf("bull: config is required")
	}
	if !opts.Config.Bull.Enabled {
		return fmt.Errorf("bull: bull.enabled is not true")
	}
	if opts.Config.Bull.GitHubToken == "" {
		return fmt.Errorf("bull: bull.github_token is required")
	}
	if len(opts.Config.Tracks) == 0 {
		return fmt.Errorf("bull: at least one track must be configured")
	}

	// TODO: construct DaemonStore from opts.DB once the store layer is built.
	return fmt.Errorf("bull: store not yet implemented")
}
