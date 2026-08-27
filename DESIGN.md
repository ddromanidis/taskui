# Design notes

Why taskui works the way it does. The [README](README.md) covers what it is and how to
drive it; this is the reasoning underneath, kept because the decisions here were the
expensive part and a decision without its reason is just a constraint.


## Notes on the design

A node can be **both a group and a task**. `backend:migrate` applies migrations *and*
parents `backend:migrate:down`, `:prod`, `:status`, `:schema`. Collapsing those into one
concept loses one of them, so `Node` carries `task` and `children` independently.

This is why `space` and `⏎` are kept strictly separate rather than one key that folds
groups and runs leaves. `⏎` on `▸ migrate  5` runs it from its own header, so the subtree
never has to relist the task just to make it reachable.

**Domain and file are not the same thing.** In atlas, `sec:*` and `wt:*` are namespaced
inline in the root `Taskfile.yml` rather than included from their own files, so "which
namespace is this in" and "where do I go to edit it" have different answers. A file pivot
is a few lines away — `location.taskfile` is already parsed — but it is not wired up yet.

**`*:default` tasks are dropped.** In a UI where a namespace is itself a selectable row, a
task whose only job is "show available tasks" is noise.

**Production tasks are flagged `⚠` and need a confirmation.** `⏎` runs things for real and
a fuzzy filter puts every task one keypress away, so the dangerous ones stop and ask:

```
  ⚠   run task infra:deploy  —  this one touches production.  y to run, anything else cancels
```

Which tasks those are comes from a `.taskui-danger` file in the project root: one pattern
per line, `*` matches any characters, `#` comments. Its presence switches off the
description heuristic **entirely** — a guess and a declaration disagreeing about which
tasks are dangerous is worse than either alone, so once the list is written down, the list
is the answer.

With no such file, the old heuristic over names and descriptions applies, which both over-
and under-matches. atlas's list flags 18 of 122 tasks; the heuristic caught
`infra:deploy:plan` too, which is explicitly read-only.

**Filtering is fuzzy over the full colon path**, so `blint` finds `backend:lint`. It will
also occasionally surprise you — `lint` matches `api:gen:client` — the price of fuzzy
matching. Matches keep tree order rather than score order, because the tree *is* the
organisation and resorting it by score would destroy the grouping you are looking at.

## Runs

`⏎` starts `task <name>` and switches to the run view: the execution tree, each task's
status and duration, and **a window on the last few lines it printed**.

```
 taskui ▸ task all                                  FAILED    12.4s   exit 201
 ─────────────────────────────────────────────────────────────────────────────
   ✗ all                                                              12.40s
     ✓ lint                                                            3.40s
   ▿ ✓ api:check                                             29 more     1.10s
     30   checked 41 files
     31   no issues found
     ▸ ✓ app:lint                                           12 lines     0.80s
▌  ▾ ✗ backend:test                                                    7.10s
      1 ❯ cargo test --workspace
      2   --- FAIL: TestOrderTotal (0.00s)
      3       order_test.go:88: want 1200, got 1180
```

Every task carries its own clock, ticking while it runs rather than appearing only once it
finishes — during a slow build the useful question is *which step* is slow, and the total
in the header cannot answer it. Durations are formatted to be read at a glance: `4ms`,
`1.5s`, `2m14s`, rather than `0.00s` and `134.20s`.

Output lines are rows of the same list as the tasks that produced them, which is what lets
one fold tree hold both.

### The peek window

Folded used to mean empty, and empty is not an answer. A run of 26 nodes collapsed to 26
rows tells you what ran and how long it took, and to find out whether any of it is worth
reading you open them one at a time and guess. So `space`/`o` walks **three** states rather
than two, and the resting one shows something:

| glyph | state | what you get |
| --- | --- | --- |
| `▸` | hidden | the task, its status, its duration, `34 lines` |
| `▿` | **peek** | the last 5 lines, one row each, and `29 more` |
| `▾` | full | all of it, wrapped |

The peek is a **window on the end** of the buffer, not the start. A command's output puts
its verdict last — the assertion, the stack frame, the "3 migrations pending" — and a task
still going has its news at the bottom by definition. It tails: what is in the window is
whatever arrived most recently, so a peeking `docker compose logs -f` is a live five-line
view of the stack while you work on something else.

Peeked lines are **cut at the edge rather than wrapped**, which is the trade the window
makes: five lines has to mean five lines, and one 300-character stack frame wrapped into
nine rows would mean showing one of them. Open it fully and wrapping comes back — that is
the mode you read in, and the one where a search hit past column 100 has to be visible.

