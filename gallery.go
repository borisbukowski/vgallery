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
	"time"

	"github.com/cozy/goexif2/exif"
)

type Image struct {
	Name       string
	CapturedAt time.Time
	MonthKey   string
	MonthLabel string
}

type MonthGroup struct {
	Key    string
	Label  string
	Images []Image
}

type IndexData struct {
	Title  string
	Months []MonthGroup
}

type MonthNav struct {
	Key   string
	Label string
}

type MonthPageData struct {
	Title       string
	ThumbHeight int
	Month       MonthGroup
	Prev        *MonthNav
	Next        *MonthNav
}

var indexTpl = template.Must(template.New("index").Parse(`<!doctype html>
<html>
<head>
	<meta charset="utf-8">
	<title>{{.Title}}</title>
	<style>
		body {
			font-family: sans-serif;
			background: #111;
			color: #eee;
			margin: 0;
			padding: 16px;
		}
		h1 {
			margin: 0 0 16px;
		}
		.months {
			display: flex;
			flex-direction: column;
			gap: 12px;
			max-width: 900px;
		}
		.month-link {
			display: flex;
			align-items: center;
			gap: 14px;
			padding: 12px 14px;
			background: #1b1b1b;
			border: 1px solid #333;
			border-radius: 8px;
			color: #8cf;
			text-decoration: none;
		}
		.month-link:hover {
			background: #222;
		}
		.preview {
			flex: 0 0 auto;
		}
		.preview img {
			display: block;
			width: 140px;
			height: 100px;
			object-fit: cover;
			border-radius: 6px;
			background: #222;
		}
		.info {
			display: flex;
			flex: 1 1 auto;
			justify-content: space-between;
			align-items: center;
			gap: 12px;
			min-width: 0;
		}
		.label {
			font-size: 1rem;
			color: #8cf;
		}
		.count {
			color: #aaa;
			white-space: nowrap;
		}
	</style>
</head>
<body>
	<h1>{{.Title}}</h1>

	<div class="months">
	{{range .Months}}
		<a class="month-link" href="/month/{{.Key}}">
			<div class="preview">
				<img src="/imgs/{{(index .Images 0).Name}}" alt="{{.Label}}" loading="lazy">
			</div>
			<div class="info">
				<span class="label">{{.Label}}</span>
				<span class="count">{{len .Images}} image{{if ne (len .Images) 1}}s{{end}}</span>
			</div>
		</a>
	{{else}}
		<p>No images found.</p>
	{{end}}
	</div>
</body>
</html>`))

var monthTpl = template.Must(template.New("month").Parse(`<!doctype html>
<html>
<head>
	<meta charset="utf-8">
	<title>{{.Title}} - {{.Month.Label}}</title>
	<style>
		body {
			font-family: sans-serif;
			background: #111;
			color: #eee;
			margin: 0;
			padding: 16px;
		}
		a {
			color: #8cf;
			text-decoration: none;
		}
		h1 {
			margin: 0 0 8px;
		}
		.topbar {
			margin-bottom: 12px;
		}
		.meta {
			color: #aaa;
			margin-bottom: 16px;
		}
		.nav {
			display: flex;
			flex-wrap: wrap;
			gap: 10px;
			margin-bottom: 18px;
		}
		.nav a, .nav span {
			background: #1b1b1b;
			border: 1px solid #333;
			border-radius: 8px;
			padding: 8px 12px;
		}
		.nav .disabled {
			color: #666;
		}
		.grid {
			display: flex;
			flex-wrap: wrap;
			gap: 8px;
		}
		.grid img {
			height: {{.ThumbHeight}}px;
			width: auto;
			display: block;
			border-radius: 4px;
			background: #222;
		}
	</style>
</head>
<body>
	<div class="topbar">
		<a href="/">[all months]</a>
	</div>

	<h1>{{.Month.Label}}</h1>
	<div class="meta">{{len .Month.Images}} image{{if ne (len .Month.Images) 1}}s{{end}}</div>

	<div class="nav">
		{{if .Prev}}
			<a href="/month/{{.Prev.Key}}">[older] {{.Prev.Label}}</a>
		{{else}}
			<span class="disabled">[older]</span>
		{{end}}

		{{if .Next}}
			<a href="/month/{{.Next.Key}}">{{.Next.Label}} [newer]</a>
		{{else}}
			<span class="disabled">[newer]</span>
		{{end}}
	</div>

	<div class="grid">
	{{range .Month.Images}}
		<div>
			<a href="/imgs/{{.Name}}">
				<img src="/imgs/{{.Name}}" alt="{{.Name}}" loading="lazy">
			</a>
		</div>
	{{end}}
	</div>
</body>
</html>`))

