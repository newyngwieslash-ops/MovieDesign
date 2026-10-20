package providers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
)

const MaxVideoBytes int64 = 256 << 20

type VideoTaskObservation struct {
	UpstreamID string
	Status     string
	VideoURL   string
	Duration   *int
	Seed       *int64
	Usage      any
	Result     ProviderResult
	Failure    *ImageError
}

type VideoCancelResult struct {
	UpstreamID      string          `json:"upstream_id"`
	Status          string          `json:"status"`
	RemoteCancelled bool            `json:"remote_cancelled"`
	Message         string          `json:"message"`
	Result          *ProviderResult `json:"result,omitempty"`
}

type MP4Metadata struct {
	Format      string `json:"format"`
	Codec       string `json:"codec"`
	AudioCodec  string `json:"audioCodec,omitempty"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	DurationMS  int64  `json:"durationMs"`
	Timebase    string `json:"timebase"`
	AudioTracks int    `json:"audioTracks"`
}

func sanitizeUpstreamText(value, key string, headers Headers) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, value)
	value = bearerSecret.ReplaceAllString(value, "Bearer [redacted]")
	for _, secret := range []string{key, headers.Organization, headers.Project} {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[redacted]")
		}
	}
	runes := []rune(value)
	if len(runes) > 512 {
		value = string(runes[:512])
	}
	return value
}

func isRetryableVideoErrorCode(code string) bool {
	lower := strings.ToLower(strings.TrimSpace(code))
	if lower == "" {
		return false
	}
	for _, token := range []string{"invalid", "param", "unsupported", "policy", "safety", "content_filter", "moderation", "sensitive", "permission", "unauthorized", "forbidden", "not_found", "model", "quota_exceeded"} {
		if strings.Contains(lower, token) {
			return false
		}
	}
	return true
}

// CreateVideoTask validates frozen input and references, encodes confined reference assets as
// lowercase MIME data URLs inside the adapter, and submits one POST to contents/generations/tasks.
func (s *Service) CreateVideoTask(ctx context.Context, in GenerationInput, f FrozenProvider) (string, error) {
	if e := ValidateConfig(f.Config); e != nil {
		return "", imageFailure("INVALID_CONFIG", e.Error(), true, 0)
	}
	cap := VideoCapability(f.Config)
	if e := ValidateVideo(cap, in); e != nil {
		return "", imageFailure("INVALID_INPUT", e.Error(), true, 0)
	}
	if len(in.Assets) != len(in.References) {
		return "", imageFailure("REFERENCE_CHANGED", "Frozen reference binding missing", true, 0)
	}
	content := make([]map[string]any, 0, 1+len(in.Assets))
	content = append(content, map[string]any{
		"type": "text",
		"text": in.Prompt,
	})
	for i, ref := range in.Assets {
		expectedRole := ReferenceRole(cap, i)
		if s.Files == nil || ref.Asset.ID != in.References[i] || ref.Role != expectedRole {
			return "", imageFailure("REFERENCE_CHANGED", "Reference binding changed", true, 0)
		}
		raw, e := (store.Objects{DB: s.DB}).Get(ctx, in.ProjectID, "assets", ref.Asset.ID)
		var current store.Asset
		if e != nil || json.Unmarshal(raw, &current) != nil || current.Status != "active" || current.Path != ref.Asset.Path {
			return "", imageFailure("REFERENCE_CHANGED", "Reference ownership changed", true, 0)
		}
		if e = s.checkReferencePending(ctx, ref.Asset.ID); e != nil {
			return "", imageFailure("REFERENCE_CHANGED", "Reference has pending changes", true, 0)
		}
		if e = s.Files.Workspace.VerifyDelivery(in.ProjectID, ref.Asset.Path, ref.Asset); e != nil {
			return "", imageFailure("REFERENCE_CHANGED", "Reference file identity changed", true, 0)
		}
		file, e := s.Files.Workspace.OpenRegular(in.ProjectID, ref.Asset.Path)
		if e != nil {
			return "", imageFailure("REFERENCE_CHANGED", "Reference unavailable", true, 0)
		}
		b, e := readBounded(file, cap.Constraints.MaxImageBytes)
		file.Close()
		if e != nil || digest(b) != ref.SHA256 {
			return "", imageFailure("REFERENCE_CHANGED", "Reference bytes changed", true, 0)
		}
		c, format, e := imageBytes(b, cap)
		if e != nil || c.Width != ref.Width || c.Height != ref.Height {
			return "", imageFailure("INVALID_REFERENCE", "Invalid reference image media or dimensions", true, 0)
		}
		mimeLower := "image/" + strings.ToLower(format)
		dataURL := "data:" + mimeLower + ";base64," + base64.StdEncoding.EncodeToString(b)
		item := map[string]any{
			"type": "image_url",
			"image_url": map[string]any{
				"url": dataURL,
			},
		}
		if ref.Role != "" {
			item["role"] = ref.Role
		}
		content = append(content, item)
	}
	fields := map[string]any{
		"model":   f.Config.ModelID,
		"content": content,
	}
	for k, v := range in.Params {
		fields[k] = v
	}
	var body bytes.Buffer
	if e := json.NewEncoder(&body).Encode(fields); e != nil {
		return "", imageFailure("INVALID_INPUT", "Invalid video generation parameters", true, 0)
	}
	endpoint, e := Endpoint(f.Config.BaseURL, "contents/generations/tasks")
	if e != nil {
		return "", imageFailure("INVALID_CONFIG", "Invalid video generation endpoint", true, 0)
	}
	key, e := s.Credential(f)
	if e != nil {
		return "", imageFailure("CREDENTIAL_UNAVAILABLE", "Frozen credential version unavailable", true, 0)
	}
	client, e := newClient(f.Config.BaseURL, false, 30*time.Second)
	if e != nil {
		return "", imageFailure("INVALID_CONFIG", "Invalid video generation endpoint", true, 0)
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if e != nil {
		return "", imageFailure("INVALID_CONFIG", "Invalid video generation request", true, 0)
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if f.Config.Headers.Organization != "" {
		req.Header.Set("OpenAI-Organization", f.Config.Headers.Organization)
	}
	if f.Config.Headers.Project != "" {
		req.Header.Set("OpenAI-Project", f.Config.Headers.Project)
	}
	req.GetBody = nil
	response, e := client.Do(req)
	if e != nil {
		return "", imageFailure("OUTCOME_UNKNOWN", "Video task creation transport interrupted; do not resubmit", false, 0)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		return "", upstreamFailure(response, key, f.Config.Headers)
	}
	raw, e := readBounded(response.Body, 64<<10)
	if e != nil {
		return "", imageFailure("OUTCOME_UNKNOWN", "Video task creation response incomplete; do not resubmit", false, response.StatusCode)
	}
	var created struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &created) != nil || created.ID == "" || len(created.ID) > 256 || !identifier.MatchString(created.ID) {
		return "", imageFailure("OUTCOME_UNKNOWN", "Video task creation response missing valid task id; do not resubmit", false, response.StatusCode)
	}
	return created.ID, nil
}

func videoError(code, message string, notAccepted bool, status int) *ImageError {
	return &ImageError{Code: code, Message: message, NotAccepted: notAccepted, HTTPStatus: status}
}

// QueryVideoTask performs a single read-only GET on contents/generations/tasks/{id}.
func (s *Service) QueryVideoTask(ctx context.Context, f FrozenProvider, upstreamID string) (VideoTaskObservation, error) {
	obs := VideoTaskObservation{UpstreamID: upstreamID}
	if upstreamID == "" || len(upstreamID) > 256 || !identifier.MatchString(upstreamID) {
		err := videoError("INVALID_UPSTREAM_ID", "Invalid upstream video task ID", false, 0)
		obs.Failure = err
		return obs, err
	}
	endpoint, e := Endpoint(f.Config.BaseURL, "contents/generations/tasks/"+upstreamID)
	if e != nil {
		err := videoError("INVALID_CONFIG", "Invalid video task query endpoint", false, 0)
		obs.Failure = err
		return obs, err
	}
	key, e := s.Credential(f)
	if e != nil {
		err := videoError("CREDENTIAL_UNAVAILABLE", "Frozen credential version unavailable", false, 0)
		obs.Failure = err
		return obs, err
	}
	client, e := NewClient(f.Config.BaseURL, false)
	if e != nil {
		err := videoError("INVALID_CONFIG", "Invalid video task query endpoint", false, 0)
		obs.Failure = err
		return obs, err
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if e != nil {
		err := videoError("INVALID_CONFIG", "Invalid video task query request", false, 0)
		obs.Failure = err
		return obs, err
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if f.Config.Headers.Organization != "" {
		req.Header.Set("OpenAI-Organization", f.Config.Headers.Organization)
	}
	if f.Config.Headers.Project != "" {
		req.Header.Set("OpenAI-Project", f.Config.Headers.Project)
	}
	response, e := client.Do(req)
	if e != nil {
		err := videoError("OUTCOME_UNKNOWN", "Video task status query interrupted; do not resubmit", false, 0)
		obs.Failure = err
		return obs, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		uErr := upstreamFailure(response, key, f.Config.Headers)
		err, _ := uErr.(*ImageError)
		if err == nil {
			err = videoError("OUTCOME_UNKNOWN", uErr.Error(), false, response.StatusCode)
		}
		err.NotAccepted = false
		obs.Failure = err
		return obs, err
	}
	raw, e := readBounded(response.Body, 256<<10)
	if e != nil {
		err := videoError("OUTCOME_UNKNOWN", "Video task status response incomplete; do not resubmit", false, 200)
		obs.Failure = err
		return obs, err
	}
	var payload struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Status  string `json:"status"`
		Content struct {
			VideoURL string `json:"video_url"`
		} `json:"content"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Duration *int   `json:"duration"`
		Seed     *int64 `json:"seed"`
		Usage    any    `json:"usage"`
	}
	if json.Unmarshal(raw, &payload) != nil || payload.Status == "" {
		err := videoError("OUTCOME_UNKNOWN", "Video task status response invalid; do not resubmit", false, 200)
		obs.Failure = err
		return obs, err
	}
	status := strings.ToLower(strings.TrimSpace(payload.Status))
	obs.Status = status
	obs.Duration = payload.Duration
	obs.Seed = payload.Seed
	obs.Usage = payload.Usage
	switch status {
	case "queued", "running", "in_progress", "processing":
		if status != "queued" {
			obs.Status = "running"
		}
		return obs, nil
	case "succeeded":
		if strings.TrimSpace(payload.Content.VideoURL) == "" {
			err := videoError("INVALID_MEDIA_RESULT", "Succeeded video task did not return content.video_url", false, 200)
			obs.Failure = err
			return obs, err
		}
		obs.VideoURL = payload.Content.VideoURL
		obs.Result = ProviderResult{
			Data: []ProviderImage{{URL: payload.Content.VideoURL}},
		}
		return obs, nil
	case "failed":
		code := "VIDEO_TASK_FAILED"
		msg := "Upstream video task failed"
		if payload.Error != nil {
			if c := sanitizeUpstreamText(payload.Error.Code, key, f.Config.Headers); len(c) > 0 && len(c) <= 100 && identifier.MatchString(c) {
				code = c
			}
			if m := sanitizeUpstreamText(payload.Error.Message, key, f.Config.Headers); m != "" {
				msg = m
			}
		}
		err := videoError(code, msg, isRetryableVideoErrorCode(code), 200)
		obs.Failure = err
		return obs, err
	case "expired":
		err := videoError("TASK_EXPIRED", "Upstream video generation task expired", true, 200)
		obs.Failure = err
		return obs, err
	case "cancelled", "canceled":
		obs.Status = "cancelled"
		err := videoError("TASK_CANCELLED", "Upstream video generation task was cancelled", false, 200)
		obs.Failure = err
		return obs, err
	default:
		err := videoError("OUTCOME_UNKNOWN", "Unsupported upstream video task status; do not resubmit", false, 200)
		obs.Failure = err
		return obs, err
	}
}