Five is a default, not a law: `peek-lines:` in `config.yaml` sets it, because how much is
enough is a property of the tools you run.

`⇧O` moves every task at once, along the same cycle. Mixed states go to full first, since
mixed almost always means *I opened two of these and now want the rest*.

Output lines are numbered per task, and in full they wrap rather than truncate. Go's own tools and
anything you run through them emit lines well past 100 columns, and a line cut at the terminal edge can still
*match a search* — a hit you cannot see is worse than no hit at all. Wrapping prefers word
boundaries and hard-splits tokens too long to fit, so a long type signature stays
readable. Continuation rows keep a blank gutter so the numbers stay scannable.

The view follows whatever is running, and the moment something fails it unfolds that task
and parks there. Any deliberate cursor move turns following off — once you have gone
looking for something, the view should stop moving under you. `w` turns it back on.

Following moves the cursor **and nothing else**. It does not open what is running: every
task already rests on a window of its own last few lines, so the live one is showing its
latest output wherever it sits, and expanding it would push the twenty-five windows around
it off the screen — which is the whole reason to watch a run instead of tailing a log. A
fold you set yourself is yours, and following will not overrule it when the next task
starts.

Reading is likewise not disturbed by output arriving elsewhere. The cursor tracks the line
it is *on*, not the row number it happens to sit at, so a task higher up the tree printing
another hundred lines no longer slides the stack trace you are halfway through out from
under you.

`r` re-runs the task under the cursor, including when the cursor is on one of its output
lines. Note that this is a fresh `task <name>` and not a resume of the parent pipeline
from that point: go-task exposes no resume, and pretending otherwise would be a lie.

`⇧R` is the same re-run with `--force`. Plain `r` inherits the flags the run was started
with, so a task go-task considers up to date declines to run and hands back a tick that
proves nothing — and the picker's `F` is behind an `esc`, away from the output you are
working against. `⇧R` is what you wanted the second time you pressed `r`. The override only
adds: `r` on a run that was already forced stays forced.

`esc` leaves the run view without stopping the run — it carries on, and the picker header
says so. `x` stops it.

Stopping means signalling the **process group**, not the `task` process. Killing `task`
alone leaves the shell commands beneath it running: verified by killing taskui mid-run and
watching `sleep` carry on afterwards. The pty puts the child in its own session, so the
group can be signalled at once, which is what actually reaps the tree. The same happens
when a `Run` is dropped, and `q` walks every open run on the way out — the focused one and
every parked one — so quitting cannot silently orphan a container. Because that reaches
runs you are not looking at, `q` asks first whenever anything is still going.

The first signal is a **SIGTERM**, deliberately, because it can be caught: `docker compose
up` reads it as "take the containers down", and skipping it would leave the stack up while
taskui reported it stopped.

Which is also the problem. Things that catch SIGTERM can decline to act on it — a `trap`
in a shell script, a runtime blocked on a call that is never going to return — and one of
them outliving the run it belonged to is exactly the orphan this is all here to prevent.
So there are two answers:

- **A second `x`** sends **SIGKILL**, which cannot be caught. The run view says so while
  it is stopping rather than making you guess whether the first press landed.
- **Anything left in the group** a moment after go-task itself has gone is taken with
  SIGKILL regardless. This has to happen where it does — in the capture thread, between
  the output ending and the child being waited on — because waiting on the child releases
  its pid, and that pid is the group's only name. A moment later it names somebody else.

That second one applies only to a run you *stopped*. A run that ended on its own may have
left something behind entirely on purpose — `docker compose up -d` is a task whose whole
job is to outlive itself — and killing that would be tearing down a stack as a reward for
succeeding.

The one case nothing can cover is taskui being killed from outside — `kill -9` runs no
destructors, in any program — so an externally-killed taskui does still leave its build
running.

Two pieces of machinery make this work, and neither is obvious.

**Which task produced this line** comes from `--output prefixed`, which tags every output
line at the source. See below for why the alternatives do not work.

**Who called whom** comes from `task --summary`, recursed from the invoked root. The
`--list-all --json` output carries no dependencies, so the alternative was parsing seven
Taskfiles and reimplementing `includes:` resolution. `--summary` reports a task's direct
edges using go-task's own resolver, which costs one process per node — about 1.3s for
atlas's 26-node `all` — and cannot drift from what go-task actually does. It runs on a
worker thread, so the tree appears greyed-out immediately rather than freezing the UI.

