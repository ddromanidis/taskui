-- lua/taskui/init.lua
--
-- The public surface: setup, the four verbs `:TaskUI` dispatches to, and a
-- statusline component.
--
-- Everything here is thin. The plugin is a front end over a binary that
-- already knows how to run tasks, keep their output and resolve the file a
-- stack trace names — so the interesting code is the model and the renderer,
-- and this file is the door.

local cli = require("taskui.cli")
local config = require("taskui.config")
local panel = require("taskui.panel")
local state = require("taskui.state")
local view = require("taskui.view")

local M = {}

--- Merges user options over the defaults.
---@param opts Taskui.Config|nil
function M.setup(opts)
  config.setup(opts)
end

--- Opens the panel, reading the Taskfile if it has not been read yet.
function M.open()
  panel.open()
  if #state.tasks == 0 then
    panel.refresh()
  end
end

--- Closes the panel. Runs carry on: closing the list is not stopping anything.
function M.close()
  panel.close()
end

--- Opens the panel, or closes it if it is already up.
function M.toggle()
  if panel.is_open() then
    panel.close()
  else
    M.open()
  end
end

--- Runs a task by name, opening the panel so the output has somewhere to go.
---@param name string
---@param args string[]|nil
function M.run(name, args)
  if not name or name == "" then
    vim.notify("taskui: which task?", vim.log.levels.WARN)
    return
  end
  panel.open()
  if #state.tasks == 0 then
    -- The listing is what the panel draws its tree from; without it the run
    -- would have nowhere to hang.
    panel.refresh(function()
      panel.run(name, args)
    end)
    return
  end
  panel.run(name, args)
end

--- Opens a task's own definition in the Taskfile.
---@param name string|nil Defaults to the task under the cursor in the panel.
function M.edit(name)
  local function jump(task)
    if not task or not task.taskfile or not task.line then
      vim.notify(("taskui: no location for %s"):format(name or "that task"), vim.log.levels.WARN)
      return
    end
    panel.open_file(task.taskfile, task.line, 1)
  end

  if not name or name == "" then
    local row = panel.current_row()
    local task = row and (row.task or (row.name and state.task(row.name)))
    jump(task)
    return
  end
  if #state.tasks == 0 then
    panel.refresh(function()
      jump(state.task(name))
    end)
    return
  end
  jump(state.task(name))
end

--- Fills the quickfix list from the last stored run and opens it.
---@param name string|nil Narrow to one task's output.
function M.quickfix(name)
  panel.quickfix(name ~= "" and name or nil, { open = true })
end

--- Every task name, for command completion. Fetches the listing in the
--- background the first time, so the first `<Tab>` is empty rather than slow —
--- a completion that blocks the editor for four seconds is worse than one that
--- arrives a moment later.
---@return string[]
function M.task_names()
  if #state.tasks == 0 then
    if cli.available() then
      cli.list(function(tasks)
        if tasks then
          state.set_tasks(tasks)
        end
      end)
    end
    return {}
  end
  local names = {}
  for _, task in ipairs(state.tasks) do
    table.insert(names, task.name)
  end
  table.sort(names)
  return names
end

--- A statusline component: what is running, or what just failed.
---
--- Short by design — `✗ backend:test 7.1s` — because a statusline is read out
--- of the corner of the eye while you are working on the thing that failed.
---@return string
function M.status()
  local newest, newest_at = nil, -1
  for _, run in pairs(state.runs) do
    if run.status == "running" then
      newest = run
      break
    end
    if run.started and run.started > newest_at then
      newest, newest_at = run, run.started
    end
  end
  if not newest then
    return ""
  end
  local glyph = view.glyphs.running
  if newest.status == "ok" then
    glyph = view.glyphs.ok
  elseif newest.status == "failed" then
    glyph = view.glyphs.failed
  end
  local took = view.duration(newest.duration_ms)
  return vim.trim(("%s %s %s"):format(glyph, newest.root, took))
end

return M
