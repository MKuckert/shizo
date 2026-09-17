# Website

The presentation site is a single-page landing page built by a minimal Go
static-site generator that lives in `cmd/ssg/`. The generator compiles HTML
templates and SCSS into a ready-to-serve `public/` directory with no runtime
dependencies beyond Go itself.

## Build and preview

```sh
make site                                       # build to public/
cd cmd/ssg && go run . -root ../.. serve        # build + serve on localhost:8080
```

`make site` runs the generator's `build` command, which recreates `public/`
from scratch on every invocation. The `serve` command does the same and then
starts Go's stdlib `net/http.FileServer` on port 8080 for local preview.
There is no live-reload — refresh the browser after rebuilding.

## Source layout

```
cmd/ssg/
  main.go           single-file SSG
  go.mod / go.sum   separate Go module (one dep: go-scss/scss)

site/
  templates/
    layout.html     base HTML layout (head, body wrapper)
  content/
    index.html      landing page (raw HTML fragment, inserted as .Content)
  assets/scss/
    main.scss       styles — variables, layout, code blocks, responsive
  static/           static assets copied verbatim to public/ (favicon, images)

public/             generated output (gitignored)
```

## How the generator works

The build pipeline has three stages, each a plain function in `main.go`:

1. **renderPages** — parses `site/templates/layout.html` as a Go
   `html/template`, reads each `.html` file in `site/content/`, and executes
   the template with the file's body as `{{.Content}}`. Content files are raw
   HTML fragments, not templates themselves.

2. **compileSCSS** — compiles `site/assets/scss/main.scss` to
   `public/css/main.css` using `go-scss/scss` (pure Go, CGO=0) in compressed
   mode. SCSS variables control the color palette, fonts, and responsive
   breakpoint.

3. **copyStatic** — copies everything in `site/static/` to the output root.

## Dependencies

The SSG has its own `go.mod` in `cmd/ssg/`, separate from the CLI's module in
`src/`. The single external dependency is
[go-scss/scss](https://github.com/go-scss/scss), a pure-Go SCSS compiler.
If it ever falls short, shelling out to `dart-sass` is the documented fallback
(see `docs/research/static-site-generator.md`).

## Deployment

`public/` is the directory GitLab Pages serves. The CI deploy job ships with
the goreleaser/CI backlog item, not separately. Until then, preview locally
via `serve`.
