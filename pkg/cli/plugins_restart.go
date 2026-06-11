package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// restartResult is the decoded body of a successful POST /plugins/restart.
// Mirrors the yardmaster handler's restartResponse wire shape.
type restartResult struct {
	Name     string `json:"name"`
	OldState string `json:"old_state"`
	NewState string `json:"new_state"`
}

// pluginsRestartPost is the indirection seam used by tests to swap the
// real HTTP POST for a canned response.
var pluginsRestartPost = defaultPluginsRestartPost

// defaultPluginsRestartPost performs the real HTTP POST to
// /plugins/restart?name=<name>. CLI unit tests swap pluginsRestartPost; the
// integration test exercises it against a real httptest server.
//
// On a non-2xx response it decodes the {"error": "..."} body the handler
// returns and surfaces it as the error message.
func defaultPluginsRestartPost(ctx context.Context, baseURL, name string, timeout time.Duration) (*restartResult, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}
	q := u.Query()
	q.Set("name", name)
	u.RawQuery = q.Encode()

	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		// Try to decode the structured error body for a clean message.
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &e) == nil && e.Error != "" {
			return nil, fmt.Errorf("%s", e.Error)
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out restartResult
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &out, nil
}

// newPluginsRestartCmd returns `ry plugins restart <name>`. It POSTs to a
// running yardmaster's HTTP server to relaunch a single plugin in place
// without restarting the yard (railyard-77h.13).
//
// Restart does NOT reload railyard.yaml (plugin config is fixed for the
// yardmaster process lifetime) but DOES pick up a plugin binary replaced on
// disk, because the relaunch re-execs the recorded path. It is the escape
// hatch for a wedged plugin, a crash-budget-disabled plugin, or an
// init-failed plugin — the operator-initiated relaunch resets the plugin's
// crash budget rather than counting toward the disable threshold.
func newPluginsRestartCmd() *cobra.Command {
	var (
		configPath string
		urlFlag    string
		timeout    time.Duration
	)
	cmd := &cobra.Command{
		Use:   "restart <name>",
		Short: "Relaunch a single plugin in a running yardmaster without restarting the yard",
		Long: "Relaunch a single plugin inside a running yardmaster over HTTP, without " +
			"restarting the whole yard.\n\n" +
			"Use it to recover a wedged plugin, revive a plugin the crash-budget " +
			"supervisor permanently disabled, or pick up a plugin binary you replaced " +
			"on disk (the relaunch re-execs the binary). The operator-initiated restart " +
			"RESETS the plugin's crash budget.\n\n" +
			"Restart does NOT reload railyard.yaml — plugin config is fixed for the " +
			"yardmaster process lifetime. Prints `old-state -> new-state` on success.",
		Args: cobra.ExactArgs(1),
		// Connection-refused / unknown-name are expected operator-facing
		// errors; suppress cobra's usage dump so the hint we print isn't
		// buried under flag help.
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPluginsRestart(cmd, configPath, urlFlag, args[0], timeout)
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().StringVar(&urlFlag, "url", "", "override the target base URL (default: http://127.0.0.1:<HealthPort>/plugins/restart)")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "HTTP timeout (relaunch can take several seconds for the graceful drain)")
	return cmd
}

func runPluginsRestart(cmd *cobra.Command, configPath, urlFlag, name string, timeout time.Duration) error {
	base := urlFlag
	if base == "" {
		cfg, err := pluginsListLoadConfig(configPath)
		if err != nil {
			return fmt.Errorf("plugins restart: load config (use --url to bypass): %w", err)
		}
		port := cfg.Yardmaster.HealthPort
		if port == 0 {
			return fmt.Errorf("plugins restart: cfg.yardmaster.health_port not set; pass --url=http://host:port/plugins/restart")
		}
		base = fmt.Sprintf("http://127.0.0.1:%d/plugins/restart", port)
	}

	res, err := pluginsRestartPost(cmd.Context(), base, name, timeout)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"ry plugins restart %s: %v\n"+
				"Is yardmaster running? Try `ry plugins status` to see live plugin state.\n",
			name, err)
		return fmt.Errorf("restart %s: %w", name, err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "%s: %s -> %s\n", res.Name, res.OldState, res.NewState)
	return nil
}
