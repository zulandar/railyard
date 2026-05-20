module github.com/zulandar/railyard-hello-example

go 1.26.3

require github.com/zulandar/railyard v0.0.0

require gopkg.in/yaml.v3 v3.0.1 // indirect

// Local-dev: point at the in-tree railyard source. Drop this once you
// depend on a tagged release.
replace github.com/zulandar/railyard => ../../..
