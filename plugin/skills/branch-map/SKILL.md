---
name: branch-map
description: Read the repository's branch map with vb before working with git branches. Use it to learn which branch you are on and what it is based on, find stacked branches, check pull request, CI, and review status, spot merge conflicts before rebasing or pushing, find other branches or agents editing the same files, and clean up merged branches. Prefer it over running git branch, git log --graph, git worktree list, and gh pr commands one by one.
when_to_use: Starting work in a repository; before creating a branch, rebasing, merging, or pushing; before editing files when other agents or people work in parallel; when the user asks about branches, stacks, PRs, CI, worktrees, or cleanup.
allowed-tools: Bash(vb --agent), Bash(vb --agent *), Bash(vb show *), Bash(vb status), Bash(vb status *)
---

# Branch map with vb

`vb` builds a map of every branch in the repository: its parent (stacks are inferred), commits ahead and behind, worktree and uncommitted changes, pull request state, review decision, CI result, a merge-conflict forecast, and branches that edit the same files.
It is read-only unless a command explicitly says otherwise.

## Get the map

Use the MCP tools when they are available (they are read-only and need no approval):

- `branch_map` for the whole map, with `max_tokens` to cap the size and `focus` to limit it to one stack.
- `changes_since` with the `state` token from the last map, instead of re-reading the whole map, to see only what changed.
- `branch_detail` for one branch: commits, files, checks, overlaps, and issues.
- `who_touches` with file paths before editing them in a repository where other agents or people work in parallel.
- `check_conflicts` before pushing, merging, or rebasing.
- `cleanup_candidates` to list merged branches that can be deleted.

Without MCP, run the CLI:

```bash
vb --agent                    # compact map, about 20 tokens per branch
vb --agent --max-tokens 400   # degrade gracefully to a budget
vb --agent --since <state>    # only what changed since a previous map
vb show <branch> --agent      # one branch in detail
```

## Read the agent format

```
# vb repo=webapp trunk=main head=feat/auth-ui github=ok state=7f3a9c1d2e
main
  feat/auth-api +4 pr#142:open,approved,pass 20h
    feat/auth-ui* +2 -1 pr#143:open,review-required,fail(lint) 1d restack
attention:
- high: #143 CI failing: lint -> gh pr checks 143
- med: feat/auth-ui is 1 behind its parent feat/auth-api -> git rebase feat/auth-api feat/auth-ui
```

- Indentation shows parentage: a branch is the child of the nearest less-indented branch above it.
- `+N` and `-N` are commits ahead of and behind the parent, and `*` marks the branch checked out here.
- `pr#N:state,review,ci` summarizes the pull request.
- Flags: `conflict` (will conflict with its parent), `collision` (another branch edits the same files and will conflict), `overlap` (edits the same files), `restack` (parent moved), `parent-merged`, `diverged`, `unpushed=N`, `pull=N`, `dirty=N`, `gone` (remote branch deleted), `local` (never pushed), `stale`, `empty`.
- `parent?` means the parent was inferred with low confidence; confirm with `vb show <branch>` before acting on it.
- Items under `attention` are ranked by urgency and end with a suggested command after `->`.

## Rules

- Treat suggested commands as suggestions: explain them and ask before running anything that rewrites history, deletes branches, or pushes.
- Never run `vb cleanup --apply` or `git branch -D` without the user's explicit approval.
- Before editing a file that another branch also changes (`who_touches`, `overlap`, `collision`), tell the user and suggest coordinating or stacking on that branch.
- Pass the `state` token back through `changes_since` (or `vb --agent --since <state>`) to stay current cheaply during long tasks.
