package providers

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestVideoTimingRejects14600ms(t *testing.T) {
	data := buildTestMP4(320, 240, 438, 30000, 1000, false)
	cap := VideoCapability(validVideoConfig("timing", "http://localhost"))
	_, err := validateVideoBytes(context.Background(), data, GenerationInput{Params: map[string]any{"duration": 15}}, cap)
	if err == nil || !strings.Contains(err.Error(), "requested duration") {
		t.Fatalf("14.6s video must fail requested duration: %v", err)
	}
}

func TestVideoTimingRealFFprobe(t *testing.T) {
	cap := VideoCapability(validVideoConfig("timing", "http://localhost"))
	for _, rate := range []struct {
		name                 string
		scale, ticks, frames uint32
	}{
		{"24", 24000, 1000, 360}, {"25", 25000, 1000, 375}, {"30", 30000, 1000, 450}, {"30000/1001", 30000, 1001, 450},
	} {
		t.Run(rate.name, func(t *testing.T) {
			for _, delta := range []int{0, -1, -2, 1, 2} {
				t.Run(fmt.Sprint(delta), func(t *testing.T) {
					data := buildTestMP4(320, 240, uint32(int(rate.frames)+delta), rate.scale, rate.ticks, true)
					meta, err := validateVideoBytes(context.Background(), data, GenerationInput{Params: map[string]any{"duration": 15, "generate_audio": true}}, cap)
					// 29.97fps cannot represent exactly 15s: 450 frames are 15.015s, 451 are over one frame late.
					wantOK := delta >= -1 && delta <= 1
					if rate.name == "30000/1001" {
						wantOK = delta == 0 || delta == -1
					}
					if (err == nil) != wantOK {
						t.Fatalf("acceptance = %v, want %v: %v", err == nil, wantOK, err)
					}
					if err == nil && (meta.FrameDurationTS != int64(rate.ticks) || meta.AudioTracks != 1 || meta.DurationTS <= 0 || meta.TimingTolerance == "") {
						t.Fatalf("missing timing/audio evidence: %+v", meta)
					}
				})
			}
		})
	}
}

// Numeric corners use the same JSON parser and duration gate as real ffprobe collection.
func timingProbeJSON(t *testing.T, rate, timebase string, durations ...int64) (map[string]any, MP4Metadata) {
	t.Helper()
	tick, ok := videoRational(timebase)
	if !ok {
		t.Fatal("invalid test timebase")
	}
	var total int64
	packets := make([]any, 0, len(durations))
	for _, d := range durations {
		packets = append(packets, map[string]any{"stream_index": 0, "pts": total, "duration": d, "size": "10"})
		total += d
	}
	duration := new(big.Rat).Mul(tick, new(big.Rat).SetInt64(total))
	seconds, _ := duration.Float64()
	return map[string]any{
		"format":  map[string]any{"format_name": "mov,mp4,m4a,3gp,3g2,mj2", "duration": duration.FloatString(6)},
		"streams": []any{map[string]any{"index": 0, "codec_type": "video", "codec_name": "h264", "width": 320, "height": 240, "time_base": timebase, "duration_ts": total, "avg_frame_rate": rate, "r_frame_rate": rate}},
		"packets": packets,
	}, MP4Metadata{DurationMS: int64(math.Round(seconds * 1000))}
}

func parseTimingFixture(t *testing.T, probe map[string]any, base MP4Metadata) (MP4Metadata, error) {
	t.Helper()
	b, err := json.Marshal(probe)
	if err != nil {
		t.Fatal(err)
	}
	return parseVideoProbe(b, base)
}

