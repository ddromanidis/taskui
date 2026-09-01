# taskui

A folding, searchable front end for [go-task](https://taskfile.dev): browse a Taskfile, run
tasks, and keep their output around to fold and search afterwards — live and across
previous runs.

```
 taskui ▸ atlas                                        domain·verb   122 tasks
 ─────────────────────────────────────────────────────────────────────────────
▌▾ backend                                                                  26
 ├ build          Compile the workspace
 ├ check          Type-check only — fastest feedback                 c    ✓ 4m
 ├ lint           Clippy, warnings are errors, plus a format check        ✗ 9m
 └ migrate        Apply pending migrations                                   ⚠
 ▸ deploy                                                               ⚠    9
 ─────────────────────────────────────────────────────────────────────────────
 space o fold   ⏎ run   a args   / filter   t jump   s detail           ? keys
```

## What it is

Seven screens, one keymap.

**The picker** is the Taskfile as a fold tree, which it already is — `backend:migrate:down`
is a path, not a name. It pivots: by domain (`backend` › `migrate` › `down`) or by verb
(`lint` › `app:lint`, `backend:lint`, `infra:lint`), and the second one is the transpose of
the first. Each row says how the task went last time, or how long it has been running right
now, in a slot you may not be looking at.

**Runs unfold in the list.** `⏎` starts the task and leaves you where you were: the
execution tree grows under the row it came from — every task the run pulled in, every
command each of those ran with the verdict of that command beside it, and the last few
lines they printed. `o` walks the block through hidden, a peek, and all of it. With
several slots open, several tasks are showing their output at once.

**The run view** is the same tree with the whole screen to itself, which is where you read
one. `v` goes there. Every task rests on a *peek* — a window on its last few lines — so a
glance tells you what each step is saying without opening any of them. It follows what is
running, and the moment something fails it stops there and opens it.

**Slots** let runs outlive your attention. Start `docker compose up`, leave it, run
something else; the first one keeps going in a slot you can switch back to, with its scroll
position, its folds and its output intact. Quitting takes every one of them down rather
than orphaning it.

**The archive** keeps finished runs on disk as plain text — a directory per run, a
`manifest.json`, and a `.txt` and `.ansi` file per task. `taskui --search 'FAIL'` greps all
of it from anywhere. Beside those sits `history.ndjson`, one line per run, which remembers
how a run went long after its output has been dropped — so a timeline goes back thousands of
runs per project while the text stays capped at the last fifty.

**A timeline** is one task's own history: `⇧H` on any task lists every stored run of it,
newest first, with a duration bar and a `✓✓✓✗✗` trend across the top. `h` answers "what has
this project been doing"; `⇧H` answers "how has *this* been going", and those turn out to be
different questions.

**A diff** is what changed. `⇧D` on a failing task compares its output against the last run
in which it passed, elides the hundreds of lines both runs share, and leaves you with the
few that are new. Nothing else on your machine can do this, because nothing else kept both
runs.

**A profile** is where a run's time went. `⇧T` ranks its tasks by *self* time — what each
spent on its own commands rather than waiting for its children — so `task all` does not sit
at the top of every profile saying nothing.

And `e` opens the file. A `file:line` anywhere in captured output is underlined, and `e`
launches `$EDITOR` on it at that line. In the picker there is no error to follow, so `e`
opens the task's own definition in the Taskfile instead.

**In Neovim** it is this same tool, hosted in a terminal, with the editor filling the
quickfix list from what fails and opening the files `e` picks. See [In Neovim](#in-neovim).

## Why

`task --list` prints a flat wall of names. Running one prints a flat wall of output. When
the output scrolls past, it is gone.

That is fine at ten tasks and stops being fine somewhere around forty. A real Taskfile with
`includes:` has a hundred and twenty of them across nine namespaces, most named after a
domain and a verb, and the flat list throws away both — you scroll looking for the one
whose name you half remember. Meanwhile a `task all` that fails somewhere in the middle
gives you eight thousand lines and a cursor, and the useful part is forty of them.

taskui does five things about that, and they are the only five ideas in it:

- **The names are a tree, so show a tree.** And show it two ways, because "everything in
  the backend" and "every lint everywhere" are both real questions.
- **Output belongs to the task that printed it.** go-task's `--output prefixed` tags every
  line; pairing that with the execution graph reconstructs the tree the run actually was,
  so the output folds into the shape of the thing that produced it.
- **A peek beats a fold.** A task folded down to nothing is a task you have to open to find
  out whether it was worth opening. On a twenty-six node run that is twenty-six guesses.
  The last few lines answer it, because the end of a command's output is the part that says
  how it went.
- **A run that ended is not a run that is gone.** Keep it, in a format anything can read,
  make it searchable, and let one task's runs be lined up against each other — a list of
  outcomes answers "when did this start failing", and a diff of two of them answers "what
  changed".
- **Reading where it broke should not end in retyping.** Every compiler, test runner and
  linter prints `file:line`. taskui knows which task printed each line and where that task
  ran, which is what makes a bare `view_test.go` resolvable — so `e` opens it.

Everything else follows from those, and the reasoning is in [DESIGN.md](DESIGN.md).

## Status

Browse, pivot, filter, run, fold, and search — live and across stored runs.

## Installing

taskui drives [go-task](https://taskfile.dev), so that has to be installed and on your
`PATH` first:

```
brew install go-task            # or see https://taskfile.dev/installation
task --version
```

Then take a binary from the [releases page](https://github.com/ddromanidis/taskui/releases),
or, with a Go toolchain:

```
go install github.com/ddromanidis/taskui@latest
```

That puts `taskui` in `$GOBIN`, or `$(go env GOPATH)/bin` if you have not set one. Check
with `taskui --version`.

From a clone, which is what you want if you are changing it:

```
git clone https://github.com/ddromanidis/taskui
cd taskui
task install
```

To update, re-run whichever of those you used. To remove it, delete the binary.

A man page ships in the repository as `taskui.1`. The Homebrew formula installs it, so
`man taskui` works after `brew install`; installed any other way there is nowhere standard
to put it, so read it in place with `man ./taskui.1` or copy it somewhere on your
`MANPATH`.

It is built with Go 1.27 and uses `for range n` loops and `slices`/`maps` helpers, so it
needs a reasonably current toolchain; if an older one fails to build it, that is the likely
reason.

Nothing else is required. Configuration is optional and taskui creates it only if you ask
for it; stored runs land in `$XDG_STATE_HOME/taskui` or `~/.local/state/taskui`, and
uninstalling leaves them behind — delete that directory if you want them gone.

### Platforms

macOS and Linux, both covered by CI: the suite runs on `macos-latest` and `ubuntu-latest`
on every push, including the tests that spawn a real `task` process to cancel a run and
answer an interactive prompt.

Windows is not supported. Stopping a run signals the process group with
`syscall.Kill(-pid, …)`, which has no Windows equivalent, so `x` would kill `task` and
leave its children running.

### Homebrew

```
brew install --cask ddromanidis/tap/taskui
```

macOS only, and it pours a prebuilt binary rather than building from source, so it needs no
Go toolchain and takes a second. `go-task` comes with it as a dependency. On Linux, take an
archive from the releases page or use `go install`.

To update, `brew upgrade --cask taskui`. To remove, `brew uninstall --cask taskui` — and
`brew untap ddromanidis/tap` if you want the tap gone too.

The cask is written by the release itself, so what `brew` gives you is the same build the
release page does. Setting the tap up is a one-off:

1. Create a public GitHub repository called **`homebrew-tap`**. The `homebrew-` prefix is
   what makes it a tap; `ddromanidis/tap` is how Homebrew spells the rest.
2. Make a fine-grained personal access token with **contents: read and write** on that
   repository, and add it to *this* repository as an Actions secret named
   `HOMEBREW_TAP_GITHUB_TOKEN`. The `GITHUB_TOKEN` Actions hands the workflow is scoped to
   the repository the workflow runs in, and the tap is a different repository — which is
   the whole reason this needs a second one.
3. Tag and push. The release workflow builds, publishes, and commits `Casks/taskui.rb` into
   the tap.

## Usage

```
taskui [DIR]                  # browse the Taskfile in DIR (default: .)
taskui --list                 # print discovered tasks and exit
taskui --dump domain|verb     # print a pivot fully expanded and exit
taskui --graph all            # print the execution graph reachable from a task
taskui --run all              # run headlessly and print the captured tree
taskui --search 'FAIL|error'  # grep every stored run (works from any directory)
taskui --search FAIL --task backend:test --since 2d
taskui --timeline test        # how one task has gone, run after run
taskui --diff test            # what changed since it last passed
taskui --quickfix             # the last run's failures as file:line:col: message
taskui --lint                 # aggregates that cover less than their name claims
taskui --lint --matrix        # the whole aggregate-by-namespace table, not just the gaps
taskui --list --json          # the same listings, for a program rather than a person
taskui --run ci --json        # the run as newline-delimited events, as it happens
taskui --events /tmp/sock     # …the same events from inside the TUI, for a host
taskui --flaky                # tasks that went both ways at one commit
taskui config edit            # open ~/.config/taskui/config.yaml in $EDITOR
taskui --screenshot 90x30     # render one frame to stdout (add --keys 'p' or '/lint')
taskui examples               # worked examples, rendered at your terminal's width
taskui config edit            # open ~/.config/taskui/config.yaml in $EDITOR
```

`taskui examples` walks through the whole thing — eight worked examples, every frame drawn
by the real renderer at your terminal's width and in your theme, so nothing in it can go
stale. `taskui examples <topic>` prints just one.

`man taskui` covers the options, the commands, the keys and the files — its reference
sections are generated from the same flag set and keymap table the program uses, and a test
fails if they drift. `?` from any screen lists every
binding, grouped by context. The footer shows a subset of
the same table — one source of truth, so the two cannot disagree.

The picker is one column at any width. A list can be columnised; a tree cannot — the
columns fill in sequence, so a group header ends in one while its own children continue in
the next, and the indentation that says which task belongs to what stops meaning anything.
The width goes to the description instead. The run view still splits, because its rows do
not depend on each other.

In the picker:

| key | |
|---|---|
| `j` `k` `↑` `↓` | move |
| `{` `}` | previous / next group |
| `space` `←` `→` | fold / unfold a group |
| `o` | how much of the run under a task: hidden, a peek, all of it |
| `⇧O` `⇥` | fold or unfold every group |
| `⏎` | run — or every marked task, each in its own slot — and stay here |
| `p` | toggle the pivot |
| `⇧S` | cycle the order: name, file, recent, failed, size |
| `a` | run with arguments |
| `i` | arm interactive mode for the next run |
| `/` | filter by name |
| `t` | jump to a task, leaving the list intact |
| `s` | what this task is, and what it will run |
| `m` `⇧M` | mark a task to run alongside others / clear the marks |
| `e` | open this task's definition in `$EDITOR` |
| `v` | the whole screen for whatever is running, or the last run |
| `h` | past runs, across the project |
| `⇧H` | past runs of *this* task |
| `x` | stop this task's run, wherever it is |
| `⇧K` | stop every run |
| `esc` | back out of a filter or a panel — it does not quit |
| `q` | quit (always asks first) |

In a run:

| key | |
|---|---|
| `space` `o` `←` `→` | cycle a task's output: hidden, peek, full |
| `⇧O` | move every task through the same cycle |
| `/` | search the output |
| `n` `N` | next / previous match |
| `f` | filter to just the matching lines |
| `[` `]` | less / more context around each hit |
| `r` | re-run the task under the cursor |
| `⇧R` | the same, with `--force` |
| `⇧F` | re-run everything in this run that failed, each in its own slot |
| `a` | re-run with different arguments |
| `i` | answer the task, or re-run it interactively |
| `x` | stop the run (again to kill it) |
| `⇧K` | stop every run |
| `⇥` `⇧⇥` `1`…`9` | switch between open runs |
| `⇧X` | close the slot (once its run has stopped) |
| `⇧A` | detach: let this run outlive taskui |
| `⇧T` | where this run's time went |
| `w` | resume following |
| `y` `⇧Y` | copy the line, or everything the task printed |
| `e` | open the `file:line` under the cursor in `$EDITOR` |
| `⇧D` | what changed since this task last passed |
| `h` | past runs, across the project |
| `⇧H` | past runs of *this* task |
| `esc` | back to the picker (every run keeps going) |

On a timeline (`⇧H`):

| key | |
|---|---|
| `j` `k` `gg` `⇧G` | move |
| `⏎` | open that run |
| `⇧D` | what changed at it — against the last run that went differently |
| `esc` | back |

In a diff:

| key | |
|---|---|
| `j` `k` `gg` `⇧G` | scroll |
| `[` `]` | less / more unchanged context |
| `e` | open the `file:line` under the cursor in `$EDITOR` |
| `esc` | back |

In a profile (`⇧T`):

| key | |
|---|---|
| `j` `k` `gg` `⇧G` | move |
| `⏎` | go to that task in the run |
| `e` | open its definition in `$EDITOR` |
| `esc` | back to the run |

## Going where it broke

Three things turn "something failed" into "here is the line".

### `e` — open the file

Every compiler, test runner and linter prints `file:line`. taskui underlines them in
captured output and `e` opens the one under the cursor:

```
 ▾ ✗ test
   1 ✗ ❯ go test ./...
   2 │   === RUN   TestWrap
   3 │       view_test.go:88: want 3, got 4    ← underlined; e opens it
   4 └   --- FAIL: TestWrap (0.00s)
```

The hard part is not the parsing, it is the resolving. `go test` prints a bare basename
relative to the *package* directory, which is not the directory the run started in and is
not named anywhere in the line. So a path that does not resolve against the project gets
looked up in an index of it, built once and only when something needs it. Two files with
the same name resolve to the shallower one and the status line says it had to guess.

Pressing `e` on a line that names no file falls back to the first location the task printed
— for a compiler that is the error, for a test runner the first failing assertion — and
says where it got it, so it is never a dead keystroke.

The command comes from `$VISUAL`, then `$EDITOR`, spelled the way that editor wants it:

| `$EDITOR` | |
|---|---|
| `vim` `nvim` `vi` | `vim +212 file` |
| `nano` | `nano +212,5 file` |
| `emacs` `emacsclient` | `emacs +212:5 file` |
| `hx` `kak` | `hx file:212:5` |
| `code` `cursor` `zed` `codium` | `code --goto file:212:5` |
| `subl` `atom` `mate` | `subl file:212:5` |
| `goland` `idea` `pycharm` … | `goland --line 212 --column 5 file` |
| anything else | `$EDITOR file`, at the top |

Flags already in the variable survive: `EDITOR="code -w"` keeps its `-w`. A terminal editor
gets the terminal handed to it and taskui redraws when it exits; anything that opens its own
window is started alongside, because blacking out the UI for a command that returns in ten
milliseconds looks like a crash.

### `⇧H` — how this task has been going

```
 taskui ▸ test                                    ✓✓✓✗✗   5 runs   2 failed
 ──────────────────────────────────────────────────────────────────────────
▌✗ 12m ago      1.31s  ██████████████      44 lines  task test
 ✗ 2h ago       1.28s  █████████████       44 lines  task all
 ✓ 5h ago       1.19s  ████████████        31 lines  task all
 ✓ 6h ago         88ms █                   31 lines  task test
 ✓ yesterday    1.21s  ████████████        30 lines  task all
```

The trend across the top is where it turned, which is more use than how many failures there
were. The bar is scaled to the slowest run in the list, so "when did this get slow" is a
shape rather than a column of numbers to compare by hand. The right-hand column is the run
each appearance was part of, because `test` reached from `task all` and `test` on its own
are the same task under different circumstances, and that explains most surprising
durations.

`⏎` reopens that run in full. `⇧D` diffs it against the last run that went *differently* —
not the row below. On the newest of three consecutive failures, the row below answers
"nothing changed", which is true, useless, and lands exactly where the question was most
worth asking.

### `⇧D` — what changed

```
 taskui ▸ suite                       vs when it last passed   2h ago   +7   -3
 ──────────────────────────────────────────────────────────────────────────────
▌     ⋮
  2  2  === RUN   TestAlpha
  3  3  --- PASS: TestAlpha (0.00s)
  4  4  === RUN   TestBeta
  5   - --- PASS: TestBeta (0.00s)
     5+     beta_test.go:42: want 3, got 4
     6+ --- FAIL: TestBeta (0.00s)
  6  7  === RUN   TestGamma
```

Last *green* rather than last run: "it worked before" is the comparison that isolates the
failure, where diffing two consecutive failures usually shows only that the timestamps
moved. A task that has never passed falls back to the previous run, and the header says so
rather than letting you mistake one comparison for the other.

The stretches both runs share are elided down to a `⋮`; `[` and `]` widen and narrow what is
kept around each change. Locations are underlined here too, so `e` works on a line that has
just appeared — which is the shortest path there is from "it broke" to the file.

Both are scriptable, like everything else:

```
taskui --timeline test   # one run per line, tab-separated
taskui --diff test       # unified-ish, with the line numbers on the rows
```

### `--quickfix` — hand the failures to your editor

The same resolving, addressed to a program. `--quickfix` prints the last run's failures as
absolute `file:line:col: message`, which is the one form every editor already walks:

```vim
set errorformat=%f:%l:%c:\ %m
:cexpr system('taskui --quickfix')
:cexpr system('taskui --run backend:test --quickfix')   " run it and populate in one go
```

Absolute is the point. `go test` prints `order_test.go:88` relative to the package it ran
in; an editor resolves that against its own working directory and the jump opens nothing.
taskui knows which task printed the line and where that task ran, so it can answer properly
— and a reference it could only guess at is left out rather than sent as a guess you would
follow without looking. `--task` narrows it to one task, whether or not that task failed.

## Knowing what a run will do, and what it did

### Why a task did not run

go-task skips a task when its `sources:` are unchanged, its `status:` command passes, or a
`precondition:` fails — and it says so, on a line with no `[name]` prefix, which lands in the
*parent's* output several rows away from the `⏸` it explains. taskui copies the reason onto
the task it is about:

```
 ▾ ✓ all
   1   task: Task "cached" is up to date
   2   task: Task "gated" is up to date
     ⏸ cached  up to date
     ⏸ gated  up to date
   ▾ ✗ guarded  precondition not met
```

`⇧R` re-runs with `--force`, which is the answer to most of them. Before running anything,
`s` says the same thing in advance — `would run — but go-task says it is up to date` — so
that `⏎` doing nothing is a prediction rather than a discovery.

### Where the time went

`⇧T` in a run:

```
 taskui ▸ task all                                       2.1s total   4 tasks
 ────────────────────────────────────────────────────────────────────────────
▌✓     1.2s   58%  ▄▄▄▄▄▄▄▄  build
 ✓    502ms   24%  ▄▄▄▄      test
 ✓    208ms   10%  ▄▄        fmt
 ✓      0ms    0%            all   1.9s inc.
```

Ranked by **self** time — what each task spent on its own commands, with its children's time
subtracted. Ranked by total instead, every aggregate outranks every task that did any work
and the profile announces that `all` is the slow one, which is true and useless. `all` sits
at the bottom here with `1.9s inc.` beside it, which is the honest description of a task
whose job is to invoke three others.

It stays current while the run does, and holds still once the run stops. `⏎` goes to that
task in the run view.

### Flaky tasks

Each run records the git revision it happened at, which makes flakiness a fact rather than a
guess: **the same commit, both outcomes**. A task that failed and then passed has the obvious
explanation that somebody fixed it, and only the commit tells those two apart.

```
$ taskui --flaky
coinflip	d1f091a	4 passed	4 failed	just now
-- 1 flaky
```

It exits non-zero when it finds any, so it composes: `taskui --flaky || echo "look into it"`.
A task's timeline says so too, in the header — `flaky at d1f091a` — which is where you are
already standing when you need to know whether a failure means anything. Runs from a dirty
tree never count: two runs of uncommitted work are not two runs of the same code.

### What the archive remembers, and for how long

Two caps, because two different things are being kept.

The **output** of a run is unbounded — kilobytes for a `task fmt`, megabytes for a `task all`
carrying build logs — so the last fifty runs keep their text and older ones are deleted. The
**record** of a run is the manifest without any of that: 461 bytes at the median, so
`history.ndjson` holds the last two thousand per project and costs a megabyte to do it.

Capping them together was the bug. The fifty counted every project at once, so an afternoon
in one repository deleted another's history, and with it the three things worth keeping runs
for: a timeline had one point to draw, `--flaky` needs one commit to appear twice and never
saw it, and the `✓`/`✗` column forgot tasks you ran yesterday. Now only a diff is bounded by
the output cap, because a diff is the only one that needs the lines.

What you notice: `⇧H` and `--flaky` go back much further, and `⇧D` on a run old enough to
have been dropped says the output is no longer stored rather than diffing against nothing.
The ledger is written the first time a run is saved after upgrading, absorbing whatever
directories are still there — no migration step, and nothing to delete.

### Aggregates that cover less than they claim

`task test` says "Run all automated tests". Whether that is true depends on which namespaces
have a `test` and which of them the execution graph reaches, and those are two facts in
different files with a `desc:` string between them claiming they agree. Nothing checks the
claim: go-task runs what it is told, yamllint reads syntax, and a description is prose.

`--lint` checks it, because taskui is holding both halves already:

```
$ taskui --lint
test — Run all automated tests
  ✗ web:test                     declared, never reached

check — Type-check everything (fast feedback; no codegen)
  · api:check, api:control:check  covered by lint — not by check

1 gap, 1 note
```

The first is the one worth having. `web:test` exists, root `test` claims to run all the
tests, and the graph never gets there — so those tests run nowhere, which is a green tick
over untested code and reads as correct until you go looking.

Reachability rather than names, which is what makes it right: an aggregate that reaches a
namespace through something else still covers it. `lint` never calls `api:lint` — it calls
`api:check`, which reaches `api:tenant:lint` two levels down — and a check that matched names
would call that a gap and be wrong. The question is asked per namespace and not per task for
the same reason: `build` calling `app:dist` rather than `app:build` is a deliberate choice
about which artifact a deploy consumes, not a hole, and the namespace got its chance to run.

A `·` is the softer finding: covered, just not by the aggregate sharing its verb. `api:check`
sits in `lint` and not in `check` because `check` promises not to codegen — deliberate, worth
saying, not worth failing over. Notes do not count towards the exit code.

`✗` does. It exits `2`, the code `--flaky` already uses for "found what it was looking for",
so a script can tell a hole in the Taskfile from no Taskfile at all, and `precommit` fails on
either.

Most of what a first run reports is correct as written — a deploy that must not fire from a
local gate, a docs build, a binary for another platform — so `.taskui-cover` in the project
root silences those, one glob per line, `#` for the reason:

```
# the deploy pipeline runs from CI with production credentials
deploy:*
# the site has its own workflow
site:build
```

Same shape as `.taskui-danger`, and a repository without one loses nothing. A check whose
findings are mostly deliberate is a check people stop reading, which is the failure mode
worth designing against.

`--lint --matrix` prints the table the findings came out of rather than only the
disagreements:

```
$ taskui --lint --matrix
       api  app  backend  deploy  dev  infra  site
all     —    —      —       ✓      —     —     —
build   ✗    ✓      ✓       —      —     —     ✓
check   ·    ✓      ✓       ✓      —     ✓     ✓
clean   ✓    ✓      ✓       —      ✓     ✓     ✓
fmt     —    ✓      ✓       —      —     ✓     —
lint    ✓    ✓      ✓       —      —     ✓     —
test    —    ✓      ✓       —      —     —     —

✓ reached   · covered by another aggregate   ✗ never reached   ~ exempt   — not declared
```

This is the table that gets written by hand. A Taskfile organised by `includes:` tends to
grow a comment above the aggregates with the verbs down one side and the domains across the
top, and it drifts, because nothing regenerates it — the one it replaced here claimed `api`
had no `build`, and `api:tenant:docs:build` had been added since. Both views come out of one
grid, so the table and the report cannot disagree about what covered means. Only namespaces
that answer some aggregate get a column.

### Running several at once

Slots have always held six concurrent runs; filling them meant six trips through the picker.
`m` marks a task, `⏎` runs every marked one in its own slot:

```
 ◉ all            Everything: format, lint, test, build
 ├ check          Type-check and vet only — fastest feedback
 ◉ clean          Remove build artifacts and Task's own run cache
 ─────────────────────────────────────────────────────────────────
 ◉ 2 marked   ⏎ run them   m unmark   ⇧M clear
```

A batch with anything on the danger list asks once for the whole set rather than putting a
prompt between each pair of starts. More marks than free slots starts what it can and says
what it left — a batch that quietly did less than you asked is worse than one that refuses.

Since a run unfolds under the row it came from, a batch of three is three blocks of live
output in one list, each with its own tasks, commands and peek window. That is the shape
the run view cannot show: it has one run on screen at a time.

### Letting a run outlive taskui

`⇧A` detaches the run in the focused slot. Quitting stops being responsible for it; `x` still
is. The child is a session leader in its own process group, so it genuinely survives — that
is verified rather than assumed, by killing taskui and watching the run keep working.

What it cannot do is keep showing you the output: once taskui is gone the pty is closed and
everything printed after that is lost. Which is why detaching archives what it has at that
moment — otherwise a two-hour run leaves nothing behind.

## Odds and ends that turned out to matter

### Completion

```
taskui completion zsh > "${fpath[1]}/_taskui"     # or bash, fish, powershell
```

cobra gives the command away free and what it gives away completes *flag names* — the half
you did not need, since `--help` lists them. The values are the useful part, and taskui
already knows how to discover them: `--run`, `--graph`, `--timeline` and `--diff` complete
task names from the Taskfile in the current directory, with their descriptions; `--dump`
completes pivot names including any your config added; `--theme` completes themes.

### Re-running what failed

`⇧F` in a run starts everything in it that broke, each in its own slot. After a red
`task all` that is the whole loop: you do not want the pipeline again, and you do not want
three trips back through the tree.

It starts the tasks that *actually* failed, not the ones merely reported as failing — an
aggregate is failed because its child was, and re-running the aggregate would run
everything, which is the thing this exists to avoid.

### Being told when it is done

A run that finishes while you are looking at something else rings the terminal:

```yaml
bell: on        # or `off`, or `failed` for only the ones that broke
```

Deliberately narrow. A run you watched finish needs no announcing — you watched it — and the
whole reason to want a bell is that you left a long one going and went to read something.
Each run rings once; what your terminal does with the BEL, flash or beep or nothing, stays
your terminal's business.

### Narrowing the archive search

`--search 'FAIL'` greps every stored run, which is the right default and the wrong thing to
do twice. Once you know which task has been failing:

```
taskui --search 'FAIL' --task backend:test --since 2d
```

`--since` takes Go durations plus the units an archive actually gets asked about — `2d`,
`3w`.

### Talking to something other than a terminal

`--json` is the machine-readable form of three flags that already existed — a front end
somewhere else gets the engine without reimplementing any of it:

```
taskui --list --json                   # the tasks: descriptions, danger flags, where each
                                       # is written, and how the archive says it went
taskui --timeline backend:test --json  # one task's stored runs
taskui --run ci --json                 # the run as it happens, one event per line
```

The run form is a stream of newline-delimited JSON: a `run` object, then `graph`, then
`task` and `line` events as they arrive, then `exit`. A task is announced before its output
and its verdict after it, so a consumer never sees `Ok` followed by more of that task's
lines; line indices count from the start of the run rather than the start of the buffer, so
a long-lived task dropping its oldest lines does not renumber what you have already stored.
The run is archived like any other, and the process exits with the task's own code.

```json
{"type":"run","root":"ci","dir":"/src/acme","started_unix":1787983902}
{"type":"graph","edges":{"ci":["build","test"],"build":[],"test":[]}}
{"type":"task","name":"build","status":"Running"}
{"type":"line","task":"build","index":0,"text":"go build ./...","command":true}
{"type":"task","name":"build","status":"Ok","duration_ms":314}
{"type":"exit","code":1,"duration_ms":2100,"saved":"~/.local/state/taskui/runs/…"}
```

One process per run rather than a daemon: the archive on disk is already the shared state,
so separate processes see each other's history for free, and a caller that wants three runs
at once has a process table to multiplex them with.

`--events <path>` is the same stream from inside the interactive UI, written to a unix
socket (or a file) instead of stdout — for a host that is *showing* taskui rather than
drawing it. The output lines are left out, because whoever is looking at that terminal can
already see them; what goes out is what started, how each task went, what it exited with,
and the location `e` was pressed on, which the host opens in its own editor. That last one
is why the Neovim plugin does not have to know anything about `$EDITOR`.

## Pivots

Grouping is a pivot, not a set of bespoke views: one flat list of tasks plus a key
function, rendered as a fold tree. `p` cycles through them.

Three ship. Two are transposes of each other; the third answers a different question.

**Domain** splits the name on `:`, n levels deep. This is how you browse.

```
▾ backend  26
    build              Build the workspace
    fmt                Format Rust code
    lint               Rust lints + format check
  ▸ migrate  5         Apply pending migrations to the local database
  ▸ docker   2
```

**Verb** groups on the last segment instead, collecting the cross-cutting concerns that
the domain tree scatters. Groups are sorted by size, so what you pivoted to find is on
top; verbs that only appear once pool into a flat `other` bucket at the bottom, because
grouping a singleton reads worse than not grouping it.

```
▾ lint  5
    lint               Lint all source code
    api:lint           Validate the spec
    app:lint           Clippy on the wasm target
    backend:lint       Rust lints + format check
    infra:lint         Check Terraform formatting
```

The root aggregate sits directly above its own fan-out, so the verb pivot doubles as a
static preview of what `task lint` will actually do — without having run anything.

**File** groups by the Taskfile each task is written in, which on a project with `includes:`
is the answer to "where do I edit this":

```
▸ api/Taskfile.yml       20
▸ backend/Taskfile.yml   30
▸ site/Taskfile.yml      14
▸ acme/Taskfile.yml      21
```

### Adding your own

A pivot is a key function, so a project can supply one. Two forms, in `config.yaml`:

```yaml
pivots:
  # Group by what a pattern captures from the task name.
  - name: layer
    regex: '^([^:]+):([^:]+)'
    path: ["{1}", "{2}"]

  # …or hand the question to a program.
  - name: risk
    command: ["./scripts/taskui-pivot-risk"]
```

They join the cycle after the built-ins, in the order you wrote them, and `--dump layer`
prints one the same way it prints `domain`.

**`regex`** is matched against the task name; `path` builds the grouping from its captures,
`{1}` being the first. `path` defaults to one level of `{1}`, or of the whole match when the
pattern has no groups. A segment that comes out empty is dropped, so one pattern can serve
tasks at different depths. A name the pattern does not match is not an error — that task
lands in `(other)`.

**`command`** is the escape hatch. taskui writes the task names to its stdin, one per line,
and reads back `name<TAB>outer/inner`:

```sh
#!/bin/sh
while IFS= read -r name; do
  case "$name" in
    *deploy*|*migrate*) printf '%s\tdangerous\n' "$name" ;;
    *)                  printf '%s\tsafe/ordinary\n' "$name" ;;
  esac
done
```

A name it says nothing about pools into `(other)` — as does every name if the program is
missing, fails, or takes longer than five seconds. A pivot that cannot answer should leave
you with a usable list rather than an empty one. It runs once per task list and the answer is
cached: filtering changes what is shown, not where a task belongs, so a keystroke does not
spawn a process.

Leaves in a custom pivot show the full task name. The grouping is orthogonal to the name —
grouping by owner does not make the owner part of what a task is called — so flattening it
into the label would throw away the one thing identifying it.

### Why not Go plugins

`plugin.Open` needs the host and the plugin built by the same toolchain version, against
identical versions of every shared dependency, with matching build flags, and it needs cgo.
taskui's releases are built `CGO_ENABLED=0` — where every `plugin.Open` returns `plugin: not
implemented` — and with `-trimpath`, which a locally built plugin would not match even with
cgo on. A Go plugin would work only for someone who built taskui themselves, on the same
machine, that afternoon. An external command has none of those constraints and can be
written in anything.

Three properties make `g` read as a pivot rather than a navigation reset, and they are
most of the implementation cost:

- **Selection survives it.** The task under the cursor stays under the cursor, at its new
  address, with the folds needed to reveal it opened.
- **The filter survives it.** Type `lint`, pivot, still filtered.
- **Fold state is per-mode.** Bouncing between pivots does not collapse what you opened on
  the other side.

### Ordering

A pivot decides what is inside what. What sits above what is a separate question, and it is
yours to answer:

```yaml
sort: file      # default | name | file | recent | failed | size
groups: last    # or `mixed`
pin: ["dev", "backend:test"]
```

| `sort:`   | what leads                                                          |
| --------- | ------------------------------------------------------------------- |
| `default` | each grouping's own order — alphabetical in `domain` and `file`, biggest group first in `verb` |
| `name`    | alphabetical, everywhere                                            |
| `file`    | the order the tasks are written in the Taskfile                     |
| `recent`  | the last thing you ran                                              |
| `failed`  | what is broken, most recent failure first                           |
| `size`    | the biggest group                                                   |

`file` is the one order somebody chose on purpose: a Taskfile is written in a sequence, and
alphabetising it throws that sequence away. It needs the locations from the JSON listing,
which arrive a moment after the first frame — the list settles once, early, and a task whose
location has not arrived sorts by name rather than claiming line zero.

`⇧S` cycles the same list at the keyboard, starting from whatever `sort:` set, and the
header names the order beside the pivot while it is not the default. It moves `sort:` only:
`groups:` and `pin:` below are decisions about a project rather than about the next ten
seconds.

`recent` and `failed` read the archive, so they are answers about this project's history
rather than about its Taskfile: a task that has never run sorts last in both, and with no
stored runs at all they are alphabetical. A group is as recent as its most recent task and
as broken as its worst one, so a fold tells you whether there is anything in there worth
opening.

**`groups:`** is where a subgroup sits among the plain tasks of the same namespace. `last`
keeps a namespace's own verbs together above its subtrees — the default, and the reason
`docker` sits below `lint` here despite `d` sorting first:

```
groups: last            groups: mixed

▾ backend  26           ▾ backend  26
    build                   build
    fmt                   ▸ docker   2
    lint                    fmt
  ▸ docker   2              lint
```

Worth setting to `mixed` alongside `recent` or `failed`, where the whole point is that the
interesting row rises to the top — and a group holding it would otherwise still be stuck
below every task beside it.

**`pin:`** hoists task names to the top of wherever they land, in the order you write them,
with the same `*` globbing as `.taskui-danger`. It is how you say "these are the ones I
actually run" without teaching taskui what a daily driver is. A group rises with anything it
holds, so pinning `backend:test` also lifts `backend` to the top of the list — a pin you
have to go looking for is not a pin.

## Arguments

Plenty of tasks need them — `wt:new NAME=backend`, `backend:test -- -p ingest`,
`site:new -- "My Post Title"` — and a runner that cannot pass arguments can only run half
of a real Taskfile. `a` opens a prompt:

```
task backend:test █   e.g. -- -p ingest for one crate
```

Two different things fill that line, and the distinction matters.

**Declared variables are pre-filled.** A task with `requires: { vars: [NAME] }` gets
`NAME=` typed for it, caret parked after the `=`:

```
task wt:new NAME=█            e.g. NAME=backend
task backend:blocklist:check WORD=█   e.g. WORD=адрес
```

That is safe to pre-fill because it is a declaration rather than prose — go-task will
refuse to run without it anyway — and because only the *key* is filled. Pre-filling the
example value would be handing you someone else's argument. The keys come from one
`task --summary` call (~40ms) made when the prompt opens; for tasks that declare nothing,
a `KEY=value` shape in the description is used instead.

**Everything else is only shown**, as the `e.g.` hint. Descriptions trail off into prose
often enough — `-- -p ingest for one crate`, where `for one crate` is commentary — that
pre-filling would hand you a command that is wrong in a way you might not notice before
pressing enter.

The line is a real input: `←`/`→`, `home`/`end`, `delete`. Input is split shell-style, so
`-- "My Post Title"` reaches go-task as one argument rather than three. `r` re-runs with whatever the run was started with; `a` from the run view
re-runs with something different.

## Customising

Three things are yours: what it looks like, what the keys do, and how much output a peek
shows. All of it lives in one optional file at `~/.config/taskui/config.yaml`
(`$XDG_CONFIG_HOME/taskui/config.yaml` if that is set, or `--config PATH`), and a missing
file is the normal case rather than an error.

```
taskui completion zsh  # shell completion — and it completes task names, not just flags
taskui config          # where it is, and whether anything is there yet
taskui config edit     # open it in $EDITOR, creating it from the defaults first
taskui config init     # just create it
taskui config path     # the path and nothing else, for scripts
```

`config edit` writes the annotated defaults before opening, because an editor on an empty
buffer is a worse starting point than one showing every setting there is. It never
overwrites a file you already have — `--force` is the way to say you meant it.

A subcommand rather than a valueless `--config`, which is the one thing no tool does:
`--config` takes a path everywhere, and making its value optional means
`taskui --config my.yaml` parses as `--config` plus a positional `my.yaml`, opening an
editor and then browsing a Taskfile in a directory named after your config file. `git config
--edit`, `npm config edit` and `crontab -e` all put the verb where it cannot be mistaken for
a path.

```yaml
theme: synthwave

keys:
  filter-matches: z
  edit: E

peek-lines: 8

colors:
  selection: "#101030"
  location: "#7dcfff"
```

Every key is optional, so a two-line file is valid. Anything wrong with it is reported in
the status bar rather than swallowed — a setting that silently does nothing is worse than
one that says why:

```
config: colors: `acccent` is not a colour setting; keys: pivot: `pp` is not a single key
```

Scalars can also come from the environment, prefixed and upper-cased:
`TASKUI_THEME=90s taskui`, `TASKUI_PEEK_LINES=12 taskui`. The flag beats the environment
beats the file.

### Themes

A theme is a file. Pick one with `theme:` in your config or `--theme` on the command line:

```
taskui --list-themes

  90s          ░▒▓ TASKUI ▓▒░
  charm        taskui
  default      taskui
  neubrutalism [ TASKUI ]
  synthwave    ▄▀▄ TASKUI ▄▀▄
  tokyonight   taskui
  y2k          ✧･ﾟ TASKUI ･ﾟ✧
```

Seven ship inside the binary. `default` uses ANSI names throughout, so it follows your
terminal's own colourscheme rather than arguing with it. `90s` does the opposite on
purpose — pinned magenta and cyan, double-ruled boxes, blocky arrows — because a vaporwave
palette that politely deferred to your Solarized scheme would not be one. `charm` is after
[charm.land](https://charm.land), whose libraries this front end is built on: indigo,
pink, mint, and restraint everywhere else. `synthwave` is the loud one: the genre's five
colours, half blocks instead of hairlines, and a selected row that is raised rather than
highlighted. `tokyonight` is the palette from
[folke/tokyonight.nvim](https://github.com/folke/tokyonight.nvim), in its `night` variant:
blue carrying the structure, magenta the accent, and a selected row the same indigo the
editor uses for its own visual selection. It is colours only — a palette has no opinion
about what a fold marker looks like — so it inherits every glyph from `default` and changes
nothing about the geometry.

`y2k` is the millennium: chrome silver doing the structural work, bubblegum and lilac on
top of it, and a selected row bevelled like a button you could press. It is also the only
one that wobbles — see below. `neubrutalism` is the opposite temperament: the heaviest
guides and rules the box-drawing set has, six flat colours each meaning exactly one thing,
and a selected row that is a black block with a thick yellow border and a hard offset
shadow. A terminal has no canvas to paint off-white and no corner radius to set to zero, so
that theme carries the parts of the style that survive the translation — the border, the
zero-blur shadow, the categorical colour — rather than pretending to the ones that do not.

#### Raised rows

The cursor's row is framed by two columns — a lit edge down its left and a darker one down
its right. Set the `selection-shade` glyph to a block and those two faces give the bar
thickness:

```yaml
colors:
  selection: "#3b1a6b"        # the face
  selection-light: "#ff9ee8"  # the lit edge
  selection-shade: "#1a0838"  # the shaded one
glyphs:
  rail: "▐"
  selection-shade: "▌"
```

It is not a cast shadow — nothing is falling on anything. It is the same trick a bevelled
button used, which is why it reads as something raised off the screen rather than painted
onto it. Leave the glyph as a space and the column goes back to being the right margin the
layout already left there; both columns are reserved either way, so a row is the same width
in every theme. Geometry that changed with the colours would be a theme that could break a
layout.

#### The wordmark

The header's wordmark is an ordinary string, so it can say anything. Two placeholders make
it say something that changes:

```yaml
glyphs:
  wordmark: "{frame}･ﾟ {project} ･ﾟ{frame}"
animation:
  wordmark-frames: "✧✧✧✦✦✦"
```

`{project}` is the directory you opened, so the header names what you are looking at rather
than the tool you are looking at it with — and the theme keeps its own decoration around it
instead of trading it away:

```
 ✧･ﾟ atlas ･ﾟ✧                        domain·verb  122 tasks
```

Having named the project, the header does not name it again — the ` › atlas` that usually
follows the wordmark is dropped, because a row that says the same thing twice is a row
wasted. That also applies to a literal wordmark that happens to match: open taskui on
taskui and you get one `taskui`, not two.

`{frame}` is where `wordmark-frames` goes. One column per frame, same rule as the glyphs —
a wordmark that changed width every frame would shove the whole header about. Leave the
frames out, or set `interval-ms: 0`, and the placeholder closes up rather than printing
itself at you.

#### Making it move

A terminal cannot move a row without moving everything under it, so nothing animates
vertical position — a list that shifted while you were reading it would be a bad trade for
any amount of charm. Three things can move, all of them on the cursor's own row:

| | |
|---|---|
| `selection-frames` | the two columns framing the row, cycling through half blocks — the marker climbs to the top of its cell, fills it, drops to the bottom and comes back |
| `selection-jiggle` | the row's own text, leaning a column or two right and back |
| `selection-blink`  | the row's highlight, going out for a frame or two — the bar drops, the row and rail stay |
| `wordmark-frames`  | whatever the wordmark's `{frame}` placeholder holds, so the header can twinkle |

```yaml
animation:
  selection-frames: "▀█▄█"
  selection-jiggle: "000000111111100"
  selection-blink:  "1111111111100"
  interval-ms: 320
```

Frames are one string rather than a list, because that is what a sequence looks like. Each
is one column, same rule as the glyphs. Anything from 40ms to 2s is accepted — below that
it is a strobe, above it it stops reading as motion — and `interval-ms: 0` turns it off,
which is how a theme extending `synthwave` stops it moving without losing the rest.

The jiggle is one digit per frame saying how many columns right the row sits: `0` is home,
`1` and `2` lean. The blink is the same shape: `1` lit, `0` dark. Every sequence runs off
the one clock, so **the length is the speed** — write more frames to slow something down,
rather than reaching for a second timer to race the first.

**Write them the same length and they become one movement.** Stack the three above and the
shape is legible: six frames still and lit while the chrome edge slides down its column,
then three where it dips — edge at the bottom, text leaned a column right, bar dark — then
back. Lengths that differ never line up, which sounds like richness and reads like three
unrelated things twitching at a row you are trying to read.

The blink is taskui's own, not the terminal's `SGR 5`. Terminals do have a blink attribute
and each one blinks it at whatever rate it likes — plenty of people switch it off entirely —
and a blink you cannot time is not a setting. The rail keeps drawing through the dark
frames, deliberately: a cursor that vanishes is not a blink, it is a place you have to find
again.

The two halves are independent, and `animation:` works in your own `config.yaml` as well as
in a theme file — so keeping one and dropping the other is a line, not a fork:

```yaml
theme: y2k
animation:
  selection-frames: ""    # the bounce off, the jiggle and blink kept
  # selection-jiggle: "" — the lean off, and the reserved column goes back to the row
  # selection-blink: ""  — the highlight stays on
  # interval-ms: 0       — all of it off, nothing redraws at all
```

Sideways is safe in a way that up and down is not: the row keeps its line, so nothing under
it moves. The room it leans into is reserved on *every* row, whether or not that row is the
one moving — a row that got wider as it leaned would have to take the column out of its own
right edge, where the counts and the timestamps are, and you would watch a `13` become a
`1` every time the cursor went past. A theme that wobbles pays one column of width, once,
in layout.

Off unless a theme asks for it. `y2k` is the one that fidgets — it does all four, and it
is meant to be the only one that does: its wordmark brightens on the same three frames the
row dips, so the header and the cursor are one gesture rather than two things that happen
to be moving. `synthwave` moves its edges and nothing else, and
`default`, `charm`, `90s` and `neubrutalism` sit still. A theme that animates costs a redraw every interval
for as long as taskui is open — cheap, but not nothing, and not a decision to make on
somebody else's behalf. `taskui --theme y2k --screenshot 92x20 --phase 8 --colour .`
renders one frame of it if you want to look at the sequence a step at a time.

None of them has any special standing. A file in `~/.config/taskui/themes/` shadows a
built-in of the same name, so nothing that ships is a decision you are stuck with.

#### Writing one

```
task theme:new NAME=mine FROM=charm     # or: taskui --dump-theme charm > ~/.config/taskui/themes/mine.yaml
task theme THEME=mine                   # look at it, in colour, without launching anything
```

`--dump-theme` prints a theme *fully resolved* — every value it landed on, annotated with
what it paints — so the starting point is a file you edit rather than a form you fill in.
It round-trips: a dumped theme loads back identical, for all three, which is tested rather
than assumed.

Most themes should be short. `extends:` inherits everything you do not mention, so the
whole of `90s` is nine colours and a dozen glyphs on top of `default`:

```yaml
extends: default
colors:
  accent: "#ff2fd0"
  rule: "#ff2fd0"
  selection: "#3a1a78"
glyphs:
  wordmark: "░▒▓ TASKUI ▓▒░"
  fold-open: "▼"
  guide-branch: "╠"
  rule: "═"
```

A theme sets two blocks, and they fail differently.

**Colours** are the twenty-nine roles below. A bad one costs you that colour and says so.

**Glyphs** are the twenty-five characters the UI draws its structure out of — fold markers,
tree guides, the cursor rail, status ticks, the hairlines. Each is **one terminal column**,
and that is checked when the theme loads. The layout arithmetic is built on knowing how
wide a glyph is before it is drawn, so a theme that could smuggle in a three-column marker
could push a column off the edge of somebody else's terminal, where nobody would ever see
it happen. `wordmark` is the exception: it is a label, it is measured, and it can be as
wide as it likes.

Anything the config's own `colors:` and `glyphs:` blocks set lands *on top* of the chosen
theme, so liking a theme except for one thing is two lines rather than a fork:

```yaml
theme: 90s
colors:
  selection: "#101030"
```

### Colours

Every colour the UI draws is a named role, settable from a theme or straight from the
config:

```yaml
colors:
  accent: magenta
  status-ok: "#7ee787"
  status-failed: "#ff7b72"
  selection: "#1e2030"
  search: bright-cyan
```

If the defaults wash out — embedded terminals like Neovim's `:terminal` often render the
dimmer half of the ANSI palette very close to the background — there is a louder set ready
to go:

```
mkdir -p ~/.config/taskui && cp config.high-contrast.yaml ~/.config/taskui/config.yaml
```

Values are an ANSI name (`red`, `bright-blue`, `purple`), a `#rrggbb`, or a 0–255 palette
index. Names follow whatever your terminal's own scheme says — usually what you want from
a terminal program — while `#rrggbb` pins the colour exactly.

`taskui --dump-config` prints every key at its current default, each with a comment
saying what it paints, so `taskui --dump-config > ~/.config/taskui/config.yaml` gives you
a file to edit rather than a form to fill in. It round-trips: a dumped config loads back
identical, which is tested rather than assumed.

`selection` is special: `default` means reverse video, which cannot be invisible whatever
your colourscheme is. Any real colour is used as a background instead. The original
default was a fixed `#282c34`, which looked deliberate on the theme it was picked against
and disappeared on every other one.

Every key is optional, so a two-line file is valid and a missing file is the normal case.
Both `colors:` and `colours:` are accepted. A bad value or a misspelled key is reported in
the status bar rather than ignored — a colour that silently does nothing is worse than one
that says why:

```
config: bad.yaml: colors: unknown field `moode`, expected one of `accent`, `dim`, …
```

The colours live in one table in `internal/theme/theme.go` and the glyphs in one table in
`internal/theme/glyph.go`, so adding either is a single line and no call site holds a
literal. That is the reason "themeable" was a contained change rather than a hunt through
the rendering code — and it is what makes `--dump-theme`, the validation and the
round-trip test read from the same source as the renderer.

### Keys

Every action can be pointed at a different key. The action keeps its meaning on every
screen that offers it, so rebinding `filter-matches` moves it in the run view and nowhere
else has to care:

```yaml
keys:
  pivot: P
  filter-matches: z
  stop-all: Q
  fold-all: shift+space
  rerun: ctrl+r
```

A key is a character, or `ctrl+`, `alt+` and `shift+` in front of one. `space` names the
space bar, since a lone space in YAML is not a thing you can write and read back.

`shift+` only works on keys shift cannot change — `space`, and the same key under `ctrl` or
`alt`. On a letter the shift is already in the character, so `G` is the binding and
`shift+g` is refused rather than accepted and never fired. Modified keys also need a
terminal that reports them: Kitty, Ghostty, WezTerm, foot, recent Alacritty and iTerm2 do.
Somewhere that does not, `ctrl+r` arrives as plain `r` and `shift+space` as plain space, so
keep anything you need everywhere on a plain character.

`taskui --dump-config` lists every action name. A key bound to two actions on one screen is
reported rather than silently resolved — a shadowed key looks like a broken one. What `?`
and the footer print is written by hand, so a rebinding moves the key without moving the
label.

### Everything else

```yaml
# How many lines a task shows when its output is folded to a peek. Five is enough for a Go
# test failure's assertion and its file:line, or a compiler error and its note — but
# "enough" is a property of the tools you run, not of taskui.
peek-lines: 8

# Ask the terminal for mouse events, so the wheel scrolls taskui.
mouse: on
```

The wheel moves one row a notch on whichever screen you are on — the picker, a run, the
history list, the timeline, the diff, the profile. It is defined as arrowing rather than as
scrolling of its own, so everything that hangs off the arrow keys comes with it: scrolling
away from a running task stops following it, exactly as `k` does.

`mouse: off` gives the mouse back to the terminal. The trade is real in both directions: a
terminal that is forwarding mouse events to a program is not selecting text with them, so
drag-to-select over taskui's output needs your terminal's own override — shift in most of
them, option on macOS — until you turn this off.

A `.taskui-danger` file in the project marks tasks that need a confirmation before they
run — one pattern per line, `#` comments, `*` supported:

```

# Ask the terminal for mouse events, so the wheel scrolls taskui.
mouse: on
deploy:*
backend:migrate:prod
The wheel moves one row a notch on whichever screen you are on — the picker, a run, the
history list, the timeline, the diff, the profile. It is defined as arrowing rather than as
scrolling of its own, so everything that hangs off the arrow keys comes with it: scrolling
away from a running task stops following it, exactly as `k` does.

`mouse: off` gives the mouse back to the terminal. The trade is real in both directions: a
terminal that is forwarding mouse events to a program is not selecting text with them, so
drag-to-select over taskui's output needs your terminal's own override — shift in most of
them, option on macOS — until you turn this off.

*:wipe
```

Its presence switches off the description heuristic entirely. A guess and a declaration
disagreeing about which tasks are dangerous is worse than either alone; once you have
written the list down, that list is the answer.

## In Neovim

The tool itself, hosted in a terminal, with the editor doing the two things a
terminal cannot do from inside itself.

```lua
-- lazy.nvim
{ "ddromanidis/taskui", build = "go install .", cmd = "TaskUI", opts = {} }
```

Neovim 0.10 or newer, the `taskui` binary on your `PATH`, and go-task — which is what
`:checkhealth taskui` checks, in that order.

```
:TaskUI                    open the terminal (again to hide it)
:TaskUI backend:test       open it and run one, with completion over the task names
:TaskUI edit backend:test  open the task's definition in the Taskfile
:TaskUI quickfix           the last run's failures, in the quickfix list
:TaskUI stop               stop taskui and every run it owns
```

**What you see is taskui.** Not a reimplementation of it in Lua: the pivots, the fold
tree, the peek windows, the slots, the archive, the timeline and the diff are all there,
because it is the same program. The plugin's job is to host it and to listen.

**What Neovim adds** is what the terminal cannot do from inside itself:

- **The quickfix list.** A failed run fills it — resolved by the binary, so `]q` lands on
  the failing assertion of a `go test` whose paths are relative to a directory Neovim knows
  nothing about. `quickfix = "always" | "never"` if you would rather it did not.
- **`e` opens the file here.** Press `e` on a `file:line` in the terminal and it opens in
  the editor that is already open, rather than launching `$EDITOR` in a window inside it.
- **A statusline component.** `require("taskui").status()` is `✗ backend:test 7.1s`, fed by
  the events, so it keeps saying so with the terminal closed.
- **Notifications**, for when the terminal is not the window you are looking at.

**The wheel scrolls taskui**, not the buffer it is drawn into. Neovim forwards mouse events
to a terminal program when that program asks for them and processes them itself when it does
not — so before taskui asked, a notch over the picker scrolled the terminal buffer instead,
through frames taskui had already replaced. It asks now. If you have `set mouse=`, Neovim
has no mouse to forward and `:checkhealth taskui` says so; `mouse: off` in taskui's own
config opts back out from the other end.

That is the whole seam: `taskui --events <socket>` writes what its runs are doing as
newline-delimited JSON, the plugin listens, and nothing else crosses between them.

```lua
require("taskui").setup({
  binary = "taskui",       -- or an absolute path
**The wheel scrolls taskui**, not the buffer it is drawn into. Neovim forwards mouse events
to a terminal program when that program asks for them and processes them itself when it does
not — so before taskui asked, a notch over the picker scrolled the terminal buffer instead,
through frames taskui had already replaced. It asks now. If you have `set mouse=`, Neovim
has no mouse to forward and `:checkhealth taskui` says so; `mouse: off` in taskui's own
config opts back out from the other end.

  project = nil,           -- nil means Neovim's cwd
  position = "float",      -- float | left | right | top | bottom | tab
  width = 80,              -- for a left or right split
  height = 20,             -- for a top or bottom one
  args = {},               -- extra arguments, e.g. { "--theme", "y2k" }
  quickfix = "on_failure", -- on_failure | always | never
  open_quickfix = false,   -- open the quickfix window when a run fills it
  notify = true,           -- say how a run went
  keys = {                 -- bound inside the terminal only
    toggle = "<A-t>",      -- the same key you bound `:TaskUI` to
    close = "<C-q>",
  },
})
```

Two keys are the host's, and both are bound **inside the terminal buffer only**, so neither
exists anywhere else in the editor. `toggle` needs saying twice — once in your `:TaskUI`
mapping and once here — because a focused terminal is in terminal mode, where every
keystroke goes to the program: the normal-mode mapping that opened the window cannot close
it. Set it to whatever you opened it with.

Everything else in that terminal belongs to taskui, because taking its keys would be taking
them from the thing you asked for. Hiding is hiding — the process carries on with its slots
and its scroll position, and `:TaskUI` brings it back where you left it.

## Development## Development

The project's own Taskfile is the smallest honest test of taskui — it is deliberately
shaped to exercise what it claims to handle, with tasks that are both runnable and parents
of others, cross-cutting verbs for the verb pivot, and `requires:` blocks so the args
prompt has something real to pre-fill.

```
task            # every task, which is the point
task check      # build and vet, fastest feedback
task test       # the suite
task test:race  # the suite under the race detector
task lint       # golangci-lint, plus a format check
task all        # format, lint, test, build
```

Driving it against itself:

```
task run                        # open taskui on this repo
task probe:run                  # capture a deliberately failing pipeline
task shot -- --keys Ojj         # render one frame to stdout, no terminal needed
task theme THEME=synthwave      # preview a theme in colour
```

### Exit codes

```
0   nothing went wrong
1   taskui could not do what was asked — bad flag, no Taskfile, no go-task, unreadable config
2   a check found what it was looking for: `--flaky`, and nothing else
```

`--run` is the exception: it means run this and be it, so it exits with the task's own status
— which for go-task's own errors is in the 200s. That makes `taskui --run ci` usable as a CI
step, which it was not before: it printed `exit 1` and returned 0.

`--flaky` gets a code of its own so a script can tell "this task is flaky" from "there is no
Taskfile here", which is the distinction an exit code exists to draw.

`--screenshot` is why the rendering is testable at all: `View()` returns a string, so a
frame rendered off-screen is the same code path the live UI runs. The suite renders every
screen at nine awkward terminal sizes — one row of body, narrower than a task name, either
side of every layout threshold — and asserts the frame is exactly its terminal size. Tests
that need a real `task` process spawn one; they skip rather than fail if go-task is
missing.

Layout:

```
cmd/                  the command line: flags, headless output, the screenshot path
internal/task         discovery — `task --list-all`, parsed
internal/graph        the execution graph — `task --summary`, recursed
internal/pivot        the fold tree, one builder per pivot
internal/run          the pty, the capture, the process group
internal/redact       masking credentials out of captured output
internal/store        the archive
internal/search       one matcher over the live run and the archive both
internal/diff         Myers, for comparing two runs of one task
internal/loc          finding `file:line` in output, and how to open it
internal/keys         the keymap, as data
internal/theme        colours, glyphs, animation, and the theme files
internal/app          state, key handling, rendering
```

## Releasing

`ci.yml` runs the formatter, `golangci-lint` and the tests on macOS and Linux, and separately runs the
project's own `task all` — the tool exists to run Taskfiles, so running its own is a test
of both.

`release.yml` fires on a `v*` tag. goreleaser builds darwin and linux binaries for amd64
and arm64, runs the test suite before it builds anything, attaches the archives and their
checksums to the GitHub release, and writes a Homebrew cask into `ddromanidis/homebrew-tap`.

That last step pushes to a *different* repository, which the default workflow token cannot
do. It needs a `HOMEBREW_TAP_GITHUB_TOKEN` secret — a fine-grained personal access token
with contents write on `ddromanidis/homebrew-tap`:

```
gh secret set HOMEBREW_TAP_GITHUB_TOKEN --repo ddromanidis/taskui
```

Without it the cask step skips itself and the release still succeeds. A release going red
because an optional convenience is unset would be worse than publishing the cask by hand.

### Cutting one

The tag *is* the version. There is no file to bump: goreleaser reads the tag, and the
Taskfile stamps `git describe` into the binary — a `VERSION` file would be a second answer
to a question that already has one, and the two would disagree the first time someone
edited only one of them.

```
task release:version            # what the binary would call itself right now
task release:notes              # every commit since the last tag
task release:tag VERSION=v0.3.0 # check, tag, push — this publishes
```

`release:tag` refuses before it does anything irreversible. The version has to be
`vMAJOR.MINOR.PATCH`, the tree has to be clean, you have to be on `main`, `main` has to
match `origin/main`, the tag must not already exist locally or on the remote, and `task all`
has to pass. Reusing a tag is the one mistake that cannot be quietly undone — anyone who
already fetched it keeps the old commit, and their build and yours disagree forever.

If the release goes wrong in the first few minutes:

```
task release:untag VERSION=v0.3.0
```

That deletes the tag locally and remotely and removes the GitHub release. It is only useful
before anyone has fetched it; after that the version is spent and the answer is to cut the
next patch.

## Design notes

The long-form reasoning — why a peek rather than a fold, why `--output prefixed`, how a
stopped run reaps its process group, what the archive is for and what it deliberately is
not — lives in [DESIGN.md](DESIGN.md). [EXAMPLES.md](EXAMPLES.md) walks through it against
a real hundred-and-twenty-task Taskfile.

## Licence

MIT. See [LICENSE](LICENSE).
