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

Six screens, one keymap.

**The picker** is the Taskfile as a fold tree, which it already is — `backend:migrate:down`
is a path, not a name. It pivots: by domain (`backend` › `migrate` › `down`) or by verb
(`lint` › `app:lint`, `backend:lint`, `infra:lint`), and the second one is the transpose of
the first. Each row says how the task went last time, or how long it has been running right
now, in a slot you may not be looking at.

**The run view** is the execution tree with the output folded into it. Every task rests on
a *peek* — a window on its last few lines — so a glance tells you what each step is saying
without opening any of them. It follows what is running, and the moment something fails it
stops there and opens it.

**Slots** let runs outlive your attention. Start `docker compose up`, leave it, run
something else; the first one keeps going in a slot you can switch back to, with its scroll
position, its folds and its output intact. Quitting takes every one of them down rather
than orphaning it.

**The archive** keeps finished runs on disk as plain text — a directory per run, a
`manifest.json`, and a `.txt` and `.ansi` file per task. `taskui --search 'FAIL'` greps all
of it from anywhere.

**A timeline** is one task's own history: `⇧H` on any task lists every stored run of it,
newest first, with a duration bar and a `✓✓✓✗✗` trend across the top. `h` answers "what has
this project been doing"; `⇧H` answers "how has *this* been going", and those turn out to be
different questions.

**A diff** is what changed. `⇧D` on a failing task compares its output against the last run
in which it passed, elides the hundreds of lines both runs share, and leaves you with the
few that are new. Nothing else on your machine can do this, because nothing else kept both
runs.

And `e` opens the file. A `file:line` anywhere in captured output is underlined, and `e`
launches `$EDITOR` on it at that line.

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
taskui --timeline test        # how one task has gone, run after run
taskui --diff test            # what changed since it last passed
taskui --screenshot 90x30     # render one frame to stdout (add --keys 'g/lint')
```

`man taskui` covers the options, the keys and the files. `?` from any screen lists every
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
| `space` `o` `←` `→` | fold / unfold |
| `⇧O` `⇥` | fold or unfold everything |
| `⏎` | run |
| `p` | toggle the pivot |
| `a` | run with arguments |
| `i` | arm interactive mode for the next run |
| `/` | filter by name |
| `t` | jump to a task, leaving the list intact |
| `s` | what this task is, and what it will run |
| `v` | go to whatever is running, or the last run |
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
| `a` | re-run with different arguments |
| `i` | answer the task, or re-run it interactively |
| `x` | stop the run (again to kill it) |
| `⇧K` | stop every run |
| `⇥` `⇧⇥` `1`…`9` | switch between open runs |
| `⇧X` | close the slot (once its run has stopped) |
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
| `⇧D` | what changed between it and the run before it |
| `esc` | back |

In a diff:

| key | |
|---|---|
| `j` `k` `gg` `⇧G` | scroll |
| `[` `]` | less / more unchanged context |
| `e` | open the `file:line` under the cursor in `$EDITOR` |
| `esc` | back |

## Going where it broke

Three things turn "something failed" into "here is the line".

### `e` — open the file

Every compiler, test runner and linter prints `file:line`. taskui underlines them in
captured output and `e` opens the one under the cursor:

```
 ▾ ✗ test
   1 ❯ go test ./...
   2   === RUN   TestWrap
   3       view_test.go:88: want 3, got 4      ← underlined; e opens it
   4   --- FAIL: TestWrap (0.00s)
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

`⏎` reopens that run in full. `⇧D` diffs it against the one below it.

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

## Two pivots

Grouping is a pivot, not a set of bespoke views: one flat list of tasks plus a key
function, rendered as a fold tree. Two keys are wired up, and they are transposes of each
other.

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

Three properties make `g` read as a pivot rather than a navigation reset, and they are
most of the implementation cost:

- **Selection survives it.** The task under the cursor stays under the cursor, at its new
  address, with the folds needed to reveal it opened.
