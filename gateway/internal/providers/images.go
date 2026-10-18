package providers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
)

type ProviderImage struct {
	B64JSON string `json:"b64_json,omitempty"`
	URL     string `json:"url,omitempty"`
}
type ProviderResult struct {
	Data []ProviderImage `json:"data"`
}
type ImageError struct {
	Code        string
	Message     string
	NotAccepted bool
	HTTPStatus  int
}

func (e *ImageError) Error() string { return e.Message }
func imageFailure(code, message string, notAccepted bool, status int) error {
	return &ImageError{code, message, notAccepted, status}
}

var bearerSecret = regexp.MustCompile(`(?i)bearer\s+[^\s,;"']+`)

func upstreamFailure(response *http.Response, key string, headers Headers) error {
	rejected := response.StatusCode >= 400 && response.StatusCode < 500 && response.StatusCode != 408
	code := fmt.Sprintf("HTTP_%d", response.StatusCode)
	message := fmt.Sprintf("Provider returned HTTP %d; response body redacted", response.StatusCode)
	raw, e := readBounded(response.Body, 16<<10)
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if e == nil && json.Unmarshal(raw, &body) == nil {
		clean := func(value string) string {
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
		if v := clean(body.Error.Code); len(v) > 0 && len(v) <= 100 && identifier.MatchString(v) {
			code = v
		}
		if v := clean(body.Error.Message); v != "" {
			message = fmt.Sprintf("HTTP %d: %s", response.StatusCode, v)
		}
	}
	return imageFailure(code, message, rejected, response.StatusCode)
}

func imageBytes(data []byte, cap Capability) (image.Config, string, error) {
	var zero image.Config
	if len(data) == 0 || int64(len(data)) > cap.Constraints.MaxImageBytes {
		return zero, "", invalid("Image exceeds byte limit")
	}
	c, format, e := image.DecodeConfig(bytes.NewReader(data))
	if e != nil || format != "png" && format != "jpeg" || c.Width < 1 || c.Height < 1 || int64(c.Width) > cap.Constraints.MaxPixels/int64(c.Height) {
		return zero, "", invalid("Expected bounded PNG or JPEG image")
	}
	actual := http.DetectContentType(data)
	if actual != "image/"+format {
		return zero, "", invalid("Image MIME does not match content")
	}
	decoded, _, e := image.Decode(bytes.NewReader(data))
	if e != nil || decoded.Bounds().Dx() != c.Width || decoded.Bounds().Dy() != c.Height {
		return zero, "", invalid("Image is truncated or invalid")
	}
	return c, format, nil
}
func readBounded(r io.Reader, limit int64) ([]byte, error) {
	b, e := io.ReadAll(io.LimitReader(r, limit+1))
	if e != nil || int64(len(b)) > limit {
		return nil, invalid("Media response is incomplete or too large")
	}
	return b, nil
}
func digest(data []byte) string { v := sha256.Sum256(data); return hex.EncodeToString(v[:]) }

func pngAlpha(data []byte) bool {
	if len(data) < 33 || !bytes.Equal(data[:8], []byte{137, 80, 78, 71, 13, 10, 26, 10}) {
		return false
	}
	if data[25] == 4 || data[25] == 6 {
		return true
	}
	for offset := 8; offset+12 <= len(data); {
		n := int64(binary.BigEndian.Uint32(data[offset : offset+4]))
		if n > int64(len(data)-offset-12) {
			return false
		}
		if string(data[offset+4:offset+8]) == "tRNS" {
			return n > 0
		}
		offset += int(n) + 12
	}
	return false
}
func (s *Service) FreezeReferences(ctx context.Context, p string, in GenerationInput, f FrozenProvider) ([]ReferenceAsset, error) {
	cap := ImageCapability(f.Config)
	if e := Validate(cap, in); e != nil {
		return nil, e
	}
	refs := make([]ReferenceAsset, 0, len(in.References))
	for i, id := range in.References {
		if s.Files == nil {
			return nil, ErrUnavailable
		}
		raw, e := (store.Objects{DB: s.DB}).Get(ctx, p, "assets", id)
		if e != nil {
			return nil, invalid("Reference asset unavailable in project")
		}
		var a store.Asset
		if json.Unmarshal(raw, &a) != nil || a.Status != "active" {
			return nil, invalid("Reference asset is unavailable")
		}
		if e = s.checkReferencePending(ctx, id); e != nil {
			return nil, e
		}
		file, e := s.Files.Workspace.OpenRegular(p, a.Path)
		if e != nil {
			return nil, invalid("Reference file unavailable")
		}
		b, e := readBounded(file, cap.Constraints.MaxImageBytes)
		file.Close()
		if e != nil {
			return nil, e
		}
		c, format, e := imageBytes(b, cap)
		if e != nil {
			return nil, e
		}
		// Capture the existing platform identity through the same confined workspace path.
		frozen, e := s.Files.Workspace.InspectDelivery(p, a.Path, id)
		if e != nil {
			return nil, e
		}
		if a.DevID == nil || a.Inode == nil || a.QuickHash == nil || frozen.DevID == nil || frozen.Inode == nil || frozen.QuickHash == nil || a.Size != frozen.Size || *a.DevID != *frozen.DevID || *a.Inode != *frozen.Inode || *a.QuickHash != *frozen.QuickHash {
			return nil, invalid("Registered reference identity changed")
		}
		a.FullHash = frozen.FullHash
		role := ReferenceRole(cap, i)
		if role == "mask" && (format != "png" || !pngAlpha(b) || refs[0].Asset.MIMEType == nil || *refs[0].Asset.MIMEType != "image/png" || c.Width != refs[0].Width || c.Height != refs[0].Height) {
			return nil, invalid("PNG mask must match the first image dimensions")
		}
		refs = append(refs, ReferenceAsset{Asset: a, Role: role, SHA256: digest(b), Width: c.Width, Height: c.Height})
	}
	return refs, nil
}

// GenerateImage returns durable response evidence before any downloads/materialization.
// Its caller must persist this response on the attempt before invoking CollectImages.
func (s *Service) GenerateImage(ctx context.Context, in GenerationInput, f FrozenProvider) (ProviderResult, error) {
	var result ProviderResult
	if e := ValidateConfig(f.Config); e != nil {
		return result, imageFailure("INVALID_CONFIG", e.Error(), true, 0)
	}
	cap := ImageCapability(f.Config)
	if e := Validate(cap, in); e != nil {
		return result, imageFailure("INVALID_INPUT", e.Error(), true, 0)
	}
	if len(in.Assets) != len(in.References) {
		return result, imageFailure("REFERENCE_CHANGED", "Frozen reference binding missing", true, 0)
	}
	var body bytes.Buffer
	contentType := "application/json"
	operation := "images/generations"
	if len(in.References) == 0 {
		fields := map[string]any{"model": f.Config.ModelID, "prompt": in.Prompt, "n": in.Count}
		for k, v := range in.Params {
			fields[k] = v
		}
		if e := json.NewEncoder(&body).Encode(fields); e != nil {
			return result, imageFailure("INVALID_INPUT", "Invalid generation parameters", true, 0)
		}
	} else {
		operation = "images/edits"
		writer := multipart.NewWriter(&body)
		contentType = writer.FormDataContentType()
		fields := map[string]string{"model": f.Config.ModelID, "prompt": in.Prompt, "n": fmt.Sprint(in.Count)}
		for k, v := range in.Params {
			fields[k] = parameterText(v)
		}
		for k, v := range fields {
			if e := writer.WriteField(k, v); e != nil {
				return result, e
			}
		}
		for i, ref := range in.Assets {
			if s.Files == nil || ref.Asset.ID != in.References[i] || ref.Role != ReferenceRole(cap, i) {
				return result, imageFailure("REFERENCE_CHANGED", "Reference binding changed", true, 0)
			}
			raw, e := (store.Objects{DB: s.DB}).Get(ctx, in.ProjectID, "assets", ref.Asset.ID)
			var current store.Asset
			if e != nil || json.Unmarshal(raw, &current) != nil || current.Status != "active" || current.Path != ref.Asset.Path {
				return result, imageFailure("REFERENCE_CHANGED", "Reference ownership changed", true, 0)
			}
			if e = s.checkReferencePending(ctx, ref.Asset.ID); e != nil {
				return result, imageFailure("REFERENCE_CHANGED", "Reference has pending changes", true, 0)
			}
			if e = s.Files.Workspace.VerifyDelivery(in.ProjectID, ref.Asset.Path, ref.Asset); e != nil {
				return result, imageFailure("REFERENCE_CHANGED", "Reference file identity changed", true, 0)
			}
			file, e := s.Files.Workspace.OpenRegular(in.ProjectID, ref.Asset.Path)
			if e != nil {
				return result, imageFailure("REFERENCE_CHANGED", "Reference unavailable", true, 0)
			}
			b, e := readBounded(file, cap.Constraints.MaxImageBytes)
			file.Close()
			if e != nil || digest(b) != ref.SHA256 {
				return result, imageFailure("REFERENCE_CHANGED", "Reference bytes changed", true, 0)
			}
			c, format, e := imageBytes(b, cap)
			if e != nil || c.Width != ref.Width || c.Height != ref.Height || ref.Role == "mask" && (format != "png" || !pngAlpha(b) || in.Assets[0].Asset.MIMEType == nil || *in.Assets[0].Asset.MIMEType != "image/png" || c.Width != in.Assets[0].Width || c.Height != in.Assets[0].Height) {
				return result, imageFailure("INVALID_REFERENCE", "Invalid reference media or mask dimensions", true, 0)
			}
			field := "image[]"
			if ref.Role == "mask" {
				field = "mask"
			}
			header := textproto.MIMEHeader{}
			header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": field, "filename": ref.Asset.ID + "." + format}))
			header.Set("Content-Type", "image/"+format)
			part, e := writer.CreatePart(header)
			if e != nil {
				return result, e
			}
			if _, e = part.Write(b); e != nil {
				return result, e
			}
		}
		if e := writer.Close(); e != nil {
			return result, e
		}
	}
	endpoint, e := Endpoint(f.Config.BaseURL, operation)
	if e != nil {
		return result, imageFailure("INVALID_CONFIG", "Invalid generation endpoint", true, 0)
	}
	key, e := s.Credential(f)
	if e != nil {
		return result, imageFailure("CREDENTIAL_UNAVAILABLE", "Frozen credential version unavailable", true, 0)
	}
	client, e := newClient(f.Config.BaseURL, false, 10*time.Minute)
	if e != nil {
		return result, imageFailure("INVALID_CONFIG", "Invalid generation endpoint", true, 0)
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if e != nil {
		return result, imageFailure("INVALID_CONFIG", "Invalid request", true, 0)
	}
	req.Header.Set("Content-Type", contentType)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if f.Config.Headers.Organization != "" {
		req.Header.Set("OpenAI-Organization", f.Config.Headers.Organization)
	}
	if f.Config.Headers.Project != "" {
		req.Header.Set("OpenAI-Project", f.Config.Headers.Project)
	}
	// Disable GetBody even for an in-memory payload: no transport replay of a POST.
	req.GetBody = nil
	response, e := client.Do(req)
	if e != nil {
		return result, imageFailure("OUTCOME_UNKNOWN", "Generation transport interrupted; do not resubmit", false, 0)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return result, upstreamFailure(response, key, f.Config.Headers)
	}
	raw, e := readBounded(response.Body, int64(in.Count)*(cap.Constraints.MaxImageBytes*4/3+4096))
	if e != nil {
		return result, imageFailure("OUTCOME_UNKNOWN", "Generation response incomplete; do not resubmit", false, 200)
	}
	if json.Unmarshal(raw, &result) != nil {
		return ProviderResult{}, imageFailure("OUTCOME_UNKNOWN", "Generation response invalid; do not resubmit", false, 200)
	}
	if len(result.Data) != in.Count {
		return result, imageFailure("OUTPUT_COUNT_MISMATCH", "Provider output count differs from declared slots; no files collected", false, 200)
	}
	for _, item := range result.Data {
		if (item.B64JSON == "") == (item.URL == "") {
			return result, imageFailure("INVALID_MEDIA_RESULT", "Expected one base64 or URL image per output", false, 200)
		}
	}
	return result, nil
}

func (s *Service) checkReferencePending(ctx context.Context, id string) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	return store.CheckAssetFilePending(ctx, tx, id)
}

type CollectionError struct {
	Code, Message string
	Retryable     bool
}

func (e *CollectionError) Error() string { return e.Message }
func (s *Service) CollectImages(ctx context.Context, p string, result ProviderResult, f FrozenProvider, keys []string) (pathsOut []string, errOut error) {
	// Only fixed local diagnostics cross the task boundary, never signed URLs or
	// filesystem errors. Immutable malformed media cannot be repaired by retry.
	defer func() {
		if errOut == nil {
			return
		}
		code, message, retry := "COLLECTION_IO", "Saved image could not be collected; retry collection", true
		text := errOut.Error()
		if strings.Contains(text, "download") || strings.Contains(text, "Result URL") {
			code, message = "DOWNLOAD_FAILED", "Saved result download failed; retry downloads without generating again"
		} else if errors.Is(errOut, ErrInvalid) && !strings.Contains(text, "unavailable") {
			code, message, retry = "INVALID_SAVED_MEDIA", "Saved result is invalid or exceeds declared media limits; no automatic retry", false
		} else {
			var corrupt base64.CorruptInputError
			if errors.As(errOut, &corrupt) {
				code, message, retry = "INVALID_SAVED_MEDIA", "Saved base64 image is invalid; no automatic retry", false
			}
		}
		errOut = &CollectionError{code, message, retry}
	}()
	if s.Files == nil {
		return nil, ErrUnavailable
	}
	if len(keys) == 0 || len(keys) != len(result.Data) {
		return nil, invalid("Result count does not match reserved outputs")
	}
	seen := map[string]bool{}
	for _, k := range keys {
		if !identifier.MatchString(k) || len(k) > 100 || seen[k] {
			return nil, invalid("Invalid reserved output identity")
		}
		seen[k] = true
	}
	cap := ImageCapability(f.Config)
	paths := make([]string, 0, len(keys))
	for i, item := range result.Data {
		// A durable completed upload remains collectible even after a signed URL expires.
		var saved string
		e := s.DB.QueryRowContext(ctx, "SELECT receipt FROM file_operations WHERE project_id=? AND request_id=? AND operation='upload' AND stage='completed'", p, "image-"+keys[i]).Scan(&saved)
		if e == nil {
			var receipt struct {
				Path string `json:"path"`
			}
			if json.Unmarshal([]byte(saved), &receipt) != nil {
				return nil, ErrUnavailable
			}
			file, err := s.Files.Workspace.OpenRegular(p, receipt.Path)
			if err != nil {
				return nil, invalid("Collected file is unavailable")
			}
			data, err := readBounded(file, cap.Constraints.MaxImageBytes)
			file.Close()
			if err != nil {
				return nil, err
			}
			if _, _, err = imageBytes(data, cap); err != nil {
				return nil, err
			}
			if _, err = s.Files.Stage(ctx, p, "image-"+keys[i], path.Base(receipt.Path), bytes.NewReader(data)); err != nil {
				return nil, err
			}
			paths = append(paths, receipt.Path)
			continue
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
		var data []byte
		if item.B64JSON != "" && item.URL == "" {
			if int64(len(item.B64JSON)) > cap.Constraints.MaxImageBytes*4/3+4 || strings.ContainsAny(item.B64JSON, "\r\n") {
				return nil, invalid("Invalid base64 image")
			}
			data, e = readBounded(base64.NewDecoder(base64.StdEncoding.Strict(), strings.NewReader(item.B64JSON)), cap.Constraints.MaxImageBytes)
		} else if item.URL != "" && item.B64JSON == "" {
			client, ce := NewClient(f.Config.BaseURL, true)
			if ce != nil {
				return nil, ce
			}
			req, ce := http.NewRequestWithContext(ctx, "GET", item.URL, nil)
			if ce != nil {
				return nil, invalid("Invalid result URL")
			}
			res, ce := client.Do(req)
			if ce != nil {
				return nil, invalid("Result download failed")
			}
			if res.StatusCode != 200 {
				res.Body.Close()
				return nil, invalid("Result download rejected")
			}
			data, e = readBounded(res.Body, cap.Constraints.MaxImageBytes)
			res.Body.Close()
			if e == nil {
				declared, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
				if declared != "image/png" && declared != "image/jpeg" || declared != http.DetectContentType(data) {
					return nil, invalid("Downloaded media MIME mismatch")
				}
			}
		} else {
			return nil, invalid("Invalid saved image result")
		}
		if e != nil {
			return nil, e
		}
		_, format, e := imageBytes(data, cap)
		if e != nil {
			return nil, e
		}
		target, e := s.Files.Stage(ctx, p, "image-"+keys[i], path.Base(keys[i])+"."+format, bytes.NewReader(data))
		if e != nil {
			return nil, e
		}
		paths = append(paths, target)
	}
	return paths, nil
}
