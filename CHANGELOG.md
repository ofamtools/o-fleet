# Changelog

## Unreleased

- Changed: machine and check are thing nouns with stores (machine: list, get, create, set, delete over machines.json; check: read-only). `machine set` is the standard set: `machine set NAME disk=/ notes=...` or `--props`, no per-field flags; the ssh field is the prop `destination` (machine.ssh is a verb).
- Removed: `machine rename NAME TO` (no alias; `machine set NAME @label=TO`, or `name=TO`, renames and the status read follows). `add`, `remove`, `list`, `show` and the remote verbs are unchanged.
- Changed: machine removal declares destructive metadata so agent-aware hosts refuse deletion of registry records.
