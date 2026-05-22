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
