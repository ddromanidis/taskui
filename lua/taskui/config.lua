-- lua/taskui/config.lua
--
-- Configuration defaults and merging. One flat table: the plugin is a front
-- end over one binary, so there is nothing here that needs a section of its
-- own — and a nested option is one the user has to remember the path to.

local M = {}

---@class Taskui.Config
---@field binary string Executable name or absolute path.
---@field project string|nil Directory to read the Taskfile from. Nil means the cwd.
---@field peek integer How many lines a peeking task shows, as `peek-lines:` does in the TUI.
---@field position "left"|"right"|"bottom"|"top" Where the panel opens.
---@field width integer Columns for a left or right panel.
---@field height integer Rows for a top or bottom panel.
---@field quickfix "on_failure"|"always"|"never" When a finished run fills the quickfix list.
---@field open_quickfix boolean Open the quickfix window when it is filled from a failure.
---@field notify boolean Report a run's result with vim.notify as well as in the panel.
---@field keys table<string, string|false> Panel keymaps, by action. False unbinds one.
M.defaults = {
  binary = "taskui",
  project = nil,
  peek = 5,
  position = "left",
  width = 62,
  height = 18,
  -- The quickfix list is the plugin's largest daily return, and a failed run
  -- is exactly the moment it is wanted — but filling it on a run that passed
  -- would clear whatever you were working through.
  quickfix = "on_failure",
  open_quickfix = false,
  notify = true,
  keys = {
    run = "<CR>",
    fold_tree = "<Space>",
    fold_output = "o",
    fold_all = "O",
    rerun = "r",
    stop = "x",
    quickfix = "c",
    edit = "e",
    refresh = "R",
    help = "?",
    close = "q",
  },
}

M.options = vim.deepcopy(M.defaults)

--- Merges user options over the defaults.
---@param opts Taskui.Config|nil
function M.setup(opts)
  M.options = vim.tbl_deep_extend("force", vim.deepcopy(M.defaults), opts or {})
end

--- The project directory a command should run in: what was configured, or
--- wherever Neovim is, which is what every other tool in the editor assumes.
---@return string
function M.project()
  return M.options.project or vim.fn.getcwd()
end

return M
