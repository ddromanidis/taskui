# Examples

Worked examples, in the order you are likely to need them. Every screenshot here is real
output, not a mock-up.

---

## Finding a task in a Taskfile you did not write

A repo with 122 tasks across nine namespaces. Opening it gives you the shape, not the list:

```
taskui · atlas  group: domain   122 tasks
▸ (root)  12
▸ api     13
▸ app     17
▸ backend 26
▸ deploy   9  ⚠
▸ dev     11
▸ infra   11  ⚠
▸ sec      4
▸ site    14
▸ wt       4
```

Three ways in, and they answer different questions.

**`/` filters** — narrows the list to what matches, hiding everything else. Use it when you
want to see *all* the linting tasks:

```
taskui · atlas  group: domain   filter: lint   6/122 tasks
▾ (root)  1
    lint            Lint all source code
▾ api  2
    lint            Validate the spec
▾ backend  1
    lint            Rust lints (clippy warnings are errors) + format check
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
taskui · wt:new

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

`⏎` on `ci`. The view follows the run, and the moment something fails it unfolds that task
and stops there:

```
taskui · ✗ task ci   0.2s   exit 201
  ✗ ci                    2.0s
    ▿ ✓ build             1.2s
         3 │ compiling api
         4 │ built in 1.2s
    ▾ ✗ test              780ms
         1 $ task: [test] go test ./...
         2 │ running 42 tests
         3 │ --- FAIL: TestOrderTotal
         4 │     order_test.go:88: want 1200, got 1180
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
taskui · ✗ task ci   0.2s   exit 201   /pending  1/2  filtered ±2
▾ ✗ test   780ms
  │ 3 migrations pending, refusing to start
```

`[` and `]` widen and narrow the context around each hit — `--- FAIL:` on its own hides
the assertion underneath it, which is the half that says what broke.

`y` copies the line under the cursor, `⇧Y` the whole task's output.

---

## "When did this start failing?"

Every finished run is stored. `h` lists them, scoped to this project:

```
taskui · history · atlas   14 runs   6 failed
✗ 9m ago    task ci                    180ms    14 lines
✓ 42m ago   task backend:check --force  3.3s    17 lines
```

`/` greps every stored run and keeps only the ones that matched:

```
taskui · history · atlas   /migrations   4 runs   4 failed
✗ 9m ago    task ci    180ms   14 lines   2 hits
✗ 42m ago   task ci    190ms   14 lines   2 hits
✓ 3h ago    task ci    170ms   12 lines   0 hits   ← last clean run
```

`⏎` opens a matched run with the query already applied. `a` widens to all projects.

From the shell, without opening the TUI:

```
taskui --search 'migration.*pending'
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
  │ task: Task "backend:check" is up to date
```

That is go-task's `sources:` fingerprinting, not an error. `⇧F` arms `--force`; the header
shows `force`, and `⏎` and `r` both ignore the cache until you turn it off.

---

## The edit–check loop

Run `check`, then `⇧W`:

```
taskui · ▶ task check   1.2s   watching check
```

Save a file and it re-runs. Build output, `.git`, `.task`, `node_modules` and editor
scratch files are ignored, changes are debounced so one save is one run, and a save during
a run is skipped rather than stacking.

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
taskui --screenshot 120x30 --keys 'p'  # render one frame to stdout
```

`--screenshot` with `--run` plays the keys into a *live* run, so interactive flows can be
driven end to end:

```
taskui --run deploy --screenshot 100x20 --keys $'iy\n'
```
