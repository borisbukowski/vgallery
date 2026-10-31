package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type sizeSpec struct {
	N      int  // max edge (or square side) in pixels
	Square bool // centre-crop to a square
}

var sizeSpecs = map[string]sizeSpec{
	"thumb":  {N: 320, Square: true},
	"medium": {N: 1400, Square: false},
}

var errUnknownSize = errors.New("unknown image size")

var genMu sync.Mutex // one thumbnail render at a time keeps memory use sane

// ingest validates an image, stores it under a content-hash name and records it.
func (s *Server) ingest(albumID int, original string, data []byte) (Photo, error) {
	original = filepath.Base(original)
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Photo{}, fmt.Errorf("%s is not a supported image (JPEG, PNG, GIF)", original)
	}
	ext := map[string]string{"jpeg": ".jpg", "png": ".png", "gif": ".gif"}[format]
	if ext == "" {
		return Photo{}, fmt.Errorf("%s: unsupported format %q", original, format)
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	if s.store.HasDuplicate(hash, albumID) {
		return Photo{}, fmt.Errorf("%s is already in this album", original)
	}
	w, h := cfg.Width, cfg.Height
	if format == "jpeg" && exifOrientation(data) >= 5 {
		w, h = h, w
	}
	file := hash[:24] + ext
	dst := filepath.Join(s.photosDir(), file)
	if _, err := os.Stat(dst); err != nil {
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return Photo{}, err
		}
	}
	title := strings.TrimSuffix(original, filepath.Ext(original))
	return s.store.AddPhoto(Photo{
		AlbumID: albumID, File: file, Original: original, Title: clip(title, 200),
		Width: w, Height: h, Hash: hash,
	})
}

// variant returns the path of a resized JPEG, rendering it on first use.
func (s *Server) variant(p *Photo, size string) (string, error) {
	spec, ok := sizeSpecs[size]
	if !ok {
		return "", errUnknownSize
	}
	src := filepath.Join(s.photosDir(), p.File)
	if !spec.Square && p.Width <= spec.N && p.Height <= spec.N {
		return src, nil // already small enough
	}
	out := filepath.Join(s.thumbsDir(), strings.TrimSuffix(p.File, filepath.Ext(p.File))+"_"+size+".jpg")
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}
	genMu.Lock()
	defer genMu.Unlock()
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	view := img
	if format == "jpeg" {
		if o := exifOrientation(data); o > 1 {
			view = oriented{src: img, o: o}
		}
	}
	b := view.Bounds()
	var dst *image.RGBA
	if spec.Square {
		side := min(b.Dx(), b.Dy())
		x0 := b.Min.X + (b.Dx()-side)/2
		y0 := b.Min.Y + (b.Dy()-side)/2
		n := min(spec.N, side)
		dst = resize(view, image.Rect(x0, y0, x0+side, y0+side), n, n)
	} else {
		scale := float64(spec.N) / float64(max(b.Dx(), b.Dy()))
		dw := max(1, int(float64(b.Dx())*scale+0.5))
		dh := max(1, int(float64(b.Dy())*scale+0.5))
		dst = resize(view, b, dw, dh)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85}); err != nil {
		return "", err
	}
	tmp := out + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return "", err
	}
	return out, os.Rename(tmp, out)
}

// resize is an area-averaging downscaler (at most ~4x4 samples per output
// pixel) that composites transparency onto white.
func resize(src image.Image, sr image.Rectangle, dw, dh int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	sw := float64(sr.Dx()) / float64(dw)
	sh := float64(sr.Dy()) / float64(dh)
	for y := 0; y < dh; y++ {
		y0 := sr.Min.Y + int(float64(y)*sh)
		y1 := min(sr.Min.Y+int(float64(y+1)*sh), sr.Max.Y)
		if y1 <= y0 {
			y1 = y0 + 1
		}
		ys := max(1, (y1-y0)/4)
		for x := 0; x < dw; x++ {
			x0 := sr.Min.X + int(float64(x)*sw)
			x1 := min(sr.Min.X+int(float64(x+1)*sw), sr.Max.X)
			if x1 <= x0 {
				x1 = x0 + 1
			}
			xs := max(1, (x1-x0)/4)
			var r, g, bl, n uint64
			for yy := y0; yy < y1; yy += ys {
				for xx := x0; xx < x1; xx += xs {
					cr, cg, cb, ca := src.At(xx, yy).RGBA()
					w := uint64(0xffff - ca) // blend onto white
					r += uint64(cr) + w
					g += uint64(cg) + w
					bl += uint64(cb) + w
					n++
				}
			}
			dst.SetRGBA(x, y, color.RGBA{uint8(r / n >> 8), uint8(g / n >> 8), uint8(bl / n >> 8), 255})
		}
	}
	return dst
}

// oriented is a zero-copy view of an image with an EXIF orientation applied.
type oriented struct {
	src image.Image
	o   int
}

func (v oriented) ColorModel() color.Model { return v.src.ColorModel() }

func (v oriented) Bounds() image.Rectangle {
	b := v.src.Bounds()
	if v.o >= 5 {
		return image.Rect(0, 0, b.Dy(), b.Dx())
	}
	return image.Rect(0, 0, b.Dx(), b.Dy())
}

func (v oriented) At(x, y int) color.Color {
	b := v.src.Bounds()
	w, h := b.Dx(), b.Dy()
	var sx, sy int
	switch v.o {
	case 2:
		sx, sy = w-1-x, y
	case 3:
		sx, sy = w-1-x, h-1-y
	case 4:
		sx, sy = x, h-1-y
	case 5:
		sx, sy = y, x
	case 6:
		sx, sy = y, h-1-x
	case 7:
		sx, sy = w-1-y, h-1-x
	case 8:
		sx, sy = w-1-y, x
	default:
		sx, sy = x, y
	}
	return v.src.At(b.Min.X+sx, b.Min.Y+sy)
}

// exifOrientation reads the EXIF orientation tag (1–8) from JPEG data; 1 if absent.
func exifOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	i := 2
	for i+4 <= len(data) {
		if data[i] != 0xFF {
			return 1
		}
		marker := data[i+1]
		if marker == 0x01 || (marker >= 0xD0 && marker <= 0xD8) {
			i += 2
			continue
		}
		if marker == 0xDA || marker == 0xD9 {
			return 1
		}
		segLen := int(data[i+2])<<8 | int(data[i+3])
		if segLen < 2 || i+2+segLen > len(data) {
			return 1
		}
		if marker == 0xE1 {
			seg := data[i+4 : i+2+segLen]
			if len(seg) > 6 && string(seg[:6]) == "Exif\x00\x00" {
				return tiffOrientation(seg[6:])
			}
		}
		i += 2 + segLen
	}
	return 1
}

func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 1
	}
	ifd := int(bo.Uint32(t[4:8]))
	if ifd < 8 || ifd+2 > len(t) {
		return 1
	}
	n := int(bo.Uint16(t[ifd : ifd+2]))
	for k := 0; k < n; k++ {
		e := ifd + 2 + k*12
		if e+12 > len(t) {
			return 1
		}
		if bo.Uint16(t[e:e+2]) == 0x0112 {
			if v := int(bo.Uint16(t[e+8 : e+10])); v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}
