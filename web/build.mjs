// Builds the web view into ../internal/web/dist, which the Go binary embeds.
import { build, context } from "esbuild";
import { copyFileSync, mkdirSync, rmSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const out = join(here, "..", "internal", "web", "dist");
const watch = process.argv.includes("--watch");

rmSync(out, { recursive: true, force: true });
mkdirSync(out, { recursive: true });
copyFileSync(join(here, "src", "index.html"), join(out, "index.html"));
copyFileSync(join(here, "src", "favicon.svg"), join(out, "favicon.svg"));

const options = {
  entryPoints: { app: join(here, "src", "main.ts"), styles: join(here, "src", "styles.css") },
  outdir: out,
  bundle: true,
  minify: !watch,
  sourcemap: false,
  target: "es2022",
  format: "esm",
  legalComments: "none",
  logLevel: "info",
};

if (watch) {
  const ctx = await context(options);
  await ctx.watch();
} else {
  await build(options);
}
