# o-fleet: agent guide

`o-fleet` (so `o fleet`) registers a user's machines and runs read-only checks across them over ssh. Usage is in README.md. It is an [ochazuke](https://github.com/ofamtools/ochazuke) application with an [oden](https://github.com/ofamtools/oden) command line: read ochazuke's AGENTS.md ("Building an application on ochazuke" and the Invariants) before changing the app, page or verbs.

## Nothing personal

o-fleet ships no knowledge of any one fleet. Machine names, users, tailnet domains, addresses, mounts to hide and the disk `status` reports come from the user's files (`machines.json`, `checks.json`, the ochazuke user files), never from code or the embedded defaults. Embedded defaults stay generic (OS pseudo mounts, not a device's own); test data is synthetic, with example names and `*.node.example` domains.

## Where does X live

| Concern | File |
|---|---|
| The registry: `Machine`, `Fleet`, machines.json read/save | `registry.go` |
| Running a script on a machine: `Runner`, ssh (BatchMode, sh -c), local | `runner.go` |
| Checks: the row format, embedded + user file, the uname case, parsers, hide/dedupe/sort, parallel runs | `checks.go`, `checks.json` |
| Status: the status and df checks folded into one summary; sizes and durations for people | `status.go` |
| Tailnet: `tailscale status --json` (adapted from [tuios](https://github.com/Gaurav-Gosain/tuios) federation), ping, ~/.ssh/config aliases | `tailnet.go` |
| Rows, columns, cells | `rows.go` |
| Verbs | `verbs.go` |
| App, runtime, lists, follow | `app.go` |
| The page | `view.go` |
| tui, --smoke, the model | `tui.go` |
| main, oden, the df/status/ping shortcuts, ssh exec | `main.go` |
| Default keys | `bindings.json` |

## Invariants

- Read-only on machines. Shipped checks only read. Tests never reach a machine: they use `fakeRunner`, a fake tailscale script and a temp ssh config, with `O_FLEET_*` pointing into `t.TempDir()`.
- A check is a data row (script + parser name), never a code branch. A new parser is a new generic entry in `parsers`, never one for a single check.
- Remote verbs are `Pending` (off the loop), ask every machine at once, each under its check's timeout; an unreachable machine is a row with an error, not a failed command.
- Keys live in `bindings.json` only; a verb that means the focused machine declares `From: "row:name"` on its parameter (`name`, `machines`), so keys, the help and the palette fill it alike; a binding that wants every machine passes `"args": {"machines": "*"}`.
- Every verb declares its history kind.

## Checks

```sh
gofmt -l . && GOWORK=off GOFLAGS=-trimpath go vet ./... && GOWORK=off GOFLAGS=-trimpath go test -race ./...
GOWORK=off go run . tui --smoke --no-status
GOWORK=off go run . catalog >/dev/null
```

ochazuke (`v0.35.0`) and oden (`v0.1.1`) are required at their tags; never commit `go.work` or a `replace`.
