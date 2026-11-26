package main

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
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
	"golang.org/x/crypto/bcrypt"
	_ "golang.org/x/image/webp"
)

//go:embed gallery.html
var assets embed.FS

var (
	root           *os.Root
	trashMu        sync.Mutex
	csrfToken      = rand.Text()
	adminPasswords map[string][]byte
	secureCookie   bool
	sessionMu      sync.Mutex
	sessions       = make(map[string]time.Time)

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
	Trash    string
	Album    bool
}

type Page struct {
	Admin     bool
	CSRFToken string
	Title     string
	Current   string
	Parent    string
	HasParent bool
	Items     []Item
}

// expand expands a leading tilde using the current home directory.
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

// supported checks the extension; rendering also validates the decoded format.
func supported(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".jpg", ".jpeg", ".webp":
		return true
	default:
		return false
	}
}

// link encodes a root-relative photo path into a route URL.
func link(route, p string) string {
	return route + "?" + url.Values{
		"p": {p},
	}.Encode()
}

// requestPath rejects hidden, nonrelative, and symlink paths.
func requestPath(r *http.Request) (string, error) {
	p := r.URL.Query().Get("p")

	if p == "" || p == "." {
		return ".", nil
	}

	if strings.Contains(p, "\\") || !fs.ValidPath(p) {
		return "", fmt.Errorf("invalid path")
	}

	// Block hidden paths (including .trash) and symlink aliases on all routes.
	for _, part := range strings.Split(p, "/") {
		if strings.HasPrefix(part, ".") {
			return "", fmt.Errorf("hidden path")
		}
	}
	clean := filepath.FromSlash(p)
	for q := clean; q != "."; q = filepath.Dir(q) {
		st, err := root.Lstat(q)
		if err != nil || st.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("missing or symlink path")
		}
	}
	return clean, nil
}

// regular requires a supported regular image file.
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

// hasImages reports whether a directory contains a supported image, including
// in visible subdirectories. Hidden entries and symlinks are ignored.
func hasImages(dir string) bool {
	f, err := root.Open(dir)
	if err != nil {
		return false
	}
	entries, err := f.ReadDir(-1)
	f.Close()
	if err != nil {
		return false
	}

	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") ||
			entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if entry.Type().IsRegular() && supported(entry.Name()) {
			return true
		}
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") ||
			entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			continue
		}
		if hasImages(filepath.Join(dir, entry.Name())) {
			return true
		}
	}
	return false
}

// browse renders the public gallery with optional authenticated admin controls.
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
		Admin:     isAdmin(r),
		CSRFToken: csrfToken,
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
			if !hasImages(filepath.Join(p, entry.Name())) {
				continue
			}
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
				Trash:    link("/trash", rel),
			})
		}
	}

	sort.Slice(data.Items, func(i, j int) bool {
		a, b := data.Items[i], data.Items[j]

		if a.Album != b.Album {
			return a.Album
		}

		return a.Name > b.Name
	})

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Add("Vary", "Cookie")

	if err := page.ExecuteTemplate(w, "gallery.html", data); err != nil {
		log.Printf("template: %v", err)
	}
}

// loadAdminCredentials loads bcrypt entries produced by Apache htpasswd -B.
// Fail closed on missing, empty, malformed, duplicate, or unsupported entries.
func loadAdminCredentials(name string) {
	f, err := os.Open(expand(name))
	if err != nil {
		log.Fatalf("htpasswd: %v", err)
	}
	defer f.Close()

	adminPasswords = make(map[string][]byte)
	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		user, hash, ok := strings.Cut(line, ":")
		if !ok || user == "" || strings.ContainsAny(user, "\r\n") {
			log.Fatalf("htpasswd: malformed entry on line %d", lineNo)
		}
		if _, exists := adminPasswords[user]; exists {
			log.Fatalf("htpasswd: duplicate username on line %d", lineNo)
		}
		if len(hash) != 60 || !(strings.HasPrefix(hash, "$2a$") ||
			strings.HasPrefix(hash, "$2b$") || strings.HasPrefix(hash, "$2y$")) {
			log.Fatalf("htpasswd: line %d requires bcrypt; create entries with htpasswd -B", lineNo)
		}
		if _, err := bcrypt.Cost([]byte(hash)); err != nil {
			log.Fatalf("htpasswd: invalid bcrypt hash on line %d: %v", lineNo, err)
		}
		adminPasswords[user] = []byte(hash)
	}
	if err := scanner.Err(); err != nil {
		log.Fatalf("htpasswd: %v", err)
	}
	if len(adminPasswords) == 0 {
		log.Fatal("htpasswd: no users configured")
	}
}

// Sessions live only in memory, expire after eight hours, and vanish on restart.
func isAdmin(r *http.Request) bool {
	c, err := r.Cookie("vgallery_admin")
	if err != nil {
		return false
	}
	sessionMu.Lock()
	defer sessionMu.Unlock()
	expires, ok := sessions[c.Value]
	if ok && !time.Now().Before(expires) {
		delete(sessions, c.Value)
		return false
	}
	return ok
}

