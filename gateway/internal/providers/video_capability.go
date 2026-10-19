package providers

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

const VideoKnowledgeCard = `# Ark Video Task Generation (ark-video)
Capability video:<provider_id> submits an asynchronous video generation task to user-configured Volcano Engine Ark (/api/v3/contents/generations/tasks) or compatible local/cloud endpoints.
- Exact prompt bytes are sent in content[0]={"type":"text","text":prompt}; prompt text is never mutated or appended with CLI flags.
- Reference images must be registered active project assets; the gateway verifies file confinement and SHA-256 digests and encodes ordered image_url data URLs (data:image/<format>;base64,...) inside the adapter.
- Explicit body parameters: size, aspect_ratio, resolution, duration, generate_audio, audio_mode, fps, seed, camera_fixed, watermark. Undeclared parameters are rejected.
- Asynchronous lifecycle: POST creates one task per human-approved attempt and checkpoints upstream_id before polling GET /contents/generations/tasks/{id}. Process restart recovers non-terminal upstream_id via read-only GET polling and never re-POSTs.
- Cancellation: DELETE /contents/generations/tasks/{id} is only supported while upstream status is queued. Running tasks cannot be remotely interrupted and may still incur supplier charges; terminal tasks (succeeded/failed/expired) are never DELETEd so upstream audit evidence is preserved. Late results after local cancellation require explicit human adoption before Delivery.`

var (
	videoAspectRatios = []string{"16:9", "9:16", "1:1", "4:3", "3:4", "21:9"}
	videoResolutions  = []string{"480p", "720p", "1080p"}
	videoAudioModes   = []string{"none", "sfx", "music", "narration", "narration+music"}
	videoFPSValues    = []int{24, 25, 30, 60}
)

func intParam(v any) (int64, bool) {
	n, ok := number(v)
	if !ok || !n.IsInt() {
		return 0, false
	}
	num := n.Num()
	if !num.IsInt64() {
		return 0, false
	}
	return num.Int64(), true
}

// VideoCapability projects the user-declared video provider config onto the capability catalog.
func VideoCapability(c Config) Capability {
	cap := c.Capability
	cap.ID = "video:" + c.ID
	cap.Operation = "video"
	cap.Protocol = c.Protocol
	cap.ModelID = c.ModelID
	cap.KnowledgeCard = "knowledge/providers/ark-video.md"
	if len(cap.ParamSchema) == 0 {
		cap.ParamSchema = defaultVideoParamSchema(cap)
	}
	if cap.Constraints.MaxPromptCharacters == 0 {
		cap.Constraints.MaxPromptCharacters = 32000
	}
	if cap.Constraints.MaxImageBytes == 0 {
		cap.Constraints.MaxImageBytes = 20 << 20
	}
	if cap.Constraints.MaxPixels == 0 {
		cap.Constraints.MaxPixels = 16777216
	}
	if cap.Sizes == nil {
		cap.Sizes = []string{}
	} else {
		cap.Sizes = append([]string{}, cap.Sizes...)
	}
	if cap.DurationSeconds == nil {
		cap.DurationSeconds = []int{}
	} else {
		cap.DurationSeconds = append([]int{}, cap.DurationSeconds...)
	}
	if len(cap.ReferenceRoles) > 0 {
		cap.ReferenceRoles = append([]string{}, cap.ReferenceRoles...)
	}
	return cap
}

func defaultVideoParamSchema(c Capability) json.RawMessage {
	enumeration := func(values ...string) parameter {
		v := make([]any, len(values))
		for i, s := range values {
			v[i] = s
		}
		return parameter{Type: "string", Enum: v}
	}
	intEnum := func(values ...int) parameter {
		v := make([]any, len(values))
		for i, n := range values {
			v[i] = json.Number(strconv.Itoa(n))
		}
		return parameter{Type: "integer", Enum: v}
	}
	seedMin, seedMax := json.Number("0"), json.Number("2147483647")
	props := map[string]parameter{
		"aspect_ratio":   enumeration(videoAspectRatios...),
		"resolution":     enumeration(videoResolutions...),
		"generate_audio": {Type: "boolean"},
		"audio_mode":     enumeration(videoAudioModes...),
		"fps":            intEnum(videoFPSValues...),
		"seed":           {Type: "integer", Minimum: &seedMin, Maximum: &seedMax},
		"camera_fixed":   {Type: "boolean"},
		"watermark":      {Type: "boolean"},
	}
	if len(c.Sizes) > 0 {
		props["size"] = enumeration(c.Sizes...)
	} else {
		minLen, maxLen := 3, 32
		props["size"] = parameter{Type: "string", MinLength: &minLen, MaxLength: &maxLen}
	}
	if len(c.DurationSeconds) > 0 {
		props["duration"] = intEnum(c.DurationSeconds...)
	} else {
		durMin, durMax := json.Number("1"), json.Number("60")
		props["duration"] = parameter{Type: "integer", Minimum: &durMin, Maximum: &durMax}
	}
	closed := false
	raw, _ := json.Marshal(parameterSchema{
		Type:                 "object",
		Properties:           props,
		AdditionalProperties: &closed,
	})
	return raw
}

