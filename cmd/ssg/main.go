package main

import (
	"flag"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/go-scss/scss"
)

var (
	siteDir string
	outDir  string
	version = ""
)

func main() {
	root := flag.String("root", ".", "project root directory")
	flag.StringVar(&version, "version", "",
		"release version substituted for {{VERSION}} in content (default: latest reachable git tag)")
	flag.Parse()

	siteDir = filepath.Join(*root, "site")
	outDir = filepath.Join(*root, "public")

	args := flag.Args()
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: ssg [-root DIR] [-version V] <build|serve>")
		os.Exit(2)
	}
	if version == "" {
		version = mustDescribeVersion(*root)
	}
	switch args[0] {
	case "build":
		build()
	case "serve":
		build()
		serve()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", args[0])
		os.Exit(2)
	}
}

// mustDescribeVersion returns the latest reachable release tag (e.g. "v0.5")
// so the site's release link can't drift from the repo. Content files carry
// {{VERSION}} placeholders that renderPages substitutes; failing here keeps a
// version-less build from shipping a dead link.
func mustDescribeVersion(root string) string {
	out, err := exec.Command("git", "-C", root, "describe", "--tags", "--abbrev=0").Output()
	if err != nil {
		log.Fatal("site: cannot determine version from git tags; pass -version explicitly")
	}
	v := strings.TrimSpace(string(out))
	if v == "" {
		log.Fatal("site: git describe returned no version; pass -version explicitly")
	}
	return v
}

func build() {
	os.RemoveAll(outDir)
	must(os.MkdirAll(outDir, 0o755))

	renderPages()
	compileSCSS()
	copyStatic()

	log.Println("site built -> " + outDir + "/")
}

func renderPages() {
	layoutPath := filepath.Join(siteDir, "templates", "layout.html")
	tmpl := template.Must(template.ParseFiles(layoutPath))

	contentDir := filepath.Join(siteDir, "content")
	entries, err := os.ReadDir(contentDir)
	must(err)

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".html") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(contentDir, e.Name()))
		must(err)
		body = []byte(strings.ReplaceAll(string(body), "{{VERSION}}", version))

		outPath := filepath.Join(outDir, e.Name())
		f, err := os.Create(outPath)
		must(err)
		must(tmpl.Execute(f, map[string]any{
			"Content": template.HTML(body),
		}))
		f.Close()
		log.Printf("  page: %s", e.Name())
	}
}

func compileSCSS() {
	entry := filepath.Join(siteDir, "assets", "scss", "main.scss")
	res, err := scss.Compile(entry, &scss.Options{
		Style:     scss.Compressed,
		LoadPaths: []string{filepath.Join(siteDir, "assets", "scss")},
	})
	must(err)

	cssDir := filepath.Join(outDir, "css")
	must(os.MkdirAll(cssDir, 0o755))
	must(os.WriteFile(filepath.Join(cssDir, "main.css"), []byte(res.CSS), 0o644))
	log.Println("  scss: main.css")
}

func copyStatic() {
	staticDir := filepath.Join(siteDir, "static")
	if _, err := os.Stat(staticDir); os.IsNotExist(err) {
		return
	}
	filepath.WalkDir(staticDir, func(path string, d fs.DirEntry, err error) error {
		must(err)
		rel, _ := filepath.Rel(staticDir, path)
		dest := filepath.Join(outDir, rel)
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		data, err := os.ReadFile(path)
		must(err)
		return os.WriteFile(dest, data, 0o644)
	})
}

func serve() {
	addr := ":8080"
	log.Printf("serving %s on http://localhost%s", outDir, addr)
	log.Fatal(http.ListenAndServe(addr, http.FileServer(http.Dir(outDir))))
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
