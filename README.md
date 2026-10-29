# o-fleet

`o-fleet` (so `o fleet`) keeps a registry of your machines (the fleet) and runs read-only checks across them, in parallel, over ssh. Machines are reached by their `~/.ssh/config` alias (which can point at a Tailscale address) or their tailnet name; the machine you are on can be `--local`. Nothing about any particular fleet is built in: the registry starts empty and every machine, name, user and tailnet domain comes from your own files.

```sh
o-fleet machine discover                 # the tailnet's machines, registered or new (dry run)
o-fleet machine discover --apply --only backup,web
o-fleet machine add backup --ssh backup --tailscale backup.example.ts.net --tags storage
o-fleet machine add web --tailscale web.example.ts.net --user deploy
o-fleet machine add desk --local
o-fleet df backup                        # free/used/total per mount, as a table
o-fleet df --json                        # every machine, JSON rows
o-fleet status                           # up, uptime, load, memory, main disk, every machine at once
o-fleet ping                             # tailscale ping (or --via ssh)
o-fleet ssh backup                       # a shell on backup (o-fleet machine ssh backup prints the command)
o-fleet check run uname backup,web       # any check on any machines
o-fleet                                  # the Fleet page (TUI)
```

The machine names above are examples; use your own.

## Nouns and verbs

- `machine`: `list`, `show`, `add`, `set`, `remove` (confirm), `rename`, `discover`, `ping`, `ssh`, `df`, `status`, `check CHECK [MACHINES]`, `state`.
- `check`: `list`, `show` (prints the script it sends), `run CHECK [MACHINES]` (the same handler as `machine check`).
- `df`, `status`, `ping` at the top level are the machine verbs printed as tables: bare words are machine names; `--json` or `--format markdown|toon` picks another output.

Every `NOUN VERB` prints JSON by default and takes `--format table|markdown|toon`. A machine that does not answer is a row with an `error`, never a failed command.

## Files

- `~/.config/o-fleet/machines.json` (`$O_FLEET_MACHINES`): `{"machines": [{name, tailscale, ip, ssh, user, local, os, disk, tags, notes}]}`. Only `add`, `set`, `remove`, `rename` and `discover --apply` write it. `disk` is the mount `status` reports (default: the largest one df shows).
- `~/.config/o-fleet/checks.json` (`$O_FLEET_CHECKS`): more checks, same format as the embedded [checks.json](checks.json); a row named like a default replaces it.
- `~/.config/o-fleet/bindings.json` etc.: the usual ochazuke user files (keys, settings, actions).

## Checks are data

A check is a row: `name`, a POSIX sh `script` (or `scripts` keyed by a `uname -s` case pattern such as `Linux`, `Darwin`, `*`), a `parser` (`df`, `kv`, `lines`, `json`, `text`), and for row parsers `hide` (glob patterns per field), `dedupe` (a field) and `sort` (a field, `-` for descending), plus an optional `timeout`. The script is sent as `sh -c '...'`, so the remote login shell never parses it. Add a check by adding a row:

```json
{"checks": [{"name": "zpool", "script": "zpool list -H -o name,size,free,health", "parser": "lines"}]}
```

To change a default, copy its row from `o-fleet check show NAME` (or [checks.json](checks.json)) into your file and edit it. For example, to hide a mount of your own from `df` and `status`, give your `df` row the default `hide` lists plus your pattern under `mount` (say `"/mnt/scratch*"`). `o-fleet check list` shows each row's `source`.

To pin the mount `status` reports for one machine rather than the largest: `o-fleet machine set backup --disk /srv`.

Ship only reads: o-fleet runs whatever a row says, so a row that changes a machine is the author's choice.

## The Fleet page

`o-fleet` or `o-fleet tui` (`--smoke` renders one frame, `--no-status` skips the status read on open). Machines on the left with up, uptime, load, memory and disk columns; the focused machine's detail and mounts on the right (its df is read when it is first focused). Keys (bindings.json, overridable): `R` status of every machine, `s` status of the focused one, `enter` its df again, `p` ping it, `d d` forget it (confirm), `q` quit, `?` all keys.

## Build

```sh
GOWORK=off GOFLAGS=-trimpath go build -o o-fleet .
```
