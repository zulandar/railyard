package pluginhost

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

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
