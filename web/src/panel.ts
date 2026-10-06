// The side panel: what needs attention, the selected branch in detail, and
// overlapping branches.

import type { BranchMap, Detail, Issue, Overlap } from "./types";
import { age, copyText, h, icon, plural } from "./util";

export type Tab = "attention" | "branch" | "overlaps";

export interface PanelEvents {
  select(name: string): void;
  tab(tab: Tab): void;
}

const severityOrder: Record<string, number> = { high: 0, medium: 1, ready: 2, low: 3 };
const severityLabel: Record<string, string> = { high: "Blocking", medium: "Needs attention", ready: "Ready", low: "Housekeeping" };

export class Panel {
  readonly el: HTMLElement;
  private readonly tabs: HTMLElement;
  private readonly body: HTMLElement;
  private current: Tab = "attention";

  constructor(private readonly events: PanelEvents) {
    this.tabs = h("nav", { class: "tabs", role: "tablist" });
    this.body = h("div", { class: "panel-body" });
    this.el = h("aside", { class: "panel" }, this.tabs, this.body);
  }

  get tab(): Tab {
    return this.current;
  }

  setTab(tab: Tab): void {
    this.current = tab;
  }

  renderTabs(m: BranchMap, selected: string): void {
    const attention = (m.attention ?? []).filter((i) => i.severity !== "low").length;
    const overlaps = (m.overlaps ?? []).length;
    const items: [Tab, string, number | null][] = [
      ["attention", "Needs you", attention],
      ["branch", selected || "Branch", null],
      ["overlaps", "Overlaps", overlaps],
    ];
    this.tabs.replaceChildren(
      ...items.map(([id, label, count]) =>
        h(
          "button",
          {
            class: `tab${id === this.current ? " active" : ""}`,
            role: "tab",
            "aria-selected": String(id === this.current),
            onclick: () => this.events.tab(id),
            title: label,
          },
          h("span", { class: "tab-label" }, label),
          count ? h("span", { class: "count" }, String(count)) : null,
        ),
      ),
    );
  }

  showAttention(m: BranchMap): void {
    const issues = [...(m.attention ?? [])].sort((a, b) => (severityOrder[a.severity] ?? 9) - (severityOrder[b.severity] ?? 9));
    if (issues.length === 0) {
      this.body.replaceChildren(empty("Nothing needs you", "Every branch is in a good state."));
      return;
    }
    const groups = new Map<string, Issue[]>();
    for (const is of issues) {
      const list = groups.get(is.severity) ?? [];
      list.push(is);
      groups.set(is.severity, list);
    }
    const out: HTMLElement[] = [];
    for (const [sev, list] of groups) {
      out.push(h("h3", { class: "group" }, severityLabel[sev] ?? sev, h("span", { class: "group-count" }, String(list.length))));
      for (const is of list) out.push(this.issue(is));
    }
    this.body.replaceChildren(...out);
  }

  private issue(is: Issue): HTMLElement {
    return h(
      "article",
      { class: `issue sev-${is.severity}` },
      h("span", { class: "dot", "aria-hidden": "true" }),
      h(
        "div",
        { class: "issue-main" },
        h("p", { class: "issue-msg" }, is.message),
        h(
          "div",
          { class: "issue-refs" },
          branchChip(is.branch, this.events),
          is.other && is.kind !== "pr_conflict" ? branchChip(is.other, this.events) : null,
        ),
        is.hint ? command(is.hint) : null,
      ),
    );
  }

  showOverlaps(m: BranchMap): void {
    const list = m.overlaps ?? [];
    if (list.length === 0) {
      this.body.replaceChildren(empty("No overlaps", "No two unrelated branches edit the same files."));
      return;
    }
    this.body.replaceChildren(...list.map((o) => this.overlap(o)));
  }

  private overlap(o: Overlap): HTMLElement {
    return h(
      "article",
      { class: `overlap${o.conflict ? " conflict" : ""}` },
      h(
        "div",
        { class: "overlap-head" },
        branchChip(o.a, this.events),
        h("span", { class: "overlap-sep" }, o.conflict ? "conflicts with" : "overlaps"),
        branchChip(o.b, this.events),
      ),
      h("ul", { class: "files" }, ...o.files.map((f) => h("li", {}, f))),
    );
  }

