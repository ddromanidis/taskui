# Examples

Worked examples, in the order you are likely to need them. Every screenshot here is real
output, not a mock-up.

`taskui examples` prints a shorter version of this in your terminal, rendered live at your
width and in your theme — that one cannot go stale, because the frames are drawn when you
run it rather than written down here.

---

## Finding a task in a Taskfile you did not write

A repo with 122 tasks across nine namespaces. Opening it gives you the shape, not the list:

```
 taskui ▸ atlas                                        domain·verb   122 tasks
 ─────────────────────────────────────────────────────────────────────────────
▌▸ (root)                                                                   12
 ▸ api                                                                      13
 ▸ app                                                                      17
 ▸ backend                                                                  26
 ▸ deploy                                                               ⚠    9
 ▸ dev                                                                      11
 ▸ infra                                                                ⚠   11
 ▸ sec                                                                       4
 ▸ site                                                                     14
 ▸ wt                                                                        4
```

Three ways in, and they answer different questions.

**`/` filters** — narrows the list to what matches, hiding everything else. Use it when you
want to see *all* the linting tasks:

```
 taskui ▸ atlas                              domain·verb   /lint   6/122 tasks
 ─────────────────────────────────────────────────────────────────────────────
 ▾ (root)                                                                    1
▌└ lint           Lint all source code
 ▾ api                                                                       2
 └ lint           Validate the spec
 ▾ backend                                                                   1
 └ lint           Rust lints (clippy warnings are errors) + format check
```

**`t` jumps** — moves the cursor to the match and leaves the tree intact. Use it when you
know what you want and the surroundings still matter:

```
 jump: blint█   1/1   ⇥ next   ⏎ stay   esc go back
```

Both match fuzzily over the whole colon path, so `blint` finds `backend:lint`.

**`p` pivots** — regroups by verb instead of by namespace:

```
▾ lint  5
    lint            Lint all source code
    api:lint        Validate the spec
    app:lint        Clippy on the wasm target
    backend:lint    Rust lints + format check
    infra:lint      Check Terraform formatting
```

The aggregate sits directly above its own fan-out, so this doubles as a preview of what
`task lint` will do — without running anything.

---

## Working out what a task will do before running it

`s` on a task:

```
 taskui ▸ wt:new                                                              
 ─────────────────────────────────────────────────────────────────────────────
   New worktree + branch for an agent: task wt:new NAME=backend

requires
  NAME=   must be supplied with `a`

will run
  set -eu
  dir=".worktrees/"
  branch="agent/"
  test ! -e "$dir" || { echo "✗ $dir already exists (task wt:ls)" >&2; exit 1; }
  git worktree add "$dir" -b "$branch"
```

`⏎` runs it from there, `a` runs it with arguments.

---

## Running something that needs arguments

`a` instead of `⏎`. Tasks that declare `requires: { vars: [NAME] }` arrive pre-filled:

```
task wt:new NAME=█            e.g. NAME=backend
```

Only the key is filled in, never the example value. Tasks that take `--` style arguments
get a hint instead:

```
task site:new █               e.g. -- "My Post Title"
```

Input is split shell-style, so `-- "My Post Title"` reaches go-task as one argument.

Your Taskfile uses both go-task conventions:

| shape | read as | tasks |
|---|---|---|
| `NAME=backend` | `{{.NAME}}` | `wt:new`, `wt:rm`, `backend:gen:migration` |
| `-- convert report.pdf` | `{{.CLI_ARGS}}` | `run`, `dev:cmd`, `backend:test`, `site:new` |

---

## Reading a failed run

`⏎` on `ci` starts it and leaves you in the list. The run unfolds under the row it came
from: the tasks it pulled in, each command with the verdict of the step it announces, and
the last few lines each task printed.

```
 taskui ▸ acme                                    domain·verb·file   17 tasks
 ─────────────────────────────────────────────────────────────────────────────
 ▾ (root)                                                                   6
▌├ ci             Everything: build, test, package                ▿ ✗ 2.0s
 │ ├ ▿ ✓ build                                                29 more   1.2s
 │ │   3 │   compiling api
 │ │   4 └   built in 1.2s
 │ └ ▾ ✗ test                                                          780ms
 │     1 ✓ ❯ go build ./...
 │     2 └   running 42 tests
 │     3 ✗ ❯ go test ./...
 │     4 │   --- FAIL: TestOrderTotal
 │     5 └       order_test.go:88: want 1200, got 1180
```

