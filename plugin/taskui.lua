-- plugin/taskui.lua
--
-- Registers :TaskUI. Loading the plugin requires no Lua module of its own, so
-- startup costs nothing until the command is actually used.
--
--   :TaskUI                    open the panel (again to close it)
--   :TaskUI run <task> [args]  run one, opening the panel for its output
--   :TaskUI edit <task>        open the task's definition in the Taskfile
--   :TaskUI quickfix [task]    the last run's failures, in the quickfix list

if vim.g.loaded_taskui then
  return
end
vim.g.loaded_taskui = true

local verbs = { "run", "edit", "quickfix", "open", "close" }

vim.api.nvim_create_user_command("TaskUI", function(cmd)
  local taskui = require("taskui")
  local verb = cmd.fargs[1]

  if not verb then
    taskui.toggle()
    return
  end

  local rest = {}
  for i = 3, #cmd.fargs do
    table.insert(rest, cmd.fargs[i])
  end

  if verb == "run" then
    taskui.run(cmd.fargs[2], rest)
  elseif verb == "edit" then
    taskui.edit(cmd.fargs[2])
  elseif verb == "quickfix" then
    taskui.quickfix(cmd.fargs[2])
  elseif verb == "open" then
    taskui.open()
  elseif verb == "close" then
    taskui.close()
  else
    -- No verb given means the task itself: `:TaskUI backend:test` is what
    -- anybody types before reading this file.
    taskui.run(verb, vim.list_slice(cmd.fargs, 2))
  end
end, {
  nargs = "*",
  desc = "taskui: browse and run the project's tasks",
  complete = function(lead, line)
    local taskui = require("taskui")
    local words = vim.split(vim.trim(line), "%s+")
    -- The first argument completes to a verb or straight to a task name; after
    -- `run` or `edit` it is always a task.
    if #words <= 2 then
      local out = {}
      for _, candidate in ipairs(vim.list_extend(vim.deepcopy(verbs), taskui.task_names())) do
        if vim.startswith(candidate, lead) then
          table.insert(out, candidate)
        end
      end
      return out
    end
    if words[2] == "run" or words[2] == "edit" or words[2] == "quickfix" then
      return vim.tbl_filter(function(name)
        return vim.startswith(name, lead)
      end, taskui.task_names())
    end
    return {}
  end,
})
