// Small DOM and formatting helpers. No framework: the view is simple enough
// that direct DOM updates stay readable and fast.

type Attrs = Record<string, string | number | boolean | undefined | EventListener>;
type Child = Node | string | null | undefined | false;

export function h<K extends keyof HTMLElementTagNameMap>(tag: K, attrs: Attrs = {}, ...children: Child[]): HTMLElementTagNameMap[K] {
  const el = document.createElement(tag);
  applyAttrs(el, attrs);
  append(el, children);
  return el;
}

const SVG_NS = "http://www.w3.org/2000/svg";

export function s<K extends keyof SVGElementTagNameMap>(tag: K, attrs: Attrs = {}, ...children: Child[]): SVGElementTagNameMap[K] {
  const el = document.createElementNS(SVG_NS, tag);
  applyAttrs(el, attrs);
  append(el, children);
  return el;
}

function applyAttrs(el: Element, attrs: Attrs): void {
  for (const [key, value] of Object.entries(attrs)) {
    if (value === undefined || value === false) continue;
    if (typeof value === "function") {
      el.addEventListener(key.replace(/^on/, "").toLowerCase(), value);
    } else if (value === true) {
      el.setAttribute(key, "");
    } else {
      el.setAttribute(key, String(value));
    }
  }
}

function append(el: Element, children: Child[]): void {
  for (const c of children) {
    if (c === null || c === undefined || c === false) continue;
    el.append(typeof c === "string" ? document.createTextNode(c) : c);
  }
}

/** Compact relative age: now, 5m, 3h, 4d, 2w, 5mo, 1y. */
export function age(iso: string | undefined, now: number = Date.now()): string {
  if (!iso) return "";
  const d = Math.max(0, now - Date.parse(iso)) / 1000;
  if (d < 60) return "now";
  if (d < 3600) return `${Math.floor(d / 60)}m`;
  if (d < 86400) return `${Math.floor(d / 3600)}h`;
  if (d < 14 * 86400) return `${Math.floor(d / 86400)}d`;
  if (d < 60 * 86400) return `${Math.floor(d / (7 * 86400))}w`;
  if (d < 365 * 86400) return `${Math.floor(d / (30 * 86400))}mo`;
  return `${Math.floor(d / (365 * 86400))}y`;
}

export function plural(n: number, one: string, many: string): string {
  return n === 1 ? one : many;
}

export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}

export const storage = {
  get(key: string): string | null {
    try {
      return localStorage.getItem(key);
    } catch {
      return null;
    }
  },
  set(key: string, value: string): void {
    try {
      localStorage.setItem(key, value);
    } catch {
      // Storage can be unavailable (private mode); preferences are optional.
    }
  },
};

/** Inline icons, drawn on a 16px grid with a 1.5px stroke. */
export function icon(name: "sun" | "moon" | "auto" | "copy" | "check" | "download" | "plus" | "minus" | "fit" | "external"): SVGSVGElement {
  const paths: Record<typeof name, string> = {
    sun: "M8 11a3 3 0 1 0 0-6 3 3 0 0 0 0 6ZM8 1.5v1.5M8 13v1.5M1.5 8H3M13 8h1.5M3.4 3.4l1.1 1.1M11.5 11.5l1.1 1.1M3.4 12.6l1.1-1.1M11.5 4.5l1.1-1.1",
    moon: "M13.5 9.5A5.5 5.5 0 0 1 6.5 2.5a5.5 5.5 0 1 0 7 7Z",
    auto: "M8 2a6 6 0 1 0 0 12A6 6 0 0 0 8 2Zm0 0v12",
    copy: "M5.5 5.5V3.5a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v6a1 1 0 0 1-1 1h-2M3.5 5.5h6a1 1 0 0 1 1 1v6a1 1 0 0 1-1 1h-6a1 1 0 0 1-1-1v-6a1 1 0 0 1 1-1Z",
    check: "M3 8.5l3 3 7-7",
    download: "M8 2.5v8M4.5 7 8 10.5 11.5 7M3 13.5h10",
    plus: "M8 3v10M3 8h10",
    minus: "M3 8h10",
    fit: "M2.5 6V3.5a1 1 0 0 1 1-1H6M10 2.5h2.5a1 1 0 0 1 1 1V6M13.5 10v2.5a1 1 0 0 1-1 1H10M6 13.5H3.5a1 1 0 0 1-1-1V10",
    external: "M9.5 2.5h4v4M13.5 2.5 7.5 8.5M11.5 9v3.5a1 1 0 0 1-1 1h-7a1 1 0 0 1-1-1v-7a1 1 0 0 1 1-1H7",
  };
  return s(
    "svg",
    { viewBox: "0 0 16 16", width: 16, height: 16, fill: "none", stroke: "currentColor", "stroke-width": 1.5, "stroke-linecap": "round", "stroke-linejoin": "round", "aria-hidden": "true" },
    s("path", { d: paths[name] }),
  );
}
