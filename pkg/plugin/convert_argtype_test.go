package plugin

import (
	"testing"

	protov1 "github.com/zulandar/railyard/pkg/plugin/proto/v1"
)

// TestArgTypeZeroValueIsUnspecified proves the SDK ArgType zero value is
// ArgUnspecified (mapping to ARG_TYPE_UNSPECIFIED), so an ArgSpec that omits
// Type means "any type / presence-only" — matching the host — rather than a
// silent "must be a string" (railyard-uv8.12).
func TestArgTypeZeroValueIsUnspecified(t *testing.T) {
	var zero ArgType
	if zero != ArgUnspecified {
		t.Errorf("zero ArgType = %d, want ArgUnspecified (%d)", zero, ArgUnspecified)
	}
	if got := argTypeToProto(zero); got != protov1.ArgType_ARG_TYPE_UNSPECIFIED {
		t.Errorf("argTypeToProto(zero) = %v, want ARG_TYPE_UNSPECIFIED", got)
	}

	// Explicit types still map correctly after the iota shift.
	for sdk, want := range map[ArgType]protov1.ArgType{
		ArgString: protov1.ArgType_ARG_TYPE_STRING,
		ArgInt:    protov1.ArgType_ARG_TYPE_INT,
		ArgBool:   protov1.ArgType_ARG_TYPE_BOOL,
		ArgFloat:  protov1.ArgType_ARG_TYPE_FLOAT,
	} {
		if got := argTypeToProto(sdk); got != want {
			t.Errorf("argTypeToProto(%d) = %v, want %v", sdk, got, want)
		}
	}
}
