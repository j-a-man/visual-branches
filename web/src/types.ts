// Types mirror the JSON produced by the Go model package (schemaVersion 1).

export interface Upstream {
  name: string;
  ahead: number;
  behind: number;
  gone?: boolean;
}

export interface Worktree {
  path: string;
  display: string;
  main?: boolean;
  staged?: number;
  unstaged?: number;
  untracked?: number;
  conflicted?: number;
}

export interface Check {
  name: string;
  status: "pass" | "fail" | "pending" | "skipped" | string;
  url?: string;
}

export interface PR {
  number: number;
  title: string;
  url: string;
  state: "open" | "merged" | "closed";
  draft?: boolean;
  base: string;
  headOid: string;
  review?: "approved" | "changes_requested" | "review_required";
  ci: "pass" | "fail" | "pending" | "none";
  checks?: Check[];
  mergeable?: "clean" | "conflicting" | "unknown";
  additions: number;
  deletions: number;
  changedFiles: number;
  author?: string;
  updatedAt: string;
}

export interface Merge {
  how: "ancestry" | "pr" | "squash" | "rebase";
  into: string;
}

export interface Forecast {
  target: string;
  clean: boolean;
  files?: string[];
}

export interface Branch {
  name: string;
  ref: string;
  remoteOnly?: boolean;
  tip: string;
  subject: string;
  author: string;
  committedAt: string;
  trunk?: boolean;
  head?: boolean;
  parent?: string;
  parentSource?: string;
  parentConfidence?: "high" | "medium" | "low";
  children?: string[];
  depth: number;
  ahead: number;
  behind: number;
  trunkAhead: number;
  trunkBehind: number;
  upstream?: Upstream;
  worktree?: Worktree;
  stashes?: number;
  pr?: PR;
  merged?: Merge;
  empty?: boolean;
  agent?: string;
  forecast?: Forecast;
  files?: string[];
  flags?: string[];
}

export interface Overlap {
  a: string;
  b: string;
  files: string[];
  conflict: boolean;
  conflictChecked: boolean;
}

export type Severity = "high" | "medium" | "ready" | "low";

export interface Issue {
  kind: string;
  severity: Severity;
  branch: string;
  other?: string;
  message: string;
  hint?: string;
}

export interface GitHubStatus {
  status: string;
  message?: string;
  fetchedAt?: string;
}

export interface Repo {
  name: string;
  root: string;
  head?: string;
  headDetached?: boolean;
  trunks: string[];
  gitVersion: string;
  source: string;
}

export interface BranchMap {
  schemaVersion: number;
  tool: string;
  generatedAt: string;
  state: string;
  repo: Repo;
  github: GitHubStatus;
  branches: Branch[];
  overlaps?: Overlap[];
  attention?: Issue[];
  warnings?: string[];
}

export interface LayoutNode {
  name: string;
  parent?: string;
  depth: number;
  x: number;
  y: number;
  w: number;
  h: number;
  status: "trunk" | "danger" | "warning" | "ready" | "merged" | "normal";
  label: string;
  meta: string;
  head?: boolean;
  agent?: string;
}

export interface LayoutEdge {
  from: string;
  to: string;
  d: string;
  status: string;
}

export interface Layout {
  nodes: LayoutNode[];
  edges: LayoutEdge[];
  width: number;
  height: number;
}

export interface Theme {
  name: string;
  dark: boolean;
  bg: string;
  surface: string;
  overlay: string;
  border: string;
  text: string;
  subtle: string;
  muted: string;
  tree: string;
  accent: string;
  success: string;
  warning: string;
  danger: string;
  info: string;
  merged: string;
  agent: string;
}

export interface MapResponse {
  map: BranchMap;
  layout: Layout;
  themes: { light: Theme; dark: Theme; mode: "auto" | "fixed" };
  remoteUrl?: string;
}

export interface DetailCommit {
  oid: string;
  short: string;
  subject: string;
  author: string;
  committedAt: string;
  coAuthors?: string[];
}

export interface DetailFile {
  path: string;
  status: string;
  additions: number;
  deletions: number;
  binary?: boolean;
}

export interface Detail {
  branch: Branch;
  lineage: string[] | null;
  commits: DetailCommit[] | null;
  moreCommits?: number;
  files: DetailFile[] | null;
  additions: number;
  deletions: number;
  issues?: Issue[];
  overlaps?: Overlap[];
  compareTo?: string;
}
