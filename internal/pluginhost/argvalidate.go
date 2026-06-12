package pluginhost

import (
	"fmt"

	"github.com/zulandar/railyard/pkg/plugin"
	protov1 "github.com/zulandar/railyard/pkg/plugin/proto/v1"
)

// validatePluginArgs checks a dispatched [plugin.CommandArgs] map against
// the typed argument schema a subprocess plugin declared via
// RegisterCommandSpec (railyard-77h.16). It returns nil when args satisfy
// the spec, or a descriptive error on the first violation.
//
// The error message style mirrors the core-binding validator in
// command.go (validateArgs) so dispatch errors read uniformly across core
// allow-list commands and plugin-registered commands:
//
//   - missing required arg → `missing required argument %q`
//   - present-but-wrong-type → `argument %q has wrong type`
//
// A nil/empty spec validates nothing — the caller skips this function for
// commands with no stored spec, but the empty-args guard here makes the
// function safe to call unconditionally.
//
// Type checking mirrors the wire encoding conventions in
// pkg/plugin/convert.go: every value round-trips through
// google.protobuf.Struct, so each JSON number arrives as a float64. An
// ARG_TYPE_INT therefore accepts a float64 only when it is integral; an
// ARG_TYPE_FLOAT accepts any float64; ARG_TYPE_STRING accepts a string;
// ARG_TYPE_BOOL accepts a bool. ARG_TYPE_UNSPECIFIED imposes no type
// check (presence only, when required).
func validatePluginArgs(spec *protov1.CommandSchema, args plugin.CommandArgs) error {
	if spec == nil {
		return nil
	}
	for _, arg := range spec.Args {
		if arg == nil || arg.Name == "" {
			continue
		}
		raw, present := args[arg.Name]
		if !present {
			if arg.Required {
				return fmt.Errorf("missing required argument %q", arg.Name)
			}
			// Optional and absent: nothing to type-check.
			continue
		}
		if !argTypeMatches(raw, arg.Type) {
			return fmt.Errorf("argument %q has wrong type", arg.Name)
		}
	}
	return nil
}

// argTypeMatches reports whether v satisfies the declared wire ArgType.
// The coercion rules mirror pkg/plugin/convert.go: args arrive as
// map[string]any decoded from a structpb.Struct, so JSON numbers are
// always float64. An INT accepts an integral float64 (or a native Go
// int/int64 for in-process callers); a FLOAT accepts any float64 (or a
// native int/int64); BOOL accepts a bool; STRING accepts a string.
// ARG_TYPE_UNSPECIFIED matches anything.
func argTypeMatches(v any, t protov1.ArgType) bool {
	switch t {
	case protov1.ArgType_ARG_TYPE_STRING:
		_, ok := v.(string)
		return ok
	case protov1.ArgType_ARG_TYPE_BOOL:
		_, ok := v.(bool)
		return ok
	case protov1.ArgType_ARG_TYPE_INT:
		switch n := v.(type) {
		case int, int64:
			return true
		case float64:
			// structpb encodes every JSON number as float64; an INT arg
			// accepts one only when it is integral.
			return n == float64(int64(n))
		}
		return false
	case protov1.ArgType_ARG_TYPE_FLOAT:
		switch v.(type) {
		case float64, int, int64:
			return true
		}
		return false
	case protov1.ArgType_ARG_TYPE_UNSPECIFIED:
		// No declared type → presence-only; any value is acceptable.
		return true
	default:
		return true
	}
}
