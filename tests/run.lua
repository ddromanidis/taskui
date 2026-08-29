-- tests/run.lua
--
-- Integration tests for the Neovim side, run headlessly and with no plugin
-- dependencies:
--
--   task test:nvim
--   TASKUI_BIN=./bin/taskui nvim --headless -l tests/run.lua
--
-- They drive the real :TaskUI against the real binary over a Taskfile written
-- into a temporary directory, so they cover what unit tests cannot: the NDJSON
-- stream arriving in chunks, the buffer the panel renders into, the keymaps,
-- and the quickfix list that comes out the other end.

local root = vim.fn.fnamemodify(debug.getinfo(1, "S").source:sub(2), ":p:h:h")
local binary = vim.env.TASKUI_BIN or "taskui"

if vim.fn.executable(binary) == 0 then
  io.stderr:write(("taskui binary %q not found; run `task build` first\n"):format(binary))
  vim.cmd("cquit 1")
end
if vim.fn.executable("task") == 0 then
  io.stderr:write("go-task is not on PATH; the plugin has nothing to drive\n")
  vim.cmd("cquit 1")
end

vim.opt.runtimepath:prepend(root)
dofile(root .. "/plugin/taskui.lua")

-- A project of its own, so the tests never run this repository's Taskfile and
-- never write into anybody's real archive.
local project = vim.fn.tempname()
vim.fn.mkdir(project, "p")
vim.env.XDG_STATE_HOME = vim.fn.tempname()
vim.fn.writefile({
  "version: \"3\"",
  "tasks:",
  "  build:",
  "    desc: Compile it",
  "    cmds: ['echo \"compiling core\"', 'echo \"compiling api\"']",
  "  test:",
  "    desc: Run the suite",
  "    cmds:",
  "      - 'echo \"running 42 tests\"'",
  "      - 'echo \"    order_test.go:88:12: want 1200, got 1180\"'",
  "      - 'exit 1'",
  "  ci:",
  "    desc: Everything",
  "    cmds: [{task: build}, {task: test}]",
}, project .. "/Taskfile.yml")
vim.fn.writefile({ "package p" }, project .. "/order_test.go")

require("taskui").setup({ binary = binary, project = project, notify = false, quickfix = "never" })

local panel = require("taskui.panel")
local state = require("taskui.state")

local failures = 0

local function check(name, fn)
  local ok, err = pcall(fn)
  if ok then
    io.write("ok   - " .. name .. "\n")
  else
    failures = failures + 1
    io.write("FAIL - " .. name .. "\n       " .. tostring(err) .. "\n")
  end
end

local function assert_contains(haystack, needle)
  if not haystack:find(needle, 1, true) then
    error(("expected to find %q in:\n%s"):format(needle, haystack), 2)
  end
end

local function assert_not_contains(haystack, needle)
  if haystack:find(needle, 1, true) then
    error(("did not expect to find %q in:\n%s"):format(needle, haystack), 2)
  end
end

--- The panel as text, which is what a person would be looking at.
local function screen()
  return table.concat(vim.api.nvim_buf_get_lines(panel.buf, 0, -1, false), "\n")
end

--- Puts the cursor on the first row a predicate accepts, and returns the row.
local function cursor_on(predicate)
  for at, row in ipairs(panel.rows) do
    if predicate(row) then
      vim.api.nvim_win_set_cursor(panel.win, { at, 0 })
      return row
    end
  end
  error("no row matched", 2)
end

local function wait_for(what, predicate, ms)
  local ok = vim.wait(ms or 15000, predicate, 30)
  if not ok then
    error(("timed out waiting for %s; panel:\n%s"):format(what, screen()), 2)
  end
end

--- Waits for something to appear on screen rather than in the state. The panel
--- redraws a tick behind the events — one draw per burst rather than one per
--- line — so a test that read the buffer the moment the state changed would be
--- reading the frame before.
local function wait_on_screen(text)
  local ok = vim.wait(5000, function()
    return screen():find(text, 1, true) ~= nil
  end, 30)
  if not ok then
    error(("%q never appeared; panel:\n%s"):format(text, screen()), 2)
  end
end

local function press(keys)
  vim.api.nvim_feedkeys(vim.api.nvim_replace_termcodes(keys, true, false, true), "x", false)
end

-- --- the listing -------------------------------------------------------------

vim.cmd("TaskUI")
wait_for("the task listing", function()
  return #state.tasks > 0
end)

check("the panel lists the project's tasks", function()
  local out = screen()
  for _, want in ipairs({ "build", "test", "ci", "Compile it" }) do
    assert_contains(out, want)
  end
end)

check("the listing carries where each task is written", function()
  local task = state.task("build")
  if not task or not task.taskfile or not task.line then
    error("no location on " .. vim.inspect(task))
  end
end)

-- --- running -----------------------------------------------------------------

check("enter runs the task under the cursor and its output lands under it", function()
  cursor_on(function(row)
    return row.kind == "task" and row.task and row.task.name == "ci"
  end)
  press("<CR>")

  wait_for("the run to finish", function()
    local run = state.runs["ci"]
    return run and run.status ~= "running"
  end)
  -- The last thing the run prints is the last thing drawn, so waiting for it
  -- is waiting for the final frame rather than for a frame.
  wait_on_screen("✗ ❯ exit 1")

  local out = screen()
  -- The execution tree, under the row it was started from.
  assert_contains(out, "build")
  assert_contains(out, "compiling core")
  -- Commands carry the verdict of the step they announce, and without the
  -- `task: [build] ` the capture tagged them with.
  assert_contains(out, "✓ ❯ echo \"compiling core\"")
  assert_contains(out, "✗ ❯ exit 1")
  assert_not_contains(out, "task: [build]")
  -- And output hangs off the command that printed it.
  assert_contains(out, "└   compiling core")
  -- A peek is a window on the end: `running 42 tests` is the first of test's
  -- seven lines and is out of a five-line window, which is the trade.
  assert_contains(out, "order_test.go:88:12")
end)