func validateVideoDeclaration(c Config) error {
	d := c.Capability
	if d.Constraints.MaxPromptCharacters < 0 || d.Constraints.MaxPromptCharacters > 32000 || d.Constraints.MaxImageBytes < 0 || d.Constraints.MaxImageBytes > 20<<20 || d.Constraints.MaxPixels < 0 || d.Constraints.MaxPixels > 16777216 {
		return invalid("Constraints may only tighten video adapter limits")
	}
	allowedRoles := []string{"first_frame", "last_frame", "reference_image", "image", "reference_0", "reference_1", "reference_2", "reference_3"}
	for _, role := range d.ReferenceRoles {
		if !slices.Contains(allowedRoles, role) {
			return invalid("Unsupported video reference role: " + role)
		}
	}
	if len(d.ReferenceRoles) > d.MaxReferences {
		return invalid("Reference roles exceed max_references")
	}
	if len(d.ParamSchema) == 0 {
		return nil
	}
	s, e := parseSchema(d.ParamSchema)
	if e != nil {
		return e
	}
	base, _ := parseSchema(defaultVideoParamSchema(d))
	for k, p := range s.Properties {
		b, ok := base.Properties[k]
		if !ok || b.Type != p.Type {
			return invalid("Unsupported video adapter parameter: " + k)
		}
		if b.Enum != nil {
			if len(p.Enum) == 0 {
				return invalid("Schema cannot widen video adapter enum")
			}
			for _, v := range p.Enum {
				if !validParameter(b, v) {
					return invalid("Schema cannot widen video adapter enum")
				}
			}
		}
		if b.Minimum != nil && (p.Minimum == nil || compareNumbers(*p.Minimum, *b.Minimum) < 0) || b.Maximum != nil && (p.Maximum == nil || compareNumbers(*p.Maximum, *b.Maximum) > 0) || b.MaxLength != nil && (p.MaxLength == nil || *p.MaxLength > *b.MaxLength) {
			return invalid("Schema cannot widen video adapter bounds")
		}
	}
	return nil
}

func videoSizeMatchesAspect(size, aspect string) bool {
	if size == aspect {
		return true
	}
	parts := strings.Split(size, "x")
	rParts := strings.Split(aspect, ":")
	if len(parts) != 2 || len(rParts) != 2 {
		return false
	}
	w, e1 := strconv.Atoi(parts[0])
	h, e2 := strconv.Atoi(parts[1])
	rw, e3 := strconv.Atoi(rParts[0])
	rh, e4 := strconv.Atoi(rParts[1])
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || w <= 0 || h <= 0 || rw <= 0 || rh <= 0 {
		return false
	}
	return w*rh == h*rw
}

