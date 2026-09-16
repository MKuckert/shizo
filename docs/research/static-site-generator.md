# Static Site Generator — Research: HTML + SCSS/SASS, Go, minimal deps

Date: 2026-07 (research pass)
Goal: find possible, *simple* solutions for a static website generator with
- HTML as the base content/template format
- SCSS/SASS support in the pipeline
- Go executable, few dependencies (single static binary ideal)

---

## 1. Landscape summary

| Option | Lang | HTML-first? | SCSS support | Deps / weight | Verdict for this use case |
|---|---|---|---|---|---|
| **Roll your own (Go)** | Go | ✅ native | via `go-scss/scss` or shelling out to `sass` | 1–3 Go deps, one static binary | **Best fit** — simplest thing that does exactly this |
| **Hugo (extended)** | Go | ✅ (Go templates) | Built-in, but embedded LibSass **deprecated since v0.153** → now expects a Dart Sass binary on PATH | Single binary, but huge feature surface; SCSS needs external `sass` binary in practice | Overkill for a simple site, but the most battle-tested; SCSS story is now "bring your own dart-sass" |
| **Zola** | Rust | ✅ (Tera) | ❌ no SCSS (plain CSS only) | Single tiny binary, very fast | Great SSG, fails the SCSS requirement |
| **Eleventy** | JS/Node | ✅ | via npm `sass` package | Node toolchain, node_modules | Not Go; rejected by constraints |
| **altlimit/sitegen** | Go | ✅ HTML + MD sources, Go templates | ❌ built-in; but has **file handlers** — per-file custom build commands (you can wire `sass` as a handler) | Small project (~8 stars), single binary | Closest existing off-the-shelf Go SSG to the requirement; still young |
| **goranite / piuma / s3gen / huan / forge / twain** | Go | markdown-centric, Go templates | ❌ plain CSS only | Very small, minimal deps | Good as reference implementations for a DIY build; none ship SCSS |

Key insight: **no established Go SSG bundles first-class SCSS support.** Hugo is the only major one, and it has moved to "external Dart Sass binary" (its embedded LibSass was deprecated in v0.153). Everyone else is plain-CSS-only. So the realistic options are:

1. **DIY minimal Go generator** (recommended) — you control the pipeline, SCSS is one import away.
2. **Hugo extended** — if you want a full ecosystem and accept feature bloat + an external `sass` binary for SCSS.
3. **altlimit/sitegen** — if you want an existing Go SSG and are OK with wiring SCSS through its file-handler hook (or forking).

---

## 2. Recommended: build a minimal Go SSG (~300–600 LOC)

This is the "simple solution" that exactly matches the constraints. The whole
pipeline can be:

```
content/            # .html pages (with optional frontmatter) + .md if wanted
templates/          # layout.html, partials (Go html/template)
assets/scss/        # *.scss sources
config.json or config.yaml
        │
        ▼   single Go binary:  ssg build / ssg serve
public/             # static output
```

### Components & dependency choices

| Concern | Simple choice | Notes |
|---|---|---|
| Templating | **stdlib `html/template`** | HTML-first, auto-escaping, no dep. Same engine Hugo/sitegen use. |
| Frontmatter | `gopkg.in/yaml.v3` (or JSON to stay stdlib-only) | One small dep; or skip frontmatter for pure-HTML pages. |
| Markdown (optional) | `github.com/yuin/goldmark` | Only if you want `.md` alongside HTML. Zero-dep alternative: skip it. |
| **SCSS** | **`github.com/go-scss/scss`** (pure Go) *or* shell out to `sass` | See §3 — this is the crux. |
| Dev server (optional) | stdlib `http.FileServer` + fsnotify (`fsnotify/fsnotify`) | Live-reload via one small dep; or just rebuild on save. |
| Minify (optional) | `github.com/benmannaatwork/css` or skip | |

Realistic total: **1–3 third-party deps, one static Go binary, zero runtime
dependencies** (if you use the pure-Go SCSS compiler).

### Why this is simple
- Each stage is an independent function: `loadPages → renderTemplates → compileSCSS → copyStatic`.
- Easy to test, easy to extend (add MD later, add pagination later).
- You own the SCSS step: watch `assets/scss`, compile to `public/css/` with
  compressed output, cache-bust via hash if desired.

---

## 3. The SCSS-in-Go decision (the crux)

Three viable engines:

