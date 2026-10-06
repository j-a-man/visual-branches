// The branch graph: cards laid out by the Go server (render.ComputeLayout),
// drawn as SVG with pan and zoom.

import type { Layout, LayoutNode } from "./types";
import { s } from "./util";

export interface GraphEvents {
  select(name: string): void;
  open(name: string): void;
}

interface View {
  x: number;
  y: number;
  k: number;
}

const MIN_ZOOM = 0.25;
const MAX_ZOOM = 2.5;

export class Graph {
  readonly el: SVGSVGElement;
  private readonly world: SVGGElement;
  private layout: Layout = { nodes: [], edges: [], width: 0, height: 0 };
  private view: View = { x: 0, y: 0, k: 1 };
  private selected = "";
  private query = "";
  // Until the user pans or zooms, the map stays fitted to the viewport.
  private userMoved = false;
  private cards = new Map<string, SVGGElement>();
  private edges = new Map<string, SVGPathElement>();

  constructor(private readonly events: GraphEvents) {
    this.world = s("g");
    this.el = s("svg", { class: "graph", role: "img", "aria-label": "Branch map" }, this.world);
    this.bindPanZoom();
  }

  render(layout: Layout): void {
    this.layout = layout;
    this.world.replaceChildren();
    this.cards.clear();
    this.edges.clear();
    const edgeLayer = s("g", { class: "edges" });
    for (const e of layout.edges) {
      const path = s("path", { d: e.d, class: `edge edge-${e.status}` });
      this.edges.set(e.to, path);
      edgeLayer.append(path);
    }
    const nodeLayer = s("g", { class: "nodes" });
    for (const n of layout.nodes) {
      const card = this.card(n);
      this.cards.set(n.name, card);
      nodeLayer.append(card);
    }
    this.world.append(edgeLayer, nodeLayer);
    this.applySelection();
    this.applyQuery();
    if (this.userMoved) {
      this.applyView();
    } else {
      this.fit();
    }
  }

  private card(n: LayoutNode): SVGGElement {
    const g = s(
      "g",
      {
        class: `card status-${n.status}${n.head ? " head" : ""}`,
        transform: `translate(${n.x} ${n.y})`,
        tabindex: 0,
        role: "button",
        "aria-label": `${n.name}: ${n.meta}`,
        "data-name": n.name,
      },
      s("rect", { class: "card-bg", width: n.w, height: n.h, rx: 8 }),
      s("rect", { class: "card-bar", x: 6, y: 10, width: 3, height: n.h - 20, rx: 1.5 }),
      s("text", { class: "card-name", x: 20, y: 23 }, n.label),
      s("text", { class: "card-meta", x: 20, y: 41 }, n.meta),
    );
    if (n.label !== n.name) g.append(s("title", {}, n.name));
    g.addEventListener("click", (ev) => {
      ev.stopPropagation();
      this.events.select(n.name);
    });
    g.addEventListener("dblclick", (ev) => {
      ev.stopPropagation();
      this.events.open(n.name);
    });
    g.addEventListener("keydown", (ev) => {
      if (ev.key === "Enter" || ev.key === " ") {
        ev.preventDefault();
        this.events.select(n.name);
      }
    });
    return g;
  }

  select(name: string, reveal = false): void {
    this.selected = name;
    this.applySelection();
    if (reveal) this.reveal(name);
  }

  filter(query: string): void {
    this.query = query.trim().toLowerCase();
    this.applyQuery();
  }

  nodeNames(): string[] {
    return this.layout.nodes.map((n) => n.name);
  }

  private applySelection(): void {
    for (const [name, card] of this.cards) {
      card.classList.toggle("selected", name === this.selected);
    }
  }

  private applyQuery(): void {
    const q = this.query;
    for (const [name, card] of this.cards) {
      const dim = q !== "" && !name.toLowerCase().includes(q);
      card.classList.toggle("dim", dim);
      this.edges.get(name)?.classList.toggle("dim", dim);
    }
  }

