-- Configuration for `task lint:lua`. The plugin runs inside Neovim, whose API
-- arrives as the vim global rather than through require.
std = "luajit"
read_globals = { "vim" }
