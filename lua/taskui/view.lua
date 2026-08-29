-- lua/taskui/view.lua
--
-- Rows to text, and text to highlights.
--
-- The glyph vocabulary is the TUI's, deliberately: `▸ ▿ ▾` for the three fold
-- states, `✓ ✗ ▶` for how something went, `❯` for a command, `├ │ └` for what
-- hangs off what. Somebody who uses both should not have to learn the second
-- one — and where the two disagree it is because a terminal and an editor
-- genuinely differ, not because this file drifted.
--
-- Rendering returns lines plus spans rather than writing to a buffer: the
-- separation is what lets the tests assert on what would be drawn without a
-- window existing.

local state = require("taskui.state")

local M = {}

M.glyphs = {
  fold_hidden = "▸",
  fold_peek = "▿",
  fold_open = "▾",
  ok = "✓",
  failed = "✗",
  running = "▶",
  pending = "·",
  skipped = "⏸",
  command = "❯",
  danger = "⚠",
  guide_vertical = "│",
  guide_branch = "├",
  guide_last = "└",
}

--- The highlight groups, defined once and linked to what every colourscheme
--- already has, so the panel is coloured before anybody configures anything
--- and overridable by those who want to.
function M.define_highlights()
  local links = {
    TaskuiGroup = "Directory",
    TaskuiTask = "Normal",
    TaskuiDesc = "Comment",
    TaskuiGuide = "NonText",
    TaskuiOk = "DiagnosticOk",
    TaskuiFailed = "DiagnosticError",
    TaskuiRunning = "DiagnosticWarn",
    TaskuiSkipped = "Comment",
    TaskuiCommand = "Special",
    TaskuiDanger = "DiagnosticWarn",
    TaskuiDuration = "Comment",
    TaskuiCount = "Comment",
    TaskuiHeader = "Title",
  }
  for group, target in pairs(links) do
    vim.api.nvim_set_hl(0, group, { link = target, default = true })
  end
end

local function status_glyph(status)
  local g = M.glyphs
  if status == "Ok" or status == "ok" then
    return g.ok, "TaskuiOk"
  elseif status == "Failed" or status == "failed" then
    return g.failed, "TaskuiFailed"
  elseif status == "Running" or status == "running" then
    return g.running, "TaskuiRunning"
  elseif status == "Skipped" then
    return g.skipped, "TaskuiSkipped"
  end
  return g.pending, "TaskuiSkipped"
end

local function fold_glyph(fold)
  local g = M.glyphs
  if fold == state.HIDDEN then
    return g.fold_hidden
  elseif fold == state.FULL then
    return g.fold_open
  end
  return g.fold_peek
end

--- A duration read at a glance rather than in full: `4ms`, `1.5s`, `2m14s`.
---@param ms integer|nil
---@return string
function M.duration(ms)
  if not ms or ms <= 0 then
    return ""
  end
  if ms < 1000 then
    return ("%dms"):format(ms)
  end
  local seconds = ms / 1000
  if seconds < 60 then
    return ("%.1fs"):format(seconds)
  end
  return ("%dm%02ds"):format(math.floor(seconds / 60), math.floor(seconds % 60))
end