func TestVideoTimingRationalBoundaries(t *testing.T) {
	for _, rate := range []string{"24/1", "25/1", "30/1", "30000/1001"} {
		t.Run(rate, func(t *testing.T) {
			fps, _ := videoRational(rate)
			timebase := "1/" + fps.Num().String()
			ticks := fps.Denom().Int64()
			probe, base := timingProbeJSON(t, rate, timebase, ticks, ticks, ticks)
			meta, err := parseTimingFixture(t, probe, base)
			if err != nil {
				t.Fatal(err)
			}
			frame := new(big.Rat).Inv(fps)
			tick, _ := videoRational(timebase)
			for _, sign := range []int64{-1, 1} {
				want := new(big.Rat).Add(meta.timing.duration, new(big.Rat).Mul(frame, big.NewRat(sign, 1)))
				if err := validateVideoTiming(meta, want); err != nil {
					t.Fatalf("exactly one frame rejected: %v", err)
				}
				want.Add(want, new(big.Rat).Mul(tick, big.NewRat(sign, 1)))
				if err := validateVideoTiming(meta, want); err == nil {
					t.Fatal("one frame plus one tick accepted")
				}
			}
		})
	}
}

func TestVideoTimingInvalidEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"empty timebase", func(p map[string]any) { p["streams"].([]any)[0].(map[string]any)["time_base"] = "" }},
		{"zero timebase", func(p map[string]any) { p["streams"].([]any)[0].(map[string]any)["time_base"] = "0/0" }},
		{"negative timebase", func(p map[string]any) { p["streams"].([]any)[0].(map[string]any)["time_base"] = "-1/30" }},
		{"overflow timebase", func(p map[string]any) {
			p["streams"].([]any)[0].(map[string]any)["time_base"] = "1/9223372036854775808"
		}},
		{"bad rate", func(p map[string]any) { p["streams"].([]any)[0].(map[string]any)["avg_frame_rate"] = "bad" }},
		{"NaN rate", func(p map[string]any) { p["streams"].([]any)[0].(map[string]any)["avg_frame_rate"] = "NaN" }},
		{"Inf rate", func(p map[string]any) { p["streams"].([]any)[0].(map[string]any)["r_frame_rate"] = "Inf" }},
		{"rate conflict", func(p map[string]any) { p["streams"].([]any)[0].(map[string]any)["r_frame_rate"] = "24/1" }},
		{"NaN duration", func(p map[string]any) { p["format"].(map[string]any)["duration"] = "NaN" }},
		{"Inf duration", func(p map[string]any) { p["format"].(map[string]any)["duration"] = "Inf" }},
		{"duration_ts contradiction", func(p map[string]any) { p["streams"].([]any)[0].(map[string]any)["duration_ts"] = 100001 }},
		{"duration_ts overflow", func(p map[string]any) {
			p["streams"].([]any)[0].(map[string]any)["duration_ts"] = json.Number("9223372036854775808")
		}},
		{"no packets", func(p map[string]any) { p["packets"] = []any{} }},
		{"missing PTS", func(p map[string]any) { delete(p["packets"].([]any)[0].(map[string]any), "pts") }},
		{"packet overflow", func(p map[string]any) { p["packets"].([]any)[0].(map[string]any)["pts"] = int64(math.MaxInt64) }},
		{"empty packet", func(p map[string]any) { p["packets"].([]any)[0].(map[string]any)["size"] = "0" }},
		{"gap", func(p map[string]any) { p["packets"].([]any)[1].(map[string]any)["pts"] = 1001 }},
		{"overlap", func(p map[string]any) { p["packets"].([]any)[1].(map[string]any)["pts"] = 999 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, base := timingProbeJSON(t, "30/1", "1/30000", 1000, 1000, 1000)
			tc.mutate(p)
			if _, err := parseTimingFixture(t, p, base); err == nil {
				t.Fatal("invalid evidence accepted")
			}
		})
	}
	for _, absent := range []string{"", "0/0"} {
		p, base := timingProbeJSON(t, absent, "1/30000", 1000, 1000, 1000)
		meta, err := parseTimingFixture(t, p, base)
		if err != nil || meta.TimingTolerance != "1/30" {
			t.Fatalf("actual packet fallback with rate %q: %+v %v", absent, meta, err)
		}
	}
	if _, err := probeMP4WithFFprobe(context.Background(), "", nil, MP4Metadata{}); err == nil {
		t.Fatal("missing pinned ffprobe accepted")
	}
	var buffer bytes.Buffer
	if _, err := (&videoProbeBuffer{buffer: &buffer, limit: 64}).Write(make([]byte, 65)); err == nil {
		t.Fatal("unbounded probe output accepted")
	}
}

