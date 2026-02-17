package main

import (
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zulandar/railyard/internal/config"
	"github.com/zulandar/railyard/internal/db"
	"github.com/zulandar/railyard/internal/orchestration"
)

func newDoctorCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check system prerequisites and configuration",
		Long:  "Runs diagnostic checks on Railyard prerequisites: config, binaries, database, schema, tracks, tmux session, and git repo.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor(cmd, configPath)
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "railyard.yaml", "path to Railyard config file")
	return cmd
}

type checkResult struct {
	name   string
	status string // "PASS", "FAIL", "WARN"
	detail string
}

func runDoctor(cmd *cobra.Command, configPath string) error {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Railyard Doctor")
	fmt.Fprintln(out, "===============")

	var results []checkResult

	// 1. Config
	cfg, cfgResult := checkConfig(configPath)
	results = append(results, cfgResult)

	// 2. Binaries
	for _, bin := range []string{"go", "dolt", "tmux", "claude"} {
		results = append(results, checkBinary(bin))
	}

	// 3. Dolt server
	if cfg != nil {
		results = append(results, checkDoltServer(cfg.Dolt.Host, cfg.Dolt.Port))
	} else {
		results = append(results, checkResult{"Dolt server", "FAIL", "skipped (no config)"})
	}

	// 4. Database
	if cfg != nil {
		results = append(results, checkDatabase(cfg.Dolt.Host, cfg.Dolt.Port, cfg.Dolt.Database))
	} else {
		results = append(results, checkResult{"Database", "FAIL", "skipped (no config)"})
	}

	// 5. Schema
	if cfg != nil {
		results = append(results, checkSchema(cfg))
	} else {
		results = append(results, checkResult{"Schema", "FAIL", "skipped (no config)"})
	}

	// 6. Tracks
	if cfg != nil {
		results = append(results, checkTracks(cfg))
	} else {
		results = append(results, checkResult{"Tracks", "FAIL", "skipped (no config)"})
	}

	// 7. tmux sessions
	results = append(results, checkTmuxSession()...)

	// 8. Git repo
	results = append(results, checkGitRepo())

	// Print results.
	passed, failed, warned := 0, 0, 0
	for _, r := range results {
		printCheckResult(out, r)
		switch r.status {
		case "PASS":
			passed++
		case "FAIL":
			failed++
		case "WARN":
			warned++
		}
	}

	fmt.Fprintf(out, "\n%d passed, %d failed, %d warning\n", passed, failed, warned)

	if failed > 0 {
		return fmt.Errorf("%d check(s) failed", failed)
	}
	return nil
}

func printCheckResult(out io.Writer, r checkResult) {
	fmt.Fprintf(out, "[%s] %s: %s\n", r.status, r.name, r.detail)
}

func checkConfig(path string) (*config.Config, checkResult) {
	cfg, err := config.Load(path)
	if err != nil {
		return nil, checkResult{"Config file", "FAIL", fmt.Sprintf("%s: %v", path, err)}
	}
	return cfg, checkResult{"Config file", "PASS", path}
}

func checkBinary(name string) checkResult {
	path, err := exec.LookPath(name)
	if err != nil {
		label := name
		switch name {
		case "claude":
			return checkResult{"Claude CLI", "WARN", "not found (engines need this to spawn agents)"}
		}
		return checkResult{label, "FAIL", "not found in PATH"}
	}

	// Try to get version.
	var versionArgs []string
	switch name {
	case "go":
		versionArgs = []string{"version"}
	case "dolt":
		versionArgs = []string{"version"}
	case "tmux":
		versionArgs = []string{"-V"}
	case "claude":
		versionArgs = []string{"--version"}
	default:
		versionArgs = []string{"--version"}
	}

	cmd := exec.Command(path, versionArgs...)
	out, err := cmd.Output()
	if err != nil {
		return checkResult{binaryLabel(name), "PASS", "found (version unknown)"}
	}

	version := strings.TrimSpace(strings.Split(string(out), "\n")[0])
	return checkResult{binaryLabel(name), "PASS", version}
}

func binaryLabel(name string) string {
	switch name {
	case "go":
		return "Go"
	case "dolt":
		return "Dolt"
	case "tmux":
		return "tmux"
	case "claude":
		return "Claude CLI"
	default:
		return name
	}
}

