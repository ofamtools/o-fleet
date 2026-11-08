# Changelog

## Unreleased

- Changed: requires ochazuke v0.102.0 and oden v0.3.0 (the Operations registry and three-part keys; `cmd/opname` moved the code). The tool declares its namespace, `fleet` (`ochazuke.DeclareNamespace`), so its keys are `fleet.<noun>.<verb>` and the catalog carries `namespace: fleet`. CLI spellings are unchanged.
- Changed: machine and check are thing nouns with stores (machine: list, get, create, set, delete over machines.json; check: read-only). `machine set` is the standard set: `machine set NAME disk=/ notes=...` or `--props`, no per-field flags; the ssh field is the prop `destination` (machine.ssh is a verb).
- Removed: `machine rename NAME TO` (no alias; `machine set NAME @label=TO`, or `name=TO`, renames and the status read follows). `add`, `remove`, `list`, `show` and the remote verbs are unchanged.
- Changed: machine removal declares destructive metadata so agent-aware hosts refuse deletion of registry records.
