package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/zulandar/railyard/internal/pluginhost"
)

// pluginsStatusFetch is the indirection seam used by tests to swap the
// real HTTP fetch for a canned response.
var pluginsStatusFetch = defaultPluginsStatusFetch

// defaultPluginsStatusFetch performs the real HTTP GET. CLI unit tests
// never invoke it (they swap pluginsStatusFetch); the integration test
// in plugins_integration_test.go exercises it against a real httptest
// server.
func defaultPluginsStatusFetch(url string, timeout time.Duration) (*pluginhost.Snapshot, error) {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url) //nolint:noctx
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var snap pluginhost.Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &snap, nil
}

// newPluginsStatusCmd returns `ry plugins status`. The subcommand queries
// a live yardmaster's HTTP server for runtime plugin state. Compare with
// `ry plugins list`, which is the build-time view from disk.
func newPluginsStatusCmd() *cobra.Command {
	var (
		configPath string
		urlFlag    string
		jsonOut    bool
		timeout    time.Duration
	)
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show runtime plugin state from a running yardmaster",
		Long: "Query a running yardmaster over HTTP and show per-plugin runtime " +
			"state: which plugins are running, disabled, failed, or skipped; how " +
			"many subscriptions and commands each one owns; restart count and last " +
			"activity. Use `ry plugins list` for the build-time view (what's on disk).",
		Args: cobra.NoArgs,
		// Connection-refused is the expected first-time path for this
		// command. Suppress cobra's usage/flags dump on RunE error so
		// the human-readable hint we print to stderr isn't followed by
		// a confusing block of flag help.
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPluginsStatus(cmd, configPath, urlFlag, jsonOut, timeout)
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().StringVar(&urlFlag, "url", "", "override the target URL (default: http://127.0.0.1:<HealthPort>/plugins/status)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit the raw JSON response instead of a table")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Second, "HTTP timeout")
	return cmd
}

func runPluginsStatus(cmd *cobra.Command, configPath, urlFlag string, jsonOut bool, timeout time.Duration) error {
	url := urlFlag
	if url == "" {
		cfg, err := pluginsListLoadConfig(configPath)
		if err != nil {
			return fmt.Errorf("plugins status: load config (use --url to bypass): %w", err)
		}
		port := cfg.Yardmaster.HealthPort
		if port == 0 {
			return fmt.Errorf("plugins status: cfg.yardmaster.health_port not set; pass --url=http://host:port/plugins/status")
		}
		url = fmt.Sprintf("http://127.0.0.1:%d/plugins/status", port)
	}

	snap, err := pluginsStatusFetch(url, timeout)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"ry plugins status: yardmaster not reachable at %s: %v\n"+
				"Is yardmaster running? Try `ry plugins list` for build-time state.\n",
			url, err)
		return fmt.Errorf("yardmaster not reachable: %w", err)
	}

	if jsonOut {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(snap)
	}

	return renderStatusTable(cmd.OutOrStdout(), snap)
}

// renderStatusTable writes a tab-aligned table mirroring the look of
// `ry plugins list`. LAST-ACTIVITY is rendered as a relative duration
// ("3m ago", "just now", "-" for zero).
func renderStatusTable(out io.Writer, snap *pluginhost.Snapshot) error {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTATUS\tRESTARTS\tSUBS\tCMDS\tLAST-ACTIVITY\tPID\tPATH")
	for _, p := range snap.Plugins {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			p.Name,
			p.Status,
			dashIfZero(p.RestartCount),
			dashIfZero(p.SubscriptionCount),
			dashIfZero(p.CommandCount),
			renderRelative(p.LastActivity),
			dashIfZero(p.PID),
			dashIfEmpty(p.Path),
		)
	}
	return w.Flush()
}

func dashIfZero(n int) string {
	if n == 0 {
		return "-"
	}
	return fmt.Sprintf("%d", n)
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// renderRelative renders t as "Ns ago" / "Nm ago" / "Nh ago" / a date for
// very old timestamps. Zero time prints "-".
func renderRelative(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < 0:
		return "just now"
	case d < 5*time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return t.Format("2006-01-02")
	}
}