check("the run knows how it went", function()
  local run = state.runs["ci"]
  if run.status ~= "failed" or run.exit == 0 then
    error("run = " .. vim.inspect({ status = run.status, exit = run.exit }))
  end
end)

check("the statusline component says what happened", function()
  assert_contains(require("taskui").status(), "✗ ci")
end)

-- --- folding -----------------------------------------------------------------

check("o walks the block through hidden, peek and full", function()
  cursor_on(function(row)
    return row.kind == "task" and row.task and row.task.name == "ci"
  end)
  local run = state.runs["ci"]

  local peeked = #vim.api.nvim_buf_get_lines(panel.buf, 0, -1, false)
  press("o")
  if state.block_fold(run) ~= state.FULL then
    error("after one press: " .. state.block_fold(run))
  end
  local full = #vim.api.nvim_buf_get_lines(panel.buf, 0, -1, false)
  if full <= peeked then
    error(("full showed %d rows and the peek showed %d"):format(full, peeked))
  end

  press("o")
  if state.block_fold(run) ~= state.HIDDEN then
    error("after two: " .. state.block_fold(run))
  end
  assert_not_contains(screen(), "compiling core")

  press("o")
  if state.block_fold(run) ~= state.PEEK then
    error("after three: " .. state.block_fold(run))
  end
end)

check("o inside the block moves one task", function()
  cursor_on(function(row)
    return row.kind == "runtask" and row.name == "build"
  end)
  press("o")
  local run = state.runs["ci"]
  if state.fold_of(run, "build") ~= state.FULL then
    error("build = " .. state.fold_of(run, "build"))
  end
  if state.fold_of(run, "test") ~= state.PEEK then
    error("test moved too: " .. state.fold_of(run, "test"))
  end
end)

-- --- going where it broke ----------------------------------------------------

check("enter on an output line opens the file it names", function()
  cursor_on(function(row)
    return row.kind == "line"
      and row.line.text:find("order_test.go", 1, true)
      and not row.line.command
  end)
  press("<CR>")

  local name = vim.api.nvim_buf_get_name(0)
  if not name:find("order_test.go", 1, true) then
    error("landed in " .. name)
  end
  if vim.api.nvim_win_get_cursor(0)[1] ~= 88 then
    -- The file is one line long, so Neovim clamps; what matters is that it
    -- opened the file the failure named rather than the panel's own buffer.
    if vim.api.nvim_win_get_cursor(0)[1] < 1 then
      error("cursor = " .. vim.inspect(vim.api.nvim_win_get_cursor(0)))
    end
  end
  vim.cmd("TaskUI")
end)

check("the quickfix list comes from the binary, with absolute paths", function()
  local done = false
  panel.quickfix("test", {
    open = false,
    quiet = true,
  })
  wait_for("the quickfix list", function()
    local items = vim.fn.getqflist()
    done = #items > 0
    return done
  end)
  local first = vim.fn.getqflist()[1]
  local path = vim.fn.bufname(first.bufnr)
  if not path:find("order_test.go", 1, true) then
    error("first entry is " .. path)
  end
  if first.lnum ~= 88 or first.col ~= 12 then
    error(("entry at %d:%d"):format(first.lnum, first.col))
  end
  assert_contains(first.text, "want 1200, got 1180")
end)

check("edit opens the task's own definition", function()
  vim.cmd("TaskUI edit build")
  local name = vim.api.nvim_buf_get_name(0)
  if not name:find("Taskfile.yml", 1, true) then
    error("landed in " .. name)
  end
  vim.cmd("TaskUI")
end)

-- --- the tree ----------------------------------------------------------------

check("namespaces fold, and a task inside one is reachable", function()
  vim.fn.writefile({
    "version: \"3\"",
    "tasks:",
    "  backend:lint:",
    "    cmds: ['true']",
    "  backend:test:",
    "    cmds: ['true']",
  }, project .. "/Taskfile.yml")

  state.runs = {}
  panel.refresh()
  wait_for("the new listing", function()
    return state.task("backend:lint") ~= nil
  end)

  assert_contains(screen(), "backend")
  assert_not_contains(screen(), "lint")

  cursor_on(function(row)
    return row.kind == "group" and row.key == "backend"
  end)
  press("<Space>")
  assert_contains(screen(), "lint")
end)

check("stopping a run reaps it", function()
  vim.fn.writefile({
    "version: \"3\"",
    "tasks:",
    "  sleeper:",
    "    cmds: ['sleep 30']",
  }, project .. "/Taskfile.yml")

  state.runs = {}
  panel.refresh()
  wait_for("the listing", function()
    return state.task("sleeper") ~= nil
  end)

  cursor_on(function(row)
    return row.kind == "task" and row.task and row.task.name == "sleeper"
  end)
  press("<CR>")
  wait_for("the run to start", function()
    return state.runs["sleeper"] ~= nil
  end)
  press("x")
  wait_for("the run to stop", function()
    local run = state.runs["sleeper"]
    return run and run.status ~= "running"
  end)
end)

check("checkhealth runs", function()
  require("taskui.health").check()
end)

if failures > 0 then
  io.write(("\n%d failing\n"):format(failures))
  vim.cmd("cquit 1")
end

io.write("\nall tests passed\n")
vim.cmd("qall!")
