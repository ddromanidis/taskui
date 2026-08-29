# Ideas

Things worth building on taskui, kept with their reasoning. The ranked cross-project
list lives in `~/Documents/ideas.md`; this file is for ideas that live entirely inside
taskui.

## A Neovim frontend

Not a port — a second renderer. The mistake to avoid is a third implementation of the
engine (Go, Rust, now Lua); the right shape is taskui as the engine and a Lua plugin as
a frontend over a headless mode. The package layout already enforces the split:
`internal/app` is the only package that knows Bubble Tea exists, and everything the
plugin needs — `run`, `store`, `graph`, `pivot`, `search`, `diff`, `task`, `loc` — is
UI-free.

### Why the Go side is small

`internal/run` already produces a typed event stream: `Event` with `GraphReady`,
`Partial`, `Redacting`, `LineEvent`, `FailedEvent`, `Exited`. The TUI consumes it
through Bubble Tea's update loop; headless mode consumes the same stream and marshals
each event as one NDJSON line on stdout. No engine package changes — it is a new
command in `cmd/` beside the print-and-exit family (`--dump`, `--graph`, `--search`,
`--timeline`, `--diff`, `--flaky`), which already settled the pattern of "the engine,
minus the screen".

The protocol needs two shapes:

- **streaming**: `taskui run <name> --json` — the `Event` stream as NDJSON, one process
  per run, exiting when the run does
- **request/response**: `--list-tasks --json` returning the parsed tree (colon paths,
  descriptions, danger flags, last outcomes from `store.LastOutcomes`), and
  `--timeline <task> --json` for history

**Per-run processes, not a daemon.** A long-lived `taskui serve` owning slots is the
alternative, and it loses: the store on disk is already the shared state, so separate
processes see each other's history for free, and Neovim's job/buffer model *is* a
multiplexer — a daemon would reimplement in the protocol what buffers give away.

### The plugin side

*As built:* jobstart plus a renderer, one scratch buffer rendered with extmarks — the
neo-tree/oil pattern, a buffer you render into rather than a file. The three-state fold
cycle (`▸ ▿ ▾`) does not map onto native folds — foldtext is one line and a peek is five —
so the tree is drawn manually, which is what every tree plugin does anyway.

One panel rather than the planned picker-float-plus-run-split: the TUI's own move of
unfolding a run under its row made the two the same screen, and a second window would have
been two places to look for one thing.

Two features dissolve rather than translate:

- **Slots become buffers.** Switching, listing and lifecycle already exist (`:ls`,
  bufferline), so slots stop being a feature.
- **The fuzzy filter becomes a picker.** `:TaskUI` can open telescope/fzf-lua over the
  colon paths — muscle memory users already have. The tree stays for browsing; the
  picker handles "I know the name".

### What Neovim adds that the TUI cannot

The plugin's justification is proximity to the code, not re-hosting the TUI:

- **Quickfix.** `order_test.go:88: want 1200, got 1180` is dead text in a terminal; in
  Neovim it is `:cnext` onto the failing assertion. `loc.Resolver` already turns bare
  paths absolute using the task's run directory, so the errorformat never has to be
  clever. This is idea 4 in `~/Documents/ideas.md` (`taskui --quickfix`) — the plugin
  subsumes it, and shipping that flag first means the plugin's biggest win exists
  before the plugin does.
- `gf` on any path in output; `:TaskUI edit` jumping to the task's definition via what
  `internal/loc` already knows (`loc/editor.go` exists for exactly this).
- Archive search feeding telescope instead of grep.
- A statusline component — `✗ backend:test 7.1s` visible while editing the fix.

### Prior art

overseer.nvim owns the "task runner in Neovim" niche and has Taskfile support. What it
lacks is the output model — peek windows, tasks and their output as one fold tree, the
archive/timeline/diff. That is the differentiator, so the plugin leads with the run
view, not the picker.

### Phases and effort

1. ~~`--quickfix`~~ — **shipped 2026-08-29.** `taskui --quickfix` prints the last stored
   run's failures as absolute `file:line:col: message`; `--run <task> --quickfix` runs and
   populates in one go; `--task` narrows it. Command echoes are skipped (a command names
   files and is not a report), and references `loc.Resolver` could only guess at are left
   out rather than sent.
2. ~~`--list-tasks --json` and `run --json`~~ — **shipped 2026-08-29**, as `--json` on the
   flags that already existed rather than as new ones: `--list --json`, `--timeline <task>
   --json`, and `--run <task> --json` for the stream. Three near-duplicate flags would have
   been three things to keep in step.

   The stream is NDJSON: `run`, `graph`, `task`, `line`, `prompt`, `exit`. Events are
   differences against the run's own state rather than the engine's raw event stream —
   "pending → running" has no event of its own and is exactly what a front end draws. A
   task is announced before its output and its verdict after it; line indices count from
   the start of the run so the 20,000-line cap dropping the oldest lines renumbers nothing.
3. ~~Plugin core~~ — **shipped 2026-08-29**, and smaller than the estimate: about 1,300
   lines of Lua across `lua/taskui/{config,cli,state,view,panel,health,init}.lua`, plus
   `plugin/taskui.lua` and thirteen headless integration tests in `tests/run.lua` that
   drive the real binary. The estimate of a week assumed the renderer was the long pole;
   it was not, because the protocol had already done the hard part — what arrives is
   folded state, not raw events, so the Lua side is a projection and a keymap.

   The picker and the run view did not stay separate: since the TUI learned to unfold a
   run under its row, the plugin has one panel that is both, and `state.rows()` is the
   Lua twin of `RebuildPickerRows`. The statusline component came for free with it.
4. Left: a telescope/fzf-lua picker over the colon paths (the tree stays for browsing),
   the timeline and diff views, `--args` from a prompt, and archive search feeding
   telescope. Each independently shippable, none blocking the others.

The ordering was deliberate and held: phases 1 and 2 are pure taskui and useful without any
plugin (any editor can consume NDJSON), so the Lua work starts against a stable, tested
protocol rather than co-evolving with it.

### What the TUI learned in the meantime

The picker now unfolds a run under the row it was started from — the execution tree, the
commands with their own verdicts, and a peek at each task's last few lines — which changes
what the plugin should copy. The run view is no longer the only place a run is visible, so
the Lua side's first screen can be the list with runs in it rather than a bottom split.