func main() {
	dir := flag.String("dir", "~/media/good", "directory with images (can start with ~/)")
	listen := flag.String("listen", ":8080", "address to listen on")
	title := flag.String("title", "Gallery", "page title")
	thumbHeight := flag.Int("thumb-height", 200, "thumbnail height in pixels")
	cacheMaxAge := flag.Int("cache-max-age", 31536000, "Cache-Control max-age in seconds for images")
	cacheImmutable := flag.Bool("cache-immutable", true, "add immutable to Cache-Control for images")
	flag.Parse()

	expandedDir, err := expandHome(*dir)
	if err != nil {
		log.Fatalf("expand dir: %v", err)
	}

	months, err := listImagesGroupedByMonth(expandedDir)
	if err != nil {
		log.Fatalf("list images: %v", err)
	}

	monthMap := make(map[string]MonthGroup, len(months))
	monthPos := make(map[string]int, len(months))
	for i, m := range months {
		monthMap[m.Key] = m
		monthPos[m.Key] = i
	}

	mux := http.NewServeMux()

	fs := http.FileServer(http.Dir(expandedDir))
	mux.Handle("/imgs/",
		http.StripPrefix("/imgs/",
			cacheControl(fs, *cacheMaxAge, *cacheImmutable),
		),
	)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}

		data := IndexData{
			Title:  *title,
			Months: months,
		}
		if err := indexTpl.Execute(w, data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	mux.HandleFunc("/month/", func(w http.ResponseWriter, r *http.Request) {
		monthKey := strings.TrimPrefix(r.URL.Path, "/month/")
		if monthKey == "" || strings.Contains(monthKey, "/") {
			http.NotFound(w, r)
			return
		}

		month, ok := monthMap[monthKey]
		if !ok {
			http.NotFound(w, r)
			return
		}

		pos := monthPos[monthKey]

		var prev *MonthNav
		var next *MonthNav

		if pos+1 < len(months) {
			m := months[pos+1]
			prev = &MonthNav{Key: m.Key, Label: m.Label}
		}
		if pos-1 >= 0 {
			m := months[pos-1]
			next = &MonthNav{Key: m.Key, Label: m.Label}
		}

		data := MonthPageData{
			Title:       *title,
			ThumbHeight: *thumbHeight,
			Month:       month,
			Prev:        prev,
			Next:        next,
		}
		if err := monthTpl.Execute(w, data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	log.Printf("Serving gallery on %s (dir=%s)", *listen, expandedDir)
	log.Fatal(http.ListenAndServe(*listen, mux))
}

func listImagesGroupedByMonth(dir string) ([]MonthGroup, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	type fileWithTime struct {
		name string
		t    time.Time
	}

	var files []fileWithTime

	for _, e := range entries {
		if e.IsDir() {
			continue
		}

		ext := strings.ToLower(filepath.Ext(e.Name()))
		switch ext {
		case ".jpg", ".jpeg", ".png", ".gif", ".webp":
		default:
			continue
		}

		fullPath := filepath.Join(dir, e.Name())

		t, err := imageTime(fullPath)
		if err != nil {
			info, statErr := e.Info()
			if statErr != nil {
				continue
			}
			t = info.ModTime()
		}

		files = append(files, fileWithTime{
			name: e.Name(),
			t:    t,
		})
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].t.After(files[j].t)
	})

	var months []MonthGroup
	monthIndex := make(map[string]int)

	for _, f := range files {
		monthKey := f.t.Format("2006-01")
		monthLabel := f.t.Format("January 2006")

		img := Image{
			Name:       f.name,
			CapturedAt: f.t,
			MonthKey:   monthKey,
			MonthLabel: monthLabel,
		}

		if idx, ok := monthIndex[monthKey]; ok {
			months[idx].Images = append(months[idx].Images, img)
		} else {
			monthIndex[monthKey] = len(months)
			months = append(months, MonthGroup{
				Key:    monthKey,
				Label:  monthLabel,
				Images: []Image{img},
			})
		}
	}

	return months, nil
}

func imageTime(path string) (time.Time, error) {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}, err
	}
	defer f.Close()

	x, err := exif.Decode(f)
	if err != nil {
		return time.Time{}, err
	}

	return x.DateTime()
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
