-- lua/taskui/panel.lua
--
-- The window, the buffer, and what the keys do.
--
-- A scratch buffer rendered into rather than a file — the neo-tree and oil
-- pattern — because the content is a projection of state that changes under
-- you: lines arrive while you read, and a buffer you edited would have to be
-- reconciled with a run that has moved on.
--
-- The cursor is kept on the row it was on rather than on the line number it
-- happened to have. Output arriving above the cursor moves every row below it,
-- and a list that slid a line at a time while you read it would be unusable.

local cli = require("taskui.cli")
local config = require("taskui.config")
local state = require("taskui.state")
local view = require("taskui.view")

local M = {}

M.buf = nil
M.win = nil

--- rows is what is on screen, one entry per buffer line, so a keypress can ask
--- what it is standing on.
M.rows = {}

local ns = vim.api.nvim_create_namespace("taskui")

local function valid_win()
  return M.win and vim.api.nvim_win_is_valid(M.win)
end

local function valid_buf()
  return M.buf and vim.api.nvim_buf_is_valid(M.buf)
end

--- The row under the cursor, or nil in an empty panel.
---@return table|nil
function M.current_row()
  if not valid_win() then
    return nil
  end
  local at = vim.api.nvim_win_get_cursor(M.win)[1]
  return M.rows[at]
end

--- An identity for a row, so the cursor can be put back on it after a redraw
--- rather than on whatever now occupies its old line number.
local function identity(row)
  if not row then
    return nil
  end
  if row.kind == "line" then
    return ("line\0%s\0%s\0%d"):format(row.root, row.name, row.line.index)
  elseif row.kind == "runtask" then
    return ("runtask\0%s\0%s"):format(row.root, row.name)
  end
  return ("tree\0%s"):format(row.key)
end

--- Breaks a message into lines that fit, preferring word boundaries.
---@param text string
---@param width integer
---@return string[]
local function wrapped(text, width)
  local out = {}
  for line in vim.gsplit(text, "\n", { trimempty = true }) do
    while #line > width do
      local at = line:sub(1, width):find("%s[^%s]*$")
      at = at or width
      table.insert(out, vim.trim(line:sub(1, at)))
      line = line:sub(at + 1)
    end
    table.insert(out, vim.trim(line))
  end
  return out
end

--- Draws the current state into the buffer.
function M.render()
  if not valid_buf() then
    return
  end

  local anchor = identity(M.current_row())
  local width = valid_win() and vim.api.nvim_win_get_width(M.win) or config.options.width
  M.rows = state.rows()
  local lines, spans = view.render(M.rows, width - 1)
  for at, line in ipairs(lines) do
    -- A rendered row can end in the padding that right-anchored something; a
    -- buffer line ending in spaces is one `$` lands past and `:%s/\s\+$//`
    -- would rewrite.
    lines[at] = line:gsub("%s+$", "")
  end

  if #lines == 0 then
    -- Nothing to list: say why, wrapped, because the reason is usually
    -- go-task's own sentence about a missing Taskfile and a panel sixty
    -- columns wide would otherwise show the first third of it.
    lines, spans = {}, {}
    for _, chunk in ipairs(wrapped(state.error or "no tasks found", width - 4)) do
      table.insert(lines, "  " .. chunk)
      table.insert(spans, {})
    end
    M.rows = {}
  end

  vim.bo[M.buf].modifiable = true
  vim.api.nvim_buf_set_lines(M.buf, 0, -1, false, lines)
  vim.bo[M.buf].modifiable = false
  vim.bo[M.buf].modified = false

  vim.api.nvim_buf_clear_namespace(M.buf, ns, 0, -1)
  for at, row_spans in ipairs(spans) do
    for _, span in ipairs(row_spans) do
      pcall(vim.api.nvim_buf_set_extmark, M.buf, ns, at - 1, span.from, {
        end_col = span.to,
        hl_group = span.hl,
      })
    end
  end

  if valid_win() then
    vim.wo[M.win].winbar = view.header()
    if anchor then
      for at, row in ipairs(M.rows) do
        if identity(row) == anchor then
          pcall(vim.api.nvim_win_set_cursor, M.win, { at, 0 })
          break
        end
      end
    end
    local count = math.max(1, vim.api.nvim_buf_line_count(M.buf))
    local at = vim.api.nvim_win_get_cursor(M.win)[1]
    if at > count then
      pcall(vim.api.nvim_win_set_cursor, M.win, { count, 0 })
    end
  end
end

