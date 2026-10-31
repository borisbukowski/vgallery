package main

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed templates/*.html static/*
var assets embed.FS

const (
	perPage   = 48
	maxUpload = 1 << 30 // 1 GiB per upload request
	cookieKey = "gallery_session"
)

type Server struct {
	store    *Store
	dataDir  string
	tpl      map[string]*template.Template
	sessions *Sessions
	password string
}

func NewServer(dataDir, password string) (*Server, error) {
	for _, d := range []string{"photos", "thumbs"} {
		if err := os.MkdirAll(filepath.Join(dataDir, d), 0o755); err != nil {
			return nil, err
		}
	}
	st, err := OpenStore(filepath.Join(dataDir, "gallery.json"))
	if err != nil {
		return nil, err
	}
	tpl, err := loadTemplates()
	if err != nil {
		return nil, err
	}
	return &Server{store: st, dataDir: dataDir, tpl: tpl, sessions: NewSessions(), password: password}, nil
}

func (s *Server) photosDir() string { return filepath.Join(s.dataDir, "photos") }
func (s *Server) thumbsDir() string { return filepath.Join(s.dataDir, "thumbs") }

func loadTemplates() (map[string]*template.Template, error) {
	funcs := template.FuncMap{"date": func(t time.Time) string { return t.Format("2 Jan 2006") }}
	out := map[string]*template.Template{}
	for _, name := range []string{"home", "album", "photo", "tags", "results", "login"} {
		t, err := template.New(name).Funcs(funcs).ParseFS(assets, "templates/layout.html", "templates/"+name+".html")
		if err != nil {
			return nil, err
		}
		out[name] = t
	}
	return out, nil
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(assets, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))

	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("GET /album/{id}", s.album)
	mux.HandleFunc("GET /photo/{id}", s.photo)
	mux.HandleFunc("GET /img/{id}/{size}", s.image)
	mux.HandleFunc("GET /tags", s.tags)
	mux.HandleFunc("GET /tag/{tag}", s.tag)
	mux.HandleFunc("GET /search", s.search)
	mux.HandleFunc("GET /login", s.loginForm)
	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("POST /logout", s.logout)

	mux.HandleFunc("POST /admin/album", s.admin(s.createAlbum))
	mux.HandleFunc("POST /admin/album/{id}/edit", s.admin(s.editAlbum))
	mux.HandleFunc("POST /admin/album/{id}/delete", s.admin(s.deleteAlbum))
	mux.HandleFunc("POST /admin/upload", s.admin(s.upload))
	mux.HandleFunc("POST /admin/photo/{id}/edit", s.admin(s.editPhoto))
	mux.HandleFunc("POST /admin/photo/{id}/delete", s.admin(s.deletePhoto))

	return s.secure(mux)
}

// secure adds basic hardening: no MIME sniffing and a same-origin check on POSTs.
func (s *Server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		if r.Method == http.MethodPost {
			if o := r.Header.Get("Origin"); o != "" {
				if u, err := url.Parse(o); err != nil || u.Host != r.Host {
					http.Error(w, "cross-origin request blocked", http.StatusForbidden)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) admin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.isAdmin(r) {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		h(w, r)
	}
}

// ---------- rendering helpers ----------

func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, data map[string]any) {
	s.renderStatus(w, r, http.StatusOK, name, data)
}

func (s *Server) renderStatus(w http.ResponseWriter, r *http.Request, status int, name string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	for _, k := range []string{"Title", "Query", "Error"} {
		if _, ok := data[k]; !ok {
			data[k] = ""
		}
	}
	data["Admin"] = s.isAdmin(r)
	data["Msg"] = r.URL.Query().Get("msg")
	var buf bytes.Buffer
	if err := s.tpl[name].ExecuteTemplate(&buf, "layout", data); err != nil {
		log.Printf("template %s: %v", name, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	buf.WriteTo(w)
}

func redirect(w http.ResponseWriter, r *http.Request, path, msg string) {
	if msg != "" {
		path += "?msg=" + url.QueryEscape(msg)
	}
	http.Redirect(w, r, path, http.StatusSeeOther)
}

func pathID(r *http.Request) (int, bool) {
	id, err := strconv.Atoi(r.PathValue("id"))
	return id, err == nil && id > 0
}

type AlbumCard struct {
	Album
	Count int
	Cover int
}

func (s *Server) cards(parent int) []AlbumCard {
	var out []AlbumCard
	for _, a := range s.store.Children(parent) {
		n, cover := s.store.AlbumStats(a.ID)
		out = append(out, AlbumCard{a, n, cover})
	}
	return out
}

// ---------- public pages ----------

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "home", map[string]any{
		"Albums": s.cards(0),
		"Recent": s.store.Recent(12),
	})
}

func (s *Server) album(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	a, ok := s.store.Album(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	photos := s.store.AlbumPhotos(id)
	pages := max(1, (len(photos)+perPage-1)/perPage)
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	page = min(max(page, 1), pages)
	lo := (page - 1) * perPage
	hi := min(lo+perPage, len(photos))
	next := 0
	if page < pages {
		next = page + 1
	}
	s.render(w, r, "album", map[string]any{
		"Title": a.Name, "Album": a, "Crumbs": s.store.Crumbs(a.ParentID),
		"Subs": s.cards(id), "Photos": photos[lo:hi],
		"Page": page, "Pages": pages, "Prev": page - 1, "Next": next,
	})
}

func (s *Server) photo(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	p, ok := s.store.Photo(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	prev, next := s.store.Neighbors(p)
	s.render(w, r, "photo", map[string]any{
		"Title": p.Title, "Photo": p, "Crumbs": s.store.Crumbs(p.AlbumID),
		"Prev": prev, "Next": next, "TagsCSV": strings.Join(p.Tags, ", "),
	})
}

func (s *Server) image(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	p, ok := s.store.Photo(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(s.photosDir(), p.File)
	if size := r.PathValue("size"); size != "orig" {
		var err error
		path, err = s.variant(&p, size)
		if errors.Is(err, errUnknownSize) {
			http.NotFound(w, r)
			return
		} else if err != nil {
			log.Printf("variant %d/%s: %v", id, size, err)
			http.Error(w, "could not render image", http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Cache-Control", "public, max-age=604800")
	http.ServeFile(w, r, path)
}

func (s *Server) tags(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "tags", map[string]any{"Title": "Tags", "Tags": s.store.Tags()})
}

func (s *Server) tag(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("tag")
	s.render(w, r, "results", map[string]any{
		"Title": "Tag: " + name, "Heading": "Tag: " + name, "Photos": s.store.ByTag(name),
	})
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	s.render(w, r, "results", map[string]any{
		"Title": "Search", "Heading": "Search: " + q, "Query": q, "Photos": s.store.Search(q),
	})
}

// ---------- auth ----------

type Sessions struct {
	mu sync.Mutex
	m  map[string]time.Time
}

func NewSessions() *Sessions { return &Sessions{m: map[string]time.Time{}} }

func (s *Sessions) New() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	tok := hex.EncodeToString(b)
	s.mu.Lock()
	s.m[tok] = time.Now().Add(7 * 24 * time.Hour)
	s.mu.Unlock()
	return tok
}

func (s *Sessions) Valid(tok string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.m[tok]
	if ok && time.Now().After(exp) {
		delete(s.m, tok)
		return false
	}
	return ok
}

func (s *Sessions) Delete(tok string) {
	s.mu.Lock()
	delete(s.m, tok)
	s.mu.Unlock()
}

func (s *Server) isAdmin(r *http.Request) bool {
	c, err := r.Cookie(cookieKey)
	return err == nil && s.sessions.Valid(c.Value)
}

func (s *Server) loginForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "login", map[string]any{"Title": "Log in"})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.FormValue("password")), []byte(s.password)) != 1 {
		time.Sleep(700 * time.Millisecond) // slow down guessing
		s.renderStatus(w, r, http.StatusUnauthorized, "login", map[string]any{"Title": "Log in", "Error": "Wrong password."})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: cookieKey, Value: s.sessions.New(), Path: "/", MaxAge: 7 * 24 * 3600,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieKey); err == nil {
		s.sessions.Delete(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: cookieKey, Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// ---------- admin actions ----------

func (s *Server) createAlbum(w http.ResponseWriter, r *http.Request) {
	parent, _ := strconv.Atoi(r.FormValue("parent_id"))
	a, err := s.store.AddAlbum(parent, r.FormValue("name"), "")
	if err != nil {
		back := "/"
		if parent != 0 {
			back = "/album/" + strconv.Itoa(parent)
		}
		redirect(w, r, back, err.Error())
		return
	}
	redirect(w, r, "/album/"+strconv.Itoa(a.ID), "Album created.")
}

func (s *Server) editAlbum(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	msg := "Saved."
	if err := s.store.UpdateAlbum(id, r.FormValue("name"), r.FormValue("description")); err != nil {
		msg = err.Error()
	}
	redirect(w, r, "/album/"+strconv.Itoa(id), msg)
}

func (s *Server) deleteAlbum(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	a, ok := s.store.Album(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	removed, err := s.store.DeleteAlbum(id)
	if err != nil {
		redirect(w, r, "/album/"+strconv.Itoa(id), err.Error())
		return
	}
	for _, p := range removed {
		s.removeFiles(p)
	}
	back := "/"
	if a.ParentID != 0 {
		back = "/album/" + strconv.Itoa(a.ParentID)
	}
	redirect(w, r, back, "Album deleted.")
}

func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		redirect(w, r, "/", "Upload failed: "+err.Error())
		return
	}
	defer r.MultipartForm.RemoveAll()
	albumID, _ := strconv.Atoi(r.FormValue("album_id"))
	if _, ok := s.store.Album(albumID); !ok {
		http.Error(w, "no such album", http.StatusBadRequest)
		return
	}
	added := 0
	var problems []string
	for _, fh := range r.MultipartForm.File["files"] {
		f, err := fh.Open()
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		data, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		if _, err := s.ingest(albumID, fh.Filename, data); err != nil {
			problems = append(problems, err.Error())
			continue
		}
		added++
	}
	msg := fmt.Sprintf("%d photo(s) uploaded.", added)
	if len(problems) > 0 {
		msg += " Skipped: " + strings.Join(problems, "; ")
	}
	redirect(w, r, "/album/"+strconv.Itoa(albumID), msg)
}

func (s *Server) editPhoto(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	msg := "Saved."
	if err := s.store.UpdatePhoto(id, r.FormValue("title"), r.FormValue("description"), NormTags(r.FormValue("tags"))); err != nil {
		msg = err.Error()
	}
	redirect(w, r, "/photo/"+strconv.Itoa(id), msg)
}

func (s *Server) deletePhoto(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	p, err := s.store.DeletePhoto(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.removeFiles(p)
	redirect(w, r, "/album/"+strconv.Itoa(p.AlbumID), "Photo deleted.")
}

// removeFiles deletes a photo's file and cached renditions unless another
// photo record still uses the same file.
func (s *Server) removeFiles(p Photo) {
	if s.store.FileInUse(p.File) {
		return
	}
	os.Remove(filepath.Join(s.photosDir(), p.File))
	stem := strings.TrimSuffix(p.File, filepath.Ext(p.File))
	if matches, _ := filepath.Glob(filepath.Join(s.thumbsDir(), stem+"_*.jpg")); matches != nil {
		for _, m := range matches {
			os.Remove(m)
		}
	}
}

// ---------- bulk import (like Piwigo's directory sync) ----------

// ImportDir walks a folder tree: every folder becomes an album, nested like
// the folders, and every JPEG/PNG/GIF inside becomes a photo.
func (s *Server) ImportDir(root string) (added, skipped int, err error) {
	root, err = filepath.Abs(root)
	if err != nil {
		return
	}
	top := filepath.Base(root)
	albums := map[string]int{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".jpg", ".jpeg", ".png", ".gif":
		default:
			return nil
		}
		rel, _ := filepath.Rel(root, filepath.Dir(path))
		id, ok := albums[rel]
		if !ok {
			parts := []string{top}
			if rel != "." {
				parts = append(parts, strings.Split(filepath.ToSlash(rel), "/")...)
			}
			var e error
			if id, e = s.store.EnsurePath(parts); e != nil {
				return e
			}
			albums[rel] = id
		}
		data, e := os.ReadFile(path)
		if e == nil {
			_, e = s.ingest(id, filepath.Base(path), data)
		}
		if e != nil {
			log.Println("skip:", e)
			skipped++
			return nil
		}
		added++
		return nil
	})
	return
}
