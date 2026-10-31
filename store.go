package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

type Album struct {
	ID          int       `json:"id"`
	ParentID    int       `json:"parent_id"` // 0 = top level
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Created     time.Time `json:"created"`
}

type Photo struct {
	ID          int       `json:"id"`
	AlbumID     int       `json:"album_id"`
	File        string    `json:"file"`     // stored file name (content hash + ext)
	Original    string    `json:"original"` // name it was uploaded with
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Tags        []string  `json:"tags"`
	Width       int       `json:"width"`
	Height      int       `json:"height"`
	Hash        string    `json:"hash"`
	Added       time.Time `json:"added"`
}

type TagCount struct {
	Name  string
	Count int
	Size  int // 1..5, for the tag cloud
}

type dbFile struct {
	Albums    []*Album `json:"albums"`
	Photos    []*Photo `json:"photos"`
	NextAlbum int      `json:"next_album"`
	NextPhoto int      `json:"next_photo"`
}

// Store keeps all metadata in memory and persists it to one JSON file.
// That is plenty for personal galleries (tens of thousands of photos);
// swap it for SQLite/Postgres if you outgrow it.
type Store struct {
	mu        sync.RWMutex
	path      string
	albums    map[int]*Album
	photos    map[int]*Photo
	nextAlbum int
	nextPhoto int
}

func OpenStore(path string) (*Store, error) {
	s := &Store{path: path, albums: map[int]*Album{}, photos: map[int]*Photo{}, nextAlbum: 1, nextPhoto: 1}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var f dbFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	for _, a := range f.Albums {
		s.albums[a.ID] = a
		if a.ID >= s.nextAlbum {
			s.nextAlbum = a.ID + 1
		}
	}
	for _, p := range f.Photos {
		s.photos[p.ID] = p
		if p.ID >= s.nextPhoto {
			s.nextPhoto = p.ID + 1
		}
	}
	if f.NextAlbum > s.nextAlbum {
		s.nextAlbum = f.NextAlbum
	}
	if f.NextPhoto > s.nextPhoto {
		s.nextPhoto = f.NextPhoto
	}
	return s, nil
}

func (s *Store) saveLocked() error {
	f := dbFile{NextAlbum: s.nextAlbum, NextPhoto: s.nextPhoto}
	for _, a := range s.albums {
		f.Albums = append(f.Albums, a)
	}
	for _, p := range s.photos {
		f.Photos = append(f.Photos, p)
	}
	sort.Slice(f.Albums, func(i, j int) bool { return f.Albums[i].ID < f.Albums[j].ID })
	sort.Slice(f.Photos, func(i, j int) bool { return f.Photos[i].ID < f.Photos[j].ID })
	b, err := json.MarshalIndent(f, "", " ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func clonePhoto(p *Photo) Photo {
	c := *p
	c.Tags = append([]string(nil), p.Tags...)
	return c
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

// NormTags turns "Sunset, Family trip" into ["sunset", "family-trip"].
func NormTags(raw string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range strings.Split(raw, ",") {
		t = strings.Join(strings.Fields(strings.ToLower(t)), "-")
		t = strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
				return r
			}
			return -1
		}, t)
		if t != "" && !seen[t] {
			seen[t] = true
			out = append(out, clip(t, 50))
		}
	}
	return out
}

// ---------- albums ----------

func (s *Store) Album(id int) (Album, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.albums[id]
	if !ok {
		return Album{}, false
	}
	return *a, true
}

