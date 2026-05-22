# hello — minimal railyard plugin example

This directory is the working version of the hello-world plugin shown in
[`docs/plugins/authoring.md`](../../../docs/plugins/authoring.md) §2. It
subscribes to `CarCreated` events and logs each one.

Under the subprocess plugin model the example is a **standalone
executable**: the package is `main` and the entry point calls
`plugin.Serve(...)`. The host discovers the built binary in one of the
well-known `plugins.d` directories, launches it as a child process, and
brokers all interaction over a Unix-domain socket.

## Building

```bash
cd examples/plugins/hello
go build -o hello .
```

The default `go build .` produces a binary named after the directory
(`hello`). Drop it in `./plugins/`, `~/.railyard/plugins/`,
`/etc/railyard/plugins.d/`, or the directory referenced by
`plugins.plugins_dir` in `railyard.yaml`, and add `hello` to the
`plugins.enabled` list to have the host launch it.

## Layout

```
.
├── go.mod        own module; replace directive points at the in-tree railyard source
├── plugin.go     the plugin itself — package main, calls plugin.Serve
└── README.md     this file
```

## The local-dev `replace` directive

`go.mod` carries:

```
replace github.com/zulandar/railyard => ../../..
```

This lets the example build against the in-tree railyard SDK so changes
to `pkg/plugin/` propagate immediately. **Drop the replace when you
depend on a tagged railyard release.**

## Verification

The integration test at
`pkg/cli/example_plugin_build_test.go` runs `go build` from this
directory on every CI run, drops the binary in a `plugins.d` fixture,
constructs a `pluginhost.Host`, publishes a `CarCreated`, and asserts
the plugin's log line appears in the captured slog output. If the SDK
ever drifts in a way that breaks the documented hello-world, CI catches
it immediately.