// CancelVideoTask enforces Ark's cancellation semantics:
// - Only calls DELETE when the live upstream task is in 'queued' status.
// - Never calls DELETE on 'running', 'succeeded', 'failed', or 'expired' tasks (which on Ark either fails or permanently deletes task records).
func (s *Service) CancelVideoTask(ctx context.Context, f FrozenProvider, upstreamID string) (VideoCancelResult, error) {
	res := VideoCancelResult{UpstreamID: upstreamID}
	obs, err := s.QueryVideoTask(ctx, f, upstreamID)
	if err != nil && obs.Status == "" {
		res.Status = "unknown"
		res.Message = "本地已取消自动入库；远端状态暂不可达，已保留 upstream_id 供后续对账。"
		return res, err
	}
	res.Status = obs.Status
	switch obs.Status {
	case "queued":
		endpoint, e := Endpoint(f.Config.BaseURL, "contents/generations/tasks/"+upstreamID)
		if e != nil {
			return res, e
		}
		key, e := s.Credential(f)
		if e != nil {
			return res, e
		}
		client, e := NewClient(f.Config.BaseURL, false)
		if e != nil {
			return res, e
		}
		req, e := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
		if e != nil {
			return res, e
		}
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		if f.Config.Headers.Organization != "" {
			req.Header.Set("OpenAI-Organization", f.Config.Headers.Organization)
		}
		if f.Config.Headers.Project != "" {
			req.Header.Set("OpenAI-Project", f.Config.Headers.Project)
		}
		resp, e := client.Do(req)
		if e != nil {
			res.Message = "本地已取消自动入库；远端排队取消请求未完成，仍可能产生费用。"
			return res, e
		}
		_, _ = readBounded(resp.Body, 16<<10)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
			res.Status = "cancelled"
			res.RemoteCancelled = true
			res.Message = "已取消远端排队中的视频任务（queued），未进入运行阶段。"
			return res, nil
		}
		res.Status = "running"
		res.RemoteCancelled = false
		res.Message = "本地已取消自动入库；远端任务已离开排队阶段，供应商不支持中断运行中任务，仍可能产生费用。"
		return res, nil
	case "running":
		res.RemoteCancelled = false
		res.Message = "本地已取消自动入库；远端任务已进入 running 阶段，供应商不支持中断运行中任务，仍可能产生费用。若晚到产出结果，将通过确认卡询问是否采纳。"
		return res, nil
	case "succeeded":
		res.RemoteCancelled = false
		res.Result = &obs.Result
		res.Message = "本地已取消自动入库；远端视频已生成完成，未调用 DELETE 销毁记录，可通过确认卡决定是否采纳。"
		return res, nil
	default:
		res.RemoteCancelled = false
		res.Message = fmt.Sprintf("本地已取消；远端任务已处于终态（%s），保留上游记录。", obs.Status)
		return res, nil
	}
}

