# Changelog

## Unreleased

- Changed: requires ochazuke v0.133.0 and oden v0.5.1. The lexicon word list is merged with ochazuke's; the verb-count assertion is gone (the standard noun scaffold verbs, show, duplicate, history, yank, archive, watch, grow with the library). The machine noun already had the scaffold (dynamic list, show, edit, set, duplicate, delete; archive is unavailable: no archive prop). A direct run from a pane records its pane and session through the library's `MTM_*` actor reading; o-fleet reads no environment of its own.
- Changed, breaking (parity, one shot): the top-level CLI commands `df`, `status`, `ping` and `ssh` are gone as separate commands; they were the verbs `machine df|status|ping|ssh` under shorter words, so mtm, the palette and MCP could not reach them as such. The words still work (`o fleet df`, `o fleet status`, `o fleet ping`, `o fleet ssh NAME [COMMAND...]` read as the machine verbs), but a bare `df`, `status` or `ping` now prints JSON like every verb; add `--format table` for the table the shortcuts used to default to.
- Changed, breaking: `machine ssh` opens the session (a login shell, or the rest of the line as a command) on the terminal the caller gives it: the terminal on the command line, a pane in mtm (the verb is `Foreground`, agent right `confirm`). The command line it used to answer is `machine ssh NAME --print` (`{machine, argv, command}`, runs nothing). `command` is the rest of the line, so `o fleet ssh backup uptime -a` needs no `--command`.

## v0.3.0

- Changed: requires ochazuke v0.110.1 (esc only leaves insert in modals and `q` closes them; label-first pickers).

## v0.2.0 (2026-11-08)

- Changed, breaking (lexicon, ochazuke v0.105.0, one shot, no aliases; every rename is in `docs/lexicon-renames-v0.105.tsv`): the `check` noun is `probe` (`o fleet probe list|show|run`; a check row, `checks.json` and the `Check` type keep their names) because ochazuke's own verbs are named `check`; `machine check` is `machine run` (a probe on machines, the mirror of `probe run`); `machine state` is `machine lists`; the page kind `fleet` is `fleet-page` (binding context `fleet-page/normal`); `machine` is the main noun. `TestLexicon` runs with an empty baseline.
- Changed: requires ochazuke v0.102.0 and oden v0.3.0 (the Operations registry and three-part keys; `cmd/opname` moved the code). The tool declares its namespace, `fleet` (`ochazuke.DeclareNamespace`), so its keys are `fleet.<noun>.<verb>` and the catalog carries `namespace: fleet`. CLI spellings are unchanged.
- Changed: machine and check are thing nouns with stores (machine: list, get, create, set, delete over machines.json; check: read-only). `machine set` is the standard set: `machine set NAME disk=/ notes=...` or `--props`, no per-field flags; the ssh field is the prop `destination` (machine.ssh is a verb).
- Removed: `machine rename NAME TO` (no alias; `machine set NAME @label=TO`, or `name=TO`, renames and the status read follows). `add`, `remove`, `list`, `show` and the remote verbs are unchanged.
- Changed: machine removal declares destructive metadata so agent-aware hosts refuse deletion of registry records.
