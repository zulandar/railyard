package pluginhost

// bumpActivity records "this plugin just did something the host
// noticed" on the named launched plugin. Called from the success paths
// of Init, Start, supervisor relaunch, DispatchCommand, and Subscribe.
//
// Empty names and unknown names are silently ignored — call sites do
// not need to pre-check membership. This keeps the hot paths in the
// hostservice RPC server free of defensive lookups.
//
// Concurrency: takes h.mu.
func (h *Host) bumpActivity(name string) {
	if name == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	lp, ok := h.launched[name]
	if !ok {
		return
	}
	lp.lastActivity = h.clock()
}

// bumpActivityPair bumps lastActivity for two plugins in a single
// critical section. Used by DispatchCommand's plugin-routed path where
// both the dispatching plugin and the owning plugin "just did
// something" on the same RPC. Two separate bumpActivity calls would
// leave a race window where a concurrent handlePermanentDisable could
// remove the owner between the two locks.
//
// Empty / unknown names are silently ignored, matching bumpActivity.
func (h *Host) bumpActivityPair(a, b string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.clock()
	if a != "" {
		if lp, ok := h.launched[a]; ok {
			lp.lastActivity = now
		}
	}
	if b != "" {
		if lp, ok := h.launched[b]; ok {
			lp.lastActivity = now
		}
	}
}