// adminLogin is a normal navigation so browsers can show their Basic-auth prompt.
// Successful login creates a cookie; public routes never challenge for credentials.
func adminLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	user, pass, ok := r.BasicAuth()
	hash, exists := adminPasswords[user]
	if !ok || !exists || bcrypt.CompareHashAndPassword(hash, []byte(pass)) != nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="vgallery-admin", charset="UTF-8"`)
		http.Error(w, "Admin authentication required", http.StatusUnauthorized)
		return
	}
	token := rand.Text()
	now := time.Now()
	expires := now.Add(8 * time.Hour)
	sessionMu.Lock()
	for k, expiry := range sessions {
		if !now.Before(expiry) {
			delete(sessions, k)
		}
	}
	sessions[token] = expires
	sessionMu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name: "vgallery_admin", Value: token, Path: "/", HttpOnly: true,
		Secure: secureCookie, SameSite: http.SameSiteStrictMode,
		MaxAge: 8 * 60 * 60, Expires: expires,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// requireAdmin checks the session rather than challenging background fetch requests.
func requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !isAdmin(r) {
			http.Error(w, "Admin login required; open /admin/login", http.StatusUnauthorized)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(csrfToken)) != 1 {
			http.Error(w, "Invalid request token; reload the gallery", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// trashImage moves originals without decoding them, including damaged images.
// Each entry records the previous relative path; no permanent deletion is exposed.
func trashImage(w http.ResponseWriter, r *http.Request) {
	trashMu.Lock()
	defer trashMu.Unlock()
	p, err := requestPath(r)
	if err != nil {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}
	if _, err := regular(p); err != nil {
		http.NotFound(w, r)
		return
	}
	if err = root.Mkdir(".trash", 0700); err != nil && !os.IsExist(err) {
		log.Printf("trash mkdir: %v", err)
		http.Error(w, "Cannot create trash", http.StatusInternalServerError)
		return
	}
	st, err := root.Lstat(".trash")
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		http.Error(w, "Trash must be a real directory", http.StatusInternalServerError)
		return
	}
	// Reserve a private destination; never overwrite an existing trash entry.
	var dest string
	for {
		dest = filepath.Join(".trash", time.Now().UTC().Format("20060102T150405Z")+"-"+rand.Text())
		err = root.Mkdir(dest, 0700)
		if !os.IsExist(err) {
			break
		}
	}
	if err != nil {
		http.Error(w, "Cannot create trash entry", http.StatusInternalServerError)
		return
	}
	moved := false
	metadata := filepath.Join(dest, "original-path.txt")
	defer func() {
		if !moved {
			root.Remove(metadata)
			root.Remove(dest)
		}
	}()
	f, err := root.OpenFile(metadata, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err == nil {
		_, writeErr := f.WriteString(filepath.ToSlash(p) + "\n")
		closeErr := f.Close()
		err = writeErr
		if err == nil {
			err = closeErr
		}
	}
	if err == nil {
		// Rename only: cross-filesystem moves fail without copying or deleting originals.
		err = root.Rename(p, filepath.Join(dest, filepath.Base(p)))
	}
	if err != nil {
		log.Printf("trash %q: %v", p, err)
		http.Error(w, "Cannot move image; see server log", http.StatusInternalServerError)
		return
	}
	moved = true
	log.Printf("trashed %q -> %q", p, dest)
	w.WriteHeader(http.StatusNoContent)
}

// original streams the original without image conversion.
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

	// Store images in the browser, but revalidate before every reuse so that
	// moves to trash and changes at the same URL are noticed on the next request.
	w.Header().Set("Cache-Control", "private, no-cache, must-revalidate")
	// This metadata validator is weak: it is not a hash of the image contents.
	etag := sha256.Sum256([]byte(fmt.Sprintf(
		"original-v1:%s:%s:%d:%d",
		root.Name(), p, st.Size(), st.ModTime().UnixNano(),
	)))
	w.Header().Set("ETag", fmt.Sprintf(`W/"%x"`, etag))

	// ServeContent supplies Last-Modified and handles conditional requests.
	http.ServeContent(w, r, st.Name(), st.ModTime(), f)
}

// saveJPEG publishes a completed cache file using a temporary file and rename.
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

// render decodes once and produces both thumbnail and preview sizes.
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

// derivative serializes cache misses by key and limits concurrent rendering.
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
	w.Header().Set("Cache-Control", "private, no-cache, must-revalidate")
	w.Header().Set(
		"ETag",
		// The cache filename already contains the key and derivative type.
		fmt.Sprintf(`W/"%s"`, filepath.Base(target)),
	)

	http.ServeContent(
		w,
		r,
		filepath.Base(target),
		fi.ModTime(),
		f,
	)
}

// main validates configuration and installs public and admin-only routes.
func main() {
	dir := flag.String(
		"dir",
		"/var/www/vgallery",
		"Photo directory (write access needed for admin trash moves)",
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

	flag.BoolVar(&secureCookie, "secure-cookie", true, "Require HTTPS for admin session cookies (disable only for local HTTP)")
	htpasswdFile := flag.String("htpasswd", "/etc/vgallery/htpasswd", "Apache htpasswd file (bcrypt entries created with htpasswd -B)")
	flag.Parse()
	loadAdminCredentials(*htpasswdFile)

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
	mux.HandleFunc("GET /admin/login", adminLogin)
	mux.HandleFunc("POST /trash", requireAdmin(trashImage))

	handler := http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set(
			"Content-Security-Policy",
			"default-src 'self'; "+
				"connect-src 'self'; "+
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