--- Redraws on the main loop, coalescing the burst of events a chatty run
--- produces into one draw per tick. A build that prints two thousand lines
--- should cost two thousand rows, not two thousand redraws.
local scheduled = false
function M.redraw()
  if scheduled then
    return
  end
  scheduled = true
  vim.defer_fn(function()
    scheduled = false
    M.render()
  end, 30)
end

local function map(action, fn)
  local lhs = config.options.keys[action]
  if lhs == false or lhs == nil or lhs == "" then
    return
  end
  vim.keymap.set("n", lhs, fn, {
    buffer = M.buf,
    nowait = true,
    silent = true,
    desc = "taskui: " .. action,
  })
end

--- Opens the panel, creating the buffer and window if they are not already up.
function M.open()
  view.define_highlights()

  if not valid_buf() then
    M.buf = vim.api.nvim_create_buf(false, true)
    vim.bo[M.buf].buftype = "nofile"
    vim.bo[M.buf].bufhidden = "hide"
    vim.bo[M.buf].swapfile = false
    vim.bo[M.buf].filetype = "taskui"
    vim.api.nvim_buf_set_name(M.buf, "taskui://" .. config.project())
    M.bind()
  end

  if not valid_win() then
    local position = config.options.position
    local command = ({
      left = "topleft vsplit",
      right = "botright vsplit",
      top = "topleft split",
      bottom = "botright split",
    })[position] or "topleft vsplit"
    vim.cmd(command)
    M.win = vim.api.nvim_get_current_win()
    vim.api.nvim_win_set_buf(M.win, M.buf)
    if position == "left" or position == "right" then
      vim.api.nvim_win_set_width(M.win, config.options.width)
    else
      vim.api.nvim_win_set_height(M.win, config.options.height)
    end
    local wo = vim.wo[M.win]
    wo.number = false
    wo.relativenumber = false
    wo.signcolumn = "no"
    wo.wrap = false
    wo.cursorline = true
    wo.list = false
    wo.foldcolumn = "0"
    wo.spell = false
  end

  vim.api.nvim_set_current_win(M.win)
  M.render()
end

--- Closes the window and leaves the runs alone: a run outlives the panel, the
--- way it outlives the run view in the TUI. Stopping one is `x`, deliberately.
function M.close()
  if valid_win() then
    vim.api.nvim_win_close(M.win, true)
  end
  M.win = nil
end

function M.is_open()
  return valid_win()
end

--- The window the panel was opened from, for anything that has to show a file:
--- a jump that replaced the panel with a source file would take the list away
--- from you at the moment you wanted to come back to it.
---@return integer|nil
local function other_win()
  for _, win in ipairs(vim.api.nvim_list_wins()) do
    if win ~= M.win and vim.api.nvim_win_get_config(win).relative == "" then
      return win
    end
  end
  return nil
end

--- Opens a file at a position, in a window that is not the panel.
---@param path string
---@param lnum integer
---@param col integer|nil
function M.open_file(path, lnum, col)
  local win = other_win()
  if win then
    vim.api.nvim_set_current_win(win)
  else
    vim.cmd("wincmd l")
  end
  vim.cmd.edit(vim.fn.fnameescape(path))
  pcall(vim.api.nvim_win_set_cursor, 0, { lnum, math.max(0, (col or 1) - 1) })
  vim.cmd("normal! zv")
end

