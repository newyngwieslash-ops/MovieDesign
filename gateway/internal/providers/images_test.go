package providers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/workspace"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	m := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	m.Set(0, 0, color.NRGBA{R: 255, A: 255})
	if e := png.Encode(&b, m); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func imageService(t *testing.T) (*Service, string) {
	t.Helper()
	s, _ := testService(t)
	w, e := workspace.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { w.Close() })
	s.Files = workspace.NewService(s.DB, w)
	p, e := (store.Objects{DB: s.DB}).CreateProject(context.Background(), "image-project", "Images")
	if e != nil {
		t.Fatal(e)
	}
	return s, p.ID
}
func TestImageWireAndCollection(t *testing.T) {
	s, p := imageService(t)
	data := tinyPNG(t)
	calls := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/images/generations" || r.Header.Get("Authorization") != "Bearer synthetic-image-key" {
			t.Error("wrong route/credential")
		}
		var v map[string]any
		json.NewDecoder(r.Body).Decode(&v)
		if v["model"] != "custom-model" || v["prompt"] != " 原文\n " || v["n"] != float64(1) || v["response_format"] != nil {
			t.Errorf("wire %#v", v)
		}
		json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"b64_json": base64.StdEncoding.EncodeToString(data)}}})
	}))
	defer remote.Close()
	f := FrozenProvider{Config: validConfig("one", remote.URL+"/v1"), CredentialVersion: "image-key-v1"}
	f.Config.ModelID = "custom-model"
	if e := s.Secrets.Put(f.CredentialVersion, "synthetic-image-key"); e != nil {
		t.Fatal(e)
	}
	in := GenerationInput{ProjectID: p, Prompt: " 原文\n ", Count: 1, Params: map[string]any{}}
	result, e := s.GenerateImage(context.Background(), in, f)
	if e != nil {
		t.Fatal(e)
	}
	if len(result.Data) != 1 || calls != 1 {
		t.Fatal("missing result")
	}
	paths, e := s.CollectImages(context.Background(), p, result, f, []string{"output-one"})
	if e != nil {
		t.Fatal(e)
	}
	if len(paths) != 1 {
		t.Fatal("missing collected file")
	}
	file, e := s.Files.Workspace.OpenRegular(p, paths[0])
	if e != nil {
		t.Fatal(e)
	}
	got, _ := io.ReadAll(file)
	file.Close()
	if !bytes.Equal(got, data) {
		t.Fatal("media changed")
	}
	again, e := s.CollectImages(context.Background(), p, result, f, []string{"output-one"})
	if e != nil || again[0] != paths[0] || calls != 1 {
		t.Fatal("collection replay changed identity or submitted", e)
	}
	in.References = []string{"https://evil/image.png"}
	if _, e = s.GenerateImage(context.Background(), in, f); e == nil || calls != 1 {
		t.Fatal("invalid ref contacted provider")
	}
}
func TestImageOrderedEditMaskAndMutation(t *testing.T) {
	s, p := imageService(t)
	ctx := context.Background()
	data := tinyPNG(t)
	stage, e := s.Files.Stage(ctx, p, "upload-image", "source.png", bytes.NewReader(data))
	if e != nil {
		t.Fatal(e)
	}
	asset, e := s.Files.Commit(ctx, p, "commit-image", stage)
	if e != nil {
		t.Fatal(e)
	}
	calls := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/images/edits" {
			t.Error(r.URL.Path)
		}
		mr, e := r.MultipartReader()
		if e != nil {
			t.Error(e)
			return
		}
		var fields []string
		for {
			part, e := mr.NextPart()
			if e == io.EOF {
				break
			}
			if e != nil {
				t.Error(e)
				return
			}
			b, _ := io.ReadAll(part)
			if part.FileName() != "" {
				fields = append(fields, part.FormName())
				if !bytes.Equal(b, data) {
					t.Error("reference bytes changed")
				}
			}
		}
		if strings.Join(fields, ",") != "image[],image[],mask" {
			t.Error(fields)
		}
		fmt.Fprintf(w, `{"data":[{"b64_json":%q}]}`, base64.StdEncoding.EncodeToString(data))
	}))
	defer remote.Close()
	f := FrozenProvider{Config: validConfig("edit", remote.URL)}
	f.Config.Capability.Edit = true
	f.Config.Capability.MaxReferences = 3
	f.Config.Capability.ReferenceRoles = []string{"image", "image", "mask"}
	in := GenerationInput{ProjectID: p, Prompt: "edit\n", Count: 1, Params: map[string]any{}, References: []string{asset.ID, asset.ID, asset.ID}}
	in.Assets, e = s.FreezeReferences(ctx, p, in, f)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.GenerateImage(ctx, in, f); e != nil {
		t.Fatal(e)
	}
	in.Assets[0].SHA256 = "changed"
	if _, e = s.GenerateImage(ctx, in, f); e == nil || calls != 1 {
		t.Fatal("mutated reference posted")
	}
	if _, e = s.FreezeReferences(ctx, "foreign", in, f); e == nil {
		t.Fatal("cross project reference")
	}
}
func TestImageHTTPFailuresAndBoundedResponse(t *testing.T) {
	for _, status := range []int{401, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			s, _ := imageService(t)
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				io.WriteString(w, `{"error":{"message":"Bearer synthetic-secret"}}`)
			}))
			defer remote.Close()
			f := FrozenProvider{Config: validConfig("one", remote.URL)}
			_, e := s.GenerateImage(context.Background(), GenerationInput{Prompt: "x", Count: 1, Params: map[string]any{}}, f)
			failure, ok := e.(*ImageError)
			if !ok || failure.NotAccepted != (status < 500) || strings.Contains(e.Error(), "synthetic-secret") {
				t.Fatal(e)
			}
		})
	}
	for _, mode := range []string{"disconnect", "partial", "delayed"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := imageService(t)
			calls := 0
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if mode == "disconnect" {
					c, _, _ := w.(http.Hijacker).Hijack()
					c.Close()
					return
				}
				if mode == "partial" {
					io.WriteString(w, `{"data":[`)
					w.(http.Flusher).Flush()
				}
				time.Sleep(60 * time.Millisecond)
			}))
			defer remote.Close()
			f := FrozenProvider{Config: validConfig("one", remote.URL)}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			_, e := s.GenerateImage(ctx, GenerationInput{Prompt: "x", Count: 1, Params: map[string]any{}}, f)
			failure, ok := e.(*ImageError)
			if !ok || failure.NotAccepted || calls != 1 {
				t.Fatal("must remain unknown without retry", e, calls)
			}
		})
	}
}
func TestImageURLCollectionGuardsAndCount(t *testing.T) {
	s, p := imageService(t)
	data := tinyPNG(t)
	downloads := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloads++
		if r.Header.Get("Authorization") != "" || r.Header.Get("OpenAI-Organization") != "" {
			t.Error("download credentials leaked")
		}
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/good", 302)
			return
		}
		if r.URL.Path == "/wrong-mime" {
			w.Header().Set("Content-Type", "text/plain")
		} else {
			w.Header().Set("Content-Type", "image/png")
		}
		w.Write(data)
	}))
	defer remote.Close()
	f := FrozenProvider{Config: validConfig("url", remote.URL)}
	f.Config.Headers.Organization = "private-org"
	result := ProviderResult{Data: []ProviderImage{{URL: remote.URL + "/good?signature=synthetic"}}}
	paths, e := s.CollectImages(context.Background(), p, result, f, []string{"url-one"})
	if e != nil || len(paths) != 1 || downloads != 1 {
		t.Fatal(paths, e, downloads)
	}
	for i, url := range []string{remote.URL + "/redirect", remote.URL + "/wrong-mime", "http://169.254.169.254/latest/meta-data"} {
		result.Data[0].URL = url
		if _, e = s.CollectImages(context.Background(), p, result, f, []string{fmt.Sprintf("bad-url-%d", i)}); e == nil {
			t.Fatal("unsafe URL/media accepted", url)
		}
	}
	if _, e = s.CollectImages(context.Background(), p, ProviderResult{Data: []ProviderImage{{B64JSON: "eA=="}, {B64JSON: "eA=="}}}, f, []string{"one-slot"}); e == nil {
		t.Fatal("count mismatch collected")
	}
	for i, b64 := range []string{"not-base64", base64.StdEncoding.EncodeToString(data) + "\n", base64.StdEncoding.EncodeToString(data[:20]), "eB=="} {
		if _, e = s.CollectImages(context.Background(), p, ProviderResult{Data: []ProviderImage{{B64JSON: b64}}}, f, []string{fmt.Sprintf("bad-b64-%d", i)}); e == nil {
			t.Fatal("invalid base64 media accepted")
		}
	}
	f.Config.Capability.Constraints.MaxImageBytes = 10
	if _, e = s.CollectImages(context.Background(), p, ProviderResult{Data: []ProviderImage{{B64JSON: base64.StdEncoding.EncodeToString(data)}}}, f, []string{"oversize"}); e == nil {
		t.Fatal("decoded size bound ignored")
	}
}
func TestImageMaskRequiresSourcePNGAndAlpha(t *testing.T) {
	s, p := imageService(t)
	ctx := context.Background()
	var opaque bytes.Buffer
	if e := png.Encode(&opaque, image.NewRGBA(image.Rect(0, 0, 3, 2))); e != nil {
		t.Fatal(e)
	}
	register := func(key string, b []byte) string {
		stage, e := s.Files.Stage(ctx, p, key, "image.png", bytes.NewReader(b))
		if e != nil {
			t.Fatal(e)
		}
		a, e := s.Files.Commit(ctx, p, key+"-commit", stage)
		if e != nil {
			t.Fatal(e)
		}
		return a.ID
	}
	source := register("source", tinyPNG(t))
	wrong := register("wrong-size", opaque.Bytes())
	f := FrozenProvider{Config: validConfig("mask", "http://127.0.0.1:11434/v1")}
	f.Config.Capability.Edit = true
	f.Config.Capability.MaxReferences = 2
	f.Config.Capability.ReferenceRoles = []string{"image", "mask"}
	in := GenerationInput{Prompt: "mask", Count: 1, Params: map[string]any{}, References: []string{source, wrong}}
	if _, e := s.FreezeReferences(ctx, p, in, f); e == nil {
		t.Fatal("wrong mask dimensions accepted")
	}
	opaque.Reset()
	m := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			m.Set(x, y, color.Black)
		}
	}
	png.Encode(&opaque, m)
	in.References[1] = register("opaque", opaque.Bytes())
	if _, e := s.FreezeReferences(ctx, p, in, f); e == nil {
		t.Fatal("mask without alpha channel accepted")
	}
}
func TestImageSafeUpstreamDiagnostics(t *testing.T) {
	s, _ := imageService(t)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		io.WriteString(w, `{"error":{"code":"invalid_size","message":"bad size; key=synthetic-diagnostic-\u0000key; organization=secret-org; project=secret-project\n"}}`)
	}))
	defer remote.Close()
	f := FrozenProvider{Config: validConfig("diagnostics", remote.URL), CredentialVersion: "diagnostics-key"}
	s.Secrets.Put(f.CredentialVersion, "synthetic-diagnostic-key")
	f.Config.Headers.Organization = "secret-org"
	f.Config.Headers.Project = "secret-project"
	_, e := s.GenerateImage(context.Background(), GenerationInput{Prompt: "x", Count: 1, Params: map[string]any{}}, f)
	failure, ok := e.(*ImageError)
	if !ok || !strings.Contains(failure.Message, "bad size") || failure.Code != "invalid_size" {
		t.Fatal("lost safe diagnostic", e)
	}
	for _, secret := range []string{"synthetic-diagnostic-key", "secret-org", "secret-project", "\n"} {
		if strings.Contains(failure.Message, secret) {
			t.Fatal("secret or control leaked")
		}
	}
}
