-- lua/taskui/state.lua
--
-- What is known, and what it is laid out as. No buffers, no windows, no jobs:
-- this module answers "what should be on screen" and nothing about how it gets
-- there, which is what makes it the part the tests can drive directly.
--
-- The model is the TUI's, because the TUI's model turned out to be right: a
-- task tree you fold, and a run that unfolds *under the row it was started
-- from* rather than on a screen of its own. A run in the list is what lets you
-- start a second one without losing sight of the first.

local config = require("taskui.config")

local M = {}

--- Every task the project has, as the listing gave them.
---@type table[]
M.tasks = {}

--- Runs by root task name — one slot per task, as the TUI has.
---@type table<string, table>
M.runs = {}

--- Which namespaces are open. Collapsed by default: a Taskfile with a hundred
--- tasks in it is a shape before it is a list.
---@type table<string, boolean>
M.open = {}

M.error = nil

--- Folds are the TUI's three states, and the same word means the same thing:
--- hidden is one row, peek is the last few lines, full is all of it.
local HIDDEN, PEEK, FULL = "hidden", "peek", "full"

M.HIDDEN, M.PEEK, M.FULL = HIDDEN, PEEK, FULL

--- Replaces the task listing.
---@param tasks table[]
function M.set_tasks(tasks)
  M.tasks = tasks or {}
  M.error = nil
end

--- The listing entry for a task, if the project has one. A run's own tasks are
--- not always in it — a dependency of a dependency need not be a listed task —
--- so callers take the nil.
---@param name string
---@return table|nil
function M.task(name)
  for _, t in ipairs(M.tasks) do
    if t.name == name then
      return t
    end
  end
  return nil
end

--- Starts (or replaces) the run in a task's slot.
---@param root string
---@param job integer
---@return table run
function M.begin_run(root, job)
  M.runs[root] = {
    root = root,
    job = job,
    status = "running",
    exit = nil,
    order = {},
    tasks = {},
    edges = {},
    folds = {},
    started = os.time(),
    duration_ms = nil,
  }
  return M.runs[root]
end

--- Folds a run's event into it. The events are the tool's: run, graph, task,
--- line, prompt, exit.
---@param root string
---@param event table
function M.apply(root, event)
  local run = M.runs[root]
  if not run then
    return
  end

  local function task_of(name)
    if not run.tasks[name] then
      run.tasks[name] = { name = name, status = "Pending", lines = {} }
      table.insert(run.order, name)
    end
    return run.tasks[name]
  end

  if event.type == "graph" then
    run.edges = event.edges or {}
  elseif event.type == "task" then
    local t = task_of(event.name)
    t.status = event.status
    t.duration_ms = event.duration_ms
    t.note = event.note
  elseif event.type == "line" then
    local t = task_of(event.task)
    table.insert(t.lines, { index = event.index, text = event.text or "", command = event.command })
  elseif event.type == "prompt" then
    run.prompt = event.text
  elseif event.type == "exit" then
    run.exit = event.code
    run.duration_ms = event.duration_ms
    run.status = (event.code == 0) and "ok" or "failed"
    run.prompt = nil
    run.job = nil
  end
end

--- The tasks of a run in execution order: the root, then what it invoked,
--- depth first, each at its first position. The graph says who called whom;
--- the order tasks first spoke is the fallback for anything outside it.
---@param run table
---@return table[] rows Each { name = ..., depth = ... }
local function walk(run)
  local out, seen = {}, {}
  local function visit(name, depth)
    if seen[name] then
      return
    end
    seen[name] = true
    table.insert(out, { name = name, depth = depth })
    for _, child in ipairs(run.edges[name] or {}) do
      visit(child, depth + 1)
    end
  end
  visit(run.root, 0)
  for _, name in ipairs(run.order) do
    if not seen[name] and name ~= "" then
      seen[name] = true
      table.insert(out, { name = name, depth = 1 })
    end
  end
  return out
end

--- How much of one task's output a run is showing. Absent means a peek, which
--- is the resting state: a task folded to nothing is one you have to open to
--- find out whether it was worth opening.
---@param run table
---@param name string
---@return string
function M.fold_of(run, name)
  return run.folds[name] or PEEK
end

--- How much of a whole run is showing, read off the folds it already has
--- rather than stored beside them — a second piece of state would be one the
--- per-task folds could contradict.
---@param run table
---@return string
function M.block_fold(run)
  local tasks = walk(run)
  if #tasks == 0 then
    return PEEK
  end
  local hidden, full = true, true
  for _, t in ipairs(tasks) do
    local fold = M.fold_of(run, t.name)
    hidden = hidden and fold == HIDDEN
    full = full and fold == FULL
  end
  if hidden then
    return HIDDEN
  elseif full then
    return FULL
  end
  return PEEK
end

--- hidden → peek → full → hidden. A cycle rather than a toggle plus a second
--- key: the three states are one axis, and "how much of this do I want" has an
--- obvious "more" direction.
---@param fold string
---@return string
function M.next_fold(fold)
  if fold == HIDDEN then
    return PEEK
  elseif fold == PEEK then
    return FULL
  end
  return HIDDEN
end

--- Moves every task of a run along the cycle at once.
---@param run table
function M.cycle_block(run)
  local next_fold = M.next_fold(M.block_fold(run))
  for _, t in ipairs(walk(run)) do
    run.folds[t.name] = next_fold
  end
end

--- Moves one task of a run along the cycle.
---@param run table
---@param name string
function M.cycle_task(run, name)
  run.folds[name] = M.next_fold(M.fold_of(run, name))
