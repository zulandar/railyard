package pluginhost

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zulandar/railyard/internal/config"
)

// PluginCandidate describes a plugin binary found on disk along with its
// enabled-and-allow status from config. It does NOT reflect runtime
// state — whether the plugin is actually running, has crashed, or has
// ever launched — only what the host would do if it were started right
// now with this config.
type PluginCandidate struct {
	// Name is the executable basename (extension stripped). It is the
	// lookup key for the config allow-list, the registry, and the
	// would-be socket path.
	Name string

	// Path is the absolute path to the binary on disk.
	Path string

	// Source is the directory the candidate was discovered in — useful
	// for operator diagnostics when name collisions occur.
	Source string

	// Executable reports whether the file has any executable bit set.
	// DiscoverPlugins still returns non-executable candidates that
	// appear in `plugins.enabled` so the operator can see why a
	// configured plugin would not launch; for purely-discovered (not
	// enabled) candidates this is always true (non-executables are
	// skipped during the scan).
	Executable bool

	// Enabled is true when the plugin's name appears in
	// cfg.Plugins.Enabled. Only enabled plugins are launched by the
	// host on boot.
	Enabled bool

	// AllowEvents is the configured event allow-list for the plugin
	// (copy of cfg.Plugins.Settings[name].Allow.Events). Empty when no
	// allow block is configured — meaning the strict default of "deny
	// every advertised event".
	AllowEvents []string

	// AllowCommands is the configured command allow-list for the
	// plugin. Empty when no allow block is configured.
	AllowCommands []string

	// SocketPath is the would-be Unix-domain socket path the host
	// would bind for this plugin if launched right now. Computed
	// without creating any directories so this stays a read-only
	// discovery call. Empty when name resolution failed.
	SocketPath string
}

