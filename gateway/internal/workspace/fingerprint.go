package workspace

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"github.com/zeebo/xxh3"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
)

type FileFingerprint struct {
	Size       int64   `json:"size"`
	MtimeMS    int64   `json:"mtime_ms"`
	DevID      int64   `json:"dev_id"`
	Inode      int64   `json:"inode"`
	QuickHash  string  `json:"quick_hash"`
	FullHash   string  `json:"full_hash,omitempty"`
	MIME       string  `json:"mime_type"`
	Width      *int64  `json:"width"`
	Height     *int64  `json:"height"`
	DurationMS *int64  `json:"duration_ms,omitempty"`
	Metadata   *string `json:"metadata,omitempty"`
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
	} else if v.MIME == "video/mp4" && v.Size >= 32 && v.Size <= 256<<20 {
		probeMP4Fingerprint(f, v.Size, &v)
	}
	return v, nil
}

func probeMP4Fingerprint(f *os.File, size int64, out *FileFingerprint) {
	data, err := io.ReadAll(io.NewSectionReader(f, 0, min(size, 16<<20)))
	if err != nil || len(data) < 32 {
		return
	}
	var width, height, audioTracks int
	var durationMS int64
	var codec, audioCodec, timebase string
	var sawFtyp, sawMoov bool
	var parseTrak func(buf []byte)
	parseTrak = func(buf []byte) {
		var hdlr, cName, tb string
		var w, h int
		var dMS int64
		var walk func(b []byte)
		walk = func(b []byte) {
			for off := 0; off+8 <= len(b); {
				sz := int(binary.BigEndian.Uint32(b[off : off+4]))
				tp := string(b[off+4 : off+8])
				if sz == 0 {
					sz = len(b) - off
				}
				if sz < 8 || off+sz > len(b) {
					return
				}
				pl := b[off+8 : off+sz]
				switch tp {
				case "tkhd":
					if len(pl) >= 84 && pl[0] == 0 {
						tw := int(binary.BigEndian.Uint32(pl[76:80]) >> 16)
						th := int(binary.BigEndian.Uint32(pl[80:84]) >> 16)
						if tw > 0 && th > 0 {
							w, h = tw, th
						}
					}
				case "mdhd":
					if len(pl) >= 20 && pl[0] == 0 {
						ts := binary.BigEndian.Uint32(pl[12:16])
						dur := binary.BigEndian.Uint32(pl[16:20])
						if ts > 0 {
							tb = fmt.Sprintf("1/%d", ts)
							dMS = int64(math.Round(float64(dur) * 1000.0 / float64(ts)))
						}
					}
				case "hdlr":
					if len(pl) >= 12 {
						hdlr = string(pl[8:12])
					}
				case "stsd":
					if len(pl) >= 16 {
						es := int(binary.BigEndian.Uint32(pl[8:12]))
						cName = strings.TrimSpace(string(pl[12:16]))
						if hdlr == "vide" && es >= 40 && len(pl) >= 48 {
							sw := int(binary.BigEndian.Uint16(pl[40:42]))
							sh := int(binary.BigEndian.Uint16(pl[42:44]))
							if sw > 0 && sh > 0 {
								w, h = sw, sh
							}
						}
					}
				case "mdia", "minf", "stbl":
					walk(pl)
				}
				off += sz
			}
		}
		walk(buf)
		if hdlr == "vide" {
			if w > 0 && h > 0 {
				width, height = w, h
			}
			if cName != "" {
				codec = cName
			}
			if tb != "" {
				timebase = tb
			}
			if durationMS == 0 && dMS > 0 {
				durationMS = dMS
			}
		} else if hdlr == "soun" {
			audioTracks++
			if cName != "" && audioCodec == "" {
				audioCodec = cName
			}
		}
	}
	for off := 0; off+8 <= len(data); {
		sz := int(binary.BigEndian.Uint32(data[off : off+4]))
		tp := string(data[off+4 : off+8])
		if sz == 0 {
			sz = len(data) - off
		}
		if sz < 8 || off+sz > len(data) {
			break
		}
		pl := data[off+8 : off+sz]
		switch tp {
		case "ftyp":
			sawFtyp = true
		case "moov":
			sawMoov = true
			for mOff := 0; mOff+8 <= len(pl); {
				mSz := int(binary.BigEndian.Uint32(pl[mOff : mOff+4]))
				mTp := string(pl[mOff+4 : mOff+8])
				if mSz == 0 {
					mSz = len(pl) - mOff
				}
				if mSz < 8 || mOff+mSz > len(pl) {
					break
				}
				mPl := pl[mOff+8 : mOff+mSz]
				if mTp == "mvhd" && len(mPl) >= 20 && mPl[0] == 0 {
					ts := binary.BigEndian.Uint32(mPl[12:16])
					dur := binary.BigEndian.Uint32(mPl[16:20])
					if ts > 0 {
						durationMS = int64(math.Round(float64(dur) * 1000.0 / float64(ts)))
						if timebase == "" {
							timebase = fmt.Sprintf("1/%d", ts)
						}
					}
				} else if mTp == "trak" {
					parseTrak(mPl)
				}
				mOff += mSz
			}
		}
		off += sz
	}
	if sawFtyp && sawMoov && width > 0 && height > 0 && durationMS > 0 {
		w64, h64, d64 := int64(width), int64(height), durationMS
		out.Width = &w64
		out.Height = &h64
		out.DurationMS = &d64
		metaMap := map[string]any{
			"format":      "mp4",
			"codec":       codec,
			"width":       width,
			"height":      height,
			"durationMs":  durationMS,
			"timebase":    timebase,
			"audioTracks": audioTracks,
		}
		if audioCodec != "" {
			metaMap["audioCodec"] = audioCodec
		}
		if encoded, err := json.Marshal(metaMap); err == nil {
			s := string(encoded)
			out.Metadata = &s
		}
	}
}
