package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

const DocumentLimit = 1 << 20

type TextRead struct {
	Content     string         `json:"content"`
	Coverage    store.Coverage `json:"coverage"`
	SourceBytes int            `json:"sourceBytes"`
}

func TextPage(content string, offset, limit int) (TextRead, error) {
	if offset < 0 || limit < 1 || limit > 2000 || len(content) > DocumentLimit || !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
		return TextRead{}, invalid("Expected bounded UTF-8 text and offset >= 0, limit 1..2000")
	}
	lines := strings.Split(content, "\n")
	start := min(offset, len(lines))
	end := min(start+limit, len(lines))
	return TextRead{strings.Join(lines[start:end], "\n"), store.Coverage{Total: len(lines), NextOffset: end, Exhausted: end == len(lines)}, len(content)}, nil
}
func (s *Service) ReadText(ctx context.Context, p, v string, offset, limit int) (TextRead, error) {
	switch strings.ToLower(path.Ext(v)) {
	case ".md", ".txt", ".srt", ".ass":
	default:
		return TextRead{}, invalid("Only md/txt/srt/ass documents are supported")
	}
	f, e := s.OpenContent(ctx, p, v)
	if e != nil {
		return TextRead{}, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, DocumentLimit+1))
	if e != nil {
		return TextRead{}, e
	}
	return TextPage(string(b), offset, limit)
}

// VersionPath resolves owned active content; callers never accept host paths.
func (s *Service) VersionPath(ctx context.Context, p, id string) (string, error) {
	var v string
	e := s.DB.QueryRowContext(ctx, `SELECT a.path FROM versions v JOIN resources r ON r.id=v.resource_id JOIN compositions c ON c.id=r.composition_id JOIN assets a ON a.id=v.asset_id JOIN asset_projects ap ON ap.asset_id=a.id WHERE v.id=? AND c.project_id=? AND ap.project_id=? AND a.status='active'`, id, p, p).Scan(&v)
	if e != nil {
		return "", missing("Version content unavailable in this project")
	}
	return v, nil
}

type MediaMetadata struct {
	Format      string `json:"format"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
	Duration    string `json:"duration,omitempty"`
	AudioTracks int    `json:"audioTracks"`
}

func (s *Service) AnalyseMedia(ctx context.Context, p, v, probe string) (MediaMetadata, error) {
	rel, e := relative(p, v)
	if e != nil {
		return MediaMetadata{}, e
	}
	// Hold the complete ancestor chain before opening the leaf, through ffprobe.
	_, release, e := s.Workspace.heldParent(p, path.Dir(rel))
	if e != nil {
		return MediaMetadata{}, e
	}
	defer release()
	f, e := s.OpenContent(ctx, p, v)
	if e != nil {
		return MediaMetadata{}, e
	}
	defer f.Close()
	cfg, format, e := image.DecodeConfig(f)
	if e == nil {
		return MediaMetadata{Format: format, Width: cfg.Width, Height: cfg.Height}, nil
	}
	switch strings.ToLower(path.Ext(v)) {
	case ".mp4", ".mov", ".webm", ".mp3", ".wav", ".m4a", ".ogg", ".flac":
	default:
		return MediaMetadata{}, invalid("Unsupported or invalid media")
	}
	if probe == "" || !filepath.IsAbs(probe) {
		return MediaMetadata{}, invalid("Pinned ffprobe is unavailable")
	}
	// The confined file handle remains held without delete sharing during probing.
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	filename, e := heldFilePath(f)
	if e != nil {
		return MediaMetadata{}, e
	}
	command := exec.CommandContext(ctx, probe, "-v", "error", "-max_alloc", "67108864", "-probesize", "8388608", "-analyzeduration", "10000000", "-protocol_whitelist", "file", "-format_whitelist", "mov,matroska,webm,mp3,wav,ogg,flac", "-show_entries", "format=format_name,duration:stream=codec_type,width,height", "-of", "json", filename)
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
	var out bytes.Buffer
	command.Stdout = &probeBuffer{buffer: &out}
	if e = command.Run(); e != nil {
		return MediaMetadata{}, invalid("Media probe failed or exceeded its limit")
	}
	var data struct {
		Format struct {
			FormatName string `json:"format_name"`
			Duration   string `json:"duration"`
		}
		Streams []struct {
			CodecType     string `json:"codec_type"`
			Width, Height int
		}
	}
	if json.Unmarshal(out.Bytes(), &data) != nil {
		return MediaMetadata{}, invalid("Invalid probe output")
	}
	result := MediaMetadata{Format: data.Format.FormatName, Duration: data.Format.Duration}
	for _, stream := range data.Streams {
		if stream.CodecType == "audio" {
			result.AudioTracks++
		}
		if stream.CodecType == "video" && result.Width == 0 {
			result.Width = stream.Width
			result.Height = stream.Height
		}
	}
	return result, nil
}

type probeBuffer struct{ buffer *bytes.Buffer }

func (b *probeBuffer) Write(p []byte) (int, error) {
	if b.buffer.Len()+len(p) > 1<<20 {
		return 0, invalid("Probe output exceeds limit")
	}
	return b.buffer.Write(p)
}
func (s *Service) AssetPath(ctx context.Context, p, id string) (string, error) {
	var v string
	if e := s.DB.QueryRowContext(ctx, "SELECT a.path FROM assets a JOIN asset_projects ap ON ap.asset_id=a.id WHERE a.id=? AND ap.project_id=? AND a.status='active'", id, p).Scan(&v); e != nil {
		return "", missing("Asset unavailable in this project")
	}
	return v, nil
}
