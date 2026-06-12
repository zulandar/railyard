package cli

import (
	"context"
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
//
// ctx is the cobra command context — cancellation propagates to the
// in-flight request so Ctrl+C returns immediately rather than waiting
// for client.Timeout.
func defaultPluginsStatusFetch(ctx context.Context, url string, timeout time.Duration) (*pluginhost.Snapshot, error) {
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
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
		verbose    bool
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
			return runPluginsStatus(cmd, configPath, urlFlag, jsonOut, verbose, timeout)
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	cmd.Flags().StringVar(&urlFlag, "url", "", "override the target URL (default: http://127.0.0.1:<HealthPort>/plugins/status)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit the raw JSON response instead of a table")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "after the table, print per-plugin lifetime counters (events delivered/dropped, commands handled/failed, avg latency)")
	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Second, "HTTP timeout")
	return cmd
}

func runPluginsStatus(cmd *cobra.Command, configPath, urlFlag string, jsonOut, verbose bool, timeout time.Duration) error {
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

	snap, err := pluginsStatusFetch(cmd.Context(), url, timeout)
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

	if err := renderStatusTable(cmd.OutOrStdout(), snap); err != nil {
		return err
	}
	if verbose {
		if err := renderStatusCounters(cmd.OutOrStdout(), snap); err != nil {
			return err
		}
		renderStatusCommandSignatures(cmd.OutOrStdout(), snap)
	}
	return nil
}

// renderStatusCommandSignatures prints each plugin's command signatures
// (railyard-77h.16) in the -v detail block, one "name(arg:type, ...)" per
// command. Kept out of the default table to keep it readable. Plugins
// with no commands are skipped; nothing is printed if no plugin owns any
// command.
func renderStatusCommandSignatures(out io.Writer, snap *pluginhost.Snapshot) {
	any := false
	for _, p := range snap.Plugins {
		if len(p.CommandSignatures) > 0 {
			any = true
			break
		}
	}
	if !any {
		return
	}
	fmt.Fprintln(out, "\nCOMMAND SIGNATURES:")
	for _, p := range snap.Plugins {
		if len(p.CommandSignatures) == 0 {
			continue
		}
		fmt.Fprintf(out, "  %s: %s\n", p.Name, strings.Join(p.CommandSignatures, ", "))
	}
}

// renderStatusCounters prints the per-plugin lifetime runtime counters
// (railyard-77h.14) below the default table. Kept out of the main table
// so the default view stays narrow and readable; -v opts into the detail.
// Counters are process-lifetime (reset on yard restart) but survive a
// plugin relaunch.
func renderStatusCounters(out io.Writer, snap *pluginhost.Snapshot) error {
	fmt.Fprintln(out, "\nRUNTIME COUNTERS (process-lifetime; reset on yardmaster restart):")
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tEVENTS-DELIVERED\tEVENTS-DROPPED\tCMDS-HANDLED\tCMDS-FAILED\tAVG-LATENCY")
	for _, p := range snap.Plugins {
		fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%d\t%s\n",
			p.Name,
			p.EventsDelivered,
			p.EventsDropped,
			p.CommandsHandled,
			p.CommandsFailed,
			formatAvgLatency(p.CommandLatencyAvgMicros, p.CommandsHandled),
		)
	}
	return w.Flush()
}

// formatAvgLatency renders the derived average command latency. With zero
// commands handled there is no meaningful average, so we print a dash.
func formatAvgLatency(avgMicros, handled uint64) string {
	if handled == 0 {
		return "-"
	}
	return (time.Duration(avgMicros) * time.Microsecond).String()
}

// renderStatusTable writes a tab-aligned table mirroring the look of
// `ry plugins list`. LAST-ACTIVITY is rendered as a relative duration
// ("3m ago", "just now", "-" for zero).
func renderStatusTable(out io.Writer, snap *pluginhost.Snapshot) error {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTATUS\tHEALTH\tSDK\tRESTARTS\tSUBS\tCMDS\tLAST-ACTIVITY\tPID\tPATH\tERROR")
	for _, p := range snap.Plugins {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			p.Name,
			p.Status,
			renderHealth(p.Health, p.HealthCheckedAt),
			dashIfEmpty(p.SDKVersion),
			dashIfZero(p.RestartCount),
			dashIfZero(p.SubscriptionCount),
			dashIfZero(p.CommandCount),
			renderRelative(p.LastActivity),
			dashIfZero(p.PID),
			dashIfEmpty(p.Path),
			truncateError(p.Error, errorColumnMax),
		)
	}
	return w.Flush()
}

// renderHealth formats the optional plugin health verdict for the HEALTH
// column (railyard-77h.12). A polled plugin shows "<value> <age>" (e.g.
// "ok 12s"); "n/a" (the plugin does not implement HealthReporter) is shown
// bare without an age; an empty value (never polled, or a non-running
// row) renders as a dash.
func renderHealth(value string, checkedAt time.Time) string {
	if value == "" {
		return "-"
	}
	if value == "n/a" {
		return value
	}
	if checkedAt.IsZero() {
		return value
	}
	return value + " " + healthAge(checkedAt)
}

// healthAge renders how long ago the last health poll completed as a
// compact suffix ("12s", "3m", "2h"); very old timestamps fall back to a
// date. Mirrors renderRelative's buckets but omits the " ago" suffix to
// keep the HEALTH cell narrow.
func healthAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		secs := int(d.Seconds())
		if secs < 0 {
			secs = 0
		}
		return fmt.Sprintf("%ds", secs)
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return t.Format("2006-01-02")
	}
}

// errorColumnMax bounds the ERROR column to keep the table readable in
// a standard terminal. Full untruncated error remains in --json.
const errorColumnMax = 80

// truncateError returns "-" for empty or whitespace-only errors and
// otherwise collapses internal whitespace (newlines mangle tabwriter
// alignment) and clips the result to max runes with a trailing
// ellipsis.
func truncateError(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return "-"
	}
	if max <= 1 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
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