  showLoading(name: string): void {
    this.body.replaceChildren(h("p", { class: "muted pad" }, `Loading ${name}…`));
  }

  showNoSelection(): void {
    this.body.replaceChildren(empty("No branch selected", "Click a branch in the map, or use j and k."));
  }

  showDetail(d: Detail, remoteUrl: string | undefined): void {
    const b = d.branch;
    const rows: [string, Node | string][] = [];
    if (b.parent) {
      const conf = b.parentConfidence && b.parentConfidence !== "high" ? h("span", { class: "warn" }, ` ${b.parentConfidence} confidence`) : null;
      rows.push(["parent", h("span", {}, branchChip(b.parent, this.events), h("span", { class: "muted" }, ` ${sourceLabel(b.parentSource)}`), conf ?? "")]);
    }
    rows.push(["tip", h("span", {}, h("code", {}, b.tip.slice(0, 7)), ` ${b.subject}`, h("span", { class: "muted" }, ` · ${b.author}, ${age(b.committedAt)} ago`))]);
    if (b.merged) {
      rows.push(["merged", h("span", { class: "merged" }, `${b.merged.how} into ${b.merged.into}`)]);
    } else if (!b.trunk && b.parent) {
      const parts = [];
      if (b.ahead) parts.push(`↑${b.ahead}`);
      if (b.behind) parts.push(`↓${b.behind}`);
      rows.push(["commits", parts.length ? `${parts.join(" ")} vs ${b.parent}` : `even with ${b.parent}`]);
    }
    if (b.upstream) {
      const u = b.upstream;
      let state = "in sync";
      if (u.gone) state = "deleted on remote";
      else if (u.ahead && u.behind) state = `diverged: ${u.ahead} to push, ${u.behind} to pull`;
      else if (u.ahead) state = `${u.ahead} to push`;
      else if (u.behind) state = `${u.behind} to pull`;
      rows.push(["remote", h("span", {}, u.name, h("span", { class: "muted" }, ` · ${state}`))]);
    } else if (!b.trunk && !b.remoteOnly) {
      rows.push(["remote", h("span", { class: "muted" }, "not pushed")]);
    }
    if (b.worktree) {
      const w = b.worktree;
      const dirty = [
        w.staged ? `${w.staged} staged` : "",
        w.unstaged ? `${w.unstaged} modified` : "",
        w.untracked ? `${w.untracked} untracked` : "",
        w.conflicted ? `${w.conflicted} conflicted` : "",
      ].filter(Boolean);
      const where = w.display === "." ? h("span", {}, "here") : h("code", {}, w.display);
      rows.push(["worktree", h("span", {}, where, dirty.length ? h("span", { class: "warn" }, ` · ${dirty.join(", ")}`) : h("span", { class: "muted" }, " · clean"))]);
    }
    if (b.agent) rows.push(["agent", h("span", { class: "agent" }, b.agent)]);
    if (b.forecast) {
      rows.push([
        "merge",
        b.forecast.clean
          ? h("span", { class: "ok" }, `✓ merges cleanly into ${b.forecast.target}`)
          : h("span", { class: "bad" }, `✗ conflicts with ${b.forecast.target}`, b.forecast.files?.length ? h("span", { class: "muted" }, `: ${b.forecast.files.join(", ")}`) : ""),
      ]);
    }

    const sections: HTMLElement[] = [];
    const tags = [b.head ? "current" : "", b.trunk ? "trunk" : "", b.remoteOnly ? "remote only" : ""].filter(Boolean);
    sections.push(
      h(
        "header",
        { class: "detail-head" },
        h("h2", {}, b.name),
        tags.length ? h("div", { class: "tags" }, ...tags.map((t) => h("span", { class: "tag" }, t))) : null,
      ),
    );
    sections.push(h("dl", { class: "facts" }, ...rows.flatMap(([k, v]) => [h("dt", {}, k), h("dd", {}, v)])));

    if (b.pr) {
      const pr = b.pr;
      const state = pr.draft && pr.state === "open" ? "draft" : pr.state;
      const review = pr.review ? pr.review.replace("_", " ") : "";
      sections.push(
        h(
          "section",
          { class: "pr" },
          h(
            "a",
            { class: "pr-title", href: pr.url, target: "_blank", rel: "noreferrer" },
            h("span", { class: `pr-num state-${state}` }, `#${pr.number}`),
            h("span", {}, pr.title),
            icon("external"),
          ),
          h("p", { class: "muted small" }, [state, review, pr.mergeable === "conflicting" ? `conflicts with ${pr.base}` : "", `+${pr.additions} −${pr.deletions} in ${pr.changedFiles} ${plural(pr.changedFiles, "file", "files")}`].filter(Boolean).join(" · ")),
          pr.checks?.length
            ? h("ul", { class: "checks" }, ...pr.checks.map((c) => h("li", { class: `check check-${c.status}` }, h("span", { class: "check-icon" }, c.status === "pass" ? "✓" : c.status === "fail" ? "✗" : c.status === "pending" ? "●" : "–"), c.name)))
            : null,
        ),
      );
    } else if (remoteUrl && b.upstream && !b.trunk) {
      sections.push(h("p", { class: "small pad-x" }, h("a", { href: `${remoteUrl}/compare/${encodeURIComponent(b.name)}?expand=1`, target: "_blank", rel: "noreferrer" }, "Open a pull request ", icon("external"))));
    }

    if (d.issues?.length) {
      sections.push(h("h3", { class: "section" }, "Needs you"), ...d.issues.map((is) => this.issue(is)));
    }
    if (d.overlaps?.length) {
      sections.push(h("h3", { class: "section" }, "Overlaps"), ...d.overlaps.map((o) => this.overlap(o)));
    }
    if (d.commits?.length) {
      sections.push(
        h("h3", { class: "section" }, d.compareTo ? `Commits not on ${d.compareTo}` : "Recent commits"),
        h(
          "ol",
          { class: "commits" },
          ...d.commits.map((c) => h("li", {}, h("code", {}, c.short), h("span", { class: "subject" }, c.subject), h("span", { class: "muted" }, age(c.committedAt)))),
        ),
        d.moreCommits ? h("p", { class: "muted small pad-x" }, `and ${d.moreCommits} more`) : h("span"),
      );
    }
    if (d.files?.length) {
      sections.push(
        h("h3", { class: "section" }, `Files `, h("span", { class: "ok" }, `+${d.additions}`), " ", h("span", { class: "bad" }, `−${d.deletions}`)),
        h(
          "ul",
          { class: "file-stats" },
          ...d.files.slice(0, 40).map((f) =>
            h(
              "li",
              {},
              h("span", { class: `fstat fstat-${f.status}` }, f.status),
              h("span", { class: "path", title: f.path }, f.path),
              f.binary ? h("span", { class: "muted" }, "binary") : h("span", { class: "delta" }, h("span", { class: "ok" }, `+${f.additions}`), " ", h("span", { class: "bad" }, `−${f.deletions}`)),
            ),
          ),
        ),
        d.files.length > 40 ? h("p", { class: "muted small pad-x" }, `and ${d.files.length - 40} more`) : h("span"),
      );
    }
    this.body.replaceChildren(...sections);
    this.body.scrollTop = 0;
  }
}

/** How the parent was determined, in words. */
function sourceLabel(source: string | undefined): string {
  switch (source) {
    case "config":
      return "pinned";
    case "pr":
      return "from PR base";
    case "ancestry":
      return "from history";
    case "reflog":
      return "from reflog";
    case "trunk":
      return "trunk";
    case undefined:
      return "";
    default:
      return `from ${source}`;
  }
}

function branchChip(name: string, events: PanelEvents): HTMLElement {
  return h("button", { class: "chip", onclick: () => events.select(name), title: `Show ${name}` }, name);
}

function command(cmd: string): HTMLElement {
  const btn = h("button", { class: "copy", title: "Copy command", "aria-label": "Copy command" }, icon("copy"));
  btn.addEventListener("click", async () => {
    if (await copyText(cmd)) {
      btn.replaceChildren(icon("check"));
      btn.classList.add("copied");
      setTimeout(() => {
        btn.replaceChildren(icon("copy"));
        btn.classList.remove("copied");
      }, 1200);
    }
  });
  return h("div", { class: "cmd" }, h("code", {}, cmd), btn);
}

function empty(title: string, text: string): HTMLElement {
  return h("div", { class: "empty" }, h("p", { class: "empty-title" }, title), h("p", { class: "muted" }, text));
}