A task reached by two paths is one node in the graph and gets one row: atlas's `app:css`
is invoked by both `check` and `build`, and showing it twice would imply it ran twice.

### Long-running tasks

Tasks that do not end are fine here — `docker compose up`, `docker compose logs -f`, a dev
server, a `tail -f`. Leave one up and keep working: **runs live in slots, one per task
name, and starting a different task no longer disturbs the one already going.**

```
 taskui ▸ task test                                            RUNNING    2.1s
  1 ▶ logs 41m12s   2 ▶ test 2.1s   3 ✓ lint 3.4s
 ─────────────────────────────────────────────────────────────────────────────
```

The bar appears once there is a second run to switch to. `⇥` and `⇧⇥` cycle, `1`…`9` jump
straight to a slot, and each slot keeps its own scroll position, folds and follow state —
coming back to a stack you left up an hour ago should not mean coming back to the top of
its log. Six slots; a finished one is recycled automatically when you need a seventh, a
live one never is.

Everything keeps draining while it is parked, so nothing is lost while you are looking
somewhere else, and a background run archives itself the moment it ends — the status line
says so rather than making you go and check. From the picker, the header counts what is
still going, and names it when there is only one.

**And the rows themselves say what is running.** A task with a live slot shows `▶` and a
ticking elapsed time in place of its outcome column — the same reading as the slot bar's, so
the two agree at a glance. Previously that column only ever answered *how did it go last
time*, which meant a task running this second looked exactly like one that ran yesterday:
the footer could say `3 running` while every row in front of you showed nothing but history.
Running now displaces the historical mark rather than sitting beside it, because it is the
more urgent of the two questions; once the run ends the row goes back to reporting how it
went.

**`v` goes to what is running**, not to whichever slot you left last. If the slot on screen
is already live it stays put; otherwise it takes the earliest-started live one, so pressing
`v` twice lands in the same place rather than hopping between two running tasks. With
nothing live it falls back to the last run — *what just happened* being the useful answer
when there is nothing to watch.

**Stopping one you are not looking at** does not mean going to look at it first. `x` in the
picker stops whichever slot holds the task under the cursor — from there nothing is on
screen, so every run is a background run, and loading a 20,000-line buffer to press one key
is not a reasonable toll. `⇧K` stops every slot at once without leaving taskui. Both ask
before taking down more than the run in front of you.

Output is capped at 20,000 lines per task, dropped in blocks from the oldest end and
counted, so the row says `12000 earlier dropped` rather than quietly presenting a
truncated log as the whole thing. Without the cap a tailed container log is a few hundred
bytes a line for as long as you leave it up, which is gigabytes by the afternoon.

Two things are worth knowing. A run that never ends is never archived — stopping it with
`x` writes it out, but until then it exists only in memory. And there is still no detach:
`q` stops every slot, because a container left behind by a tool that has exited is exactly
the orphan the process-group handling above exists to prevent. Quitting therefore waits for
those groups to actually be gone rather than signalling them and walking away, which is a
second or so with a stack up.

For **running tasks against a stack you have up**, split the finite part from the infinite
one so go-task can still order them:

```yaml
tasks:
  up:    { cmds: ["docker compose up -d --wait"] }   # exits when healthy — deps-able
  logs:  { cmds: ["docker compose logs -f"] }        # the one that owns a slot
  test:  { deps: [up], cmds: ["docker compose exec -T app pytest"] }
```

