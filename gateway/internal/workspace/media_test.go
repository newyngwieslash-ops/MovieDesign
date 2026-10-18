package workspace

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"os"
	"strings"
	"testing"
)

func TestPinnedProbeManagedAudio(t *testing.T) {
	probe := os.Getenv("MOVIEDESIGN_FFPROBE_TEST_BINARY")
	if probe == "" {
		t.Skip("build pinned public ffprobe first")
	}
	s, p := setup(t)
	var wav bytes.Buffer
	wav.WriteString("RIFF")
	binary.Write(&wav, binary.LittleEndian, uint32(36+16000))
	wav.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(1), uint32(8000), uint32(16000), uint16(2), uint16(16)} {
		binary.Write(&wav, binary.LittleEndian, v)
	}
	wav.WriteString("data")
	binary.Write(&wav, binary.LittleEndian, uint32(16000))
	wav.Write(make([]byte, 16000))
	v, e := s.Stage(context.Background(), p, "audio", "sound.wav", &wav)
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.Commit(context.Background(), p, "audio-commit", v)
	if e != nil {
		t.Fatal(e)
	}
	m, e := s.AnalyseMedia(context.Background(), p, a.Path, probe)
	if e != nil || m.AudioTracks != 1 || m.Duration != "1.000000" {
		t.Fatal(m, e)
	}
	for n, content := range []string{"#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXTINF:1,\nfile:///C:/Windows/win.ini\n#EXT-X-ENDLIST\n", "ffconcat version 1.0\nfile 'C:/Windows/win.ini'\n"} {
		v, e := s.Stage(context.Background(), p, fmt.Sprint("playlist", n), "disguised.mp4", strings.NewReader(content))
		if e != nil {
			t.Fatal(e)
		}
		a, e := s.Commit(context.Background(), p, fmt.Sprint("playlist-commit", n), v)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.AnalyseMedia(context.Background(), p, a.Path, probe); e == nil {
			t.Fatal("external-resource demuxer accepted")
		}
	}
}

func TestManagedTextReadAndImageMetadata(t *testing.T) {
	s, p := setup(t)
	ctx := context.Background()
	v, e := s.Stage(ctx, p, "text", "note.md", strings.NewReader("第一行\nsecond\nthird"))
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.Commit(ctx, p, "commit-text", v)
	if e != nil {
		t.Fatal(e)
	}
	v = a.Path
	r, e := s.ReadText(ctx, p, v, 1, 1)
	if e != nil || r.Content != "second" || r.Coverage.Total != 3 || r.Coverage.NextOffset != 2 {
		t.Fatal(r, e)
	}
	if _, e = s.ReadText(ctx, p, "C:/secret.txt", 0, 10); e == nil {
		t.Fatal("host path accepted")
	}
	if _, e = s.ReadText(ctx, p, v, -1, 10); e == nil {
		t.Fatal("negative offset")
	}
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 7, 5)))
	v, e = s.Stage(ctx, p, "image", "test.png", &b)
	if e != nil {
		t.Fatal(e)
	}
	a, e = s.Commit(ctx, p, "commit-image", v)
	if e != nil {
		t.Fatal(e)
	}
	v = a.Path
	m, e := s.AnalyseMedia(ctx, p, v, "")
	if e != nil || m.Width != 7 || m.Height != 5 || m.Format != "png" {
		t.Fatal(m, e)
	}
	if _, e = s.ReadText(ctx, p, v, 0, 10); e == nil {
		t.Fatal("binary document accepted")
	}
	if _, e = s.AnalyseMedia(ctx, "other", v, ""); e == nil {
		t.Fatal("foreign media accepted")
	}
}
