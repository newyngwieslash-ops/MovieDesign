package agent

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/workspace"
)

const skillFileLimit = 65536

type SkillInfo struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	TriggerWords   []string `json:"trigger-words"`
	Version        string   `json:"version"`
	Revision       string   `json:"revision"`
	BundleRevision string   `json:"bundle_revision"`
	Modified       bool     `json:"modified"`
}
type Skill struct {
	SkillInfo
	Body       string            `json:"body"`
	References map[string]string `json:"references"`
}
type skillMeta struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	TriggerWords []string `json:"trigger-words"`
	Version      string   `json:"version"`
	References   []string `json:"references"`
}
type Skills struct {
	root    string
	files   *workspace.Workspace
	bundled map[string]Skill
	paths   map[string][]string
}

// Only authored JSON-valued YAML scalars/arrays are supported; no YAML tags,
// aliases, scripts or third-party skill packages are interpreted.
func parseSkillMeta(raw string) (skillMeta, error) {
	var m skillMeta
	values := map[string]json.RawMessage{}
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		key, value, ok := strings.Cut(strings.TrimSuffix(line, "\r"), ":")
		if !ok || values[key] != nil {
			return m, fmt.Errorf("invalid or duplicate skill metadata")
		}
		switch key {
		case "name", "description", "trigger-words", "version", "references":
		default:
			return m, fmt.Errorf("unknown skill metadata")
		}
		if !json.Valid([]byte(value)) {
			return m, fmt.Errorf("skill metadata values must be JSON scalars or arrays")
		}
		values[key] = json.RawMessage(value)
	}
	b, e := json.Marshal(values)
	if e != nil {
		return m, e
	}
	e = json.Unmarshal(b, &m)
	if e != nil || len(values) != 5 || strings.TrimSpace(m.Name) == "" || len(m.Name) > 128 || strings.TrimSpace(m.Description) == "" || len(m.Description) > 1024 || !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(m.Version) || len(m.TriggerWords) == 0 || len(m.TriggerWords) > 16 || len(m.References) > 8 {
		return m, fmt.Errorf("invalid skill metadata")
	}
	for _, word := range m.TriggerWords {
		if strings.TrimSpace(word) == "" || len(word) > 128 {
			return m, fmt.Errorf("invalid trigger word")
		}
	}
	seen := map[string]bool{}
	for _, ref := range m.References {
		if !regexp.MustCompile(`^references/[A-Za-z0-9_-]+\.md$`).MatchString(ref) || seen[ref] {
			return m, fmt.Errorf("invalid managed reference")
		}
		seen[ref] = true
	}
	return m, nil
}
func readSkill(name string, read func(string) (string, error), allowed []string) (Skill, error) {
	var s Skill
	meta, e := read("meta.yaml")
	if e != nil {
		return s, e
	}
	m, e := parseSkillMeta(meta)
	if e != nil || m.Name != name {
		return s, fmt.Errorf("invalid or duplicate skill name: %s", name)
	}
	body, e := read("SKILL.md")
	if e != nil {
		return s, e
	}
	normalized := strings.ReplaceAll(body, "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return s, fmt.Errorf("missing skill frontmatter")
	}
	header, _, found := strings.Cut(strings.TrimPrefix(normalized, "---\n"), "\n---\n")
	front, e := parseSkillMeta(header)
	a, _ := json.Marshal(m)
	b, _ := json.Marshal(front)
	if !found || e != nil || string(a) != string(b) {
		return s, fmt.Errorf("skill frontmatter and meta.yaml must agree")
	}
	for _, section := range []string{"适用范围", "不用于", "输入", "确认门禁序列", "产物", "失败回流"} {
		if !strings.Contains(normalized, "\n## "+section+"\n") {
			return s, fmt.Errorf("missing methodology section: %s", section)
		}
	}
	refs := map[string]string{}
	for _, ref := range m.References {
		if allowed != nil {
			ok := false
			for _, p := range allowed {
				if ref == p {
					ok = true
				}
			}
			if !ok {
				return s, fmt.Errorf("reference is outside bundled scope")
			}
		}
		refs[ref], e = read(ref)
		if e != nil {
			return s, e
		}
	}
	// Authored Markdown may link only to explicitly managed references. Images,
	// absolute paths, encoded traversal and external URLs are not skill inputs.
	link := regexp.MustCompile(`!?\[[^\]]*\]\(([^)]*)\)`)
	for _, content := range append([]string{body}, mapValues(refs)...) {
		for _, match := range link.FindAllStringSubmatch(content, -1) {
			if _, ok := refs[match[1]]; !ok {
				return s, fmt.Errorf("skill link leaves managed references")
			}
		}
		if strings.Contains(content, "]:") || strings.Contains(content, "<") {
			return s, fmt.Errorf("unsupported indirect link or HTML in skill")
		}
	}
	s = Skill{SkillInfo: SkillInfo{Name: m.Name, Description: m.Description, TriggerWords: m.TriggerWords, Version: m.Version}, Body: body, References: refs}
	frozen, _ := json.Marshal(s)
	sum := sha256.Sum256(frozen)
	s.Revision = hex.EncodeToString(sum[:])
	return s, nil
}
func mapValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
func boundedSkill(content string) (string, error) {
	if len(content) > skillFileLimit || !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
		return "", fmt.Errorf("skill file must be bounded UTF-8 text")
	}
	return content, nil
}