Each command carries the verdict of the step it announces, and its output hangs off it on a
rail that closes at the last line — so which of the next forty lines belong to which step is
something you see rather than count. The whole run hangs off the row it was started from by
the same guides the task tree draws with.

`o` walks the whole block hidden → peek → full; the same key on a row inside it moves one
task. `space` stays on the tree, so a namespace still folds where it always did.

`v` gives the run the whole screen, which is where you read one. It follows the run, and the
moment something fails it unfolds that task and stops there:

```
 taskui ▸ task ci                                    FAILED    0.2s   exit 201
 ─────────────────────────────────────────────────────────────────────────────
   ✗ ci                                                                 2.0s
   ├ ▿ ✓ build                                               29 more     1.2s
   │    3 │   compiling api
   │    4 └   built in 1.2s
▌  └ ▾ ✗ test                                                          780ms
        1 ✓ ❯ go build ./...
        2 └   running 42 tests
        3 ✗ ❯ go test ./...
        4 │   --- FAIL: TestOrderTotal
        5 └       order_test.go:88: want 1200, got 1180
```

The failure is open in full; `build`, which finished, dropped back to a **peek** — `▿`, the
last few lines it printed. That is the resting state, so a run you have not touched still
tells you what each step actually said rather than only how long it took. `o` cycles a task
hidden → peek → full, `⇧O` does it to all of them, and `peek-lines:` in `config.yaml` sets
how many lines a peek shows.

Peeked lines are cut at the edge rather than wrapped, so five lines is always five rows —
one 300-character stack frame cannot swallow the window. Open a task fully and wrapping
comes back.

Every task carries its own clock, ticking while it runs — during a slow build the useful
question is which *step* is slow.

`esc` leaves without stopping the run; the picker header then says `▶ ci running · v to
watch`. `v` goes back. `x` stops it — from the picker too, on whichever task the cursor is
on, so a background run does not have to be opened just to be stopped. The header says
`stopping — x again to kill it` while it goes; a second `x` is a SIGKILL for the things
that catch SIGTERM and ignore it. `⇧K` stops every open run at once.

---

## Finding the error in 2,000 lines of output

`/` searches, `n` and `N` step through matches in execution order.

`f` filters the run down to just the matching lines, kept under the tasks that produced
them, with the tasks that have no hits dropped entirely:

```
 taskui ▸ task ci      /pending  1/2  filtered ±2    FAILED    0.2s   exit 201
 ─────────────────────────────────────────────────────────────────────────────
   ▾ ✗ test                                                            780ms
      8   3 migrations pending, refusing to start
```

`[` and `]` widen and narrow the context around each hit — `--- FAIL:` on its own hides
the assertion underneath it, which is the half that says what broke.

`y` copies the line under the cursor, `⇧Y` the whole task's output.

---

## "When did this start failing?"

`⇧H` on any task is that question, asked directly. It lists every stored run of that one
task, newest first:

```
 taskui ▸ backend:test                            ✓✓✓✗✗   5 runs   2 failed
 ──────────────────────────────────────────────────────────────────────────
▌✗ 9m ago       1.31s  ██████████████      44 lines  task ci
 ✗ 42m ago      1.28s  █████████████       44 lines  task ci
 ✓ 3h ago       1.19s  ████████████        31 lines  task ci
 ✓ 5h ago         88ms █                   31 lines  task backend:test
 ✓ yesterday    1.21s  ████████████        30 lines  task ci
```

The trend across the top is where it turned. The bars are scaled to the slowest run in the
list, so a step that got slower shows up as a shape rather than five numbers to compare by
hand — and the right-hand column is the run each appearance was part of, which is usually
the explanation.

`⇧D` answers the next question — what actually changed — against the last run that went
differently, which here is the last one that passed:

```
 taskui ▸ backend:test              vs when it last passed   3h ago   +7   -3
 ──────────────────────────────────────────────────────────────────────────────
▌     ⋮
  2  2  === RUN   TestOrderTotal
  3   - --- PASS: TestOrderTotal (0.01s)
     3+     order_test.go:88: want 1200, got 1180
     4+ --- FAIL: TestOrderTotal (0.01s)
  4  5  === RUN   TestRefund
      ⋮
```

