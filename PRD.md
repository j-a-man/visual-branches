# visual-branches (`vb`) - Product Requirements

Status: v0.1 build
Owner: Jaylin (j-a-man)
Last updated: 2026-09-23

## 1. Summary

`vb` is a map of every branch in a git repository.
It shows how branches relate to each other, what state each one is in, and what needs attention, in one screen.
It works on any repository with zero setup and never changes anything unless explicitly asked.
It renders beautifully for humans (terminal tree, interactive TUI, local web view) and compactly for coding agents (token-budgeted text, JSON, MCP server).

The core idea: branches are the nodes, not commits.
`git log --graph` shows commits and becomes unreadable past a handful of branches.
Nobody shows "my 12 branches, how they relate, and which ones need me" on a single screen.

## 2. Problem

Developers juggle more branches than ever.
Stacked pull requests are now a first-class GitHub workflow, and parallel coding agents (Claude Code, Codex, Cursor, Copilot) each create their own branches and worktrees.

Today, answering basic questions requires many commands and a lot of mental assembly:

- Which branch is this one based on, and has its parent moved since?
- Which of my branches have open PRs, and are they approved, failing CI, or conflicting?
- Which branches are already merged (including squash merges) and safe to delete?
- Which worktree has which branch checked out, and is it dirty?
- Are two branches (often two agents) editing the same files and about to collide?

Coding agents face the same problem with a cost attached.
An agent orienting itself in a repository typically runs `git status`, `git branch -vv`, `git log --graph --all`, `git worktree list`, `gh pr list`, and `gh pr view`/`gh pr checks` per PR.
That is many round trips and thousands of tokens of mostly irrelevant output.

## 3. Goals

1. One command answers "what is going on with my branches" in under a second on a warm cache.
2. Zero setup: works on any repository, any workflow, without adopting a stack tool or writing metadata.
3. Read-only by default: safe to run anywhere, safe for agents to auto-approve.
4. First-class agent interface that measurably reduces tokens and round trips versus the equivalent git and gh commands.
5. Beautiful by default, with a curated theme and pixel-level care, so it earns its place in a README screenshot.
6. Customizable to each user and team through layered configuration.

## 4. Non-goals

- Managing stacks (creating, restacking, submitting).
  GitHub now ships native stacked PRs and `gh stack`; Graphite and git-town exist.
  `vb` complements them and reads their metadata instead of competing.
- Orchestrating agents or creating worktrees.
  Tools like Conductor, Crystal, and Superset do that; `vb` is what they and their agents can call.
- Replacing a commit graph viewer or a full git client.
- Hosting providers other than GitHub in v1.
  The provider boundary is an interface so GitLab and Bitbucket can be added later.

## 5. Users

- **The branch juggler.**
  A developer with several feature branches, some stacked, some stale, some merged.
  Wants a fast overview and cleanup.
- **The agent supervisor.**
  A developer running several coding agents in parallel worktrees.
  Wants to see which agent branches are done, stuck, or colliding.
- **The coding agent.**
  Needs a compact, accurate picture of repository state before acting, and pre-flight checks before editing files or pushing.
- **The team.**
  Wants a branch map posted on pull requests and a shared per-repo configuration.

## 6. Principles

- **A map, not a manager.**
  Every mutating action is explicit, confirmed, and limited to obvious cases (switch, delete merged branch).
- **Evidence over guessing.**
  Every inferred relationship records its source and confidence.
- **Minimal output.**
  Show only what differs from the default state; color does the work, not icons.
- **One core, many surfaces.**
  All surfaces render the same model built by a single engine.
- **Fast and cached.**
  Batch git calls, one GraphQL request, and caches keyed by commit ids.

## 7. Functional requirements

### 7.1 Branch model

| ID | Requirement |
| --- | --- |
| M1 | Enumerate local branches; optionally remote-only branches (`--all`) or remote branches as the primary set (`--source remote`, used in CI). |
| M2 | Detect trunk branches automatically (`origin/HEAD`, then `main`, `master`, `trunk`, `develop`); allow configuration of multiple trunks. |
| M3 | Infer each branch's parent with a recorded source and confidence. |
| M4 | Compute ahead/behind versus the parent and versus the upstream. |
| M5 | Detect the upstream state: in sync, unpushed, behind, diverged, gone, never pushed. |
| M6 | Detect worktree checkouts and their dirty state (staged, unstaged, untracked, conflicted). |
| M7 | Count stashes per branch. |
| M8 | Detect merged branches by ancestry, by merged PR, by rebase (per-commit patch ids), and by squash (combined patch id). |
| M9 | Detect branches with no commits yet so they are not reported as merged. |
| M10 | Detect agent-created branches by branch name, commit trailers, and worktree path, with configurable rules. |