// ValidateVideo validates a video generation request against the declared Ark video capability.
func ValidateVideo(cap Capability, in GenerationInput) error {
	if cap.Protocol != "ark-video" || cap.Operation != "video" {
		return invalid("Unsupported video capability")
	}
	chars := cap.Constraints.MaxPromptCharacters
	if chars == 0 {
		chars = 32000
	}
	if strings.TrimSpace(in.Prompt) == "" || !utf8.ValidString(in.Prompt) || len(in.Prompt) > 65536 || len(in.Prompt) > cap.MaxPromptBytes || utf8.RuneCountInString(in.Prompt) > chars {
		return invalid("Prompt is empty, invalid UTF-8, or exceeds capability limit")
	}
	if in.Count < 1 || in.Count > cap.MaxCount || in.Count != 1 {
		return invalid("Video generation requires count=1 within capability limits")
	}
	if len(in.References) > cap.MaxReferences || len(in.References) > 16 || (len(cap.ReferenceRoles) > 0 && len(in.References) != len(cap.ReferenceRoles)) {
		return invalid("References exceed video capability limits")
	}
	for _, ref := range in.References {
		if !identifier.MatchString(ref) || len(ref) > 128 {
			return invalid("References must be registered asset IDs")
		}
	}
	if in.Params == nil {
		return invalid("params must be an object")
	}
	var sizeStr, aspectStr string
	for k, v := range in.Params {
		switch k {
		case "size":
			s, ok := v.(string)
			if !ok || s == "" || !regexp.MustCompile(`^[1-9][0-9]{0,4}x[1-9][0-9]{0,4}$`).MatchString(s) {
				return invalid("Invalid video parameter: size")
			}
			sizeStr = s
			if len(cap.Sizes) > 0 && !slices.Contains(cap.Sizes, s) {
				matched := false
				for _, declared := range cap.Sizes {
					if videoSizeMatchesAspect(declared, s) {
						matched = true
						break
					}
				}
				if !matched {
					return invalid("Unsupported video size: " + s)
				}
			}
		case "aspect_ratio":
			s, ok := v.(string)
			if !ok || !slices.Contains(videoAspectRatios, s) {
				return invalid("Invalid video parameter: aspect_ratio")
			}
			aspectStr = s
			if len(cap.Sizes) > 0 {
				matched := false
				for _, declared := range cap.Sizes {
					if videoSizeMatchesAspect(declared, s) {
						matched = true
						break
					}
				}
				if !matched {
					return invalid("Unsupported video aspect_ratio: " + s)
				}
			}
		case "resolution":
			s, ok := v.(string)
			if !ok || !slices.Contains(videoResolutions, s) {
				return invalid("Invalid video parameter: resolution")
			}
		case "duration":
			d, ok := intParam(v)
			if !ok || d < 1 || d > 60 {
				return invalid("Invalid video parameter: duration")
			}
			if len(cap.DurationSeconds) > 0 && !slices.Contains(cap.DurationSeconds, int(d)) {
				return invalid(fmt.Sprintf("Unsupported video duration: %d", d))
			}
		case "generate_audio":
			b, ok := v.(bool)
			if !ok {
				return invalid("Invalid video parameter: generate_audio")
			}
			if b && !cap.Audio {
				return invalid("Video capability does not support audio generation")
			}
		case "audio_mode":
			s, ok := v.(string)
			if !ok || !slices.Contains(videoAudioModes, s) {
				return invalid("Invalid video parameter: audio_mode")
			}
			if s != "none" && !cap.Audio {
				return invalid("Video capability does not support audio mode: " + s)
			}
		case "fps":
			fps, ok := intParam(v)
			if !ok || !slices.Contains(videoFPSValues, int(fps)) {
				return invalid("Invalid video parameter: fps")
			}
		case "seed":
			seed, ok := intParam(v)
			if !ok || seed < 0 || seed > 2147483647 {
				return invalid("Invalid video parameter: seed")
			}
		case "camera_fixed", "watermark":
			if _, ok := v.(bool); !ok {
				return invalid("Invalid video parameter: " + k)
			}
		default:
			return invalid("Unsupported or undeclared video parameter: " + k)
		}
	}
	if sizeStr != "" && aspectStr != "" && !videoSizeMatchesAspect(sizeStr, aspectStr) {
		return invalid(fmt.Sprintf("Video size %s and aspect_ratio %s disagree", sizeStr, aspectStr))
	}
	if genAudio, hasGen := in.Params["generate_audio"].(bool); hasGen && !genAudio {
		if mode, hasMode := in.Params["audio_mode"].(string); hasMode && mode != "none" {
			return invalid("audio_mode requires generate_audio=true")
		}
	}
	if len(cap.ParamSchema) > 0 {
		s, e := parseSchema(cap.ParamSchema)
		if e != nil {
			return e
		}
		for _, k := range s.Required {
			if _, ok := in.Params[k]; !ok {
				return invalid("Missing parameter: " + k)
			}
		}
		for k, v := range in.Params {
			p, ok := s.Properties[k]
			if !ok || !validParameter(p, v) {
				return invalid("Invalid or undeclared parameter: " + k)
			}
		}
	}
	return nil
}