  /** Scales and centers the whole map in the viewport. */
  fit(): void {
    const box = this.el.getBoundingClientRect();
    if (box.width === 0 || this.layout.width === 0) return;
    const pad = 32;
    const k = Math.min(1.25, Math.max(MIN_ZOOM, Math.min((box.width - pad * 2) / this.layout.width, (box.height - pad * 2) / this.layout.height)));
    this.view = {
      k,
      x: (box.width - this.layout.width * k) / 2,
      y: Math.max(pad / 2, (box.height - this.layout.height * k) / 2),
    };
    this.userMoved = false;
    this.applyView();
  }

  zoomBy(factor: number): void {
    const box = this.el.getBoundingClientRect();
    this.userMoved = true;
    this.zoomAt(box.width / 2, box.height / 2, factor);
  }

  private reveal(name: string): void {
    const n = this.layout.nodes.find((x) => x.name === name);
    if (!n) return;
    const box = this.el.getBoundingClientRect();
    const { x, y, k } = this.view;
    const left = n.x * k + x;
    const top = n.y * k + y;
    const margin = 48;
    let dx = 0;
    let dy = 0;
    if (left < margin) dx = margin - left;
    else if (left + n.w * k > box.width - margin) dx = box.width - margin - (left + n.w * k);
    if (top < margin) dy = margin - top;
    else if (top + n.h * k > box.height - margin) dy = box.height - margin - (top + n.h * k);
    if (dx !== 0 || dy !== 0) {
      this.view = { k, x: x + dx, y: y + dy };
      this.applyView();
    }
  }

  private zoomAt(px: number, py: number, factor: number): void {
    const { x, y, k } = this.view;
    const nk = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, k * factor));
    const f = nk / k;
    this.view = { k: nk, x: px - (px - x) * f, y: py - (py - y) * f };
    this.applyView();
  }

  private applyView(): void {
    const { x, y, k } = this.view;
    this.world.setAttribute("transform", `translate(${x.toFixed(1)} ${y.toFixed(1)}) scale(${k.toFixed(3)})`);
  }

  private bindPanZoom(): void {
    let drag: { x: number; y: number; vx: number; vy: number; moved: boolean } | null = null;
    this.el.addEventListener("pointerdown", (ev) => {
      if (ev.button !== 0 || (ev.target as Element).closest(".card")) return;
      drag = { x: ev.clientX, y: ev.clientY, vx: this.view.x, vy: this.view.y, moved: false };
      this.el.setPointerCapture(ev.pointerId);
      this.el.classList.add("panning");
    });
    this.el.addEventListener("pointermove", (ev) => {
      if (!drag) return;
      const dx = ev.clientX - drag.x;
      const dy = ev.clientY - drag.y;
      if (Math.abs(dx) + Math.abs(dy) > 3) {
        drag.moved = true;
        this.userMoved = true;
      }
      this.view = { ...this.view, x: drag.vx + dx, y: drag.vy + dy };
      this.applyView();
    });
    const end = (ev: PointerEvent) => {
      if (!drag) return;
      if (!drag.moved) this.events.select("");
      drag = null;
      this.el.releasePointerCapture(ev.pointerId);
      this.el.classList.remove("panning");
    };
    this.el.addEventListener("pointerup", end);
    this.el.addEventListener("pointercancel", end);
    // Wheel pans; ctrl or cmd + wheel (and trackpad pinch) zooms.
    this.el.addEventListener(
      "wheel",
      (ev) => {
        ev.preventDefault();
        this.userMoved = true;
        const box = this.el.getBoundingClientRect();
        if (ev.ctrlKey || ev.metaKey) {
          this.zoomAt(ev.clientX - box.left, ev.clientY - box.top, Math.exp(-ev.deltaY * 0.01));
        } else {
          this.view = { ...this.view, x: this.view.x - ev.deltaX, y: this.view.y - ev.deltaY };
          this.applyView();
        }
      },
      { passive: false },
    );
    new ResizeObserver(() => {
      if (!this.userMoved) this.fit();
    }).observe(this.el);
  }
}