// PollVideoTask polls QueryVideoTask with bounded backoff until terminal status or context/cancellation.
func (s *Service) PollVideoTask(ctx context.Context, f FrozenProvider, upstreamID string, isCancelled func() bool) (VideoTaskObservation, error) {
	delay := 25 * time.Millisecond
	maxDelay := 1500 * time.Millisecond
	transientFailures := 0
	for {
		if isCancelled != nil && isCancelled() {
			err := videoError("TASK_CANCELLED_QUEUED", "Video task was cancelled while queued", false, 200)
			return VideoTaskObservation{UpstreamID: upstreamID, Status: "cancelled", Failure: err}, nil
		}
		obs, err := s.QueryVideoTask(ctx, f, upstreamID)
		if err != nil {
			if obs.Status == "failed" || obs.Status == "expired" || obs.Status == "cancelled" || obs.Status == "succeeded" {
				return obs, err
			}
			if ctx.Err() != nil {
				return VideoTaskObservation{UpstreamID: upstreamID, Status: "running"}, nil
			}
			transientFailures++
			if transientFailures >= 3 {
				return obs, err
			}
		} else {
			transientFailures = 0
			if obs.Status == "succeeded" {
				return obs, nil
			}
			if obs.Status != "queued" && obs.Status != "running" {
				return obs, obs.Failure
			}
		}
		if isCancelled != nil && isCancelled() {
			continue
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return VideoTaskObservation{UpstreamID: upstreamID, Status: "running"}, nil
		case <-timer.C:
		}
		if delay < maxDelay {
			delay *= 2
			if delay > maxDelay {
				delay = maxDelay
			}
		}
	}
}