### 7.2 Parent inference

Git does not record which branch a branch was created from.
`vb` combines signals in priority order:

1. Explicit override in `vb` config (`[parents]`) or git config (`branch.<name>.vbparent`).
2. Stack tool metadata: git-town (`git-town-branch.<name>.parent`), Graphite (`refs/branch-metadata/<name>`), git-machete (`.git/machete`).
3. The base branch of the branch's open pull request.
4. Commit ancestry: the candidate branch that shares the most unmerged commits with this branch, preferring candidates whose tip is an ancestor.
5. The reflog creation entry (`branch: Created from X`) to break ties, including branches with identical tips.
6. The closest trunk.

The result is always a forest rooted at trunks, with cycles broken deterministically.
When the pull request base disagrees with ancestry, `vb` reports it (`pr_base_mismatch`).

### 7.3 GitHub layer

| ID | Requirement |
| --- | --- |
| G1 | Authenticate by reusing the GitHub CLI login or `GH_TOKEN`/`GITHUB_TOKEN`; never ask for a token. |
| G2 | Fetch all open PRs in one GraphQL query: number, title, URL, draft, state, base, head, review decision, CI rollup with failing check names, mergeability, size, author. |
| G3 | Look up merged and closed PRs for local branches without an open PR, batched with query aliases. |
| G4 | Cache responses with a configurable TTL; work offline from cache (`--offline`). |
| G5 | Degrade gracefully: no auth, no network, or a non-GitHub remote simply hides PR data with one dim notice. |
| G6 | Support GitHub Enterprise hosts detected from the remote URL. |

### 7.4 Analysis

| ID | Requirement |
| --- | --- |
| A1 | Conflict forecast: predict whether each branch merges cleanly into its parent using `git merge-tree`, without touching any working tree. |
| A2 | Overlap detection: find unrelated branches that change the same files, with configurable ignore globs, and predict whether they conflict with each other. |
| A3 | Restack detection: the parent moved or was merged. |
| A4 | Staleness: no commits for a configurable duration. |
| A5 | Attention list: a ranked list of issues (CI failing, changes requested, conflicts, collisions, diverged, restack, ready to merge, deletable). |
| A6 | Every issue carries a concrete suggested command. |
| A7 | Cleanup candidates: merged branches that are not checked out anywhere. |

### 7.5 Surfaces

| ID | Surface | Requirement |
| --- | --- | --- |
| S1 | `vb` | Terminal tree with aligned columns, themed colors, header and attention footer; adapts to terminal width. |
| S2 | `vb show <branch>` | Full detail: parent evidence, commits, files, PR checks, conflicts, overlaps, hints. |
| S3 | `vb status` | Current branch summary; `--line` for prompts and status lines. |
| S4 | `vb tui` | Interactive tree with detail pane, filter, and confirmed actions (switch, open PR, copy, delete merged). |
| S5 | `vb web` | Local web view (127.0.0.1 only) with graph, detail panel, attention list, overlap matrix, live refresh. |
| S6 | `vb export` | Mermaid, Graphviz DOT, SVG, and JSON exports. |
| S7 | `vb cleanup` | Lists deletable branches; `--apply` deletes after confirmation. |
| S8 | `vb mcp` | MCP server over stdio with read-only tools. |
| S9 | Agent output | `--agent` compact text, `--json` stable schema, `--since` deltas, `--focus`, `--max-tokens`. |
| S10 | Claude Code plugin | Skill, MCP server, SessionStart context hook, status line recipe. |
| S11 | GitHub Action | Posts or updates a branch map comment on pull requests and writes a job summary. |
| S12 | `vb config`, `vb themes`, `vb doctor`, `vb parent` | Configuration management, theme preview, environment diagnostics, parent overrides. |

### 7.6 Agent interface

- `--agent` output uses indentation for parentage, ASCII symbols, one legend line, and only non-default fields.
- `--max-tokens N` degrades gracefully: drop hints, collapse merged and stale branches, then keep only branches with issues and the current lineage.
- Every agent and JSON output includes a `state` token; `--since <token>` returns only what changed.
- MCP tools: `branch_map`, `branch_detail`, `changes_since`, `check_conflicts`, `who_touches`, `cleanup_candidates`.
  All are annotated read-only so clients can auto-approve them.
- `who_touches` is the pre-flight check for parallel agents: given file paths, it returns which other branches modify them.