func TestVideoTimingProbeOutputLimit(t *testing.T) {
	if os.Getenv("MOVIEDESIGN_TIMING_PROBE_OUTPUT_HELPER") == "1" {
		fmt.Print(strings.Repeat("x", 65))
		time.Sleep(10 * time.Second)
		os.Exit(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestVideoTimingProbeOutputLimit$")
	command.Env = append(os.Environ(), "MOVIEDESIGN_TIMING_PROBE_OUTPUT_HELPER=1")
	var output bytes.Buffer
	command.Stdout = &videoProbeBuffer{buffer: &output, limit: 64, cancel: cancel}
	if err := command.Run(); err == nil {
		t.Fatal("oversized probe output accepted")
	}
	if ctx.Err() != context.Canceled || output.Len() > 64 || command.ProcessState == nil {
		t.Fatalf("probe was not immediately canceled and reaped: error=%v bytes=%d state=%v", ctx.Err(), output.Len(), command.ProcessState)
	}
}

func TestVideoTimingVFRAndSinglePacket(t *testing.T) {
	for _, durations := range [][]int64{{1000, 2000, 1000}, {14600}} {
		p, base := timingProbeJSON(t, "0/0", "1/1000", durations...)
		meta, err := parseTimingFixture(t, p, base)
		if err != nil {
			t.Fatal(err)
		}
		if meta.TimingTolerance != "1/1000" {
			t.Fatalf("unjustified VFR/single packet slack: %+v", meta)
		}
		if err := validateVideoTiming(meta, new(big.Rat).Set(meta.timing.duration)); err != nil {
			t.Fatal(err)
		}
		if err := validateVideoTiming(meta, new(big.Rat).Add(meta.timing.duration, big.NewRat(1, 100))); err == nil {
			t.Fatal("VFR/single packet granted average/whole-clip frame tolerance")
		}
	}
}

func TestVideoTimingEmptyAudio(t *testing.T) {
	for _, size := range []string{"", "0", "6"} {
		p, base := timingProbeJSON(t, "30/1", "1/30000", 1000, 1000, 1000)
		p["streams"] = append(p["streams"].([]any), map[string]any{"index": 1, "codec_type": "audio", "codec_name": "aac"})
		if size != "" {
			p["packets"] = append(p["packets"].([]any), map[string]any{"stream_index": 1, "size": size, "duration": 1024})
		}
		meta, err := parseTimingFixture(t, p, base)
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		if size == "6" {
			want = 1
		}
		if meta.AudioTracks != want {
			t.Fatalf("audio size %q: got %d tracks, want %d", size, meta.AudioTracks, want)
		}
	}
	data := BuildTestMP4(320, 240, 15, true)
	for offset := 0; offset+20 <= len(data); offset++ {
		if string(data[offset+4:offset+8]) == "stsz" && binary.BigEndian.Uint32(data[offset+16:offset+20]) == 1 {
			binary.BigEndian.PutUint32(data[offset+16:offset+20], 0)
		}
	}
	cap := VideoCapability(validVideoConfig("timing", "http://localhost"))
	if _, err := validateVideoBytes(context.Background(), data, GenerationInput{Params: map[string]any{"duration": 15, "generate_audio": true}}, cap); err == nil {
		t.Fatal("empty audio sample table accepted")
	}
}

func TestVideoTimingCollectionEvidence(t *testing.T) {
	s, project := imageService(t)
	ctx := context.Background()
	data := BuildTestMP4(320, 240, 15, true)
	downloads := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloads++
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write(data)
	}))
	defer remote.Close()
	frozen := FrozenProvider{Config: validVideoConfig("timing", remote.URL)}
	in := GenerationInput{Params: map[string]any{"duration": 15, "generate_audio": true}}
	result := ProviderResult{Data: []ProviderImage{{URL: remote.URL + "/video.mp4"}}}
	for i := 0; i < 2; i++ {
		if _, err := s.CollectVideos(ctx, project, in, result, frozen, []string{"timing-output"}); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	var raw string
	if err := s.DB.QueryRow("SELECT count(*),payload_json FROM events WHERE project_id=? AND type='video_timing_verified'", project).Scan(&count, &raw); err != nil {
		t.Fatal(err)
	}
	var evidence struct {
		Key      string      `json:"collection_key"`
		Hash     string      `json:"sha256"`
		Metadata MP4Metadata `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(raw), &evidence); err != nil {
		t.Fatal(err)
	}
	if count != 1 || downloads != 1 || evidence.Key != "timing-output" || evidence.Hash != digest(data) || evidence.Metadata.FrameDurationTS != 1000 || evidence.Metadata.DurationTS != 450000 || evidence.Metadata.TimingTolerance != "1/30" || evidence.Metadata.Timebase != "1/30000" {
		t.Fatalf("wrong persisted evidence: count=%d downloads=%d %s", count, downloads, raw)
	}
	// Failed technical validation must never leave a successful timing event.
	in.Params["duration"] = 16
	if _, err := s.CollectVideos(ctx, project, in, result, frozen, []string{"timing-invalid"}); err == nil {
		t.Fatal("invalid duration accepted")
	}
	if err := s.DB.QueryRow("SELECT count(*) FROM events WHERE type='video_timing_verified'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("invalid collection left evidence: %d %v", count, err)
	}
	in.Params["duration"] = 15
	if _, err := s.DB.Exec("UPDATE events SET payload_json=json_set(payload_json,'$.sha256','changed') WHERE type='video_timing_verified'"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CollectVideos(ctx, project, in, result, frozen, []string{"timing-output"}); err == nil || !strings.Contains(err.Error(), "evidence changed") {
		t.Fatalf("changed evidence accepted: %v", err)
	}
}

func TestVideoTimingRealVFR(t *testing.T) {
	data := buildTestMP4(320, 240, 4, 1000, 1000, false)
	// Replace one stts entry by four durations with the same four-second total. Keep chunk offsets and ancestor sizes valid.
	var rewrite func([]byte) []byte
	rewrite = func(data []byte) []byte {
		var out []byte
		for offset := 0; offset < len(data); {
			size := int(binary.BigEndian.Uint32(data[offset : offset+4]))
			kind, payload := string(data[offset+4:offset+8]), append([]byte(nil), data[offset+8:offset+size]...)
			switch kind {
			case "moov", "trak", "mdia", "minf", "stbl":
				payload = rewrite(payload)
			case "stts":
				payload = make([]byte, 40)
				binary.BigEndian.PutUint32(payload[4:8], 4)
				for i, ticks := range []uint32{500, 1500, 500, 1500} {
					binary.BigEndian.PutUint32(payload[8+i*8:12+i*8], 1)
					binary.BigEndian.PutUint32(payload[12+i*8:16+i*8], ticks)
				}
			case "stco":
				binary.BigEndian.PutUint32(payload[8:12], binary.BigEndian.Uint32(payload[8:12])+24)
			}
			out = append(out, mp4Box(kind, payload)...)
			offset += size
		}
		return out
	}
	data = rewrite(data)
	cap := VideoCapability(validVideoConfig("timing", "http://localhost"))
	meta, err := validateVideoBytes(context.Background(), data, GenerationInput{Params: map[string]any{"duration": 4}}, cap)
	if err != nil || meta.TimingTolerance != "1/1000" || meta.FrameDurationTS != 1500 {
		t.Fatalf("real VFR timing: %+v %v", meta, err)
	}
	if err := validateVideoTiming(meta, big.NewRat(401, 100)); err == nil {
		t.Fatal("real VFR received wide average-frame tolerance")
	}
}