func resolveFFprobeBinary(explicit string) string {
	for _, candidate := range []string{
		explicit,
		os.Getenv("MOVIEDESIGN_FFPROBE_TEST_BINARY"),
	} {
		if candidate != "" && filepath.IsAbs(candidate) {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate
			}
		}
	}
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "media", "ffprobe.exe")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	if _, file, _, ok := runtime.Caller(0); ok {
		root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
		candidate := filepath.Join(root, "desktop", "src-tauri", "binaries", "media", "ffprobe.exe")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

// InspectMP4Bytes parses ISO BMFF / MP4 boxes (ftyp, moov, mvhd, trak, tkhd, mdia, mdhd, hdlr, stsd)
// to verify container integrity, dimensions, duration, timebase, video codec, and audio tracks.
func InspectMP4Bytes(data []byte) (MP4Metadata, error) {
	var meta MP4Metadata
	if len(data) < 32 {
		return meta, invalid("Video file is truncated or too small")
	}
	if strings.HasPrefix(strings.TrimSpace(string(data[:min(len(data), 64)])), "#EXTM3U") || strings.HasPrefix(strings.TrimSpace(string(data[:min(len(data), 64)])), "ffconcat") {
		return meta, invalid("Playlist demuxers are forbidden")
	}
	var sawFtyp, sawMoov bool
	offset := 0
	for offset+8 <= len(data) {
		size := int(binary.BigEndian.Uint32(data[offset : offset+4]))
		boxType := string(data[offset+4 : offset+8])
		headerSize := 8
		if size == 1 {
			if offset+16 > len(data) {
				return meta, invalid("Truncated 64-bit MP4 box header")
			}
			size64 := binary.BigEndian.Uint64(data[offset+8 : offset+16])
			if size64 > uint64(len(data)-offset) || size64 < 16 {
				return meta, invalid("Invalid 64-bit MP4 box size")
			}
			size = int(size64)
			headerSize = 16
		} else if size == 0 {
			size = len(data) - offset
		}
		if size < headerSize || offset+size > len(data) {
			return meta, invalid("Corrupt or truncated MP4 box: " + boxType)
		}
		payload := data[offset+headerSize : offset+size]
		switch boxType {
		case "ftyp":
			if len(payload) < 8 {
				return meta, invalid("Invalid MP4 ftyp box")
			}
			sawFtyp = true
			meta.Format = "mp4"
		case "moov":
			sawMoov = true
			if err := parseMoovBox(payload, &meta); err != nil {
				return meta, err
			}
		}
		offset += size
	}
	if !sawFtyp || !sawMoov || meta.Width <= 0 || meta.Height <= 0 || meta.DurationMS <= 0 || meta.Codec == "" || meta.Timebase == "" {
		return meta, invalid("MP4 is missing required ftyp/moov video track metadata")
	}
	return meta, nil
}

func parseMoovBox(data []byte, meta *MP4Metadata) error {
	for offset := 0; offset+8 <= len(data); {
		size := int(binary.BigEndian.Uint32(data[offset : offset+4]))
		boxType := string(data[offset+4 : offset+8])
		if size == 0 {
			size = len(data) - offset
		}
		if size < 8 || offset+size > len(data) {
			return invalid("Corrupt moov child box")
		}
		payload := data[offset+8 : offset+size]
		switch boxType {
		case "mvhd":
			if len(payload) < 20 {
				return invalid("Invalid mvhd box")
			}
			version := payload[0]
			if version == 0 {
				timescale := binary.BigEndian.Uint32(payload[12:16])
				duration := binary.BigEndian.Uint32(payload[16:20])
				if timescale == 0 {
					return invalid("mvhd timescale is zero")
				}
				meta.DurationMS = int64(math.Round(float64(duration) * 1000.0 / float64(timescale)))
				if meta.Timebase == "" {
					meta.Timebase = fmt.Sprintf("1/%d", timescale)
				}
			} else if version == 1 && len(payload) >= 32 {
				timescale := binary.BigEndian.Uint32(payload[20:24])
				duration := binary.BigEndian.Uint64(payload[24:32])
				if timescale == 0 {
					return invalid("mvhd timescale is zero")
				}
				meta.DurationMS = int64(math.Round(float64(duration) * 1000.0 / float64(timescale)))
				if meta.Timebase == "" {
					meta.Timebase = fmt.Sprintf("1/%d", timescale)
				}
			}
		case "trak":
			parseTrakBox(payload, meta)
		}
		offset += size
	}
	return nil
}

func parseTrakBox(data []byte, meta *MP4Metadata) {
	var handlerType, codec, timebase string
	var width, height int
	var durationMS int64
	var walk func(buf []byte)
	walk = func(buf []byte) {
		for offset := 0; offset+8 <= len(buf); {
			size := int(binary.BigEndian.Uint32(buf[offset : offset+4]))
			boxType := string(buf[offset+4 : offset+8])
			if size == 0 {
				size = len(buf) - offset
			}
			if size < 8 || offset+size > len(buf) {
				return
			}
			payload := buf[offset+8 : offset+size]
			switch boxType {
			case "tkhd":
				if len(payload) >= 84 && payload[0] == 0 {
					w := int(binary.BigEndian.Uint32(payload[76:80]) >> 16)
					h := int(binary.BigEndian.Uint32(payload[80:84]) >> 16)
					if w > 0 && h > 0 {
						width, height = w, h
					}
				}
			case "mdhd":
				if len(payload) >= 20 && payload[0] == 0 {
					ts := binary.BigEndian.Uint32(payload[12:16])
					dur := binary.BigEndian.Uint32(payload[16:20])
					if ts > 0 {
						timebase = fmt.Sprintf("1/%d", ts)
						durationMS = int64(math.Round(float64(dur) * 1000.0 / float64(ts)))
					}
				}
			case "hdlr":
				if len(payload) >= 12 {
					handlerType = string(payload[8:12])
				}
			case "stsd":
				if len(payload) >= 16 {
					entrySize := int(binary.BigEndian.Uint32(payload[8:12]))
					entryType := string(payload[12:16])
					codec = strings.TrimSpace(entryType)
					if handlerType == "vide" && entrySize >= 40 && len(payload) >= 8+40 {
						w := int(binary.BigEndian.Uint16(payload[8+32 : 8+34]))
						h := int(binary.BigEndian.Uint16(payload[8+34 : 8+36]))
						if w > 0 && h > 0 {
							width, height = w, h
						}
					}
				}
			case "mdia", "minf", "stbl":
				walk(payload)
			}
			offset += size
		}
	}
	walk(data)
	switch handlerType {
	case "vide":
		if width > 0 && height > 0 {
			meta.Width = width
			meta.Height = height
		}
		if codec != "" {
			meta.Codec = codec
		}
		if timebase != "" {
			meta.Timebase = timebase
		}
		if meta.DurationMS == 0 && durationMS > 0 {
			meta.DurationMS = durationMS
		}
	case "soun":
		meta.AudioTracks++
		if codec != "" && meta.AudioCodec == "" {
			meta.AudioCodec = codec
		}
	}
}

func probeMP4WithFFprobe(ctx context.Context, probePath string, data []byte, baseMeta MP4Metadata) (MP4Metadata, error) {
	if probePath == "" {
		return baseMeta, nil
	}
	tmp, err := os.CreateTemp("", "moviedesign-probe-*.mp4")
	if err != nil {
		return baseMeta, nil
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return baseMeta, nil
	}
	if err := tmp.Close(); err != nil {
		return baseMeta, nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, probePath,
		"-v", "error",
		"-max_alloc", "67108864",
		"-probesize", "8388608",
		"-analyzeduration", "10000000",
		"-protocol_whitelist", "file",
		"-format_whitelist", "mov,mp4,m4a,3gp,3g2,mj2",
		"-show_entries", "format=format_name,duration:stream=codec_type,codec_name,width,height,time_base",
		"-of", "json",
		tmpPath,
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return baseMeta, invalid("ffprobe rejected video container or stream layout")
	}
	var parsed struct {
		Format struct {
			FormatName string `json:"format_name"`
			Duration   string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
			TimeBase  string `json:"time_base"`
		} `json:"streams"`
	}
	if json.Unmarshal(out.Bytes(), &parsed) != nil {
		return baseMeta, invalid("Invalid ffprobe JSON output")
	}
	if !strings.Contains(parsed.Format.FormatName, "mp4") && !strings.Contains(parsed.Format.FormatName, "mov") {
		return baseMeta, invalid("Unsupported video container format: " + parsed.Format.FormatName)
	}
	if durSec, err := strconv.ParseFloat(parsed.Format.Duration, 64); err == nil && durSec > 0 {
		baseMeta.DurationMS = int64(math.Round(durSec * 1000.0))
	}
	audioCount := 0
	sawVideo := false
	for _, st := range parsed.Streams {
		switch st.CodecType {
		case "video":
			sawVideo = true
			if st.Width > 0 && st.Height > 0 {
				baseMeta.Width = st.Width
				baseMeta.Height = st.Height
			}
			if st.CodecName != "" {
				baseMeta.Codec = st.CodecName
			}
			if st.TimeBase != "" {
				baseMeta.Timebase = st.TimeBase
			}
		case "audio":
			audioCount++
			if st.CodecName != "" {
				baseMeta.AudioCodec = st.CodecName
			}
		}
	}
	if !sawVideo {
		return baseMeta, invalid("Video container contains no decodable video stream")
	}
	baseMeta.AudioTracks = audioCount
	return baseMeta, nil
}

func validateVideoBytes(ctx context.Context, data []byte, in GenerationInput, cap Capability) (MP4Metadata, error) {
	if len(data) == 0 || int64(len(data)) > MaxVideoBytes {
		return MP4Metadata{}, invalid("Video response exceeds byte limit")
	}
	meta, err := InspectMP4Bytes(data)
	if err != nil {
		return MP4Metadata{}, err
	}
	meta, err = probeMP4WithFFprobe(ctx, resolveFFprobeBinary(""), data, meta)
	if err != nil {
		return MP4Metadata{}, err
	}
	if meta.Width < 1 || meta.Height < 1 || int64(meta.Width)*int64(meta.Height) > cap.Constraints.MaxPixels {
		return MP4Metadata{}, invalid("Video dimensions are invalid or exceed capability pixel ceiling")
	}
	if rawDur, hasDur := in.Params["duration"]; hasDur {
		if wantSec, ok := intParam(rawDur); ok && wantSec > 0 {
			wantMS := int64(wantSec) * 1000
			diff := meta.DurationMS - wantMS
			if diff < 0 {
				diff = -diff
			}
			if diff > 500 {
				return MP4Metadata{}, invalid(fmt.Sprintf("Delivered video duration %dms disagrees with requested duration %dms", meta.DurationMS, wantMS))
			}
		}
	}
	requiresAudio := false
	if b, ok := in.Params["generate_audio"].(bool); ok && b {
		requiresAudio = true
	}
	if mode, ok := in.Params["audio_mode"].(string); ok && mode != "" && mode != "none" {
		requiresAudio = true
	}
	if requiresAudio && meta.AudioTracks < 1 {
		return MP4Metadata{}, invalid("Delivered video is missing required audio stream")
	}
	return meta, nil
}

// CollectVideos downloads or replays a completed video output into a confined staging path
// and verifies container, duration, timebase, dimensions, and required audio tracks before Delivery.
func (s *Service) CollectVideos(ctx context.Context, p string, in GenerationInput, result ProviderResult, f FrozenProvider, keys []string) (pathsOut []string, errOut error) {
	defer func() {
		if errOut == nil {
			return
		}
		code, message, retry := "COLLECTION_IO", "Saved video could not be collected; retry collection", true
		text := errOut.Error()
		if strings.Contains(text, "download") || strings.Contains(text, "Result URL") {
			code, message = "DOWNLOAD_FAILED", "Saved video download failed; retry downloads without generating again"
		} else if errors.Is(errOut, ErrInvalid) && !strings.Contains(text, "unavailable") {
			code, message, retry = "INVALID_SAVED_MEDIA", text, false
		}
		errOut = &CollectionError{code, message, retry}
	}()
	if s.Files == nil {
		return nil, ErrUnavailable
	}
	if len(keys) == 0 || len(keys) != len(result.Data) {
		return nil, invalid("Video result count does not match reserved outputs")
	}
	seen := map[string]bool{}
	for _, k := range keys {
		if !identifier.MatchString(k) || len(k) > 100 || seen[k] {
			return nil, invalid("Invalid reserved video output identity")
		}
		seen[k] = true
	}
	cap := VideoCapability(f.Config)
	paths := make([]string, 0, len(keys))
	for i, item := range result.Data {
		var saved string
		e := s.DB.QueryRowContext(ctx, "SELECT receipt FROM file_operations WHERE project_id=? AND request_id=? AND operation='upload' AND stage='completed'", p, "video-"+keys[i]).Scan(&saved)
		if e == nil {
			var receipt struct {
				Path string `json:"path"`
			}
			if json.Unmarshal([]byte(saved), &receipt) != nil {
				return nil, ErrUnavailable
			}
			file, err := s.Files.Workspace.OpenRegular(p, receipt.Path)
			if err != nil {
				return nil, invalid("Collected video file is unavailable")
			}
			data, err := readBounded(file, MaxVideoBytes)
			file.Close()
			if err != nil {
				return nil, err
			}
			if _, err = validateVideoBytes(ctx, data, in, cap); err != nil {
				return nil, err
			}
			if _, err = s.Files.Stage(ctx, p, "video-"+keys[i], path.Base(receipt.Path), bytes.NewReader(data)); err != nil {
				return nil, err
			}
			paths = append(paths, receipt.Path)
			continue
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
		if item.URL == "" || item.B64JSON != "" {
			return nil, invalid("Expected content.video_url in saved video result")
		}
		client, ce := NewClient(f.Config.BaseURL, true)
		if ce != nil {
			return nil, ce
		}
		req, ce := http.NewRequestWithContext(ctx, http.MethodGet, item.URL, nil)
		if ce != nil {
			return nil, invalid("Invalid result URL")
		}
		res, ce := client.Do(req)
		if ce != nil {
			return nil, invalid("Result download failed")
		}
		if res.StatusCode != http.StatusOK {
			res.Body.Close()
			return nil, invalid("Result download rejected")
		}
		data, e := readBounded(res.Body, MaxVideoBytes)
		res.Body.Close()
		if e != nil && !errors.Is(e, ErrInvalid) {
			return nil, invalid("Result download interrupted")
		}
		if e != nil {
			return nil, e
		}
		declared, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
		if declared != "video/mp4" || http.DetectContentType(data) != "video/mp4" {
			return nil, invalid("Downloaded video MIME mismatch; expected video/mp4")
		}
		if _, err := validateVideoBytes(ctx, data, in, cap); err != nil {
			return nil, err
		}
		target, e := s.Files.Stage(ctx, p, "video-"+keys[i], path.Base(keys[i])+".mp4", bytes.NewReader(data))
		if e != nil {
			return nil, e
		}
		paths = append(paths, target)
	}
	return paths, nil
}

func mp4Box(boxType string, payloads ...[]byte) []byte {
	total := 8
	for _, p := range payloads {
		total += len(p)
	}
	out := make([]byte, total)
	binary.BigEndian.PutUint32(out[0:4], uint32(total))
	copy(out[4:8], boxType)
	pos := 8
	for _, p := range payloads {
		copy(out[pos:], p)
		pos += len(p)
	}
	return out
}

func mp4FullBox(boxType string, version byte, flags uint32, payloads ...[]byte) []byte {
	hdr := make([]byte, 4)
	hdr[0] = version
	hdr[1] = byte(flags >> 16)
	hdr[2] = byte(flags >> 8)
	hdr[3] = byte(flags)
	all := append([][]byte{hdr}, payloads...)
	return mp4Box(boxType, all...)
}

// BuildTestMP4 constructs a deterministic, standards-compliant ISO BMFF MP4 (H.264 avc1 + optional AAC mp4a)
// that passes both ffprobe stream/container inspection and HTML5 <video> element playback.
func BuildTestMP4(width, height, durationSeconds int, includeAudio bool) []byte {
	if width <= 0 {
		width = 1280
	}
	if height <= 0 {
		height = 720
	}
	if durationSeconds <= 0 {
		durationSeconds = 15
	}
	const timescale = 1000
	durationUnits := uint32(durationSeconds * timescale)

	// Minimal valid H.264 Baseline IDR access unit (4-byte length-prefixed NAL unit)
	sps := []byte{0x67, 0x42, 0xc0, 0x1f, 0xda, 0x01, 0x40, 0x16, 0xec, 0x04, 0x40, 0x00, 0x00, 0x03, 0x00, 0x40, 0x00, 0x00, 0x0f, 0x03, 0xc5, 0x8b, 0xb8}
	pps := []byte{0x68, 0xce, 0x3c, 0x80}
	idr := []byte{0x65, 0x88, 0x84, 0x00, 0x33, 0xff}
	videoSample := make([]byte, 4+len(idr))
	binary.BigEndian.PutUint32(videoSample[0:4], uint32(len(idr)))
	copy(videoSample[4:], idr)

	// Minimal valid AAC-LC silent frame (raw data_block_element: 0x21, 0x10, 0x04, 0x60, 0x8c, 0x1c)
	audioSample := []byte{0x21, 0x10, 0x04, 0x60, 0x8c, 0x1c}

	ftyp := mp4Box("ftyp", []byte("isom\x00\x00\x02\x00isomiso2avc1mp41"))

	// Build placeholder moov first to compute exact chunk offsets in mdat
	buildMoov := func(videoOffset, audioOffset uint32) []byte {
		mvhdBody := make([]byte, 96)
		binary.BigEndian.PutUint32(mvhdBody[8:12], timescale)
		binary.BigEndian.PutUint32(mvhdBody[12:16], durationUnits)
		binary.BigEndian.PutUint32(mvhdBody[16:20], 0x00010000) // rate 1.0
		binary.BigEndian.PutUint16(mvhdBody[20:22], 0x0100)     // volume 1.0
		binary.BigEndian.PutUint32(mvhdBody[32:36], 0x00010000)
		binary.BigEndian.PutUint32(mvhdBody[48:52], 0x00010000)
		binary.BigEndian.PutUint32(mvhdBody[64:68], 0x40000000)
		nextTrackID := uint32(2)
		if includeAudio {
			nextTrackID = 3
		}
		binary.BigEndian.PutUint32(mvhdBody[92:96], nextTrackID)
		mvhd := mp4FullBox("mvhd", 0, 0, mvhdBody)

		// Video Track
		tkhdBody := make([]byte, 80)
		binary.BigEndian.PutUint32(tkhdBody[8:12], 1) // track_ID = 1
		binary.BigEndian.PutUint32(tkhdBody[16:20], durationUnits)
		binary.BigEndian.PutUint32(tkhdBody[36:40], 0x00010000)
		binary.BigEndian.PutUint32(tkhdBody[52:56], 0x00010000)
		binary.BigEndian.PutUint32(tkhdBody[68:72], 0x40000000)
		binary.BigEndian.PutUint32(tkhdBody[72:76], uint32(width)<<16)
		binary.BigEndian.PutUint32(tkhdBody[76:80], uint32(height)<<16)
		tkhd := mp4FullBox("tkhd", 0, 3, tkhdBody)

		mdhdBody := make([]byte, 20)
		binary.BigEndian.PutUint32(mdhdBody[8:12], timescale)
		binary.BigEndian.PutUint32(mdhdBody[12:16], durationUnits)
		binary.BigEndian.PutUint16(mdhdBody[16:18], 0x55c4) // und
		mdhd := mp4FullBox("mdhd", 0, 0, mdhdBody)

		hdlrBody := make([]byte, 21)
		copy(hdlrBody[4:8], "vide")
		copy(hdlrBody[20:], "\x00")
		hdlr := mp4FullBox("hdlr", 0, 0, hdlrBody)

		vmhd := mp4FullBox("vmhd", 0, 1, make([]byte, 8))
		urlBox := mp4FullBox("url ", 0, 1)
		dref := mp4FullBox("dref", 0, 0, []byte{0, 0, 0, 1}, urlBox)
		dinf := mp4Box("dinf", dref)

		avcCBody := bytes.NewBuffer(nil)
		avcCBody.Write([]byte{0x01, sps[1], sps[2], sps[3], 0xff, 0xe1})
		_ = binary.Write(avcCBody, binary.BigEndian, uint16(len(sps)))
		avcCBody.Write(sps)
		avcCBody.WriteByte(0x01)
		_ = binary.Write(avcCBody, binary.BigEndian, uint16(len(pps)))
		avcCBody.Write(pps)
		avcC := mp4Box("avcC", avcCBody.Bytes())

		avc1Header := make([]byte, 78)
		binary.BigEndian.PutUint16(avc1Header[6:8], 1) // data_reference_index
		binary.BigEndian.PutUint16(avc1Header[24:26], uint16(width))
		binary.BigEndian.PutUint16(avc1Header[26:28], uint16(height))
		binary.BigEndian.PutUint32(avc1Header[28:32], 0x00480000)
		binary.BigEndian.PutUint32(avc1Header[32:36], 0x00480000)
		binary.BigEndian.PutUint16(avc1Header[40:42], 1) // frame_count
		binary.BigEndian.PutUint16(avc1Header[74:76], 0x0018)
		binary.BigEndian.PutUint16(avc1Header[76:78], 0xffff)
		avc1 := mp4Box("avc1", avc1Header, avcC)
		stsd := mp4FullBox("stsd", 0, 0, []byte{0, 0, 0, 1}, avc1)

		sttsBody := make([]byte, 12)
		binary.BigEndian.PutUint32(sttsBody[0:4], 1)
		binary.BigEndian.PutUint32(sttsBody[4:8], 1)
		binary.BigEndian.PutUint32(sttsBody[8:12], durationUnits)
		stts := mp4FullBox("stts", 0, 0, sttsBody)

		stss := mp4FullBox("stss", 0, 0, []byte{0, 0, 0, 1, 0, 0, 0, 1})

		stscBody := make([]byte, 16)
		binary.BigEndian.PutUint32(stscBody[0:4], 1)
		binary.BigEndian.PutUint32(stscBody[4:8], 1)
		binary.BigEndian.PutUint32(stscBody[8:12], 1)
		binary.BigEndian.PutUint32(stscBody[12:16], 1)
		stsc := mp4FullBox("stsc", 0, 0, stscBody)

		stszBody := make([]byte, 12)
		binary.BigEndian.PutUint32(stszBody[0:4], uint32(len(videoSample)))
		binary.BigEndian.PutUint32(stszBody[4:8], 1)
		binary.BigEndian.PutUint32(stszBody[8:12], uint32(len(videoSample)))
		stsz := mp4FullBox("stsz", 0, 0, stszBody)

		stcoBody := make([]byte, 8)
		binary.BigEndian.PutUint32(stcoBody[0:4], 1)
		binary.BigEndian.PutUint32(stcoBody[4:8], videoOffset)
		stco := mp4FullBox("stco", 0, 0, stcoBody)

		stbl := mp4Box("stbl", stsd, stts, stss, stsc, stsz, stco)
		minf := mp4Box("minf", vmhd, dinf, stbl)
		mdia := mp4Box("mdia", mdhd, hdlr, minf)
		videoTrak := mp4Box("trak", tkhd, mdia)

		if !includeAudio {
			return mp4Box("moov", mvhd, videoTrak)
		}

		// Audio Track (AAC-LC 44100 Hz stereo)
		const audioRate = 44100
		audioDurationUnits := uint32(durationSeconds * audioRate)
		aTkhdBody := make([]byte, 80)
		binary.BigEndian.PutUint32(aTkhdBody[8:12], 2) // track_ID = 2
		binary.BigEndian.PutUint32(aTkhdBody[16:20], durationUnits)
		binary.BigEndian.PutUint16(aTkhdBody[32:34], 0x0100) // volume 1.0
		binary.BigEndian.PutUint32(aTkhdBody[36:40], 0x00010000)
		binary.BigEndian.PutUint32(aTkhdBody[52:56], 0x00010000)
		binary.BigEndian.PutUint32(aTkhdBody[68:72], 0x40000000)
		aTkhd := mp4FullBox("tkhd", 0, 3, aTkhdBody)

		aMdhdBody := make([]byte, 20)
		binary.BigEndian.PutUint32(aMdhdBody[8:12], audioRate)
		binary.BigEndian.PutUint32(aMdhdBody[12:16], audioDurationUnits)
		binary.BigEndian.PutUint16(aMdhdBody[16:18], 0x55c4)
		aMdhd := mp4FullBox("mdhd", 0, 0, aMdhdBody)

		aHdlrBody := make([]byte, 21)
		copy(aHdlrBody[4:8], "soun")
		copy(aHdlrBody[20:], "\x00")
		aHdlr := mp4FullBox("hdlr", 0, 0, aHdlrBody)

		smhd := mp4FullBox("smhd", 0, 0, make([]byte, 4))

		// ESDS for AAC-LC 44.1kHz 2ch (AudioSpecificConfig = 0x12, 0x10)
		esdsPayload := []byte{
			0x03, 0x19, 0x00, 0x02, 0x00,
			0x04, 0x11, 0x40, 0x15, 0x00, 0x00, 0x00, 0x00, 0x01, 0xf4, 0x00, 0x00, 0x01, 0xf4, 0x00,
			0x05, 0x02, 0x12, 0x10,
			0x06, 0x01, 0x02,
		}
		esds := mp4FullBox("esds", 0, 0, esdsPayload)

		mp4aHeader := make([]byte, 28)
		binary.BigEndian.PutUint16(mp4aHeader[6:8], 1)
		binary.BigEndian.PutUint16(mp4aHeader[16:18], 2)  // channelcount = 2
		binary.BigEndian.PutUint16(mp4aHeader[18:20], 16) // samplesize = 16
		binary.BigEndian.PutUint32(mp4aHeader[24:28], uint32(audioRate)<<16)
		mp4a := mp4Box("mp4a", mp4aHeader, esds)
		aStsd := mp4FullBox("stsd", 0, 0, []byte{0, 0, 0, 1}, mp4a)

		aSttsBody := make([]byte, 12)
		binary.BigEndian.PutUint32(aSttsBody[0:4], 1)
		binary.BigEndian.PutUint32(aSttsBody[4:8], 1)
		binary.BigEndian.PutUint32(aSttsBody[8:12], audioDurationUnits)
		aStts := mp4FullBox("stts", 0, 0, aSttsBody)

		aStsc := mp4FullBox("stsc", 0, 0, stscBody)

		aStszBody := make([]byte, 12)
		binary.BigEndian.PutUint32(aStszBody[0:4], uint32(len(audioSample)))
		binary.BigEndian.PutUint32(aStszBody[4:8], 1)
		binary.BigEndian.PutUint32(aStszBody[8:12], uint32(len(audioSample)))
		aStsz := mp4FullBox("stsz", 0, 0, aStszBody)

		aStcoBody := make([]byte, 8)
		binary.BigEndian.PutUint32(aStcoBody[0:4], 1)
		binary.BigEndian.PutUint32(aStcoBody[4:8], audioOffset)
		aStco := mp4FullBox("stco", 0, 0, aStcoBody)

		aStbl := mp4Box("stbl", aStsd, aStts, aStsc, aStsz, aStco)
		aMinf := mp4Box("minf", smhd, dinf, aStbl)
		aMdia := mp4Box("mdia", aMdhd, aHdlr, aMinf)
		audioTrak := mp4Box("trak", aTkhd, aMdia)

		return mp4Box("moov", mvhd, videoTrak, audioTrak)
	}

	dummyMoov := buildMoov(0, 0)
	mdatPayloadOffset := uint32(len(ftyp) + len(dummyMoov) + 8)
	videoOff := mdatPayloadOffset
	audioOff := videoOff + uint32(len(videoSample))
	moov := buildMoov(videoOff, audioOff)

	var mdatPayload []byte
	if includeAudio {
		mdatPayload = append(append([]byte{}, videoSample...), audioSample...)
	} else {
		mdatPayload = append([]byte{}, videoSample...)
	}
	mdat := mp4Box("mdat", mdatPayload)

	out := make([]byte, 0, len(ftyp)+len(moov)+len(mdat))
	out = append(out, ftyp...)
	out = append(out, moov...)
	out = append(out, mdat...)
	return out
}
