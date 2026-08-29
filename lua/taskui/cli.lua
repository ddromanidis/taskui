-- lua/taskui/cli.lua
--
-- The binary, addressed as a program rather than as a terminal.
--
-- Three shapes, matching the three the tool offers: a listing that comes back
-- once, a run that arrives as newline-delimited events until it exits, and the
-- quickfix list. Nothing here knows what a buffer is — this module is the wire,
-- and everything above it is what the wire is for.

local config = require("taskui.config")

local M = {}

--- The argv for one invocation: the configured binary, the project directory
--- as the positional argument, then whatever was asked for.
---@param args string[]
---@return string[]
local function argv(args)
  local cmd = { config.options.binary, config.project() }
  vim.list_extend(cmd, args)
  return cmd
end

--- Reports whether the binary can be found at all, so the failure is "install
--- taskui" rather than a stack trace from vim.system.
---@return boolean
function M.available()
  return vim.fn.executable(config.options.binary) == 1
end

--- Runs the binary and hands back its whole output, on the main loop.
---@param args string[]
---@param on_done fun(code: integer, stdout: string, stderr: string)
local function collect(args, on_done)
  vim.system(argv(args), { text = true }, function(res)
    vim.schedule(function()
      on_done(res.code, res.stdout or "", res.stderr or "")
    end)
  end)
end

--- Fetches the task listing.
---@param on_done fun(tasks: table[]|nil, err: string|nil)
function M.list(on_done)
  collect({ "--list", "--json" }, function(code, stdout, stderr)
    if code ~= 0 and stdout == "" then
      on_done(nil, vim.trim(stderr) ~= "" and vim.trim(stderr) or ("taskui exited " .. code))
      return
    end
    local ok, page = pcall(vim.json.decode, stdout)
    if not ok or type(page) ~= "table" or type(page.tasks) ~= "table" then
      on_done(nil, "could not read the task listing")
      return
    end
    on_done(page.tasks, nil)
  end)
end

--- Fetches the quickfix entries for the last stored run, already parsed into
--- the shape setqflist takes.
---
--- The binary resolves the paths, which is the whole point of asking it rather
--- than parsing the output here: `order_test.go:88` is relative to a directory
--- only the run knows, and Neovim would resolve it against its own.
---@param task string|nil Narrow to one task's output.
---@param on_done fun(items: table[], err: string|nil)
function M.quickfix(task, on_done)
  local args = { "--quickfix" }
  if task and task ~= "" then
    vim.list_extend(args, { "--task", task })
  end
  collect(args, function(code, stdout, stderr)
    if code ~= 0 and stdout == "" then
      on_done({}, vim.trim(stderr) ~= "" and vim.trim(stderr) or ("taskui exited " .. code))
      return
    end
    local items = {}
    for line in vim.gsplit(stdout, "\n", { trimempty = true }) do
      local file, lnum, col, text = line:match("^(.-):(%d+):(%d+): (.*)$")
      if file then
        table.insert(items, {
          filename = file,
          lnum = tonumber(lnum),
          col = tonumber(col),
          text = text,
          type = "E",
        })
      end
    end
    on_done(items, nil)
  end)
end

--- Starts a run and streams its events.
---
--- One process per run, which is what the tool offers and what Neovim is best
--- at: the job table is the multiplexer, so two runs at once need nothing from
--- this module but being called twice.
---@param task string
---@param args string[]|nil Extra arguments for the task itself.
---@param on_event fun(event: table) Called on the main loop, once per event.
---@param on_exit fun(code: integer) Called when the process is gone.
---@return integer job The job id, for jobstop; 0 or -1 when it could not start.
function M.stream(task, args, on_event, on_exit)
  local cmd = { "--run", task, "--json" }
  if args and #args > 0 then
    vim.list_extend(cmd, { "--args", table.concat(args, " ") })
  end

  -- Neovim hands stdout over in chunks that split wherever the pipe did, so a
  -- line can arrive in two pieces and the tail of a chunk is only sometimes a
  -- whole line. The remainder is carried to the next chunk.
  local rest = ""
  local function feed(chunk)
    rest = rest .. chunk
    while true do
      local at = rest:find("\n", 1, true)
      if not at then
        return
      end
      local line, remainder = rest:sub(1, at - 1), rest:sub(at + 1)
      rest = remainder
      if line ~= "" then
        local ok, event = pcall(vim.json.decode, line)
        if ok and type(event) == "table" and event.type then
          on_event(event)
        end
      end
    end
  end

  return vim.fn.jobstart(argv(cmd), {
    on_stdout = function(_, data)
      -- jobstart splits on newlines and drops them, so the chunk is rebuilt
      -- with the newlines it was written with; the last element is the
      -- possibly-partial tail.
      if data then
        feed(table.concat(data, "\n"))
      end
    end,
    on_exit = function(_, code)
      on_exit(code)
    end,
  })
end

return M