### 7.7 Configuration

Layered, later wins:

1. Built-in defaults.
2. User config: `$XDG_CONFIG_HOME/vb/config.toml`, `~/.config/vb/config.toml`, or `%AppData%\vb\config.toml` on Windows.
3. Team config committed in the repository: `.vb.toml`.
4. Personal repository config, not committed: `.git/vb.toml`.
5. Environment variables (`VB_CONFIG`, `VB_THEME`, `VB_OFFLINE`, `NO_COLOR`).
6. Command-line flags.

Customizable: trunks, remote, theme (built-in or custom palettes), icon set, visible columns, sort order, hidden branch globs, header, footer, hints, name width, staleness window, each analysis on or off, overlap ignore globs, GitHub mode and cache TTL, agent detection rules, parent overrides, agent output budget, TUI refresh, web port.

## 8. Non-functional requirements

| Area | Target |
| --- | --- |
| Performance | Warm run under 300 ms on a repository with 200 branches; cold run under 2 s excluding network. Measured on Windows (slowest process creation): about 400 ms warm for 12 local branches, and 1.2 s warm, 1.5 s cold without network, 5.5 s cold with GitHub for 254 remote branches of cli/cli. |
| Safety | No writes to refs, index, or working trees unless the user runs an explicit mutating command and confirms. |
| Portability | Windows, macOS, Linux; git 2.38 or newer, with graceful fallbacks for older features. |
| Distribution | Single static binary; `go install`, GitHub releases, Homebrew, Scoop, and `gh` extension. |
| Quality | `go vet`, `gofmt`, race-enabled tests on three operating systems, strict TypeScript for the web view. |
| Accessibility | `NO_COLOR` and `--ascii` produce fully legible output; web view meets contrast in light and dark. |

## 9. Success metrics

- Token reduction versus the baseline command set, measured by `tools/tokenbench` and published in the README.
- Time to first useful output on a fresh install: under one minute.
- GitHub stars, installs, and MCP/plugin adoption after launch.

## 10. Competitive landscape

| Category | Examples | Gap `vb` fills |
| --- | --- | --- |
| Commit graph viewers | `git log --graph`, tig, lazygit, VS Code Git Graph, GitKraken | Commit-level, noisy, little PR or CI context, not agent friendly. |
| Stack managers | GitHub stacked PRs and `gh stack`, Graphite, git-town, git-machete | Require adopting a workflow; `vb` reads their metadata and works without them. |
| Agent and worktree managers | Conductor, Crystal, Superset, Canopy Desktop, gwq | Manage sessions; no unified, read-only branch map with an agent API. |

## 11. Risks and mitigations

| Risk | Mitigation |
| --- | --- |
| Parent inference is wrong. | Show source and confidence, accept overrides, read stack tool metadata and PR bases. |
| Slow on large repositories. | Single batched git calls, bounded commit walks, OID-keyed caches, analysis limits in config. |
| GitHub rate limits. | One query for open PRs, aliased batches for merged lookups, TTL cache. |
| Conflict forecast writes objects. | Use `merge-tree --quiet` (no object writes) and only request file names for predicted conflicts. |
| Output drift breaks agents. | Versioned JSON schema and golden tests. |

## 12. Milestones

| Phase | Scope | Status |
| --- | --- | --- |
| 0 | Foundation: module, CI, fixture repository builder, golden tests. | Done |
| 1 | Local map: tree view, parent inference, ahead/behind, worktrees, merged detection, JSON and agent output. | Done |
| 2 | GitHub layer: PRs, reviews, CI, mergeability, caching. | Done |
| 3 | Agent layer: MCP server, deltas, token budget, Claude Code plugin, token benchmark. | Done |
| 4 | Analysis: conflict forecast, overlaps, attention ranking, cleanup. | Done |
| 5 | Interactive TUI. | Done |
| 6 | Web view, exports, GitHub Action, release automation. | Done |

Launch checklist, outside the code:

- Push the repository and tag `v0.1.0` so `go install` and the release workflow work.
- Create `j-a-man/homebrew-tap` and `j-a-man/scoop-bucket`, and add the `TAP_GITHUB_TOKEN` secret.
- Tag `v1` for the GitHub Action.
- Submit to awesome-go, awesome-mcp-servers, and the Claude Code plugin directory; post a Show HN with the README GIF or images.

## 13. Future

- Timeline (subway) view in the web UI.
- GitLab and Bitbucket providers.
- Team mode: branches by teammate, review load.
- Editor extensions that embed the web view.
