package plugin_test

import (
	"os/exec"
	"strings"
	"testing"
)

// allowedNonStdPrefixes is the explicit allow-list of non-stdlib module
// roots that the pkg/plugin transitive import graph may include.
//
// This is the linchpin of the module boundary documented in the
// railyard plugin spec §11: plugins import only pkg/plugin, and
// pkg/plugin in turn imports only stdlib plus a tiny set of common
// utility modules. Anything under the railyard internal/ tree is
// forbidden — that is the entire point of the test. Adding to this
// list requires a corresponding update to the plugin authoring guide
// because each entry becomes a transitive dependency for every plugin.
var allowedNonStdPrefixes = []string{
	"github.com/zulandar/railyard/pkg/plugin",
	"gopkg.in/yaml.v3",
}

// forbiddenPrefix is the railyard internal tree. The whole purpose of
// the plugin SDK boundary is that nothing under this prefix may leak
// into plugin-visible code.
const forbiddenPrefix = "github.com/zulandar/railyard/internal/"

// TestNoInternalImports asserts that the transitive import graph of
// pkg/plugin contains no package under github.com/zulandar/railyard/internal/.
// It shells out to `go list -deps` for a definitive answer and skips
// the test (rather than failing) if the go toolchain is unavailable.
//
// Scope: this checks the SDK root package only (`.`), not subpackages.
// pkg/plugin/proto/v1 holds the gRPC wire stubs, which legitimately
// import grpc/protobuf; the plugin-author-facing boundary is the SDK
// root, and the gRPC stubs sit underneath SDK adapter code that lane B
// will introduce.
func TestNoInternalImports(t *testing.T) {
	t.Parallel()

	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("go toolchain not available: %v", err)
	}

	cmd := exec.Command(goBin, "list", "-deps", "-f", "{{.ImportPath}}", ".")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list failed: %v\n%s", err, out)
	}

	var offenders []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		dep := strings.TrimSpace(line)
		if dep == "" {
			continue
		}
		if strings.HasPrefix(dep, forbiddenPrefix) {
			offenders = append(offenders, dep)
		}
	}

	if len(offenders) > 0 {
		t.Fatalf(
			"pkg/plugin must not import any github.com/zulandar/railyard/internal/* package; found:\n  %s",
			strings.Join(offenders, "\n  "),
		)
	}
}

// TestAllowedNonStdImports is a stricter companion to
// TestNoInternalImports. It asserts the transitive non-stdlib import
// graph of pkg/plugin is exactly the allow-list above. This catches
// accidental drift such as a future commit pulling in a new external
// module — a noisy failure here forces a deliberate decision to widen
// the SDK's dependency surface.
//
// Scope: SDK root only (`.`), not subpackages. See TestNoInternalImports
// for the rationale.
func TestAllowedNonStdImports(t *testing.T) {
	t.Parallel()

	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("go toolchain not available: %v", err)
	}

	cmd := exec.Command(goBin, "list", "-deps", "-f", "{{.ImportPath}}", ".")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list failed: %v\n%s", err, out)
	}

	var unexpected []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		dep := strings.TrimSpace(line)
		if dep == "" {
			continue
		}
		// Stdlib packages have no dot before the first slash.
		if !strings.Contains(strings.SplitN(dep, "/", 2)[0], ".") {
			continue
		}
		if isAllowed(dep) {
			continue
		}
		unexpected = append(unexpected, dep)
	}

	if len(unexpected) > 0 {
		t.Fatalf(
			"pkg/plugin imports a non-stdlib module outside the allow-list; "+
				"either add it to allowedNonStdPrefixes (and update the plugin "+
				"authoring guide) or remove the import:\n  %s",
			strings.Join(unexpected, "\n  "),
		)
	}
}

func isAllowed(dep string) bool {
	for _, prefix := range allowedNonStdPrefixes {
		if dep == prefix || strings.HasPrefix(dep, prefix+"/") {
			return true
		}
	}
	return false
}
