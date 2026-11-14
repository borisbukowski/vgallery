package main

import (
	"crypto/sha256"
	"embed"
	"flag"
	"fmt"
	"html/template"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/disintegration/imaging"
	_ "golang.org/x/image/webp"
)

//go:embed gallery.html
var assets embed.FS

var (
	root *os.Root

	cache string
	title string

	thumb     int
	preview   int
	maxPixels int64

	slots chan struct{}
	locks [128]sync.Mutex

	page = template.Must(template.ParseFS(assets, "gallery.html"))
)

type Item struct {
	Name     string
	Link     string
	Thumb    string
	Preview  string
	Original string
	Album    bool
}

type Page struct {
	Title     string
	Current   string
	Parent    string
	HasParent bool
	Items     []Item
}

func expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatal(err)
		}

		return filepath.Join(
			home,
			strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"),
		)
	}

	return p
}

func supported(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".jpg", ".jpeg", ".webp":
		return true
	default:
		return false
	}
}

func link(route, p string) string {
	return route + "?" + url.Values{
		"p": {p},
	}.Encode()
}

func requestPath(r *http.Request) (string, error) {
	p := r.URL.Query().Get("p")

	if p == "" || p == "." {
		return ".", nil
	}

	if strings.Contains(p, "\\") || !fs.ValidPath(p) {
		return "", fmt.Errorf("invalid path")
	}

	return filepath.FromSlash(p), nil
}

func regular(p string) (fs.FileInfo, error) {
	st, err := root.Stat(p)
	if err != nil {
		return nil, err
	}

	if !st.Mode().IsRegular() || !supported(p) {
		return nil, fmt.Errorf("not a supported image")
	}

	return st, nil
}

// albumCover selects the first supported image alphabetically
// from the folder itself, excluding hidden files and symlinks.
func albumCover(dir string) string {
	f, err := root.Open(filepath.FromSlash(dir))
	if err != nil {
		return ""
	}
	defer f.Close()

	entries, err := f.ReadDir(-1)
	if err != nil {
		return ""
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") ||
			entry.Type()&os.ModeSymlink != 0 ||
			!entry.Type().IsRegular() ||
			!supported(entry.Name()) {
			continue
		}

		return link("/thumb", path.Join(dir, entry.Name()))
	}

	return ""
}

