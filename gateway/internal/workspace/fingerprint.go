package workspace

import (
	"fmt"
	"github.com/zeebo/xxh3"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
)

type FileFingerprint struct {
	Size      int64  `json:"size"`
	MtimeMS   int64  `json:"mtime_ms"`
	DevID     int64  `json:"dev_id"`
	Inode     int64  `json:"inode"`
	QuickHash string `json:"quick_hash"`
	FullHash  string `json:"full_hash,omitempty"`
	MIME      string `json:"mime_type"`
	Width     *int64 `json:"width"`
	Height    *int64 `json:"height"`
}

func Fingerprint(f *os.File, full bool) (FileFingerprint, error) {
	var v FileFingerprint
	if e := checkHandle(f, true); e != nil {
		return v, e
	}
	s, e := f.Stat()
	if e != nil {
		return v, e
	}
	v.Size = s.Size()
	v.MtimeMS = s.ModTime().UnixMilli()
	v.DevID, v.Inode, e = fileIdentity(f)
	if e != nil {
		return v, e
	}
	h := xxh3.New()
	if _, e = io.Copy(h, io.NewSectionReader(f, 0, 4<<20)); e != nil {
		return v, e
	}
	v.QuickHash = fmt.Sprintf("%016x", h.Sum64())
	if full {
		h := xxh3.New()
		if _, e = io.Copy(h, io.NewSectionReader(f, 0, v.Size)); e != nil {
			return v, e
		}
		v.FullHash = fmt.Sprintf("%016x", h.Sum64())
	}
	b := make([]byte, 512)
	n, e := f.ReadAt(b, 0)
	if e != nil && e != io.EOF {
		return v, e
	}
	v.MIME = http.DetectContentType(b[:n])
	if c, _, e := image.DecodeConfig(io.NewSectionReader(f, 0, v.Size)); e == nil {
		x, y := int64(c.Width), int64(c.Height)
		v.Width = &x
		v.Height = &y
	}
	return v, nil
}
