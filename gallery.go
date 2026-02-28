package main

import (
	"flag"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type Image struct {
	Name string
}

type pageData struct {
	Title       string
	ThumbHeight int
	Images      []Image
}

var tpl = template.Must(template.New("index").Parse(`
<!doctype html>
<html>
<head>
	<meta charset="utf-8">
	<title>{{.Title}}</title>
	<style>
		body { font-family: sans-serif; background: #111; color: #eee; margin: 0; padding: 16px; }
		h1 { margin-top: 0; }
		.grid { display: flex; flex-wrap: wrap; gap: 8px; }
		.grid img {
			height: {{.ThumbHeight}}px;
			width: auto;
			display: block;
		}
		a { color: #8cf; text-decoration: none; }
	</style>
</head>
<body>
<h1>{{.Title}}</h1>
<div class="grid">
{{range .Images}}
  <div>
    <a href="/imgs/{{.Name}}">
      <img src="/imgs/{{.Name}}" loading="lazy">
    </a>
  </div>
{{end}}
</div>
</body>
</html>
`))

func main() {
	// Flags with useful defaults
	dir := flag.String("dir", "~/media/good", "directory with images (can start with ~/)")
	listen := flag.String("listen", ":8080", "address to listen on (e.g. :8080, 127.0.0.1:9000)")
	title := flag.String("title", "Gallery", "page title")
	thumbHeight := flag.Int("thumb-height", 200, "thumbnail height in pixels")
	cacheMaxAge := flag.Int("cache-max-age", 31536000, "Cache-Control max-age in seconds for images")
	cacheImmutable := flag.Bool("cache-immutable", true, "add 'immutable' to Cache-Control for images")

	flag.Usage = func() {
		log.Printf("Simple image gallery\n\nUsage:\n  %s [options]\n\nOptions:\n", os.Args[0])
		flag.PrintDefaults()
	}

	flag.Parse()

	expandedDir, err := expandHome(*dir)
	if err != nil {
		log.Fatalf("expand dir: %v", err)
	}

	images, err := listImagesSorted(expandedDir)
	if err != nil {
		log.Fatalf("list images: %v", err)
	}

	fs := http.FileServer(http.Dir(expandedDir))
	http.Handle("/imgs/",
		http.StripPrefix("/imgs/",
			cacheControl(fs, *cacheMaxAge, *cacheImmutable),
		),
	)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		data := pageData{
			Title:       *title,
			ThumbHeight: *thumbHeight,
			Images:      images,
		}
		if err := tpl.Execute(w, data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	log.Printf("Serving gallery on %s (dir=%s)", *listen, expandedDir)
	log.Fatal(http.ListenAndServe(*listen, nil))
}

func listImagesSorted(dir string) ([]Image, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	type fileWithTime struct {
		name string
		mod  int64
	}

	var files []fileWithTime
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := filepath.Ext(e.Name())
		switch ext {
		case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".JPG", ".JPEG", ".PNG":
		default:
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fileWithTime{
			name: e.Name(),
			mod:  info.ModTime().UnixNano(),
		})
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].mod > files[j].mod
	})

	out := make([]Image, len(files))
	for i, f := range files {
		out[i] = Image{Name: f.name}
	}
	return out, nil
}

func cacheControl(h http.Handler, maxAge int, immutable bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cc := "public, max-age=" + strconv.Itoa(maxAge)
		if immutable {
			cc += ", immutable"
		}
		w.Header().Set("Cache-Control", cc)
		h.ServeHTTP(w, r)
	})
}

// expandHome expands "~/foo" to "/home/user/foo".
func expandHome(p string) (string, error) {
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, p[2:]), nil
	}
	return p, nil
}