func (s *Store) Children(parent int) []Album {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Album
	for _, a := range s.albums {
		if a.ParentID == parent {
			out = append(out, *a)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// Crumbs returns the chain root → id (inclusive). Crumbs(0) is empty.
func (s *Store) Crumbs(id int) []Album {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Album
	for i := 0; id != 0 && i < 64; i++ {
		a, ok := s.albums[id]
		if !ok {
			break
		}
		out = append([]Album{*a}, out...)
		id = a.ParentID
	}
	return out
}

func (s *Store) descendantsLocked(id int) []int {
	ids := []int{id}
	for i := 0; i < len(ids); i++ {
		for _, a := range s.albums {
			if a.ParentID == ids[i] {
				ids = append(ids, a.ID)
			}
		}
	}
	return ids
}

// AlbumStats counts photos in the album and everything below it, and picks
// the newest photo as the cover.
func (s *Store) AlbumStats(id int) (count, cover int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	set := map[int]bool{}
	for _, d := range s.descendantsLocked(id) {
		set[d] = true
	}
	for _, p := range s.photos {
		if set[p.AlbumID] {
			count++
			if p.ID > cover {
				cover = p.ID
			}
		}
	}
	return
}

func (s *Store) AddAlbum(parent int, name, desc string) (Album, error) {
	name = clip(strings.TrimSpace(name), 200)
	if name == "" {
		return Album{}, errors.New("album name is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if parent != 0 {
		if _, ok := s.albums[parent]; !ok {
			return Album{}, errors.New("parent album does not exist")
		}
	}
	a := &Album{ID: s.nextAlbum, ParentID: parent, Name: name,
		Description: clip(strings.TrimSpace(desc), 2000), Created: time.Now()}
	s.nextAlbum++
	s.albums[a.ID] = a
	return *a, s.saveLocked()
}

func (s *Store) UpdateAlbum(id int, name, desc string) error {
	name = clip(strings.TrimSpace(name), 200)
	if name == "" {
		return errors.New("album name is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.albums[id]
	if !ok {
		return errors.New("no such album")
	}
	a.Name, a.Description = name, clip(strings.TrimSpace(desc), 2000)
	return s.saveLocked()
}

// DeleteAlbum removes the album, its sub-albums and their photos, and
// returns the removed photos so the caller can delete files.
func (s *Store) DeleteAlbum(id int) ([]Photo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.albums[id]; !ok {
		return nil, errors.New("no such album")
	}
	set := map[int]bool{}
	for _, d := range s.descendantsLocked(id) {
		set[d] = true
	}
	var removed []Photo
	for pid, p := range s.photos {
		if set[p.AlbumID] {
			removed = append(removed, clonePhoto(p))
			delete(s.photos, pid)
		}
	}
	for aid := range set {
		delete(s.albums, aid)
	}
	return removed, s.saveLocked()
}

// EnsurePath finds or creates a nested chain of albums (used by import).
func (s *Store) EnsurePath(parts []string) (int, error) {
	parent := 0
	for _, name := range parts {
		found := 0
		s.mu.RLock()
		for _, a := range s.albums {
			if a.ParentID == parent && a.Name == name {
				found = a.ID
				break
			}
		}
		s.mu.RUnlock()
		if found == 0 {
			a, err := s.AddAlbum(parent, name, "")
			if err != nil {
				return 0, err
			}
			found = a.ID
		}
		parent = found
	}
	return parent, nil
}

// ---------- photos ----------

func (s *Store) Photo(id int) (Photo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.photos[id]
	if !ok {
		return Photo{}, false
	}
	return clonePhoto(p), true
}

func (s *Store) sortedPhotosLocked() []*Photo {
	out := make([]*Photo, 0, len(s.photos))
	for _, p := range s.photos {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID }) // newest first
	return out
}

func (s *Store) albumPhotosLocked(album int) []*Photo {
	var out []*Photo
	for _, p := range s.sortedPhotosLocked() {
		if p.AlbumID == album {
			out = append(out, p)
		}
	}
	return out
}

func (s *Store) AlbumPhotos(album int) []Photo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Photo
	for _, p := range s.albumPhotosLocked(album) {
		out = append(out, clonePhoto(p))
	}
	return out
}

func (s *Store) Recent(n int) []Photo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Photo
	for _, p := range s.sortedPhotosLocked() {
		if len(out) >= n {
			break
		}
		out = append(out, clonePhoto(p))
	}
	return out
}

func (s *Store) AddPhoto(p Photo) (Photo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.albums[p.AlbumID]; !ok {
		return Photo{}, errors.New("album does not exist")
	}
	p.ID = s.nextPhoto
	s.nextPhoto++
	p.Added = time.Now()
	c := clonePhoto(&p)
	s.photos[p.ID] = &c
	return clonePhoto(&c), s.saveLocked()
}

func (s *Store) UpdatePhoto(id int, title, desc string, tags []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.photos[id]
	if !ok {
		return errors.New("no such photo")
	}
	if t := clip(strings.TrimSpace(title), 200); t != "" {
		p.Title = t
	}
	p.Description = clip(strings.TrimSpace(desc), 5000)
	p.Tags = tags
	return s.saveLocked()
}

func (s *Store) DeletePhoto(id int) (Photo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.photos[id]
	if !ok {
		return Photo{}, errors.New("no such photo")
	}
	out := clonePhoto(p)
	delete(s.photos, id)
	return out, s.saveLocked()
}

// FileInUse reports whether any photo still references a stored file
// (the same image can live in several albums).
func (s *Store) FileInUse(file string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.photos {
		if p.File == file {
			return true
		}
	}
	return false
}

func (s *Store) HasDuplicate(hash string, album int) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.photos {
		if p.Hash == hash && p.AlbumID == album {
			return true
		}
	}
	return false
}

// Neighbors returns the previous (newer) and next (older) photo IDs in the
// same album, or 0 when there is none.
func (s *Store) Neighbors(p Photo) (prev, next int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := s.albumPhotosLocked(p.AlbumID)
	for i, q := range list {
		if q.ID == p.ID {
			if i > 0 {
				prev = list[i-1].ID
			}
			if i+1 < len(list) {
				next = list[i+1].ID
			}
			break
		}
	}
	return
}

// ---------- tags & search ----------

func (s *Store) Tags() []TagCount {
	s.mu.RLock()
	defer s.mu.RUnlock()
	counts := map[string]int{}
	maxN := 0
	for _, p := range s.photos {
		for _, t := range p.Tags {
			counts[t]++
			if counts[t] > maxN {
				maxN = counts[t]
			}
		}
	}
	out := make([]TagCount, 0, len(counts))
	for name, n := range counts {
		out = append(out, TagCount{Name: name, Count: n, Size: 1 + (n-1)*4/max(1, maxN)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *Store) ByTag(tag string) []Photo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Photo
	for _, p := range s.sortedPhotosLocked() {
		for _, t := range p.Tags {
			if t == tag {
				out = append(out, clonePhoto(p))
				break
			}
		}
	}
	return out
}

func (s *Store) Search(q string) []Photo {
	words := strings.Fields(strings.ToLower(q))
	if len(words) == 0 {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Photo
	for _, p := range s.sortedPhotosLocked() {
		hay := strings.ToLower(p.Title + " " + p.Description + " " + p.Original + " " + strings.Join(p.Tags, " "))
		ok := true
		for _, w := range words {
			if !strings.Contains(hay, w) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, clonePhoto(p))
			if len(out) >= 200 {
				break
			}
		}
	}
	return out
}