--- A builder for one line: text accumulates, and each piece remembers the
--- highlight it wants and the byte range it landed in.
local function builder()
  local self = { text = "", spans = {} }
  function self.add(text, hl)
    if text == "" then
      return
    end
    if hl then
      table.insert(self.spans, { hl = hl, from = #self.text, to = #self.text + #text })
    end
    self.text = self.text .. text
  end
  return self
end

--- Right-anchors a tail against the panel's width, so every duration and count
--- ends in the same column and the eye can run down them.
local function anchor(b, tail, hl, width)
  if tail == "" then
    return
  end
  local used = vim.fn.strdisplaywidth(b.text)
  local gap = math.max(1, width - used - vim.fn.strdisplaywidth(tail))
  b.add((" "):rep(gap), nil)
  b.add(tail, hl)
end

--- The guide a run row hangs from: the block's own rail, then a branch for a
--- task and a continuation for its output.
local function rail(row)
  local g = M.glyphs
  local out = (" "):rep(row.indent or 0)
  if row.kind == "runtask" then
    return out .. (row.last and g.guide_last or g.guide_branch) .. " "
  end
  -- An output line sits under its task: the column above it carries the task's
  -- rail, and its own column is blank.
  return out .. "  "
end

--- Renders one row.
---@param row table
---@param width integer
---@return string text, table[] spans
function M.render_row(row, width)
  local g = M.glyphs
  local b = builder()

  if row.kind == "group" or row.kind == "task" then
    b.add((" "):rep(row.depth * 2), "TaskuiGuide")
    if row.kind == "group" then
      b.add((row.open and g.fold_open or g.fold_hidden) .. " ", "TaskuiGuide")
    else
      b.add("  ", nil)
    end
    b.add(row.label, row.kind == "group" and "TaskuiGroup" or "TaskuiTask")

    local task = row.task
    if task and task.dangerous then
      b.add(" " .. g.danger, "TaskuiDanger")
    end
    if task and task.desc and task.desc ~= "" then
      b.add("  " .. task.desc, "TaskuiDesc")
    end

    local tail, hl = "", "TaskuiDuration"
    local run = task and state.runs[task.name]
    if run then
      -- A run in the slot outranks the archive's "how it went last time": the
      -- one you started a minute ago is the more current answer.
      local glyph, ghl = status_glyph(run.status)
      tail = fold_glyph(state.block_fold(run)) .. " " .. glyph
      local took = M.duration(run.duration_ms)
      if took ~= "" then
        tail = tail .. " " .. took
      end
      hl = ghl
    elseif task and task.last then
      tail = task.last.ok and g.ok or g.failed
      hl = task.last.ok and "TaskuiOk" or "TaskuiFailed"
    elseif row.count then
      tail = tostring(row.count)
      hl = "TaskuiCount"
    end
    anchor(b, tail, hl, width)
    return b.text, b.spans
  end

  if row.kind == "runtask" then
    local run = state.runs[row.root]
    local task = run and run.tasks[row.name]
    b.add(rail(row), "TaskuiGuide")
    local fold = run and state.fold_of(run, row.name) or state.PEEK
    local has_lines = task and #task.lines > 0
    b.add(has_lines and (fold_glyph(fold) .. " ") or "  ", "TaskuiGuide")
    local glyph, hl = status_glyph(task and task.status or "Pending")
    b.add(glyph .. " ", hl)
    b.add(row.name, task and task.status == "Failed" and "TaskuiFailed" or "TaskuiTask")
    if task and task.note and task.note ~= "" then
      b.add("  " .. task.note, "TaskuiSkipped")
    end
    anchor(b, M.duration(task and task.duration_ms), "TaskuiDuration", width)
    return b.text, b.spans
  end

  -- An output line, with the rail that ties it to the command above it.
  local line = row.line
  b.add(rail(row), "TaskuiGuide")
  b.add(("%4d "):format(line.index + 1), "TaskuiGuide")
  if line.command then
    local run = state.runs[row.root]
    local task = run and run.tasks[row.name]
    -- The command's own verdict: ✓ once the next one started, ✗ on the one
    -- that took the task down, ▶ while it is still going. Positional, exactly
    -- as the TUI derives it, because go-task announces a command and never
    -- announces its end.
    local status = "Running"
    if row.later_command then
      status = "Ok"
    elseif task then
      status = task.status
    end
    local glyph, hl = status_glyph(status)
    b.add(glyph .. " ", hl)
    b.add(g.command .. " ", "TaskuiGuide")
    b.add(line.text, "TaskuiCommand")
  else
    if row.under_command then
      b.add((row.last_of_command and g.guide_last or g.guide_vertical) .. "   ", "TaskuiGuide")
    else
      b.add("    ", nil)
    end
    b.add(line.text, nil)
  end
  return b.text, b.spans
end

--- Renders every row.
---@param rows table[]
---@param width integer
---@return string[] lines, table[][] spans
function M.render(rows, width)
  local lines, spans = {}, {}
  for i, row in ipairs(rows) do
    local text, row_spans = M.render_row(row, width)
    lines[i] = text
    spans[i] = row_spans
  end
  return lines, spans
end

--- The panel's title line: the project, and what is going on in it.
---@return string
function M.header()
  local project = vim.fn.fnamemodify(require("taskui.config").project(), ":t")
  local running, failed = 0, 0
  for _, run in pairs(state.runs) do
    if run.status == "running" then
      running = running + 1
    elseif run.status == "failed" then
      failed = failed + 1
    end
  end
  local parts = { project }
  if running > 0 then
    table.insert(parts, ("%s %d running"):format(M.glyphs.running, running))
  end
  if failed > 0 then
    table.insert(parts, ("%s %d failed"):format(M.glyphs.failed, failed))
  end
  table.insert(parts, ("%d tasks"):format(#state.tasks))
  return table.concat(parts, "  ·  ")
end

return M
