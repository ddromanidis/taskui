-- lua/taskui/events.lua
--
-- A socket the terminal's taskui reports to, and what Neovim does about it.
--
-- The terminal is the interface — the whole terminal UI, with its pivots, its
-- folds, its archive, none of it reimplemented here. What Neovim adds is the
-- part a terminal cannot do from inside itself: land on the failing line, keep
-- the failure in the statusline while you fix it, and open the file `e` chose
-- in the editor that is already open rather than in one nested inside it.

local config = require("taskui.config")

local M = {}

--- runs is what the events have said so far, by root task name. Nothing here is
--- rendered; it is what the statusline and the quickfix hook read.
---@type table<string, table>
M.runs = {}

--- newest is the run to speak for: whatever is going, or the last thing to
--- finish. The statusline shows one thing, and this is which.
M.newest = nil

local server, path

--- Handlers by event type. Anything unknown is ignored on purpose: a newer
--- binary saying more than this plugin understands must not be an error.
local handlers = {}

function handlers.run(event)
  M.runs[event.root] = {
    root = event.root,
    dir = event.dir,
    status = "running",
    started = event.started_unix,
    tasks = {},
    failed = {},
  }
  M.newest = M.runs[event.root]
end

function handlers.task(event)
  local run = M.runs[event.root]
  if not run then
    return
  end
  run.tasks[event.name] = { status = event.status, duration_ms = event.duration_ms }
  if event.status == "Failed" then
    run.failed[event.name] = true
  end
end

function handlers.exit(event)
  local run = M.runs[event.root]
  if not run then
    return
  end
  run.status = (event.code == 0) and "ok" or "failed"
  run.exit = event.code
  run.duration_ms = event.duration_ms
  run.saved = event.saved
  M.newest = run
  M.finished(run)
end

function handlers.edit(event)
  -- `e` in the terminal, opened in the editor hosting it. Without this the TUI
  -- would launch $EDITOR inside its own window, which is a second Neovim in a
  -- terminal inside the first one.
  require("taskui.term").open_file(event.path, event.line, event.col)
  if event.note and event.note ~= "" then
    vim.notify("taskui: " .. event.note, vim.log.levels.WARN)
  end
end

--- What happens when a run ends: the quickfix list, and a word about how it
--- went — because the terminal may not be the window you are looking at.
---@param run table
function M.finished(run)
  local failed = run.status == "failed"
  local when = config.options.quickfix
  if when == "always" or (when == "on_failure" and failed) then
    require("taskui.cli").quickfix(run.root, function(items, err)
      if err or #items == 0 then
        return
      end
      vim.fn.setqflist({}, " ", { title = "taskui · " .. run.root, items = items })
      if failed and config.options.open_quickfix then
        vim.cmd("copen")
      end
    end)
  end
  if config.options.notify then
    local took = M.duration(run.duration_ms)
    if failed then
      vim.notify(
        ("taskui: %s failed (exit %s)%s"):format(run.root, tostring(run.exit), took),
        vim.log.levels.ERROR
      )
    else
      vim.notify(("taskui: %s passed%s"):format(run.root, took))
    end
  end
end

--- A duration read at a glance rather than in full: ` in 4ms`, ` in 1.5s`.
---@param ms integer|nil
---@return string
function M.duration(ms)
  if not ms or ms <= 0 then
    return ""
  end
  if ms < 1000 then
    return (" in %dms"):format(ms)
  end
  local seconds = ms / 1000
  if seconds < 60 then
    return (" in %.1fs"):format(seconds)
  end
  return (" in %dm%02ds"):format(math.floor(seconds / 60), math.floor(seconds % 60))
end

--- Feeds one line of the stream. Exposed so the tests can drive it without a
--- socket, and because a malformed line must be skipped rather than thrown.
---@param line string
function M.feed(line)
  if line == "" then
    return
  end
  local ok, event = pcall(vim.json.decode, line)
  if not ok or type(event) ~= "table" or not event.type then
    return
  end
  local handler = handlers[event.type]
  if handler then
    handler(event)
  end
end

--- Starts listening, and returns the socket path to hand the terminal. One
--- server for the session: every taskui started from this Neovim reports to
--- the same place, and the events carry which run they are about.
---@return string|nil path
function M.listen()
  if server and path then
    return path
  end
  path = vim.fn.tempname() .. ".taskui.sock"
  server = vim.uv.new_pipe(false)
  local ok, err = server:bind(path)
  if not ok then
    vim.notify("taskui: could not listen for events: " .. tostring(err), vim.log.levels.WARN)
    server, path = nil, nil
    return nil
  end
  server:listen(8, function()
    local client = vim.uv.new_pipe(false)
    server:accept(client)
    -- The stream arrives in chunks that split wherever the pipe did, so the
    -- tail of a chunk is only sometimes a whole line.
    local rest = ""
    client:read_start(function(read_err, chunk)
      if read_err or not chunk then
        client:close()
        return
      end
      rest = rest .. chunk
      while true do
        local at = rest:find("\n", 1, true)
        if not at then
          return
        end
        local line = rest:sub(1, at - 1)
        rest = rest:sub(at + 1)
        vim.schedule(function()
          M.feed(line)
        end)
      end
    end)
  end)
  return path
end

--- Stops listening and removes the socket.
function M.stop()
  if server then
    server:close()
    server = nil
  end
  if path then
    os.remove(path)
    path = nil
  end
end

return M
