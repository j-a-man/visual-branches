// vb web view: wires the header, graph, and panel to the local API.

import { Graph } from "./graph";
import { Panel, type Tab } from "./panel";
import type { BranchMap, Detail, MapResponse, Theme } from "./types";
import { age, h, icon, plural, storage } from "./util";

type ThemeMode = "auto" | "light" | "dark";

interface State {
  data: MapResponse | null;
  selected: string;
  showMerged: boolean;
  remote: boolean;
  query: string;
  themeMode: ThemeMode;
  live: boolean;
  details: Map<string, Detail>;
}

const state: State = {
  data: null,
  selected: storage.get("vb.selected") ?? "",
  showMerged: storage.get("vb.merged") !== "0",
  remote: storage.get("vb.remote") === "1",
  query: "",
  themeMode: (storage.get("vb.theme") as ThemeMode | null) ?? "auto",
  live: false,
  details: new Map(),
};

const graph = new Graph({
  select: (name) => select(name),
  open: (name) => openPR(name),
});

const panel = new Panel({
  select: (name) => select(name, true),
  tab: (tab) => setTab(tab),
});

// Header.
const title = h("h1", { class: "repo" });
const meta = h("span", { class: "meta" });
const liveDot = h("span", { class: "live", title: "Live updates" });
const search = h("input", {
  class: "search",
  type: "search",
  placeholder: "Filter branches",
  "aria-label": "Filter branches",
  spellcheck: "false",
  autocomplete: "off",
}) as HTMLInputElement;
const mergedToggle = toggle("Merged", () => state.showMerged, (v) => {
  state.showMerged = v;
  storage.set("vb.merged", v ? "1" : "0");
  void load();
});
const remoteToggle = toggle("Remote", () => state.remote, (v) => {
  state.remote = v;
  storage.set("vb.remote", v ? "1" : "0");
  state.details.clear();
  void load();
});
const themeButton = h("button", { class: "icon-button", title: "Theme", "aria-label": "Change theme" });
const exportLink = h("a", { class: "icon-button", title: "Download SVG", "aria-label": "Download SVG", download: "" }, icon("download"));

const header = h(
  "header",
  { class: "topbar" },
  h("div", { class: "title" }, title, meta, liveDot),
  h("div", { class: "controls" }, search, mergedToggle.el, remoteToggle.el, themeButton, exportLink),
);

const zoom = h(
  "div",
  { class: "zoom" },
  h("button", { class: "icon-button", title: "Zoom out", "aria-label": "Zoom out", onclick: () => graph.zoomBy(1 / 1.2) }, icon("minus")),
  h("button", { class: "icon-button", title: "Fit (f)", "aria-label": "Fit to screen", onclick: () => graph.fit() }, icon("fit")),
  h("button", { class: "icon-button", title: "Zoom in", "aria-label": "Zoom in", onclick: () => graph.zoomBy(1.2) }, icon("plus")),
);

const legend = h(
  "div",
  { class: "legend" },
  ...[
    ["danger", "blocking"],
    ["warning", "attention"],
    ["ready", "ready"],
    ["merged", "merged"],
  ].map(([cls, label]) => h("span", {}, h("i", { class: `swatch status-${cls}` }), label ?? "")),
);

const notice = h("div", { class: "notice", hidden: true });
const stage = h("main", { class: "stage" }, graph.el, zoom, legend, notice);
const app = h("div", { class: "app" }, header, h("div", { class: "body" }, stage, panel.el));
document.body.replaceChildren(app);

function toggle(label: string, get: () => boolean, set: (v: boolean) => void): { el: HTMLButtonElement; sync(): void } {
  const el = h("button", { class: "toggle", "aria-pressed": "false" }, label) as HTMLButtonElement;
  const sync = () => {
    el.setAttribute("aria-pressed", String(get()));
  };
  el.addEventListener("click", () => {
    set(!get());
    sync();
  });
  sync();
  return { el, sync };
}

function query(extra: Record<string, string> = {}): string {
  const params = new URLSearchParams(extra);
  if (!state.showMerged) params.set("merged", "0");
  if (state.remote) params.set("source", "all");
  const q = params.toString();
  return q ? `?${q}` : "";
}

async function load(): Promise<void> {
  try {
    const res = await fetch(`/api/map${query()}`);
    if (!res.ok) throw new Error(await res.text());
    const data = (await res.json()) as MapResponse;
    const changed = state.data?.map.state !== data.map.state;
    state.data = data;
    if (changed) state.details.clear();
    notice.hidden = true;
    render();
  } catch (err) {
    notice.hidden = false;
    notice.textContent = `Could not load the branch map: ${err instanceof Error ? err.message : String(err)}`;
  }
}

function render(): void {
  const data = state.data;
  if (!data) return;
  const m = data.map;
  document.title = `${m.repo.name} · vb`;
  title.textContent = m.repo.name;
  meta.textContent = summary(m);
  exportLink.setAttribute("href", `/api/export/svg${query(currentThemeIsLight() ? { theme: "light" } : {})}`);
  applyTheme();
  graph.render(data.layout);
  graph.filter(state.query);
  const names = new Set(data.layout.nodes.map((n) => n.name));
  if (state.selected && !names.has(state.selected)) state.selected = "";
  if (!state.selected && m.repo.head && names.has(m.repo.head) && panel.tab === "branch") state.selected = m.repo.head;
  graph.select(state.selected);
  renderPanel();
}