// DiscoverPlugins runs the same plugins.d scan + config intersection
// the [Host] uses on startup, without launching anything or mutating
// any filesystem state beyond what os.ReadDir requires.
//
// The returned slice contains every plugin we would consider at boot:
//
//  1. Executable candidates found in any of the three well-known
//     plugin directories (and the optional cfg.Plugins.PluginsDir
//     override), regardless of whether they appear in
//     cfg.Plugins.Enabled.
//  2. Names listed in cfg.Plugins.Enabled that did NOT resolve to a
//     binary on disk — these are returned with Path="", Executable=false
//     and Enabled=true so an operator can spot the misconfiguration.
//
// Entries are sorted by Name. A nil config is treated as "no enabled
// plugins and no override directory" — i.e. a pure on-disk scan.
func DiscoverPlugins(cfg *config.Config) ([]PluginCandidate, error) {
	logger := slog.Default()

	var (
		extra    string
		enabled  []string
		settings map[string]config.PluginSettings
	)
	if cfg != nil {
		extra = cfg.Plugins.PluginsDir
		enabled = cfg.Plugins.Enabled
		settings = cfg.Plugins.Settings
	}

	cs := discoverCandidates(extra, logger)

	enabledSet := make(map[string]struct{}, len(enabled))
	for _, name := range enabled {
		enabledSet[name] = struct{}{}
	}

	byName := make(map[string]PluginCandidate, len(cs)+len(enabled))
	for _, c := range cs {
		pc := PluginCandidate{
			Name:       c.name,
			Path:       c.path,
			Source:     c.source,
			Executable: true, // scanDir already filters non-executables
			SocketPath: predictSocketPath(c.name),
		}
		_, pc.Enabled = enabledSet[c.name]
		if s, ok := settings[c.name]; ok {
			pc.AllowEvents = append([]string(nil), s.Allow.Events...)
			pc.AllowCommands = append([]string(nil), s.Allow.Commands...)
		}
		byName[c.name] = pc
	}

	// Surface enabled-but-missing plugins so the table is honest about
	// misconfiguration. Path/Executable stay zero-valued.
	for _, name := range enabled {
		if _, ok := byName[name]; ok {
			continue
		}
		pc := PluginCandidate{
			Name:       name,
			Enabled:    true,
			SocketPath: predictSocketPath(name),
		}
		if s, ok := settings[name]; ok {
			pc.AllowEvents = append([]string(nil), s.Allow.Events...)
			pc.AllowCommands = append([]string(nil), s.Allow.Commands...)
		}
		byName[name] = pc
	}

	out := make([]PluginCandidate, 0, len(byName))
	for _, pc := range byName {
		out = append(out, pc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// systemPluginsDir is the system-wide plugin directory scanned first.
const systemPluginsDir = "/etc/railyard/plugins.d"

// userPluginsDirName is the per-user plugin directory under $HOME.
const userPluginsDirName = ".railyard/plugins"

// localPluginsDirName is the working-directory plugin directory used for
// developer convenience.
const localPluginsDirName = "plugins"

// candidate is a single discovered plugin executable.
type candidate struct {
	// name is the executable basename with any extension stripped. It is
	// the lookup key for the config allow-list and the registry.
	name string

	// path is the absolute path to the executable.
	path string

	// source is the directory the candidate was discovered in. Recorded
	// for collision diagnostics.
	source string
}

// discoverCandidates walks the well-known plugin directories in priority
// order (lowest first) and returns the merged set of candidate executables.
// On name collision the later (higher-priority) directory wins and a WARN
// is logged through the supplied logger.
//
// Directory order (low to high priority):
//  1. /etc/railyard/plugins.d/
//  2. $HOME/.railyard/plugins/
//  3. ./plugins/  (working directory)
//  4. extra (cfg.Plugins.PluginsDir) — highest priority when set
//
// Non-existent directories are silently skipped. Files without the
// executable bit set are skipped with a DEBUG log.
//
// The returned slice is deterministic: candidates are returned in
// ascending name order so the boot log line is stable across runs.
func discoverCandidates(extra string, logger *slog.Logger) []candidate {
	if logger == nil {
		logger = slog.Default()
	}

	// Build the directory list in priority order (low → high). A nil entry
	// represents an absent directory we cannot resolve (e.g. $HOME unset).
	dirs := []string{systemPluginsDir}

	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dirs = append(dirs, filepath.Join(home, userPluginsDirName))
	}

	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		dirs = append(dirs, filepath.Join(cwd, localPluginsDirName))
	}

	if extra != "" {
		dirs = append(dirs, extra)
	}

	// Merge by name. Later entries overwrite earlier ones with a WARN.
	merged := make(map[string]candidate)
	for _, dir := range dirs {
		for _, c := range scanDir(dir, logger) {
			if existing, ok := merged[c.name]; ok {
				logger.Warn("pluginhost: plugin name collision; later directory wins",
					slog.String("plugin", c.name),
					slog.String("previous_path", existing.path),
					slog.String("new_path", c.path),
				)
			}
			merged[c.name] = c
		}
	}

	out := make([]candidate, 0, len(merged))
	for _, c := range merged {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// scanDir lists every executable file in dir and returns a candidate per
// entry. Missing directories return an empty slice (no error). Permission
// errors are logged at DEBUG.
func scanDir(dir string, logger *slog.Logger) []candidate {
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			logger.Debug("pluginhost: skipping plugin dir",
				slog.String("dir", dir),
				slog.String("err", err.Error()),
			)
		}
		return nil
	}
	var out []candidate
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		full := filepath.Join(dir, ent.Name())
		info, err := ent.Info()
		if err != nil {
			continue
		}
		if !isExecutable(info.Mode()) {
			logger.Debug("pluginhost: skipping non-executable candidate",
				slog.String("path", full),
				slog.String("mode", info.Mode().String()),
			)
			continue
		}
		out = append(out, candidate{
			name:   stripExt(ent.Name()),
			path:   full,
			source: dir,
		})
	}
	return out
}

// isExecutable reports whether the mode bits indicate the file has any
// executable bit set (owner, group, or world). Plugin binaries should
// typically be 0700 — owner-executable only — but we accept any exec bit
// because operators may run with stricter umasks.
func isExecutable(m os.FileMode) bool {
	return m&0o111 != 0
}

// stripExt removes a trailing ".exe" (Windows) or no-op for everything
// else. Plugins on Linux don't have extensions; this is just future-proof.
func stripExt(name string) string {
	if i := strings.LastIndex(name, "."); i > 0 {
		ext := name[i:]
		if ext == ".exe" {
			return name[:i]
		}
	}
	return name
}

// filterEnabled returns the subset of cs whose names appear in enabled.
// Names listed in enabled but absent from cs are returned as the second
// slice so the caller can WARN about misconfigured names.
func filterEnabled(cs []candidate, enabled []string) (launch []candidate, missing []string) {
	if len(enabled) == 0 {
		return nil, nil
	}
	byName := make(map[string]candidate, len(cs))
	for _, c := range cs {
		byName[c.name] = c
	}
	for _, name := range enabled {
		if c, ok := byName[name]; ok {
			launch = append(launch, c)
		} else {
			missing = append(missing, name)
		}
	}
	return launch, missing
}