Seven lines new, three gone, out of forty-four. Everything both runs shared is elided to a
`⋮`; `[` and `]` widen and narrow what is kept around each change. The `order_test.go:88` is
underlined, so `e` opens it — see [the edit–check loop](#the-editcheck-loop).

Against the last **green** run rather than the last run at all: diffing two consecutive
failures usually shows only that the timestamps moved.

`h` is the other half — every run in the project rather than one task's:

```
 taskui ▸ history                                   atlas   14 runs   6 failed
✗ 9m ago    task ci                    180ms    14 lines
✓ 42m ago   task backend:check --force  3.3s    17 lines
```

`/` greps every stored run and keeps only the ones that matched:

```
 taskui ▸ history                      atlas   /migrations   4 runs   4 failed
✗ 9m ago    task ci    180ms   14 lines   2 hits
✗ 42m ago   task ci    190ms   14 lines   2 hits
✓ 3h ago    task ci    170ms   12 lines   0 hits   ← last clean run
```

`⏎` opens a matched run with the query already applied. `a` widens to all projects.

From the shell, without opening the TUI:

```
taskui --search 'migration.*pending'
taskui --timeline backend:test         # one run per line, tab-separated
taskui --diff backend:test             # unified-ish, line numbers on the rows
taskui --last                          # reopen the most recent run
```

The archive is plain text, so nothing stops you skipping taskui entirely:

```
rg 'FAIL' ~/.local/state/taskui/runs/*/backend.test.txt
```

---

## A task that asks questions

`wrangler`, `terraform` and anything wrapped in `npx` will stop and ask. Under normal
capture the prompt never appears — go-task's prefixer holds unterminated lines — so the
run looks hung:

```
  …   no output for a while    waiting for input?  i types at it   ⇧I re-runs so you can see it   x stops
```

`i` types at it anyway; stdin reaches the task whether or not you can see the question:

```
  input   keys go to the task   sent: y⏎   buffered: ⏎ sends a newline, output may lag
```

`⇧I` re-runs interleaved so prompts are visible from the start, and `i` in the *picker*
arms that for the next run.

---

## A task that says it is already done

```
✓ backend:check   110ms
      1   task: Task "backend:check" is up to date
```

That is go-task's `sources:` fingerprinting, not an error. `⇧F` arms `--force`; the header
shows `force`, and `⏎` and `r` both ignore the cache until you turn it off.

---

## The edit–check loop

Run `check`, then `⇧W`:

```
 taskui ▸ task check                         watching check    RUNNING    1.2s
```

Save a file and it re-runs. Build output, `.git`, `.task`, `node_modules` and editor
scratch files are ignored, changes are debounced so one save is one run, and a save during
a run is skipped rather than stacking.

The other half of the loop is `e`. Any `file:line` in captured output is underlined, and `e`
opens it in `$EDITOR` at that line:

```
 ▾ ✗ test                                                              780ms
    3   --- FAIL: TestOrderTotal
    4       order_test.go:88: want 1200, got 1180     ← e opens this
```

```
 opening backend/order_test.go:88 in nvim
```

`go test` prints a bare basename relative to its *package* directory — not the directory the
run started in, and not named anywhere in the line. taskui indexes the project the first
time you press `e` and finds it. Two files with the same name resolve to the shallower one
and the status line says it had to guess.

Pressing `e` on a line that names no file — a `--- FAIL:` header, say — falls back to the
first location the task printed and says where it came from, so it is never a dead
keystroke:

```
 opening backend/order_test.go:88 in nvim (from line 4 of `backend:test`)
```

A terminal editor takes over the terminal and taskui redraws when you quit it; `code`,
`zed`, `subl` and the JetBrains tools are handed the file and left to their own window.

---

## Not running the wrong thing

Put the tasks that must not fire from a fuzzy filter in `.taskui-danger`:

```
deploy:*
infra:deploy
backend:migrate:prod
app:publish
dev:reset
```

`*` matches any characters, `#` starts a comment. Those tasks get a `⚠`, and `⏎` stops:

```
  ⚠   run task infra:deploy  —  this one touches production.  y to run, anything else cancels
```

The file's presence switches off the built-in guess entirely — once the list is written
down, the list is the answer.

---

## Checking that an aggregate covers what it says

`task test` says "Run all automated tests". Whether that is true is a fact about the graph,
not about the sentence:

```
$ taskui --lint
test — Run all automated tests
  ✗ web:test                     declared, never reached

1 gap, 0 notes
```

`web:test` exists and nothing reaches it, so those tests run nowhere — in the local gate or
in CI — and the description reads as correct until you go looking.

Most of what a first run reports is deliberate. Write those down in `.taskui-cover`, same
shape as `.taskui-danger`, with the reason beside each:

```
# the deploy pipeline runs from CI with production credentials
deploy:*
# the site has its own workflow
site:build
```

A `·` rather than a `✗` means covered by a different aggregate — `api:check` reached by
`lint` and not by `check`, which is deliberate where `check` promises not to codegen. Those
are printed and not counted. Gaps exit `2`, so it composes as a gate:

```
taskui --lint || echo "an aggregate is claiming more than it runs"
```

`--matrix` prints the whole grid instead of only the disagreements, which is the table a
Taskfile organised by `includes:` tends to carry as a comment above its aggregates:

```
$ taskui --lint --matrix
       api  app  backend  site  infra
build   ✗    ✓      ✓      ✓     —
check   ·    ✓      ✓      ✓     ✓
test    —    ✓      ✓      —     —

✓ reached   · covered by another aggregate   ✗ never reached   ~ exempt   — not declared
```

Point the comment at the command and it stops drifting.

---

## Making it readable in your terminal

If the defaults wash out — embedded terminals often render the dim half of the palette
close to the background:

```
mkdir -p ~/.config/taskui && cp config.high-contrast.yaml ~/.config/taskui/config.yaml
```

Or tune individual things. `taskui --dump-config` prints every colour and every key at its
default, each with a comment:

```yaml
colors:
  dim: bright-white
  status-failed: "#ff7b72"
  selection: default        # reverse video; cannot be invisible

keys:
  pivot: z
  filter-matches: m
```

Colours take an ANSI name, a `#rrggbb`, or a 0–255 palette index. Keys take a single
character; the same action keeps its meaning on every screen that offers it.

---

## Driving it from scripts

Every screen can be rendered without a terminal, which is how this file's screenshots were
produced:

```
taskui --list                          # discovered tasks
taskui --dump verb                     # a pivot, fully expanded
taskui --graph all                     # the execution graph
taskui --run ci                        # run headlessly, print the captured tree
taskui --run ci --args '-- -p ingest'
taskui --timeline backend:test         # one task's runs, one per line
taskui --diff backend:test             # what changed since it last passed
taskui --screenshot 120x30 --keys 'p'  # render one frame to stdout
```

`--timeline` is tab-separated, so the trend is one `awk` away:

```
taskui --timeline backend:test | awk -F'\t' '$2=="failed"' | head -1
```

`--screenshot` with `--run` plays the keys into a *live* run, so interactive flows can be
driven end to end:

```
taskui --run deploy --screenshot 100x20 --keys $'iy\n'
```

### Feeding an editor

`--quickfix` prints the last run's failures as absolute `file:line:col: message`, which is
the form every editor already walks:

```vim
set errorformat=%f:%l:%c:\ %m
:cexpr system('taskui --quickfix')          " the run you just watched fail
:cexpr system('taskui --run backend:test --quickfix')   " run it and populate in one go
```

Absolute, because that is the part nothing else can do: `go test` prints `order_test.go:88`
relative to the package it ran in, and an editor resolving that against its own working
directory opens nothing. taskui knows which task printed the line and where that task ran.
References it could only guess at — a bare name that exists twice in the tree — are left out
rather than sent as a guess you would follow without looking.

### Feeding a program

`--json` is the machine-readable form of three flags that already existed:

```
taskui --list --json                   # the tasks, with descriptions and last outcomes
taskui --timeline backend:test --json  # one task's stored runs
taskui --run ci --json                 # the run as it happens, one event per line
```

The run form is newline-delimited JSON, and it is a stream: a `run` object, then `graph`,
then `task` and `line` events as they happen, then `exit`. A task is announced before its
output and its verdict comes after it, line indices count from the start of the run, and the
process exits with the task's own exit code — so a front end elsewhere can draw the same
tree the TUI draws without reimplementing any of the engine.

```json
{"type":"run","root":"ci","dir":"/src/acme","started_unix":1787983902}
{"type":"graph","edges":{"ci":["build","test"],"build":[],"test":[]}}
{"type":"task","name":"build","status":"Running"}
{"type":"line","task":"build","index":0,"text":"go build ./...","command":true}
{"type":"task","name":"build","status":"Ok","duration_ms":314}
{"type":"exit","code":1,"duration_ms":2100,"saved":"~/.local/state/taskui/runs/…"}
```
