# hello — minimal railyard plugin example

This directory is the working version of the hello-world plugin shown in
[`docs/plugins/authoring.md`](../../../docs/plugins/authoring.md) §2. It
subscribes to `CarCreated` events and logs each one, optionally with a
configurable greeting.

## Why no `main` package?

The example is intentionally a plugin **package only** — no enterprise
binary entry point. The enterprise-binary wiring (five lines: a
side-effect import and a call to `cli.Run()`) is documented in the
authoring guide §2.4 and is not duplicated here; the example exists to
prove the SDK contract compiles, not to produce a runnable binary. The
verification test (`pkg/cli/example_plugin_build_test.go`) runs
`go build ./...` from this directory on every CI run, so if the SDK
ever drifts in a way that breaks the documented hello-world, CI catches
it immediately.

## Building

```bash
cd examples/plugins/hello
go build ./...
```

This is what the verification test automates. A successful build is the
contract; there is nothing to run.

## Layout

```
.
├── go.mod        own module, replace-directive points at the in-tree railyard source
├── plugin.go     the plugin itself — mirrors authoring.md §2.3 byte-for-byte
└── README.md     this file
```