- **The filter survives it.** Type `lint`, pivot, still filtered.
- **Fold state is per-mode.** Bouncing between pivots does not collapse what you opened on
  the other side.

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
config: colors: `acccent` is not a colour setting; keys: pivot must be a single character
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
  synthwave    ▄▀▄ TASKUI ▄▀▄
```

Four ship inside the binary. `default` uses ANSI names throughout, so it follows your
terminal's own colourscheme rather than arguing with it. `90s` does the opposite on
purpose — pinned magenta and cyan, double-ruled boxes, blocky arrows — because a vaporwave
palette that politely deferred to your Solarized scheme would not be one. `charm` is after
[charm.land](https://charm.land), whose libraries this front end is built on: indigo,
pink, mint, and restraint everywhere else. `synthwave` is the loud one: the genre's five
colours, half blocks instead of hairlines, and a selected row that is raised rather than
highlighted.

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

#### Making it move

A terminal cannot move a row without moving everything under it, so nothing animates
position — a list that shifted while you were reading it would be a bad trade for any
amount of charm. What can move is the cursor's own two columns: give them a sequence of
half blocks and the marker climbs to the top of its cell, fills it, drops to the bottom and
comes back.

```yaml
animation:
  selection-frames: "▀█▄█"
  interval-ms: 280
```

Frames are one string rather than a list, because that is what a sequence looks like. Each
is one column, same rule as the glyphs. Anything from 40ms to 2s is accepted — below that
it is a strobe, above it it stops reading as motion — and `interval-ms: 0` turns it off,
which is how a theme extending `synthwave` stops it moving without losing the rest.

Off unless a theme asks for it, and only `synthwave` does. A theme that animates costs a
redraw every interval for as long as taskui is open — cheap, but not nothing, and not a
decision to make on somebody else's behalf. `taskui --theme synthwave --screenshot 92x20
--phase 2 --colour .` renders one frame of it if you want to look at the sequence a step at
a time.

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

Every action can be pointed at a different character. The action keeps its meaning on every
screen that offers it, so rebinding `filter-matches` moves it in the run view and nowhere
else has to care:

```yaml
keys:
  pivot: P
  filter-matches: z
  stop-all: Q
```

`taskui --dump-config` lists every action name. A key bound to two actions on one screen is
reported rather than silently resolved — a shadowed key looks like a broken one.

### Everything else

```yaml
# How many lines a task shows when its output is folded to a peek. Five is enough for a Go
# test failure's assertion and its file:line, or a compiler error and its note — but
# "enough" is a property of the tools you run, not of taskui.
peek-lines: 8
```

A `.taskui-danger` file in the project marks tasks that need a confirmation before they
run — one pattern per line, `#` comments, `*` supported:

```
deploy:*
backend:migrate:prod
*:wipe
```

Its presence switches off the description heuristic entirely. A guess and a declaration
disagreeing about which tasks are dangerous is worse than either alone; once you have
written the list down, that list is the answer.

## Development

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

`release.yml` fires on a `v*` tag: it builds binaries for `aarch64-apple-darwin`,
`x86_64-apple-darwin` and `x86_64-unknown-linux-gnu`, attaches them to the release with
checksums, hashes the source tarball, and updates the Homebrew formula to point at the new
tag.

That last step pushes to a *different* repository, which the default workflow token cannot
do. It needs a `TAP_TOKEN` secret — a fine-grained personal access token with contents
write on `ddromanidis/homebrew-tap`:

```
gh secret set TAP_TOKEN --repo ddromanidis/taskui
```

Without it the release still succeeds and prints the two lines to change by hand. A
release going red because an optional convenience is unset would be worse than doing it
manually.

So cutting a release is:

```
git tag -a v0.2.0 -m "taskui v0.2.0" && git push origin v0.2.0
```

## Design notes

The long-form reasoning — why a peek rather than a fold, why `--output prefixed`, how a
stopped run reaps its process group, what the archive is for and what it deliberately is
not — lives in [DESIGN.md](DESIGN.md). [EXAMPLES.md](EXAMPLES.md) walks through it against
a real hundred-and-twenty-task Taskfile.

## Licence

MIT. See [LICENSE](LICENSE).
