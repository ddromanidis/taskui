-- tests/run.lua
--
-- Integration tests for the Neovim side, run headlessly and with no plugin
-- dependencies:
--
--   task test:nvim
--   TASKUI_BIN=./bin/taskui nvim --headless -l tests/run.lua
--
-- The plugin hosts the real terminal UI, so these drive the real binary in a
-- real terminal buffer and assert on what comes back down the event socket —
-- which is the whole contract between the two halves.

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
  'version: "3"',
  "tasks:",
  "  build:",
  "    desc: Compile it",
  "    cmds: ['echo \"compiling core\"']",
  "  test:",
  "    desc: Run the suite",
  "    cmds:",
  "      - 'echo \"running 42 tests\"'",
  "      - 'echo \"    order_test.go:88:12: want 1200, got 1180\"'",
  "      - 'exit 1'",
}, project .. "/Taskfile.yml")
vim.fn.writefile({ "package p" }, project .. "/order_test.go")

require("taskui").setup({
  binary = binary,
  project = project,
  notify = false,
  quickfix = "never",
  position = "bottom",
})

local events = require("taskui.events")
local term = require("taskui.term")

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

--- The terminal as text, which is what a person would be looking at.
local function screen()
  if not (term.buf and vim.api.nvim_buf_is_valid(term.buf)) then
    return "(no terminal)"
  end
  return table.concat(vim.api.nvim_buf_get_lines(term.buf, 0, -1, false), "\n")
end

local function wait_for(what, predicate, ms)
  if not vim.wait(ms or 15000, predicate, 30) then
    error(("timed out waiting for %s; terminal:\n%s"):format(what, screen()), 2)
  end
end

-- --- the terminal ------------------------------------------------------------

vim.cmd("TaskUI")
wait_for("taskui to draw its first frame", function()
  return screen():find("tasks", 1, true) ~= nil
end)

check("the terminal runs the real thing", function()
  -- Its own header, its own tree, its own pivots — which is the point of
  -- hosting it rather than redrawing it. Everything below starts collapsed, as
  -- it does in a terminal, so ⇥ opens it.
  assert_contains(screen(), "taskui")
  assert_contains(screen(), "domain")
  term.send("\t")
  wait_for("the tree to open", function()
    return screen():find("build", 1, true) ~= nil
  end, 5000)
  assert_contains(screen(), "test")
end)

check("it is a terminal buffer, not a rendered one", function()
  if vim.bo[term.buf].buftype ~= "terminal" then
    error("buftype = " .. vim.bo[term.buf].buftype)
  end
  if not term.job then
    error("no job")
  end
end)

-- --- the events --------------------------------------------------------------

check("running a task reports itself down the socket", function()
  -- Through the command, which types it into the terminal the way a person
  -- would: jump to the task, then run it.
  require("taskui").run("test")
  wait_for("the run to be announced", function()
    return events.runs["test"] ~= nil
  end)
  wait_for("the run to finish", function()
    local run = events.runs["test"]
    return run and run.status ~= "running"
  end)

  local run = events.runs["test"]
  if run.status ~= "failed" or run.exit == 0 then
    error("run = " .. vim.inspect({ status = run.status, exit = run.exit }))
  end
  if not run.saved then
    error("the run was not archived")
  end
end)

check("the task statuses arrive too", function()
  local run = events.runs["test"]
  if run.tasks["test"] == nil then
    error("tasks = " .. vim.inspect(run.tasks))
  end
  if not run.failed["test"] then
    error("the failing task was not reported as failed")
  end
end)

check("the statusline component says what happened", function()
  assert_contains(require("taskui").status(), "✗ test")
end)

check("the quickfix list comes from the binary, with absolute paths", function()
  require("taskui").quickfix("test")
  wait_for("the quickfix list", function()
    return #vim.fn.getqflist() > 0
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
  vim.cmd("cclose")
end)

check("a malformed event line is skipped rather than thrown", function()
  events.feed("this is not json")
  events.feed('{"type":"nothing anyone knows about"}')
  events.feed("")
end)

-- --- what the host adds ------------------------------------------------------

check("an edit event opens the file in this editor, not one inside the terminal", function()
  events.feed(vim.json.encode({
    type = "edit",
    path = project .. "/order_test.go",
    line = 1,
    col = 1,
  }))
  wait_for("the file to open", function()
    return vim.api.nvim_buf_get_name(0):find("order_test.go", 1, true) ~= nil
  end, 3000)
end)

check("edit opens a task's own definition", function()
  vim.cmd("TaskUI edit build")
  wait_for("the Taskfile", function()
    return vim.api.nvim_buf_get_name(0):find("Taskfile.yml", 1, true) ~= nil
  end, 8000)
end)

check("hiding the terminal leaves the process alone", function()
  if not term.is_open() then
    vim.cmd("TaskUI")
    wait_for("the terminal", function()
      return term.is_open()
    end, 3000)
  end
  local job = term.job
  vim.cmd("TaskUI")
  if term.is_open() then
    error("still on screen")
  end
  if term.job ~= job then
    error("the process was replaced")
  end
end)

-- A focused terminal sends every key to the program, so the key that opened the
-- window has to exist in terminal mode too or there is no way back out of it.
check("the toggle key works from inside the terminal", function()
  vim.cmd("TaskUI")
  wait_for("the terminal", function()
    return term.is_open()
  end, 3000)
  for _, mode in ipairs({ "t", "n" }) do
    local map = vim.fn.maparg(require("taskui.config").options.keys.toggle, mode, false, true)
    if vim.tbl_isempty(map) or map.buffer ~= 1 then
      error(("no buffer-local %s-mode mapping: %s"):format(mode, vim.inspect(map)))
    end
  end
end)

check("checkhealth runs", function()
  require("taskui.health").check()
end)

term.stop()

if failures > 0 then
  io.write(("\n%d failing\n"):format(failures))
  vim.cmd("cquit 1")
end

io.write("\nall tests passed\n")
vim.cmd("qall!")
