-- lua/taskui/config.lua
--
-- Configuration defaults and merging. One flat table: the plugin is a front
-- end over one binary, so there is nothing here that needs a section of its
-- own — and a nested option is one the user has to remember the path to.

local M = {}

---@class Taskui.Config
---@field binary string Executable name or absolute path.
---@field project string|nil Directory to open taskui in. Nil means the cwd.
---@field position "float"|"left"|"right"|"top"|"bottom"|"tab" Where the terminal opens.
---@field width integer Columns for a left or right split.
---@field height integer Rows for a top or bottom split.
---@field args string[] Extra arguments for the binary, e.g. { "--theme", "y2k" }.
---@field quickfix "on_failure"|"always"|"never" When a finished run fills the quickfix list.
---@field open_quickfix boolean Open the quickfix window when it is filled from a failure.
---@field notify boolean Say how a run went — for when the terminal is not the window you are in.
---@field keys table<string, string|false> The two keys the host owns; the rest belong to taskui.
M.defaults = {
  binary = "taskui",
  project = nil,
  -- A float, because taskui is a whole interface rather than a sidebar: it has
  -- its own header, footer and columns, and squeezing those into sixty columns
  -- beside a file is how you end up reimplementing it.
  position = "float",
  width = 80,
  height = 20,
  args = {},
  -- The quickfix list is the largest daily return, and a failed run is exactly
  -- the moment it is wanted — but filling it on a run that passed would clear
  -- whatever you were working through.
  quickfix = "on_failure",
  open_quickfix = false,
  notify = true,
  keys = {
    -- Hides the terminal from inside it. Everything else in there is taskui's:
    -- intercepting its keys would be taking them from the thing you asked for.
    close = "<C-q>",
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
