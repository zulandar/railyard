// Package agentbackend centralizes the one decision every agent role must make:
// whether auth_method routes through the Railyard-owned native loop
// (internal/agentloop) or a CLI agent provider, and — when native — building the
// OpenAI-compatible client from the environment.
//
// Engine, dispatch, telegraph, bull, inspect, and yardmaster escalation all
// consume Resolve so the native-vs-CLI choice is defined once. A new role
// wiring its agent through Resolve cannot silently skip the native path the
// way the open-coded IsNativeLoopMethod/NewClientFromEnv pair could (inspect
// did until railyard-37x.1; yardmaster did until railyard-ocd).
package agentbackend

import (
	"github.com/zulandar/railyard/internal/agentloop"
	"github.com/zulandar/railyard/internal/config"
)

// Resolve reports whether cfg.AuthMethod selects the native loop and, when it
// does, returns a ready client (credentials resolved from the environment). For
// non-native methods it returns (nil, false, nil) and the caller falls back to
// its CLI agent provider.
func Resolve(cfg *config.Config) (client agentloop.Completer, useNative bool, err error) {
	if !agentloop.IsNativeLoopMethod(cfg.AuthMethod) {
		return nil, false, nil
	}
	c, err := agentloop.NewClientFromEnv(cfg.AuthMethod)
	if err != nil {
		return nil, true, err
	}
	return c, true, nil
}
