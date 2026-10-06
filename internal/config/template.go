package config

// Template is the commented starter file written by `vb config init`.
// Every setting is shown with its default value, commented out.
const Template = `# vb configuration
# Docs: https://github.com/j-a-man/visual-branches#configuration
#
# Layers, later wins:
#   user     ~/.config/vb/config.toml (Windows: %AppData%\vb\config.toml)
#   team     <repo>/.vb.toml          (commit this to share with your team)
#   personal <repo>/.git/vb.toml      (never committed)
#   env      VB_CONFIG, VB_THEME, VB_ICONS, VB_OFFLINE, VB_GITHUB, NO_COLOR
#   flags    --theme, --ascii, --offline, ...
#
# Uncomment a line to change it.

# Long-lived base branches, primary first. Empty = auto-detect
# (origin/HEAD, then main, master, trunk, develop).
# trunks = ["main"]
# remote = "origin"

[display]
# theme = "rose-pine-moon"   # see: vb themes
# icons = "unicode"          # unicode | ascii
# columns = ["pr", "review", "ci", "ahead_behind", "age", "worktree", "flags"]
# show_merged = true         # list merged branches (marked deletable)
# show_remote = false        # include remote-only branches (same as --all)
# sort = "recent"            # recent | name | oldest
# header = true
# footer = true              # the "needs you" list
# hints = true               # suggested commands in the footer
# attention_limit = 8
# max_name_width = 44
# hide = ["dependabot/*", "renovate/*"]
# only_mine = false          # only branches whose tip you authored (same as --mine)

[analysis]
# stale_after = "30d"        # also: stale branches and closed PRs skip conflict and overlap analysis
# conflicts = true           # forecast merge conflicts with git merge-tree
# overlap = true             # find unrelated branches editing the same files
# squash_detection = true    # detect squash and rebase merges via patch ids
# worktree_status = true     # count dirty files in each worktree
# max_branches = 400
# max_commits = 50000
# squash_lookback = 1000     # trunk commits searched for squash merges
# old_base_threshold = 100   # flag branches this far behind trunk; 0 disables
# read_stack_metadata = true # read git-town, Graphite, and git-machete parents

[overlap]
# ignore = ["go.sum", "*.lock", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "bun.lockb", "Cargo.lock", "poetry.lock", "uv.lock", "Gemfile.lock", "composer.lock", "CHANGELOG.md"]
# check_conflicts = true
# max_pairs = 40             # overlapping pairs checked for real conflicts
# max_branches = 80          # most recently active branches compared

[github]
# enabled = "auto"           # auto | always | never
# cache_ttl = "60s"
# host = ""                  # GitHub Enterprise host; detected from the remote by default

[agent_output]
# max_tokens = 0             # default budget for --agent; 0 = unlimited
# hints = true
# legend = true

[status]
# Template for "vb status --line". Placeholders:
#   {branch} {ab} {pr} {ci} {review} {attention} {dirty} {parent}
# format = "{branch}{ab}{pr}{ci}{attention}"
# github = "cache"           # cache | auto | never

[tui]
# refresh = "30s"
# confirm_delete = true

[web]
# port = 7878
# open_browser = true
# theme = "auto"             # auto follows the system light/dark setting
# light_theme = "rose-pine-dawn"
# dark_theme = "rose-pine-moon"

# Pin a parent when inference guesses wrong. Also settable per branch with
# "vb parent <branch> <parent>" (stored in git config).
# [parents]
# "feat/ui" = "feat/api"

# Agent detection. Built-in rules exist for claude, codex, copilot, cursor,
# devin, jules, gemini, aider, and agent. Defining a rule replaces the
# built-in rule with the same name. Branch and path globs use * for any text.
# [agents.my-bot]
# branches = ["bot/*"]
# authors = ["(?i)my-bot"]
# trailers = ["(?i)my-bot"]
# paths = ["*/.bot-worktrees/*"]
#
# [agents.aider]
# disabled = true

# Custom themes inherit from "base" and override any role:
# bg surface overlay border text subtle muted tree accent success warning
# danger info merged agent. Values are #rrggbb or ANSI numbers 0-15.
# [themes.mine]
# base = "rose-pine-moon"
# accent = "#f6c177"
`
