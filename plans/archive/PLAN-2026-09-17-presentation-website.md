# Presentation website

> BACKLOG: "Nice presentation website for the tool"

## Problem

bash-gate has thorough docs (README, `docs/`) but no landing page. A colleague
hearing about it for the first time has to read a dense README to understand
why they'd want it. A single-page advertisement site with visual hierarchy and
a clear call-to-action lowers that bar.

## Pick: minimal Go SSG + SCSS + GitLab Pages

HTML templates + SCSS compiled by a small Go static-site generator (~300–600
LOC) that lives in the repo. No external runtime dependencies — the generator
is a single static binary using `html/template` from the stdlib and
[go-scss/scss](https://github.com/go-scss/scss) (pure Go, CGO=0) for SCSS
compilation. GitLab Pages serves the output from `public/` on
`gitlab.ssc-os.local`. Source lives in `site/`, separate from the CLI code.

Why not stick with a single `index.html` + inline CSS? SCSS buys reusable
variables and mixins for the accent palette, responsive breakpoints, and code
blocks — things that are tedious to maintain by hand once the stylesheet passes
~150 lines. The Go SSG keeps the build hermetic (one binary, no PATH deps) and
sets up the pipeline for a second page later without adding real complexity now.

See `docs/research/static-site-generator.md` for the full landscape survey.

Rejected alternatives:

- **Hugo (extended).** Battle-tested, but massive feature surface. Embedded
  LibSass deprecated since v0.153; SCSS now requires an external `dart-sass`
  binary on PATH — breaks the single-binary property. Overkill for a handful
  of pages.
- **Zola.** Excellent minimal SSG (Rust, single binary), but no SCSS support
  at all — plain CSS only. Fails the SCSS requirement.
- **altlimit/sitegen.** Closest existing Go SSG to the requirement; supports
  HTML + MD, Go templates, file handlers that can wire `sass`. Very small
  project (~8 stars), maintenance risk. SCSS only via external handler.
- **Framework (React/Astro/Next).** A landing page for a CLI tool doesn't need
  hydration, a bundler, or node_modules.
- **Hosted platform (GitBook/Notion/Docusaurus).** External account, sync
  problem with the repo docs.
- **Shell out to `dart-sass`.** Most correct SCSS compiler, but adds a second
  binary to the toolchain. Documented as a fallback if go-scss ever chokes.

## Design

**Aesthetic:** dark-mode "AI-era" landing page in the vein of nono.sh and
manifest.build. Near-black background, light text, teal/cyan accent color —
visual kinship with nono.sh, since bash-gate is designed to work with it.
Sans-serif body, monospace for code blocks. Generous vertical spacing, subtle
gradients/glows on accents. One responsive breakpoint. SCSS variables govern
the accent palette and breakpoint so the theme is trivial to adjust.

**Sections (4):**

1. **Hero.** Headline: *"Control what your AI agent can run."* Subtitle:
   *"Fail-closed Bash allowlisting for Claude Code."* Three CTAs:
   - "View on GitLab" → repo link.
   - `make install` snippet (copy-paste).
   - "Download" → dimmed/muted, labeled "coming soon" until goreleaser ships.

2. **How it works.** Five-step pipeline as a visual row, one sentence each:
   **Parse** → **Resolve** → **Sandbox check** → **Match rules** → **Decide.**

3. **Rules at a glance.** Dark code block with 3–4 real allow/deny rules showing
   `ANY`/`HAS`, plus one or two directives (`sandbox nono`, `trust /usr/bin`) to
   hint at the full DSL. Link to `docs/RULES.md` for the reference.

4. **Footer.** Repo link, license. "Designed for the nono sandbox" with a link
   to nono.sh.

## Changes

**SSG generator (new Go code):**

- `cmd/ssg/main.go` — entry point: `ssg build` compiles the site to `public/`,
  `ssg serve` adds a local file server for preview.
- `internal/site/` — page loader (reads HTML templates, optional frontmatter),
  template renderer (`html/template`), static-file copy.
- `internal/scss/` — thin wrapper over `go-scss/scss`: compile
  `site/assets/scss/main.scss` → `public/css/main.css` (compressed output).
- `go.mod` addition: `github.com/go-scss/scss` (+ `gopkg.in/yaml.v3` if
  frontmatter is needed). No other new deps.

**Site source:**

- `site/templates/layout.html` — base layout (head, body wrapper, footer).
- `site/content/index.html` — the landing page content (4 sections below).
- `site/assets/scss/main.scss` — styles; imports partials for variables, layout,
  code blocks, responsive.
- `site/static/` — any static assets (favicon, images if added later).
- Include a terminal screenshot or asciinema recording on the hero.

**Build output:**

- `public/` — generated output, gitignored. GitLab Pages serves this directory.

**Other:**

- `Makefile` — `make site` target (builds SSG then runs `ssg build`).
- `.gitlab-ci.yml`: **deferred** — the pages deploy job ships with the
  goreleaser/CI backlog item, not separately. Until then, preview locally via
  `ssg serve`.
- `BACKLOG.md`: check off the item, link this plan.

## Deliberately skipped

- **Multi-page docs.** The canonical docs live in `docs/`; the site links to
  them rather than duplicating them.
- **JS interactivity.** No dark/light toggle, no animated terminal replay, no
  search.
- **Analytics, custom domain.** Not needed for an internal tool. Add any of
  these when there's a concrete reason.
- **Markdown content support.** The SSG pipeline can add `goldmark` later if
  needed; skip it now since the only page is HTML.
- **Live-reload dev server.** `fsnotify` + WebSocket injection is a nice DX but
  not a blocker — manual refresh after `ssg build` is fine for one page.
- **CSS minification beyond SCSS compressed output.** `scss.Compressed` is
  sufficient; a dedicated minifier adds a dep for marginal gain.

## Open

- Exact "Download" button URL once goreleaser lands (GitLab releases page).
