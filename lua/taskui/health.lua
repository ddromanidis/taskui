-- lua/taskui/health.lua
--
-- :checkhealth taskui

local config = require("taskui.config")

local M = {}

local health = vim.health or require("health")
local start = health.start or health.report_start
local ok = health.ok or health.report_ok
local warn = health.warn or health.report_warn
local error_ = health.error or health.report_error

function M.check()
  start("taskui")

  local binary = config.options.binary

  if vim.fn.executable(binary) == 0 then
    error_(("%q not found on PATH"):format(binary), {
      "go install github.com/ddromanidis/taskui@latest",
      "or set the binary option to an absolute path",
    })
    return
  end
  ok(("found %s"):format(vim.fn.exepath(binary)))

  local version = vim.fn.system({ binary, "--version" })
  if vim.v.shell_error ~= 0 then
    error_(("%s --version failed: %s"):format(binary, vim.trim(version)))
    return
  end
  ok(vim.trim(version))

  -- The plugin is a front end over --json and --quickfix; a build without them
  -- would fail one silent call at a time instead of saying so here.
  local help = vim.fn.system({ binary, "--help" })
  if vim.v.shell_error ~= 0 then
    error_(("%s --help failed: %s"):format(binary, vim.trim(help)))
  else
    local missing = {}
    for _, flag in ipairs({ "--json", "--quickfix", "--list" }) do
      if not help:find(flag, 1, true) then
        table.insert(missing, flag)
      end
    end
    if #missing > 0 then
      error_(("the installed binary has no %s"):format(table.concat(missing, ", ")), {
        "reinstall with: go install github.com/ddromanidis/taskui@latest",
      })
    else
      ok("binary supports --list --json, --run --json and --quickfix")
    end
  end

  -- taskui runs `task`; without it there is nothing to front.
  if vim.fn.executable("task") == 0 then
    error_("go-task is not on PATH", { "https://taskfile.dev/installation/" })
  else
    ok(vim.trim(vim.fn.system({ "task", "--version" })))
  end

  -- Neovim forwards mouse events to a terminal program that asks for them, and taskui
  -- asks. With 'mouse' empty there is nothing to forward, and the wheel scrolls the buffer
  -- taskui is drawn into instead — which looks like taskui ignoring the mouse.
  if vim.o.mouse == "" then
    warn("'mouse' is empty, so the wheel will scroll this buffer rather than taskui", {
      "set mouse=a",
      "or scroll with the keyboard, which is unaffected",
    })
  else
    ok(("mouse=%s — the wheel reaches taskui"):format(vim.o.mouse))
  end

  local project = config.project()
  local found = false
  for _, name in ipairs({ "Taskfile.yml", "Taskfile.yaml", "taskfile.yml", "taskfile.yaml" }) do
    if vim.fn.filereadable(project .. "/" .. name) == 1 then
      found = true
      break
    end
  end
  if found then
    ok(("Taskfile in %s"):format(project))
  else
    warn(("no Taskfile in %s"):format(project), {
      "open Neovim in a project that has one,",
      "or set the project option to where yours is",
    })
  end
end

return M
