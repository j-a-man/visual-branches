# vb plugin for Claude Code

Gives Claude a compact, always-current map of your git branches.

## What it adds

| Component | What it does |
| --- | --- |
| MCP server `vb` | Read-only tools: `branch_map`, `branch_detail`, `changes_since`, `check_conflicts`, `who_touches`, `cleanup_candidates`. |
| Skill `branch-map` | Teaches Claude when to consult the map and how to read it. Invoke it directly with `/vb:branch-map`. |
| SessionStart hook | Adds a branch summary (about 500 tokens at most) to the context when a session starts, resumes, clears, or compacts. |

## Install

1. Install the `vb` binary so it is on your PATH (see the main README).
2. In Claude Code:

   ```
   /plugin marketplace add j-a-man/visual-branches
   /plugin install vb@visual-branches
   ```

3. Run `vb doctor` in a repository to confirm everything is found.

## Status line (optional)

Add to `~/.claude/settings.json`:

```json
{
  "statusLine": {
    "type": "command",
    "command": "vb status --claude-code"
  }
}
```

It shows the current branch, its position against its parent, the PR and CI state, and how many items need you.
It uses cached GitHub data by default, so it stays fast.