function summary(m: BranchMap): string {
  const n = m.branches.filter((b) => !b.trunk).length;
  const prs = m.branches.filter((b) => b.pr?.state === "open").length;
  const parts = [m.repo.trunks.join(", "), `${n} ${plural(n, "branch", "branches")}`];
  if (m.github.status === "ok" || m.github.status === "cached") {
    parts.push(`${prs} open ${plural(prs, "PR", "PRs")}`);
    if (m.github.fetchedAt) {
      const a = age(m.github.fetchedAt);
      parts.push(a === "now" ? "github just now" : `github ${a} ago`);
    }
  } else if (m.github.status === "unauthenticated") {
    parts.push("run gh auth login for PRs");
  }
  return parts.join(" · ");
}

function renderPanel(): void {
  const data = state.data;
  if (!data) return;
  panel.renderTabs(data.map, state.selected);
  switch (panel.tab) {
    case "attention":
      panel.showAttention(data.map);
      break;
    case "overlaps":
      panel.showOverlaps(data.map);
      break;
    case "branch":
      void showBranch();
      break;
  }
}

async function showBranch(): Promise<void> {
  const name = state.selected;
  if (!name) {
    panel.showNoSelection();
    return;
  }
  const cached = state.details.get(name);
  if (cached) {
    panel.showDetail(cached, state.data?.remoteUrl);
    return;
  }
  panel.showLoading(name);
  try {
    const res = await fetch(`/api/branch${query({ name })}`);
    if (!res.ok) throw new Error(await res.text());
    const d = (await res.json()) as Detail;
    state.details.set(name, d);
    if (state.selected === name && panel.tab === "branch") panel.showDetail(d, state.data?.remoteUrl);
  } catch (err) {
    if (state.selected === name) panel.showNoSelection();
    console.error(err);
  }
}

function select(name: string, reveal = false): void {
  state.selected = name;
  storage.set("vb.selected", name);
  graph.select(name, reveal);
  if (name) {
    panel.setTab("branch");
  } else if (panel.tab === "branch") {
    panel.setTab("attention");
  }
  renderPanel();
}

function setTab(tab: Tab): void {
  panel.setTab(tab);
  renderPanel();
}

function openPR(name: string): void {
  const b = state.data?.map.branches.find((x) => x.name === name);
  if (b?.pr) window.open(b.pr.url, "_blank", "noreferrer");
}

// Theme: follow the system by default; the button cycles auto, light, dark.
const media = window.matchMedia("(prefers-color-scheme: dark)");
media.addEventListener("change", () => applyTheme());

function currentThemeIsLight(): boolean {
  const data = state.data;
  if (!data) return false;
  if (data.themes.mode === "fixed") return !data.themes.dark.dark;
  if (state.themeMode === "auto") return !media.matches;
  return state.themeMode === "light";
}

function applyTheme(): void {
  const data = state.data;
  if (!data) return;
  const t: Theme = currentThemeIsLight() ? data.themes.light : data.themes.dark;
  const root = document.documentElement.style;
  for (const [key, value] of Object.entries(t)) {
    if (typeof value === "string" && value.startsWith("#")) root.setProperty(`--${key}`, value);
  }
  document.documentElement.dataset["scheme"] = t.dark ? "dark" : "light";
  const mode = data.themes.mode === "fixed" ? "fixed" : state.themeMode;
  themeButton.replaceChildren(icon(mode === "light" ? "sun" : mode === "dark" ? "moon" : "auto"));
  themeButton.title = mode === "fixed" ? `Theme: ${t.name} (set in config)` : `Theme: ${mode}`;
  themeButton.toggleAttribute("disabled", data.themes.mode === "fixed");
}

themeButton.addEventListener("click", () => {
  const order: ThemeMode[] = ["auto", "light", "dark"];
  state.themeMode = order[(order.indexOf(state.themeMode) + 1) % order.length] ?? "auto";
  storage.set("vb.theme", state.themeMode);
  applyTheme();
  exportLink.setAttribute("href", `/api/export/svg${query(currentThemeIsLight() ? { theme: "light" } : {})}`);
});

search.addEventListener("input", () => {
  state.query = search.value;
  graph.filter(state.query);
});

// Keyboard: / filter, j k move, f fit, enter opens the PR, esc clears.
document.addEventListener("keydown", (ev) => {
  const typing = document.activeElement === search;
  if (ev.key === "Escape") {
    if (typing || search.value) {
      search.value = "";
      state.query = "";
      graph.filter("");
      search.blur();
    } else {
      select("");
    }
    return;
  }
  if (typing || ev.metaKey || ev.ctrlKey || ev.altKey) return;
  const names = graph.nodeNames();
  const i = names.indexOf(state.selected);
  switch (ev.key) {
    case "/":
      ev.preventDefault();
      search.focus();
      break;
    case "j":
    case "ArrowDown":
      ev.preventDefault();
      select(names[Math.min(names.length - 1, i + 1)] ?? "", true);
      break;
    case "k":
    case "ArrowUp":
      ev.preventDefault();
      select(names[Math.max(0, i - 1)] ?? "", true);
      break;
    case "f":
      graph.fit();
      break;
    case "Enter":
      if (state.selected) openPR(state.selected);
      break;
    case "1":
      setTab("attention");
      break;
    case "2":
      setTab("branch");
      break;
    case "3":
      setTab("overlaps");
      break;
  }
});

// Live updates: the server pushes an event whenever the map changes.
function connect(): void {
  const events = new EventSource("/api/events");
  events.addEventListener("open", () => {
    state.live = true;
    liveDot.classList.add("on");
    liveDot.title = "Live: updates as branches change";
  });
  events.addEventListener("changed", () => void load());
  events.addEventListener("error", () => {
    state.live = false;
    liveDot.classList.remove("on");
    liveDot.title = "Disconnected; retrying";
  });
}

void load();
connect();
setInterval(() => {
  if (state.data) meta.textContent = summary(state.data.map);
}, 30_000);