end

--- The namespace tree, built from the colon paths themselves: `backend:migrate`
--- is a path, not a name, so the grouping is already written down and does not
--- need a pivot to invent it.
---@return table[] nodes Each { key, label, depth, tasks = {names}, children = {...} }
local function tree()
  local root = { key = "", label = "", depth = -1, children = {}, index = {}, task = nil }
  for _, task in ipairs(M.tasks) do
    local segments = vim.split(task.name, ":", { plain = true })
    local node, key = root, ""
    for i, segment in ipairs(segments) do
      key = (key == "") and segment or (key .. ":" .. segment)
      if not node.index[segment] then
        local child = { key = key, label = segment, depth = i - 1, children = {}, index = {} }
        node.index[segment] = child
        table.insert(node.children, child)
      end
      node = node.index[segment]
      if i == #segments then
        -- A node can be both a group and a task: `backend:migrate` applies
        -- migrations *and* parents `backend:migrate:down`. Collapsing the two
        -- into one concept loses one of them.
        node.task = task
      end
    end
  end
  return root.children
end

--- Reports whether a namespace is unfolded.
---@param key string
---@return boolean
function M.is_open(key)
  return M.open[key] == true
end

--- Folds or unfolds a namespace.
---@param key string
function M.toggle_open(key)
  M.open[key] = not M.open[key]
end

--- Opens or closes every namespace at once.
---@param open boolean
function M.set_all_open(open)
  M.open = {}
  if not open then
    return
  end
  local function mark(nodes)
    for _, node in ipairs(nodes) do
      if #node.children > 0 then
        M.open[node.key] = true
        mark(node.children)
      end
    end
  end
  mark(tree())
end

--- The whole list, flattened into rows — the tree, and every open run unfolded
--- under the task it was started from.
---
--- One flat list rather than a tree the renderer walks, because a row is the
--- unit the cursor moves by: output that is not made of rows is output you
--- cannot put a cursor in.
---@return table[] rows
function M.rows()
  local rows = {}

  local function add_run(root, indent)
    local run = M.runs[root]
    if not run or M.block_fold(run) == HIDDEN then
      return
    end
    local tasks = walk(run)
    for i, entry in ipairs(tasks) do
      -- The root's own row is the task's row in the tree; drawing it again
      -- would say the same name twice, one line apart.
      if i > 1 then
        table.insert(rows, {
          kind = "runtask",
          root = root,
          name = entry.name,
          depth = entry.depth,
          indent = indent,
          last = (function()
            for j = i + 1, #tasks do
              if tasks[j].depth < entry.depth then
                break
              end
              if tasks[j].depth == entry.depth then
                return false
              end
            end
            return true
          end)(),
        })
      end
      local fold = M.fold_of(run, entry.name)
      if fold ~= HIDDEN then
        local task = run.tasks[entry.name]
        local lines = task and task.lines or {}
        local from = 1
        if fold == PEEK then
          -- A peek is a window on the *end* of the buffer: a task still going
          -- has its news at the bottom, and one that failed put the reason
          -- there.
          from = math.max(1, #lines - config.options.peek + 1)
        end
        for at = from, #lines do
          table.insert(rows, {
            kind = "line",
            root = root,
            name = entry.name,
            depth = entry.depth + 1,
            indent = indent,
            line = lines[at],
            peek = fold == PEEK,
            at = at,
            -- Where this line sits in its command's output: the last of it
            -- when the next line starts a new command or there is none, and
            -- under a command at all only once one has been echoed. Read from
            -- the whole buffer rather than from the peek window, so a window
            -- on the end of a command's output still says what it belongs to.
            last_of_command = lines[at + 1] == nil or lines[at + 1].command == true,
            under_command = (function()
              for back = at - 1, 1, -1 do
                if lines[back].command then
                  return true
                end
              end
              return false
            end)(),
            -- A command echo is finished the moment the next one starts; the
            -- last echo in a task carries the task's own verdict.
            later_command = (function()
              for ahead = at + 1, #lines do
                if lines[ahead].command then
                  return true
                end
              end
              return false
            end)(),
          })
        end
      end
    end
  end

  local function add(nodes)
    for _, node in ipairs(nodes) do
      local group = #node.children > 0
      table.insert(rows, {
        kind = group and "group" or "task",
        key = node.key,
        label = node.label,
        depth = node.depth,
        task = node.task,
        open = group and M.is_open(node.key) or nil,
        count = group and M.count(node) or nil,
      })
      if node.task then
        add_run(node.task.name, node.depth * 2)
      end
      if group and M.is_open(node.key) then
        add(node.children)
      end
    end
  end

  add(tree())
  return rows
end

--- How many tasks are inside a namespace, itself included.
---@param node table
---@return integer
function M.count(node)
  local n = node.task and 1 or 0
  for _, child in ipairs(node.children) do
    n = n + M.count(child)
  end
  return n
end

--- The run a row belongs to: the one under it on a task row, the one it is
--- part of on a run row.
---@param row table|nil
---@return table|nil run, string|nil root
function M.run_of(row)
  if not row then
    return nil, nil
  end
  if row.kind == "runtask" or row.kind == "line" then
    return M.runs[row.root], row.root
  end
  if row.task and M.runs[row.task.name] then
    return M.runs[row.task.name], row.task.name
  end
  return nil, nil
end

--- Whether anything is still going, for the statusline and for quitting.
---@return boolean
function M.any_running()
  for _, run in pairs(M.runs) do
    if run.status == "running" then
      return true
    end
  end
  return false
end

return M