func NewSkills(root string, bundle map[string]string) (*Skills, error) {
	s := &Skills{root: root, bundled: map[string]Skill{}, paths: map[string][]string{}}
	for file := range bundle {
		parts := strings.Split(file, "/")
		if len(parts) < 2 || parts[0] == "" || parts[0] == "." || parts[0] == ".." || strings.ContainsAny(parts[0], "\\:") {
			return nil, fmt.Errorf("invalid bundled skill path")
		}
		s.paths[parts[0]] = append(s.paths[parts[0]], strings.Join(parts[1:], "/"))
	}
	for name, paths := range s.paths {
		skill, e := readSkill(name, func(p string) (string, error) {
			v, ok := bundle[name+"/"+p]
			if !ok {
				return "", fmt.Errorf("missing bundled skill file")
			}
			return boundedSkill(v)
		}, nil)
		if e != nil {
			return nil, e
		}
		if len(paths) != len(skill.References)+2 {
			return nil, fmt.Errorf("unexpected bundled files")
		}
		skill.BundleRevision = skill.Revision
		s.bundled[name] = skill
	}
	if len(s.bundled) == 0 {
		return nil, fmt.Errorf("no bundled skills")
	}
	if e := os.MkdirAll(root, 0700); e != nil {
		return nil, e
	}
	files, e := workspace.Open(root)
	if e != nil {
		return nil, e
	}
	s.files = files
	for name, skill := range s.bundled {
		for _, p := range s.paths[name] {
			content := []byte(bundle[name+"/"+p])
			for _, target := range []string{".bundled/" + skill.BundleRevision + "/" + name + "/" + p, name + "/" + p} {
				if e = files.ManagedInstall(target, content); e != nil {
					files.Close()
					return nil, e
				}
			}
		}
	}
	return s, nil
}
func InstallSkills(root string) (*Skills, error) { return NewSkills(root, bundledSkills) }
func (s *Skills) Close() error                   { return s.files.Close() }
func (s *Skills) Get(name string) (Skill, error) {
	bundled, ok := s.bundled[name]
	if !ok {
		return Skill{}, chatError(404, "Unknown skill")
	}
	skill, e := readSkill(name, func(p string) (string, error) {
		b, e := s.files.ManagedRead(path.Join(name, p), skillFileLimit)
		if e != nil {
			return "", e
		}
		return boundedSkill(string(b))
	}, s.paths[name])
	if e != nil {
		return Skill{}, chatError(400, "Invalid managed skill files: "+e.Error())
	}
	skill.BundleRevision = bundled.Revision
	skill.Modified = skill.Revision != bundled.Revision
	return skill, nil
}
func (s *Skills) List() ([]SkillInfo, error) {
	names := make([]string, 0, len(s.bundled))
	for name := range s.bundled {
		names = append(names, name)
	}
	sort.Strings(names)
	out := []SkillInfo{}
	for _, name := range names {
		skill, e := s.Get(name)
		if e != nil {
			return nil, e
		}
		out = append(out, skill.SkillInfo)
	}
	return out, nil
}
func (s Skill) Prefix() string {
	names := make([]string, 0, len(s.References))
	for name := range s.References {
		names = append(names, name)
	}
	sort.Strings(names)
	prefix := "当前唯一采用的创作方法（取代历史方法选择；仅方法，不授予工具权限或任何后续批准）：" + s.Name + " / " + s.Version + " / " + s.Revision + "\n" + s.Body
	for _, name := range names {
		prefix += "\n\n受管引用 " + name + "\n" + s.References[name]
	}
	return prefix
}
func (s *Skills) Match(text string) (string, error) {
	list, e := s.List()
	if e != nil {
		return "", e
	}
	selected := ""
	// ponytail: unique substring triggers cover these two bundled methods only;
	// ambiguous requests stay unselected for the user to choose, not an LLM classifier.
	for _, skill := range list {
		for _, word := range skill.TriggerWords {
			if strings.Contains(text, word) {
				if selected != "" && selected != skill.Name {
					return "", nil
				}
				selected = skill.Name
				break
			}
		}
	}
	return selected, nil
}

// SelectSkill prepares an admission snapshot, never mutates the original wire.
// Existing receipts and adopted sessions are checked before any filesystem read.
func (c *Chat) SelectSkill(ctx context.Context, p, session string, in ChatInput) (*Skill, error) {
	var previous string
	e := c.DB.QueryRowContext(ctx, "SELECT response_json FROM write_requests WHERE project_id=? AND operation=? AND request_id=?", p, "chat.send."+session, in.RequestID).Scan(&previous)
	if e == nil {
		return nil, nil
	} // WriteRequest performs exact fingerprint/replay checks.
	if !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	}
	id := ""
	if in.SkillID == nil {
		e = c.DB.QueryRowContext(ctx, "SELECT w.response_json FROM chat_messages m JOIN write_requests w ON w.project_id=? AND w.operation='chat.send.'||m.session_id AND w.request_id=m.request_id WHERE m.session_id=? AND m.role='user' ORDER BY m.rowid DESC LIMIT 1", p, session).Scan(&previous)
		if e == nil {
			var adopted Message
			if e = json.Unmarshal([]byte(previous), &adopted); e != nil {
				return nil, e
			}
			if adopted.Skill != nil || adopted.SkillDisabled {
				return nil, nil
			}
		}
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
		if c.Skills == nil {
			return nil, nil
		}
		id, e = c.Skills.Match(in.Text)
		if e != nil {
			return nil, e
		}
	} else {
		id = *in.SkillID
	}
	if id == "" {
		return nil, nil
	}
	if c.Skills == nil {
		return nil, chatError(404, "Unknown skill")
	}
	skill, e := c.Skills.Get(id)
	if e != nil {
		return nil, e
	}
	return &skill, nil
}
