# Presentation website

> BACKLOG: "Nice presentation website for the tool"

## Problem

bash-gate has thorough docs (README, `docs/`) but no landing page. A colleague
hearing about it for the first time has to read a dense README to understand
why they'd want it. A single-page advertisement site with visual hierarchy and
a clear call-to-action lowers that bar.

## Pick: single `index.html` + GitLab Pages

One HTML file with inline CSS, no JS, no build step. GitLab Pages serves it
from a `public/` directory on `gitlab.ssc-os.local`. Source lives in `site/`,
separate from Go code.

Rejected alternatives:

- **Static site generator (Hugo/Jekyll/MkDocs).** Adds a dependency, a theme to
  maintain, and a sync problem with the canonical `docs/` files. Justified at
  10+ pages; bash-gate has four docs and a README.
- **Framework (React/Astro/Next).** A landing page for a CLI tool doesn't need
  hydration, a bundler, or node_modules.
- **Hosted platform (GitBook/Notion/Docusaurus).** External account, sync
  problem with the repo docs.

## Design

**Aesthetic:** dark-mode "AI-era" landing page in the vein of nono.sh and
manifest.build. Near-black background, light text, teal/cyan accent color —
visual kinship with nono.sh, since bash-gate is designed to work with it.
Sans-serif body, monospace for code blocks. Generous vertical spacing, subtle
gradients/glows on accents. One responsive breakpoint.

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

- `site/index.html`: the single-file landing page described above.
- `.gitlab-ci.yml`: **deferred** — the pages deploy job ships with the
  goreleaser/CI backlog item, not separately. Until then, the file can be
  previewed by opening it locally.
- `BACKLOG.md`: check off the item, link this plan.

## Deliberately skipped

- **Multi-page docs.** The canonical docs live in `docs/`; the site links to
  them rather than duplicating them.
- **JS interactivity.** No dark/light toggle, no animated terminal replay, no
  search.
- **Analytics, custom domain, asset pipeline.** Not needed for an internal tool.
  Add any of these when there's a concrete reason.
- **Separate stylesheet.** Extract one if the file exceeds ~300 lines, not
  before.

## Open

- Whether to include a terminal screenshot or asciinema recording on the hero.
  Nice to have, not a blocker — slot it in later.
- Exact "Download" button URL once goreleaser lands (GitLab releases page).
