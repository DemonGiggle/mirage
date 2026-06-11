# Repository Guidelines

## Project Structure & Module Organization

Mirage is a Go CLI for Linux sandboxing. The main entrypoint lives in `cmd/mirage`. Small probe binaries used by tests live in `cmd/probe-*`. Core implementation is split across `internal/cli` for command dispatch, `internal/runner` for sandbox execution and networking, `internal/rootfs` for rootfs generation and validation, and `internal/spec` for config and policy parsing. End-to-end tests live in `e2e/`, reusable fixtures in `testdata/`, operator docs in `docs/`, and packaged examples in `examples/`.

## Build, Test, and Development Commands

- `go build -o ./bin/mirage ./cmd/mirage` builds the CLI binary.
- `go build ./...` verifies all packages compile.
- `go test ./...` runs unit, fixture, and end-to-end Go tests.
- `gofmt -w $(find . -name '*.go' -print)` formats all Go sources.
- `./bin/mirage doctor` checks host readiness after a local build.

Use Linux with Go `1.24.4+`. Some runtime flows such as `rootfs init` and `run` require `sudo`, and some e2e tests need namespace and `uidmap` support.

## Coding Style & Naming Conventions

Format every Go change with `gofmt`; do not hand-format alignment. Keep packages focused by responsibility and prefer the existing `internal/<area>` split over adding broad utility modules. Follow Go naming norms: exported identifiers use `CamelCase`, unexported helpers use `camelCase`, and test helpers should stay close to the package they serve. Name probe commands by behavior, for example `probe-http-get` or `probe-file-write`.

## Testing Guidelines

Write table-driven Go tests where behavior varies by input. Keep `_test.go` files adjacent to the code under test, and place scenario fixtures in `testdata/` when inputs need to be shared. Run `go test ./...` before opening a PR. If an environment restriction blocks e2e coverage, call that out explicitly in the PR.

## Commit & Pull Request Guidelines

Recent history uses short, imperative commit subjects such as `Add rootfs init architecture option` and `Fix gofmt alignment in CLI tests`. Keep subjects concise, capitalized, and specific. Pull requests should explain the user-visible change, note any Linux or privilege assumptions, link related issues, and include command output or screenshots only when they clarify CLI behavior or docs changes.