func browse(w http.ResponseWriter, r *http.Request) {
	p, err := requestPath(r)
	if err != nil {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	f, err := root.Open(p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	entries, err := f.ReadDir(-1)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	data := Page{
		Title:     title,
		Current:   filepath.ToSlash(p),
		HasParent: p != ".",
		Parent: link(
			"/",
			filepath.ToSlash(filepath.Dir(p)),
		),
	}

	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") ||
			entry.Type()&os.ModeSymlink != 0 {
			continue
		}

		rel := filepath.ToSlash(filepath.Join(p, entry.Name()))

		if entry.IsDir() {
			data.Items = append(data.Items, Item{
				Name:  entry.Name(),
				Album: true,
				Link:  link("/", rel),
				Thumb: albumCover(rel),
			})
		} else if entry.Type().IsRegular() && supported(entry.Name()) {
			data.Items = append(data.Items, Item{
				Name:     entry.Name(),
				Thumb:    link("/thumb", rel),
				Preview:  link("/preview", rel),
				Original: link("/original", rel),
			})
		}
	}

	sort.Slice(data.Items, func(i, j int) bool {
		a, b := data.Items[i], data.Items[j]

		if a.Album != b.Album {
			return a.Album
		}

		return a.Name < b.Name
	})

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	if err := page.ExecuteTemplate(w, "gallery.html", data); err != nil {
		log.Printf("template: %v", err)
	}
}

func original(w http.ResponseWriter, r *http.Request) {
	p, err := requestPath(r)
	if err != nil {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	if _, err := regular(p); err != nil {
		http.NotFound(w, r)
		return
	}

	f, err := root.Open(p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Cache-Control", "no-cache")

	http.ServeContent(w, r, st.Name(), st.ModTime(), f)
}

func saveJPEG(name string, img image.Image) error {
	f, err := os.CreateTemp(cache, ".render-*")
	if err != nil {
		return err
	}

	tmp := f.Name()
	defer os.Remove(tmp)

	encodeErr := jpeg.Encode(f, img, &jpeg.Options{
		Quality: 85,
	})
	closeErr := f.Close()

	if encodeErr != nil {
		return encodeErr
	}

	if closeErr != nil {
		return closeErr
	}

	return os.Rename(tmp, name)
}

func render(p string, st fs.FileInfo, small, large string) error {
	f, err := root.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()

	cfg, format, err := image.DecodeConfig(f)
	if err != nil {
		return err
	}

	if format != "jpeg" && format != "webp" {
		return fmt.Errorf("unsupported format")
	}

	if cfg.Width < 1 ||
		cfg.Height < 1 ||
		int64(cfg.Width) > maxPixels/int64(cfg.Height) {
		return fmt.Errorf("image exceeds pixel limit")
	}

	if _, err := f.Seek(0, 0); err != nil {
		return err
	}

	img, err := imaging.Decode(f, imaging.AutoOrientation(true))
	if err != nil {
		return err
	}

	// Flatten transparency onto white before JPEG encoding.
	flat := image.NewRGBA(img.Bounds())

	draw.Draw(
		flat,
		flat.Bounds(),
		image.NewUniform(color.White),
		image.Point{},
		draw.Src,
	)

	draw.Draw(
		flat,
		flat.Bounds(),
		img,
		img.Bounds().Min,
		draw.Over,
	)

	if err := saveJPEG(
		large,
		imaging.Fit(flat, preview, preview, imaging.Lanczos),
	); err != nil {
		return err
	}

	if err := saveJPEG(
		small,
		imaging.Fit(flat, thumb, thumb, imaging.Lanczos),
	); err != nil {
		return err
	}

	after, err := root.Stat(p)
	if err != nil ||
		after.Size() != st.Size() ||
		!after.ModTime().Equal(st.ModTime()) {
		os.Remove(small)
		os.Remove(large)

		return fmt.Errorf("source changed during rendering; retry")
	}

	return nil
}

func derivative(w http.ResponseWriter, r *http.Request) {
	p, err := requestPath(r)
	if err != nil {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	st, err := regular(p)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	key := sha256.Sum256([]byte(fmt.Sprintf(
		"v1:%s:%s:%d:%d:%d:%d",
		root.Name(),
		p,
		st.Size(),
		st.ModTime().UnixNano(),
		thumb,
		preview,
	)))

	base := filepath.Join(cache, fmt.Sprintf("%x", key))
	small := base + "-thumb.jpg"
	large := base + "-preview.jpg"

	target := small
	if r.URL.Path == "/preview" {
		target = large
	}

	lock := &locks[int(key[0])%len(locks)]
	lock.Lock()

	if _, err := os.Stat(target); err != nil {
		select {
		case slots <- struct{}{}:
		case <-r.Context().Done():
			lock.Unlock()
			return
		}

		err = render(p, st, small, large)
		<-slots

		if err != nil {
			lock.Unlock()

			log.Printf("render %q: %v", p, err)

			http.Error(
				w,
				"Cannot render image; see server log",
				http.StatusUnprocessableEntity,
			)
			return
		}
	}

	lock.Unlock()

	f, err := os.Open(target)
	if err != nil {
		http.Error(w, "Cache error", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		http.Error(w, "Cache error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set(
		"ETag",
		fmt.Sprintf("\"%x-%s\"", key, filepath.Base(target)),
	)

	http.ServeContent(
		w,
		r,
		filepath.Base(target),
		fi.ModTime(),
		f,
	)
}

func main() {
	dir := flag.String(
		"dir",
		"~/media/good",
		"Photo directory (read-only)",
	)

	listen := flag.String(
		"listen",
		"127.0.0.1:8080",
		"HTTP listen address",
	)

	cacheDefault, err := os.UserCacheDir()
	if err != nil {
		cacheDefault = "."
	}

	flag.StringVar(
		&cache,
		"cache",
		filepath.Join(cacheDefault, "vgallery"),
		"Derivative cache (outside photo directory)",
	)

	flag.StringVar(
		&title,
		"title",
		"Fotogalerie",
		"Gallery title",
	)

	flag.IntVar(
		&thumb,
		"thumb",
		480,
		"Thumbnail maximum edge in pixels",
	)

	flag.IntVar(
		&preview,
		"preview",
		1920,
		"Preview maximum edge in pixels",
	)

	workers := flag.Int(
		"workers",
		2,
		"Maximum concurrent image conversions",
	)

	flag.Int64Var(
		&maxPixels,
		"max-pixels",
		60000000,
		"Maximum source pixels per image",
	)

	flag.Parse()

	if thumb < 1 ||
		preview < thumb ||
		*workers < 1 ||
		maxPixels < 1 {
		log.Fatal("Invalid size, workers or pixel limit")
	}

	photoDir, err := filepath.Abs(expand(*dir))
	if err != nil {
		log.Fatal(err)
	}

	photoDir, err = filepath.EvalSymlinks(photoDir)
	if err != nil {
		log.Fatal(err)
	}

	cache, err = filepath.Abs(expand(cache))
	if err != nil {
		log.Fatal(err)
	}

	if err := os.MkdirAll(cache, 0700); err != nil {
		log.Fatal(err)
	}

	cache, err = filepath.EvalSymlinks(cache)
	if err != nil {
		log.Fatal(err)
	}

	rel, err := filepath.Rel(photoDir, cache)
	if err != nil {
		log.Fatal(err)
	}

	if rel == "." ||
		(rel != ".." &&
			!strings.HasPrefix(rel, ".."+string(os.PathSeparator))) {
		log.Fatal("Cache must be outside photo directory")
	}

	root, err = os.OpenRoot(photoDir)
	if err != nil {
		log.Fatal(err)
	}
	defer root.Close()

	slots = make(chan struct{}, *workers)

	mux := http.NewServeMux()

	mux.HandleFunc("GET /robots.txt", func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, "User-agent: *\nDisallow: /\n")
	})

	mux.HandleFunc("GET /{$}", browse)
	mux.HandleFunc("GET /original", original)
	mux.HandleFunc("GET /thumb", derivative)
	mux.HandleFunc("GET /preview", derivative)

	handler := http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set(
			"Content-Security-Policy",
			"default-src 'self'; "+
				"img-src 'self'; "+
				"style-src 'unsafe-inline'; "+
				"script-src 'unsafe-inline'; "+
				"object-src 'none'; "+
				"base-uri 'none'; "+
				"frame-ancestors 'none'",
		)

		mux.ServeHTTP(w, r)
	})

	server := &http.Server{
		Addr:              *listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	log.Printf(
		"Gallery: http://%s | Photos: %s | Cache: %s",
		*listen,
		photoDir,
		cache,
	)

	log.Fatal(server.ListenAndServe())
}