--- Fills the quickfix list from a run, and says how many entries it found.
---@param root string|nil Narrow to one task; nil takes the whole run.
---@param opts table|nil { open = boolean, quiet = boolean }
function M.quickfix(root, opts)
  opts = opts or {}
  cli.quickfix(root, function(items, err)
    if err then
      vim.notify("taskui: " .. err, vim.log.levels.WARN)
      return
    end
    local title = "taskui" .. (root and (" · " .. root) or "")
    vim.fn.setqflist({}, " ", { title = title, items = items })
    if #items == 0 then
      if not opts.quiet then
        vim.notify("taskui: nothing to put in the quickfix list", vim.log.levels.INFO)
      end
      return
    end
    if opts.open then
      vim.cmd("copen")
    elseif not opts.quiet then
      vim.notify(("taskui: %d entries in the quickfix list"):format(#items))
    end
  end)
end

--- Starts a task, or focuses the run already in its slot.
---
--- One slot per task, as the TUI has: starting the task that is already running
--- would mean stopping it to start it, which on a half-finished deploy is the
--- worst thing this could do.
---@param name string
---@param args string[]|nil
function M.run(name, args)
  local existing = state.runs[name]
  if existing and existing.status == "running" then
    vim.notify(("taskui: `%s` is already running"):format(name), vim.log.levels.INFO)
    return
  end
  if not cli.available() then
    vim.notify("taskui: the taskui binary is not on your PATH", vim.log.levels.ERROR)
    return
  end

  local run
  local job = cli.stream(name, args, function(event)
    state.apply(name, event)
    M.redraw()
  end, function()
    vim.schedule(function()
      local finished = state.runs[name]
      if finished and finished.status == "running" then
        -- The process is gone without an exit event: killed, or it never got
        -- far enough to say. Saying "stopped" is better than a spinner that
        -- never stops.
        finished.status = "stopped"
        finished.job = nil
      end
      M.render()
      M.after_run(name)
    end)
  end)

  if job <= 0 then
    vim.notify("taskui: could not start " .. config.options.binary, vim.log.levels.ERROR)
    return
  end
  run = state.begin_run(name, job)
  run.args = args
  M.render()
end

--- What happens when a run ends: the quickfix list, and a word about how it
--- went if the panel is not on screen to say so itself.
---@param name string
function M.after_run(name)
  local run = state.runs[name]
  if not run then
    return
  end
  local failed = run.status == "failed"
  local when = config.options.quickfix
  if when == "always" or (when == "on_failure" and failed) then
    M.quickfix(name, { open = failed and config.options.open_quickfix, quiet = true })
  end
  if config.options.notify then
    local took = view.duration(run.duration_ms)
    local suffix = took ~= "" and (" in " .. took) or ""
    if failed then
      local text = ("taskui: %s failed (exit %s)%s"):format(name, tostring(run.exit), suffix)
      vim.notify(text, vim.log.levels.ERROR)
    elseif run.status == "ok" then
      vim.notify(("taskui: %s passed%s"):format(name, suffix))
    end
  end
end

--- Stops the run under the cursor. jobstop signals the process, and the tool
--- takes its own child's process group down with it — which is the part that
--- actually reaps a `docker compose up`.
---@param root string
function M.stop(root)
  local run = state.runs[root]
  if not run or not run.job then
    vim.notify("taskui: nothing running there", vim.log.levels.INFO)
    return
  end
  vim.fn.jobstop(run.job)
end

--- Refreshes the task listing.
---@param on_done fun()|nil
function M.refresh(on_done)
  if not cli.available() then
    state.error = "the taskui binary is not on your PATH"
    M.render()
    if on_done then
      on_done()
    end
    return
  end
  cli.list(function(tasks, err)
    if err then
      state.error = err
    else
      state.set_tasks(tasks)
    end
    M.render()
    if on_done then
      on_done()
    end
  end)
end

--- The file location a row points at, if it points at one: a `file:line[:col]`
--- in an output line, and the task's own definition on a task row.
---@param row table|nil
---@return table|nil { path, lnum, col }
function M.location(row)
  if not row then
    return nil
  end
  if row.kind == "line" then
    local text = row.line.text
    -- Same shape the tool looks for: something with an extension, then a line
    -- number. The extension is what keeps every duration and port out.
    local path, lnum, col = text:match("([%w%._/%-]+%.[%w_+]+):(%d+):(%d+)")
    if not path then
      path, lnum = text:match("([%w%._/%-]+%.[%w_+]+):(%d+)")
    end
    if path then
      local absolute = path
      if not vim.startswith(path, "/") then
        absolute = config.project() .. "/" .. path
      end
      if vim.fn.filereadable(absolute) == 1 then
        return { path = absolute, lnum = tonumber(lnum), col = tonumber(col) }
      end
    end
    return nil
  end

  local task = row.task or (row.name and state.task(row.name))
  if task and task.taskfile and task.line then
    return { path = task.taskfile, lnum = task.line, col = 1 }
  end
  return nil
end

--- Binds the panel's keys. Buffer-local, so nothing here is visible anywhere
--- else in the editor.
function M.bind()
  map("close", function()
    M.close()
  end)

  map("refresh", function()
    M.refresh()
  end)

  -- `⏎` runs a task, folds a namespace, and follows a `file:line` in output.
  -- Three meanings, but never two at once: what a row is decides which of them
  -- is available.
  map("run", function()
    local row = M.current_row()
    if not row then
      return
    end
    if row.kind == "line" then
      local at = M.location(row)
      if at then
        M.open_file(at.path, at.lnum, at.col)
      else
        vim.notify("taskui: no file:line on that row", vim.log.levels.INFO)
      end
      return
    end
    if row.kind == "runtask" then
      M.run(row.name)
      return
    end
    if row.task then
      M.run(row.task.name)
    elseif row.key then
      state.toggle_open(row.key)
      M.render()
    end
  end)

  -- Space is the tree and `o` is the output, each falling through to the other
  -- where it has nothing of its own to fold — the rule the TUI settled on.
  map("fold_tree", function()
    local row = M.current_row()
    if not row then
      return
    end
    if row.kind == "group" then
      state.toggle_open(row.key)
      M.render()
      return
    end
    M.fold_output()
  end)

  map("fold_output", function()
    if not M.fold_output() then
      local row = M.current_row()
      if row and row.kind == "group" then
        state.toggle_open(row.key)
        M.render()
      end
    end
  end)

  map("fold_all", function()
    local any = false
    for _ in pairs(state.open) do
      any = true
      break
    end
    state.set_all_open(not any)
    M.render()
  end)

  map("rerun", function()
    local _, root = state.run_of(M.current_row())
    local row = M.current_row()
    local name = root or (row and row.task and row.task.name)
    if name then
      local previous = state.runs[name]
      M.run(name, previous and previous.args or nil)
    end
  end)

  map("stop", function()
    local _, root = state.run_of(M.current_row())
    if root then
      M.stop(root)
    else
      vim.notify("taskui: nothing running there", vim.log.levels.INFO)
    end
  end)

  map("quickfix", function()
    local _, root = state.run_of(M.current_row())
    M.quickfix(root, { open = true })
  end)

  map("edit", function()
    local row = M.current_row()
    -- On an output line `e` means the task that printed it, not the file the
    -- line names — that is what `⏎` is for, and two keys doing the same jump
    -- would leave no key for the definition.
    local target = row
    if row and row.kind == "line" then
      target = { kind = "runtask", name = row.name }
    end
    local at = M.location(target)
    if at then
      M.open_file(at.path, at.lnum, at.col)
    else
      vim.notify("taskui: nothing to open there", vim.log.levels.INFO)
    end
  end)

  map("help", function()
    M.help()
  end)
end

--- Cycles the output under the cursor: the whole run on a task's own row, one
--- task on a row inside it. Reports whether there was a run to fold at all.
---@return boolean
function M.fold_output()
  local row = M.current_row()
  local run, _ = state.run_of(row)
  if not run then
    return false
  end
  if row.kind == "runtask" then
    state.cycle_task(run, row.name)
  elseif row.kind == "line" then
    state.cycle_task(run, row.name)
  else
    state.cycle_block(run)
  end
  M.render()
  return true
end

--- The keymap, in a float, because a plugin whose keys are only in the README
--- is a plugin whose keys nobody knows.
function M.help()
  local keys = config.options.keys
  local order = {
    { "run", "run the task · fold a namespace · open the file:line under the cursor" },
    { "fold_tree", "fold or unfold a namespace" },
    { "fold_output", "how much of the run: hidden, a peek, all of it" },
    { "fold_all", "fold or unfold every namespace" },
    { "rerun", "re-run the task this row belongs to" },
    { "stop", "stop the run this row belongs to" },
    { "quickfix", "put its failures in the quickfix list and open it" },
    { "edit", "open the task's own definition in the Taskfile" },
    { "refresh", "read the Taskfile again" },
    { "help", "this window" },
    { "close", "close the panel — runs carry on" },
  }
  local lines = { " taskui", "" }
  for _, entry in ipairs(order) do
    local lhs = keys[entry[1]]
    if lhs then
      lines[#lines + 1] = (" %-10s %s"):format(lhs, entry[2])
    end
  end
  lines[#lines + 1] = ""
  lines[#lines + 1] = " :TaskUI run|edit|quickfix <task>"

  local buf = vim.api.nvim_create_buf(false, true)
  vim.api.nvim_buf_set_lines(buf, 0, -1, false, lines)
  vim.bo[buf].modifiable = false
  vim.bo[buf].bufhidden = "wipe"
  local width = 0
  for _, line in ipairs(lines) do
    width = math.max(width, #line)
  end
  local win = vim.api.nvim_open_win(buf, true, {
    relative = "editor",
    width = width + 2,
    height = #lines,
    row = math.floor((vim.o.lines - #lines) / 2),
    col = math.floor((vim.o.columns - width) / 2),
    style = "minimal",
    border = "rounded",
  })
  vim.keymap.set("n", "q", function()
    vim.api.nvim_win_close(win, true)
  end, { buffer = buf, nowait = true })
  vim.keymap.set("n", "<Esc>", function()
    vim.api.nvim_win_close(win, true)
  end, { buffer = buf, nowait = true })
end

return M