### A. `github.com/go-scss/scss` — pure Go, CGO=0 (most aligned with "little dependencies")
- Pure-Go Sass/SCSS compiler; output tracks **Dart Sass** (byte-for-byte on the
  common real-world surface; ~98.5% of sass-spec applicable cases, remaining
  misses are libm-ULP/stale-fixture irreducibles).
- Supports both `.scss` and indented `.sass`, expanded + compressed output,
  `@use/@forward`, mixins/functions/extend/media nesting, built-in modules
  (`sass:math`, `sass:color`, …).
- Simple API:
  ```go
  res, err := scss.Compile("assets/scss/main.scss", &scss.Options{
      Style:     scss.Compressed,
      LoadPaths: []string{"assets/scss"},
  })
  // res.CSS
  ```
- **Risk:** very new project (BSD-3, still young, minimal adoption). Not a
  problem for typical site CSS; fine to pin and test against your own styles.

### B. Shell out to `dart-sass` (the canonical compiler)
- Most correct & future-proof; exactly what Hugo does now (`bep/godartsass`
  spawns `dart-sass-embedded`).
- Cost: a second binary on PATH (npm `sass`, or standalone dart-sass release).
  Breaks the "single self-contained binary" property, but is trivial:
  ```go
  exec.Command("sass", "--embed-version=false", "main.scss:css/main.css", "--style=compressed")
  ```
- Good fallback/escape hatch if go-scss ever chokes on something.

### C. `bep/go-tocss` (the LibSass port Hugo used)
- **Not recommended** — the path Hugo itself deprecated in v0.153; maintenance
  has moved to external Dart Sass.

**Recommendation:** use **A (go-scss/scss)** for the single-binary goal, with
**B** as a documented fallback. (If you'd rather not depend on a young
library at all, B is the boring-safe choice: one extra binary in your
toolchain.)

---

## 4. If you'd rather not build: Hugo (extended)

- Single Go binary, fastest SSG in the business, huge ecosystem.
- HTML-first: templates are Go `html/template`; content can be `.html` (or md).
- SCSS: only in **hugo extended** edition; embedded LibSass deprecated (v0.153)
  → put `sass`/`dart-sass-embedded` on PATH and use
  `{{ $css := resources.Get "scss/main.scss" | toCSS }}`.
- Trade-offs: large config/feature surface, SCSS requires the external binary,
  you inherit Hugo's conventions (content dirs, layouts/, archetypes…).
- Choose this if the site will grow into a big blog/docs site; overkill for a
  handful of pages.

## 5. altlimit/sitegen (existing Go SSG, closest match)

- Go, single binary, HTML **and** MD sources, Go templates + helper functions,
  incremental builds, live reload, minify, sitemap templating.
- SCSS: not built in, but its **file handlers** let you attach a build command
  per file type (its own docs use `npm run build:css` as the example — swap in
  `sass`). Slightly hacky; forking to add go-scss would be a small change.
- Very small project (~8 stars, 75 commits) — evaluate maintenance risk.

---

## 6. Suggested next step (if DIY is chosen)

Scaffold in this repo:
1. `cmd/ssg/main.go` — flags: `-site dir`, `-out public`, `build|serve`.
2. `internal/site/` — page loader (frontmatter + body), template renderer,
   static copy.
3. `internal/scss/` — thin wrapper over go-scss (or exec of `sass`).
4. Example site under `example/` with a layout, 2–3 HTML pages, one SCSS file
   using variables/mixins/`@use`.
5. `go.mod` with only: go-scss/scss (+ yaml.v3, goldmark, fsnotify if wanted).

Estimated effort: a working build in a few hours; the entire generator is
small enough to read in one sitting.

## Sources
- Hugo SCSS docs: https://gohugo.io/functions/css/sass/ (LibSass deprecation v0.153; Dart Sass path)
- Hugo dartsass integration: https://pkg.go.dev/github.com/gohugoio/hugo/resources/resource_transformers/tocss/dartsass
- go-scss/scss: https://github.com/go-scss/scss (pure-Go, CGO=0, sass-spec conformance report)
- altlimit/sitegen: https://github.com/altlimit/sitegen (file handlers, Go templates)
- goranite: https://github.com/jchavarri/goranite (minimal Go SSG reference)
- Zola/Eleventy comparisons: 2025–26 SSG roundups (Zola = no SCSS; Eleventy = Node)