`deps: [up]` can never work against a foreground `docker compose up`, because it never
returns; `--wait` makes readiness go-task's problem, which it is good at. The `-T` matters
for the reason described under [Why `--output prefixed`](#why---output-prefixed): every
command runs behind go-task's prefixing pipe, so `exec` without `-T` refuses with "the
input device is not a TTY".

## Searching output

`/` searches what the tasks printed. Note this is a *different* search from `/` in the
picker, which fuzzy-matches 122 short task names; this one runs a regex over potentially
megabytes of output, so the two get separate affordances despite the shared key.

There are two jobs and two keys, because "take me to the next one" and "hide everything
that is not one" are different things.

`/` is jump-to-match. `n` and `N` step through hits in execution order — the order the run
happened, not alphabetical — opening whatever fold is hiding each one.

`f` is the filter. Pressed with nothing running it opens its own prompt and narrows the
run live as you type; pressed while a query is active it toggles the filtered view on and
off, so you can search first and convert afterwards if that is how you got there. Either
way the run collapses to just the matching lines, kept under the tasks that produced them,
with tasks that have no hits dropped entirely:

```
 taskui ▸ task ci      /pending  1/2  filtered ±2    FAILED    0.2s   exit 201
 ─────────────────────────────────────────────────────────────────────────────
   ▾ ✗ test                                                            0.03s
      8   3 migrations pending, refusing to start
 ─────────────────────────────────────────────────────────────────────────────
 filter: pending█   2 matches in 1 task   ⏎ keep   esc clear
```

Backspacing to an empty pattern shows the whole run again without leaving filter mode, so
you can widen and re-narrow without starting over.

Matching is smart-case, as in ripgrep: `fail` finds `FAIL`, `FAIL` does not drag in
`fail`. Patterns are full regexes, and a half-typed one reports quietly rather than
clearing your results — incremental search spends most of its time on invalid input.

Search runs over the ANSI-stripped text, never the raw bytes. Searching the raw bytes
would miss a match wherever a colour change lands mid-word — `\x1b[31merr\x1b[0mor` does
not contain `error` — which is a bug you would never think to look for.

## Stored runs

Every finished run is written to `$XDG_STATE_HOME/taskui/runs/` (or
`~/.local/state/taskui/runs/`), last 50 kept. The format is deliberately boring: a
`manifest.json` for the structure, plus `<task>.txt` (stripped, searchable) and
`<task>.ansi` (colour intact) per task. Plain `grep` works on it, which is the point — a
format that needs taskui to read it would be a worse format.

`taskui --search PATTERN` greps the lot, newest first, grouped by run and task:

```
✗ 1787688604-ci  task ci  (2 hits)
  test
        8  3 migrations pending, refusing to start

✗ 1787688594-ci  task ci  (2 hits)
  test
        8  3 migrations pending, refusing to start
```

That answers "when did this start failing", which is the question you cannot ask today.

### Redaction

`task --summary` prints the resolved environment, so for a Taskfile with `dotenv:` it
prints real credentials — against atlas's `lint` it emits live Cloudflare and Google
values. taskui runs that command to build the graph, and it stores run output on disk, so
without care it would write your secrets into `~/.local/state/`.

Redaction happens in the capture thread, before a line reaches the UI, the buffers or the
disk — nothing unmasked is ever put on the channel, so no later code path can leak what it
never received. The secret list is harvested from that same `--summary` env dump: the leak
is also the best available list of what to plug. Run directories are `0700` and files
`0600` regardless.

The masking rule is deliberately conservative. A value is masked when its variable name
looks like a credential (`*_TOKEN`, `*_SECRET`, `*_KEY`, `*_PASSWORD`, …) or the value
carries a known credential prefix (`cfut_`, `ghp_`, `sk-`, `AKIA`, `-----BEGIN`) — and
only when it is at least 8 characters and not a boolean. Masking `LOG_REDACT_PII=false`
would replace the word `false` everywhere it legitimately appears, which is worse than the
problem. The run view reports how many secrets it masked, so zero reads as *zero found*
rather than as *checked and clean*.

### History

`h` lists what has already run, newest first, **scoped to the current project** — the
manifest records which directory a run came from, and one list mixing every repo you have
ever used taskui in stops being useful immediately. `a` widens to all projects.

`/` greps every stored run and keeps only the ones that matched, with hit counts:

```
 taskui ▸ history               all projects   /migrations   4 runs   4 failed
 ─────────────────────────────────────────────────────────────────────────────
▌✗ 9m ago    task ci                      0.16s      14 lines   2 hits
 ✗ 42m ago   task ci                      0.18s      14 lines   2 hits
```

That is the "when did this start failing" question. `⏎` opens a matched run with the same
query already applied, so you land on the thing you were looking for.

```
 taskui ▸ history                                    atlas   3 runs   2 failed
 ─────────────────────────────────────────────────────────────────────────────
▌✓ 3m ago    task leak                    0.10s       4 lines
 ✗ 5m ago    task ci                      0.18s      14 lines
 ✗ 5m ago    task ci                      0.19s      14 lines
```

`⏎` reopens one into the ordinary run view — same folding, same search — because it *is*
the same structure, read back off disk rather than off a pty. It opens on the failure,
since that is nearly always why you went looking. Colour survives the round trip: the
`.txt` half is what search matches on, the `.ansi` half is what renders.

`is_command` is not stored, because a marker in the `.txt` file would make the archive
worse to grep. It is recomputed on load from the shape of go-task's own echo line.

## Acting on what you found

The three screens above stop one step short of what you actually want. They get you to
"`test` failed and here is what it said"; what you wanted was to be in the file. These
close that gap.

### Locations

Every compiler, test runner and linter prints `file:line`. Finding them is a regular
expression; the decision worth writing down is that **the extension is required**.

Without it, every duration (`12:34`), timestamp (`10:30:45`) and port (`localhost:8080`) in
a build log becomes a candidate. A highlight that lands on a third of the numbers on screen
is a highlight you learn to ignore, and then the feature has negative value: it has taught
you to distrust a signal that is usually right. With the extension required, a false
positive has to look like a filename, which in practice means it is one.

Whether the file *exists* is a much better filter still, and it is deliberately not used
for the highlight. That check runs on every visible row of every frame — sixty stats a
frame, twenty frames a second — to answer a question that only matters once, when a key is
actually pressed. So the renderer decides syntactically and the jump decides for real.

Resolving is the part with the interesting failure. Go's own test output is:

```
--- FAIL: TestWrap (0.00s)
    view_test.go:88: want 3, got 4
```

`view_test.go` is relative to the *package* directory. That is not the directory the run
started in, and nothing in the line says which package it was. Resolving it against the
project root fails, and failing there would make every Go test failure — the single most
common thing this feature exists for — unreachable.

So a path that does not resolve directly is looked up by basename in an index of the
project, built on first use and never for a session that does not press `e`. Two files
sharing a name resolve to the shallower, and Resolve reports that it had to guess so the
status line can say so. Silently opening the wrong file is the one outcome worse than
opening nothing.

The editor argv is per-editor rather than a bare `$EDITOR file`, because an editor that
opens at line 1 when the output said line 212 has done the tedious half of the job. An
unrecognised editor gets the file and no line number: `+N` is close to universal among
terminal editors, and "close to" is not good enough when being wrong means the editor reads
it as a second filename and creates it.

Terminal editors get the terminal handed over and the UI redraws afterwards; windowed ones
are started alongside. Handing the terminal to a `code --goto` that returns in ten
milliseconds blacks the screen out for no reason, and on a slow start it reads as a crash.

### The timeline

`--search` greps a string across every stored run. The history list is every run in order.
Neither of them is *"how has `test` been going"*, and that is the question you actually have
when something that used to work does not. The manifests have held the answer since the
archive existed; nothing asked them for it.

A timeline is one task's stored appearances, newest first. Two details earn their space:

The **trend** across the header (`✓✓✓✗✗`) reads left to right, which is forwards in time and
therefore the opposite order to the list underneath it. That inconsistency is deliberate —
it is the direction every other sparkline in the world runs, and the shape of *where it
turned* is more use than a count of failures.

The **bar** is scaled to the slowest run in the list rather than to any fixed duration,
because the question is "which of these was slow" and that is a comparison within the list.
A run that took any measurable time gets at least one cell: a bar that rounds to nothing
beside a number that says 40ms is two things on one row disagreeing.

Each row names the run it was part of, because `test` reached from `task all` and `test` on
its own are the same task under different circumstances — and that is the explanation for
most surprising durations.

Building this surfaced a real bug in the archive. Run ids are `<second>-<task>`, and two
runs of the *same* task inside one second collided: the second overwrote the first. That is
exactly the pair a timeline exists to show, so a counter now disambiguates them.

### The diff

When a task fails and it passed yesterday, the useful thing is not the eight hundred lines
it printed. It is the five that are new.

Against the last **green** run rather than the last run at all: "it worked before" is the
comparison that isolates a failure, where diffing two consecutive failures usually shows
only that the timestamps moved. A task that has never passed falls back to the previous run
— that is the honest second choice rather than an error — and the header names which of the
two comparisons it made, because a diff you have mistaken for the other one is worse than no
diff.

The algorithm is Myers, which is O(ND): fast exactly when the two sides are similar, which
is the case worth being fast for. Common prefix and suffix are stripped first, and that is
not only an optimisation — build logs are mostly identical run to run, so trimming turns
almost every real input into the cheap case. Past a bounded edit distance there is no useful
alignment left to find, and the fallback says so by showing both sides in full rather than
producing a plausible-looking alignment of two unrelated logs.

Shared stretches are elided to a `⋮`, because the entire value of the view is that it is
short. A diff of two 800-line logs differing in five places is 800 rows of which 790 are
noise.

A stored run being diffed has to skip *itself* in the archive: it is in there, it is the
newest, and a diff of a thing against itself is a diff of nothing — which you would find out
by producing it.

## Not built yet

- **A binary-based formula.** Releases now carry prebuilt binaries, but the tap still
  compiles from source. Pointing the formula at those archives per platform would make
  `brew install` instant instead of a minute.
- **A Homebrew tap of prebuilt bottles.** The release builds Linux and macOS binaries for
  both architectures already; a tap that pours them would beat building from source.
- A file pivot. `location.taskfile` is already parsed and would answer "where do I edit
  this", which the domain tree gets wrong for `sec:*` and `wt:*`.
- An explicit production marker in the Taskfile to replace the `⚠` heuristic.
- Diffing two runs of a task that are not adjacent — the timeline can only compare a run
  with the one below it, and "against the run from before the refactor" needs a mark.
- Normalising timestamps and durations out of a diff. Today they show as changes, because
  they are; a log where every line carries a timestamp diffs as entirely new.
- Detaching a slot, so a stack survives quitting taskui. Today `q` stops every run, which
  is the safe default but not always the one you want.
- Incremental archiving, so a run that never ends still leaves something on disk before
  you stop it.

### Interactive tasks

`wrangler`, `terraform` and friends ask questions. Under `--output prefixed` such a task
**hangs with a blank screen**: go-task's prefixer is itself line-based, so a
`Proceed? (y/n) ` with no trailing newline is held inside it and never reaches taskui at
all. Measured — the prompt does not appear under `prefixed`, does appear under
`interleaved`.

So `i` in the picker arms interactive mode for the next run, which goes out with
`--output interleaved`. The prompt then surfaces, and `i` in the run view forwards
keystrokes to the task's terminal — `y`, `⏎`, arrows, `^C`, `^D` — with `esc` to stop
typing.

`i` works on an *ordinary* run too. go-task wraps stdout and stderr for prefixing but
leaves stdin alone, so keystrokes reach the child either way — verified against a real
`task` process. You may not see the question, but `y⏎` still answers it, which beats
restarting a half-finished deploy. `⇧I` is the deliberate restart for when seeing the
prompt matters more.

Because a buffered run can stay silent for a long time after you answer, the input bar
echoes what it sent:

```
  input   keys go to the task   sent: y⏎   buffered: ⏎ sends a newline, output may lag
```

Without that receipt, "I typed y and nothing happened" is indistinguishable from "y never
left the building". A write that fails says so instead of looking identical to one that
landed.

The cost is attribution. Interleaved output still carries go-task's `task: [name] <cmd>`
announcements, so lines are attributed to whichever task last spoke: correct for a
sequential run, wrong under parallel `deps:`. Interactive tasks are inherently sequential,
so the trade is worth making — but only when asked for, which is why it is a toggle rather
than the default.

Two things make this discoverable rather than something you have to know. A task sitting
on an unterminated line gets a `?` bar quoting the question. And a *non-interactive* run
that has produced nothing for fifteen seconds gets a warning — under `prefixed` a blocked
task emits literally nothing, so silence is the only signal that exists:

```
  …   no output for a while    if it is waiting for input: x to stop, i to re-run interactively
```

`i` on such a run re-runs it interactively rather than pretending to send keystrokes into
a void.

### Why `--output prefixed`

Measured against go-task 3.53.1. Every output line self-identifies:

```
task: [a] echo "a start"
[a] a start
[b] b start
```

That is per-line attribution with no buffering, and it survives parallel `deps:`, where
`interleaved` and `group` both emit unattributed output lines whose order tells you
nothing about which task produced them.

`group` was the obvious candidate — its `begin`/`end` templates look like fold markers —
but plain `output: group` emits no markers at all, and adding them only marks task
*completion*, which is too late to build a live tree from. It also does not buffer on this
version, contrary to
[go-task#937](https://github.com/go-task/task/issues/937); that issue still reads as open
but does not reproduce here, so the argument against `group` is attribution, not latency.

Colour is the cost, and the fix is not the one you would guess. Prefixed mode makes
go-task pipe every command through its own prefixing writer, so a command's stdout is a
pipe no matter what taskui does — measured, `isatty` reports false inside prefixed mode
even with go-task itself on a pty. Tools that auto-detect turn colour off and no pty gets
it back. Forcing by environment does, so the capture sets `CARGO_TERM_COLOR=always`,
`CLICOLOR_FORCE=1` and `FORCE_COLOR=1`, which restores cargo and clippy's colour through
the pipe intact.

The pty is still worth keeping — go-task's own output stays coloured, and it avoids the
usual switch to block buffering when stdout is not a terminal — it just is not what makes
the tools colour.