func checkDoltServer(host string, port int) checkResult {
	adminDB, err := db.ConnectAdmin(host, port)
	if err != nil {
		return checkResult{"Dolt server", "FAIL", fmt.Sprintf("%s:%d unreachable: %v", host, port, err)}
	}
	sqlDB, err := adminDB.DB()
	if err != nil {
		return checkResult{"Dolt server", "FAIL", fmt.Sprintf("get sql.DB: %v", err)}
	}
	if err := sqlDB.Ping(); err != nil {
		return checkResult{"Dolt server", "FAIL", fmt.Sprintf("%s:%d ping failed: %v", host, port, err)}
	}
	return checkResult{"Dolt server", "PASS", fmt.Sprintf("%s:%d reachable", host, port)}
}

func checkDatabase(host string, port int, dbName string) checkResult {
	gormDB, err := db.Connect(host, port, dbName)
	if err != nil {
		return checkResult{"Database", "FAIL", fmt.Sprintf("%s: %v", dbName, err)}
	}
	sqlDB, err := gormDB.DB()
	if err != nil {
		return checkResult{"Database", "FAIL", fmt.Sprintf("get sql.DB: %v", err)}
	}
	if err := sqlDB.Ping(); err != nil {
		return checkResult{"Database", "FAIL", fmt.Sprintf("%s ping failed: %v", dbName, err)}
	}
	return checkResult{"Database", "PASS", fmt.Sprintf("%s exists", dbName)}
}

func checkSchema(cfg *config.Config) checkResult {
	gormDB, err := db.Connect(cfg.Dolt.Host, cfg.Dolt.Port, cfg.Dolt.Database)
	if err != nil {
		return checkResult{"Schema", "FAIL", fmt.Sprintf("connect: %v", err)}
	}

	var tableNames []string
	if err := gormDB.Raw("SHOW TABLES").Scan(&tableNames).Error; err != nil {
		return checkResult{"Schema", "FAIL", fmt.Sprintf("show tables: %v", err)}
	}

	expected := len(db.AllModels())
	actual := len(tableNames)
	if actual >= expected {
		return checkResult{"Schema", "PASS", fmt.Sprintf("%d/%d tables migrated", actual, expected)}
	}
	return checkResult{"Schema", "WARN", fmt.Sprintf("%d/%d tables migrated", actual, expected)}
}

func checkTracks(cfg *config.Config) checkResult {
	gormDB, err := db.Connect(cfg.Dolt.Host, cfg.Dolt.Port, cfg.Dolt.Database)
	if err != nil {
		return checkResult{"Tracks", "FAIL", fmt.Sprintf("connect: %v", err)}
	}

	var count int64
	if err := gormDB.Table("tracks").Count(&count).Error; err != nil {
		return checkResult{"Tracks", "FAIL", fmt.Sprintf("count tracks: %v", err)}
	}

	configured := len(cfg.Tracks)
	return checkResult{"Tracks", "PASS", fmt.Sprintf("%d configured, %d seeded", configured, count)}
}

func checkTmuxSession() []checkResult {
	if orchestration.DefaultTmux == nil {
		return []checkResult{{"tmux session", "WARN", "tmux interface not available"}}
	}
	var results []checkResult
	if orchestration.DefaultTmux.SessionExists(orchestration.SessionName) {
		results = append(results, checkResult{"tmux session (main)", "PASS", fmt.Sprintf("%q running", orchestration.SessionName)})
	} else {
		results = append(results, checkResult{"tmux session (main)", "FAIL", fmt.Sprintf("%q not running", orchestration.SessionName)})
	}
	if orchestration.DefaultTmux.SessionExists(orchestration.DispatchSessionName) {
		results = append(results, checkResult{"tmux session (dispatch)", "PASS", fmt.Sprintf("%q running", orchestration.DispatchSessionName)})
	} else {
		results = append(results, checkResult{"tmux session (dispatch)", "FAIL", fmt.Sprintf("%q not running", orchestration.DispatchSessionName)})
	}
	return results
}

func checkGitRepo() checkResult {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	if err := cmd.Run(); err != nil {
		return checkResult{"Git repo", "FAIL", "not inside a git repository"}
	}
	return checkResult{"Git repo", "PASS", "valid"}
}
