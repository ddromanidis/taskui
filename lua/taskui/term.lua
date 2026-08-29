-- lua/taskui/term.lua
--
-- The terminal the tool runs in, and the window it lives in.
--
-- One job for the session rather than one per `:TaskUI`: taskui keeps runs in
-- slots and its own archive, so closing the window and opening it again should
-- find everything where you left it — which it does, because the process never
-- stopped. Hiding the window is hiding the window.

local cli = require("taskui.cli")
local config = require("taskui.config")
local events = require("taskui.events")

local M = {}

M.buf = nil
M.win = nil
M.job = nil

--- The window taskui was opened from, so a file it asks to open lands where
--- you were working rather than replacing the terminal you asked from.
M.origin = nil

local function valid_win()
  return M.win and vim.api.nvim_win_is_valid(M.win)
end

local function valid_buf()
  return M.buf and vim.api.nvim_buf_is_valid(M.buf)
end

--- Reports whether the terminal is on screen.
function M.is_open()
  return valid_win()
end

--- Opens the window the terminal lives in, in whichever shape was configured.
local function open_window()
  local position = config.options.position
  if position == "float" then
    local width = math.min(vim.o.columns - 4, math.max(80, math.floor(vim.o.columns * 0.9)))
    local height = math.min(vim.o.lines - 4, math.max(20, math.floor(vim.o.lines * 0.85)))
    M.win = vim.api.nvim_open_win(M.buf, true, {
      relative = "editor",
      width = width,
      height = height,
      row = math.floor((vim.o.lines - height) / 2) - 1,
      col = math.floor((vim.o.columns - width) / 2),
      style = "minimal",
      border = "rounded",
      title = " taskui ",
      title_pos = "center",
    })
    return
  end

  local command = ({
    left = "topleft vsplit",
    right = "botright vsplit",
    top = "topleft split",
    bottom = "botright split",
    tab = "tabnew",
  })[position] or "botright split"
  vim.cmd(command)
  M.win = vim.api.nvim_get_current_win()
  vim.api.nvim_win_set_buf(M.win, M.buf)
  if position == "left" or position == "right" then
    vim.api.nvim_win_set_width(M.win, config.options.width)
  elseif position == "top" or position == "bottom" then
    vim.api.nvim_win_set_height(M.win, config.options.height)
  end
end

--- Starts the terminal job, once. The socket it reports to is opened first, so
--- the tool has somewhere to report from its very first run.
local function start_job()
  local socket = events.listen()
  local argv = { config.options.binary, config.project() }
  if socket then
    vim.list_extend(argv, { "--events", socket })
  end
  vim.list_extend(argv, config.options.args or {})

  M.job = vim.fn.jobstart(argv, {
    term = true,
    env = {
      -- taskui reads a config of its own; nothing here overrides it. TERM is
      -- set because a terminal buffer is one, and some shells disagree.
      TERM = vim.env.TERM or "xterm-256color",
    },
    on_exit = function()
      M.job = nil
      if valid_win() then
        vim.api.nvim_win_close(M.win, true)
        M.win = nil
      end
      if valid_buf() then
        vim.api.nvim_buf_delete(M.buf, { force = true })
        M.buf = nil
      end
    end,
  })
  if M.job <= 0 then
    vim.notify("taskui: could not start " .. config.options.binary, vim.log.levels.ERROR)
    M.job = nil
    return false
  end
  return true
end

--- Opens the terminal, starting it if this is the first time.
function M.open()
  if not cli.available() then
    vim.notify("taskui: the taskui binary is not on your PATH", vim.log.levels.ERROR)
    return
  end

  if valid_win() then
    vim.api.nvim_set_current_win(M.win)
    vim.cmd("startinsert")
    return
  end

  M.origin = vim.api.nvim_get_current_win()

  if not valid_buf() then
    M.buf = vim.api.nvim_create_buf(false, true)
    open_window()
    vim.api.nvim_win_set_buf(M.win, M.buf)
    -- The job has to be started with the terminal buffer current, which is
    -- what termopen and its jobstart successor both require.
    vim.api.nvim_win_call(M.win, function()
      if not start_job() then
        return
      end
      M.bind()
    end)
  else
    open_window()
  end

  local wo = vim.wo[M.win]
  wo.number = false
  wo.relativenumber = false
  wo.signcolumn = "no"
  wo.spell = false
  vim.cmd("startinsert")
end

--- Hides the window. The process carries on, with its runs and its scroll
--- position, because closing a window is not stopping anything.
function M.close()
  if valid_win() then
    vim.api.nvim_win_close(M.win, true)
  end
  M.win = nil
end

function M.toggle()
  if valid_win() then
    M.close()
  else
    M.open()
  end
end

--- Sends keys to the terminal, which is how the commands drive it: `:TaskUI
--- run build` is `/build⏎` typed for you.
---@param keys string
function M.send(keys)
  if M.job then
    vim.fn.chansend(M.job, keys)
  end
end

--- Stops the process, and with it every run it owns. taskui reaps its own
--- children, so this takes the whole tree down rather than orphaning it.
function M.stop()
  if M.job then
    vim.fn.jobstop(M.job)
    M.job = nil
  end
  events.stop()
end

--- Opens a file at a position, in a window that is not the terminal.
---@param path string
---@param lnum integer|nil
---@param col integer|nil
function M.open_file(path, lnum, col)
  local target = M.origin
  if not (target and vim.api.nvim_win_is_valid(target)) or target == M.win then
    target = nil
    for _, win in ipairs(vim.api.nvim_list_wins()) do
      if win ~= M.win and vim.api.nvim_win_get_config(win).relative == "" then
        target = win
        break
      end
    end
  end

  -- A float covers what is underneath it, so it gets out of the way to show a
  -- file. A split does not, and staying put keeps the run visible beside the
  -- code it is complaining about.
  if config.options.position == "float" then
    M.close()
  end

  if target then
    vim.api.nvim_set_current_win(target)
  else
    vim.cmd("wincmd p")
  end
  vim.cmd.edit(vim.fn.fnameescape(path))
  if lnum then
    pcall(vim.api.nvim_win_set_cursor, 0, { lnum, math.max(0, (col or 1) - 1) })
    vim.cmd("normal! zv")
  end
end

--- Binds the two keys the host owns. Everything else belongs to the tool: this
--- is a terminal, and intercepting its keys would be taking them away from the
--- thing that was asked for.
function M.bind()
  local close = config.options.keys.close
  if close and close ~= "" then
    vim.keymap.set("t", close, function()
      M.close()
    end, { buffer = M.buf, desc = "taskui: hide the terminal" })
  end
  vim.keymap.set("n", "q", function()
    M.close()
  end, { buffer = M.buf, nowait = true, desc = "taskui: hide the terminal" })
end

return M
