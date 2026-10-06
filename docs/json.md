# JSON output

`vb --json`, the MCP tool `branch_map` with `format: "json"`, and the web API (`/api/map`) share one schema.
It is versioned by `schemaVersion`.
New fields may be added within a version; renaming or removing a field bumps it.
Empty and false fields are omitted.

## Map

| Field | Type | Description |
| --- | --- | --- |
| `schemaVersion` | number | Currently `1`. |
| `tool` | string | The vb version that produced the map. |
| `generatedAt` | string | RFC 3339 time. |
| `state` | string | Token for `--since` and `changes_since`; it changes whenever anything relevant changes. |
| `repo` | object | See [Repo](#repo). |
| `github` | object | `status` (`ok`, `cached`, `offline`, `unauthenticated`, `error`, `disabled`, `not-github`), `message`, `fetchedAt`. |
| `branches` | array | See [Branch](#branch), in tree order (depth first from the trunks). |
| `overlaps` | array | See [Overlap](#overlap). |
| `attention` | array | See [Issue](#issue), most urgent first. |
| `warnings` | array | Non-fatal notes, such as an unknown config key. |

## Repo

| Field | Type | Description |
| --- | --- | --- |
| `name` | string | Directory name of the repository. |
| `root` | string | Work tree root. |
| `head` | string | Current branch; empty when detached. |
| `headDetached` | boolean | HEAD is detached. |
| `headOid` | string | Commit at HEAD. |
| `trunks` | array | Trunk branch names, primary first. |
| `remote` | object | `name`, `url`, `host`, `owner`, `repo`. |
| `gitVersion` | string | Installed git version. |
| `source` | string | `local`, `all`, or `remote`. |

## Branch

| Field | Type | Description |
| --- | --- | --- |
| `name` | string | Branch name; remote-only branches are prefixed with the remote, such as `origin/feat/x`. |
| `ref` | string | Full ref name. |
| `remoteOnly` | boolean | Exists only on the remote. |
| `tip`, `subject`, `author`, `authorEmail`, `committedAt` | | The tip commit. |
| `trunk` | boolean | Is a trunk. |
| `head` | boolean | Checked out in this work tree. |
| `parent` | string | Inferred parent branch. |
| `parentSource` | string | `config`, `git-town`, `graphite`, `git-machete`, `pr`, `ancestry`, `reflog`, or `trunk`. |
| `parentConfidence` | string | `high`, `medium`, or `low`. |
| `children` | array | Child branch names. |
| `depth` | number | Depth in the tree; trunks are 0. |
| `ahead`, `behind` | number | Commits ahead of and behind the parent. |
| `trunkAhead`, `trunkBehind` | number | The same against the branch's trunk. |
| `upstream` | object | `name`, `ahead`, `behind`, `gone`. |
| `worktree` | object | `path`, `display`, `main`, `staged`, `unstaged`, `untracked`, `conflicted`. |
| `stashes` | number | Stash entries made on this branch. |
| `pr` | object | See [PR](#pr). |
| `merged` | object | `how` (`ancestry`, `pr`, `squash`, `rebase`) and `into`. |
| `empty` | boolean | Created but has no commits. |
| `agent` | string | Coding agent that created the branch, such as `claude`. |
| `agentEvidence` | string | `branch name`, `worktree path`, `author`, or `co-author`. |
| `forecast` | object | `target`, `clean`, and conflicting `files` for a merge into the parent. |
| `files` | array | Files changed since the fork point with the parent (when overlap analysis ran). |
| `flags` | array | The flags described in the README, most urgent first. |

## PR

| Field | Type | Description |
| --- | --- | --- |
| `number`, `title`, `url` | | Identity. |
| `state` | string | `open`, `merged`, or `closed`. |
| `draft` | boolean | Is a draft. |
| `base` | string | Base branch. |
| `headOid` | string | Head commit on GitHub. |
| `review` | string | `approved`, `changes_requested`, `review_required`, or empty. |
| `ci` | string | `pass`, `fail`, `pending`, or `none`. |
| `checks` | array | `name`, `status` (`pass`, `fail`, `pending`, `skipped`), `url`; failures first. |
| `mergeable` | string | `clean`, `conflicting`, or `unknown`. |
| `additions`, `deletions`, `changedFiles` | number | Size. |
| `author`, `updatedAt`, `mergedAt` | | Metadata. |

## Overlap

| Field | Type | Description |
| --- | --- | --- |
| `a`, `b` | string | The two branches. |
| `files` | array | Files both change (ignored patterns excluded). |
| `conflict` | boolean | Merging them is predicted to conflict. |
| `conflictChecked` | boolean | Whether the conflict check ran (it is capped by `overlap.max_pairs`). |

## Issue

| Field | Type | Description |
| --- | --- | --- |
| `kind` | string | `ci_failing`, `changes_requested`, `conflict`, `pr_conflict`, `collision`, `diverged`, `parent_merged`, `restack`, `pr_base_mismatch`, `overlap`, `upstream_gone`, `commits_after_merge`, `ready`, `old_base`, `unpushed`, `stale`, `deletable`, or `merged_checked_out`. |
| `severity` | string | `high`, `medium`, `ready`, or `low`. |
| `branch` | string | The branch concerned. |
| `other` | string | A second branch, when relevant (parent, overlapping branch). |
| `message` | string | Human-readable description. |
| `hint` | string | A suggested command. |

## Delta

`vb --since <state> --json` returns:

```json
{
  "since": "9bad6f74f9",
  "state": "1c0ffee123",
  "changes": [
    { "kind": "changed", "branch": "feat/ui", "detail": "tip 1a2b3c4d->5e6f7a8b; ahead/behind +2/-1 -> +3/-1" },
    { "kind": "attention", "detail": "resolved: high #13 CI failing: lint" }
  ]
}
```

`kind` is `added`, `removed`, `changed`, or `attention`.
