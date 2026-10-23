package agent

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSkillBundledSourceEqualityAndAutomaticMatch(t *testing.T) {
	count := 0
	e := filepath.WalkDir("../../../agent/skills", func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		key, _ := filepath.Rel("../../../agent/skills", p)
		if strings.ReplaceAll(string(b), "\r\n", "\n") != strings.ReplaceAll(bundledSkills[filepath.ToSlash(key)], "\r\n", "\n") {
			t.Errorf("bundled source drift: %s", p)
		}
		count++
		return nil
	})
	if e != nil || count != len(bundledSkills) {
		t.Fatal("bundle inventory drift", e, count)
	}
	s, e := InstallSkills(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	for _, tc := range []struct{ text, name string }{{"用这张图做个15秒口播视频配轻快音乐", "口播视频"}, {"请按区域标注修复图片", "图片标注修复"}, {"先口播再修复图片", ""}, {"hello", ""}} {
		name, e := s.Match(tc.text)
		if e != nil || name != tc.name {
			t.Fatal(tc, name, e)
		}
	}
}

func TestSkillManagedReparseAndHardLinksRejected(t *testing.T) {
	for _, kind := range []string{"file-symlink", "directory-junction", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			s, e := NewSkills(root, skillFixture())
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			target := filepath.Join(t.TempDir(), "outside.md")
			if e = os.WriteFile(target, []byte("outside"), 0600); e != nil {
				t.Fatal(e)
			}
			file := filepath.Join(root, "口播视频", "references", "storyboard.md")
			if kind == "directory-junction" {
				dir := filepath.Dir(file)
				if e = os.Rename(dir, dir+"-saved"); e != nil {
					t.Fatal(e)
				}
				command := exec.Command("powershell", "-NoProfile", "-Command", "New-Item -ItemType Junction -Path $env:MOVIEDESIGN_LINK_PATH -Target $env:MOVIEDESIGN_LINK_TARGET | Out-Null")
				command.Env = append(os.Environ(), "MOVIEDESIGN_LINK_PATH="+dir, "MOVIEDESIGN_LINK_TARGET="+filepath.Dir(target))
				if output, e := command.CombinedOutput(); e != nil {
					t.Fatalf("junction creation: %v %s", e, output)
				}
				if _, e = s.Get("口播视频"); e == nil {
					t.Fatal("junction reference read")
				}
				return
			}
			if e = os.Remove(file); e != nil {
				t.Fatal(e)
			}
			if kind == "hardlink" {
				e = os.Link(target, file)
			} else {
				e = os.Symlink(target, file)
			}
			if e != nil {
				t.Skipf("OS link privilege unavailable: %v", e)
			}
			if _, e = s.Get("口播视频"); e == nil {
				t.Fatal("linked reference read")
			}
		})
	}
}

func skillFixture() map[string]string {
	meta := "name: \"口播视频\"\ndescription: \"完整口播方法\"\ntrigger-words: [\"口播\"]\nversion: \"1.0.0\"\nreferences: [\"references/storyboard.md\"]\n"
	return map[string]string{"口播视频/meta.yaml": meta, "口播视频/SKILL.md": "---\n" + meta + "---\n## 适用范围\n完整视频\n## 不用于\n拼接\n## 输入\n主题\n## 确认门禁序列\n人类回执\n## 产物\n[分镜](references/storyboard.md)\n## 失败回流\n报告原问题\n", "口播视频/references/storyboard.md": "Panel frozen reference"}
}
func TestSkillInstallPreservesEditsAndRegistersUpgrade(t *testing.T) {
	root := t.TempDir()
	bundle := skillFixture()
	s, e := NewSkills(root, bundle)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	a, e := s.Get("口播视频")
	if e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(root, "口播视频", "SKILL.md")
	edited := bundle["口播视频/SKILL.md"] + "\n用户改稿\n"
	if e = os.WriteFile(file, []byte(edited), 0600); e != nil {
		t.Fatal(e)
	}
	bundle["口播视频/references/storyboard.md"] = "upgraded reference"
	upgraded, e := NewSkills(root, bundle)
	if e != nil {
		t.Fatal(e)
	}
	defer upgraded.Close()
	b, e := upgraded.Get("口播视频")
	if e != nil {
		t.Fatal(e)
	}
	if b.Body != edited || b.References["references/storyboard.md"] != "Panel frozen reference" || a.Revision == b.Revision || !b.Modified || a.BundleRevision == b.BundleRevision {
		t.Fatalf("lost edits or upgrade: %+v", b)
	}
	if _, e = os.Stat(filepath.Join(root, ".bundled", b.BundleRevision, "口播视频", "SKILL.md")); e != nil {
		t.Fatal(e)
	}
	if _, e = upgraded.Get("../口播视频"); e == nil {
		t.Fatal("traversal accepted")
	}
	if _, e = upgraded.Get("unknown"); e == nil {
		t.Fatal("unknown skill accepted")
	}
}
func TestSkillRejectsMalformedBundles(t *testing.T) {
	for _, kind := range []string{"frontmatter", "duplicate", "escape", "reference", "utf8", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			b := skillFixture()
			switch kind {
			case "frontmatter":
				b["口播视频/SKILL.md"] = "no frontmatter"
			case "duplicate":
				for p, v := range skillFixture() {
					b[strings.Replace(p, "口播视频/", "another/", 1)] = v
				}
			case "escape":
				b["口播视频/SKILL.md"] += "[outside](../secrets.md)"
			case "reference":
				b["口播视频/references/storyboard.md"] = "[outside](../../secrets.md)"
			case "utf8":
				b["口播视频/references/storyboard.md"] = string([]byte{255})
			case "oversize":
				b["口播视频/references/storyboard.md"] = strings.Repeat("x", 65537)
			}
			if s, e := NewSkills(t.TempDir(), b); e == nil {
				s.Close()
				t.Fatal("invalid bundle installed")
			}
		})
	}
}
func TestSkillFrozenChatReplayContinuationAndRestart(t *testing.T) {
	f := newChatFixture(t, 0, false)
	f.chat.Close() // Queue without a native worker.
	s, e := NewSkills(t.TempDir(), skillFixture())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	c := NewChat(f.chat.DB, f.chat.Turns, f.chat.Events, f.chat.Questions)
	c.Host = f.chat.Host
	c.Skills = s
	c.Grace = time.Millisecond
	defer c.Close()
	raw := []byte(strings.TrimSuffix(string(f.input("skill-first")), "}") + `,"skill_id":"口播视频"}`)
	m, e := c.Send(context.Background(), f.project, f.session, raw)
	if e != nil {
		t.Fatal(e)
	}
	if m.Skill == nil || !strings.Contains(m.Skill.Prefix(), "Panel frozen reference") {
		t.Fatal("method not frozen")
	}
	if e = os.WriteFile(filepath.Join(s.root, "口播视频", "SKILL.md"), []byte("broken edit"), 0600); e != nil {
		t.Fatal(e)
	}
	replay, e := c.Send(context.Background(), f.project, f.session, raw)
	if e != nil || replay.Skill.Revision != m.Skill.Revision {
		t.Fatal("replay reread files", e)
	}
	next := NewChat(c.DB, c.Turns, c.Events, c.Questions)
	next.Host = c.Host
	next.Skills = s
	next.Grace = time.Millisecond
	defer next.Close()
	continued, e := next.Send(context.Background(), f.project, f.session, f.input("continue"))
	if e != nil || continued.Skill.Revision != m.Skill.Revision {
		t.Fatal("restart drift", e)
	}
	history, e := next.Messages(context.Background(), f.project, f.session, 0, 100)
	if e != nil || history[0].Skill.Revision != m.Skill.Revision {
		t.Fatal("history lost snapshot", e)
	}
	if string(history[0].Content) != string(raw) {
		t.Fatal("original wire changed")
	}
	bad := []byte(strings.TrimSuffix(string(f.input("new-version")), "}") + `,"skill_id":"口播视频"}`)
	if _, e = next.Send(context.Background(), f.project, f.session, bad); e == nil {
		t.Fatal("explicit selection accepted malformed edit")
	}
	none := []byte(strings.TrimSuffix(string(f.input("none")), "}") + `,"skill_id":""}`)
	clear, e := next.Send(context.Background(), f.project, f.session, none)
	if e != nil || clear.Skill != nil {
		t.Fatal("explicit none failed", e)
	}
	after, e := next.Send(context.Background(), f.project, f.session, f.input("after-none"))
	if e != nil || after.Skill != nil {
		t.Fatal("none not retained", e)
	}
	var fields map[string]any
	_ = json.Unmarshal(raw, &fields)
	fields["skill"] = m.Skill
	forged, _ := json.Marshal(fields)
	if _, e = next.Send(context.Background(), f.project, f.session, forged); e == nil {
		t.Fatal("client forged snapshot")
	}
}

func TestSkillAutomaticAdoptionAfterOrdinaryMessageAndDurableDisable(t *testing.T) {
	f := newChatFixture(t, 0, false)
	f.chat.Close()
	s, e := InstallSkills(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	c := NewChat(f.chat.DB, f.chat.Turns, f.chat.Events, f.chat.Questions)
	c.Host = f.chat.Host
	c.Skills = s
	c.Grace = time.Millisecond
	defer c.Close()
	if _, e = c.Send(context.Background(), f.project, f.session, f.input("greet")); e != nil {
		t.Fatal(e)
	}
	raw := []byte(strings.Replace(string(f.input("auto")), "hello", "用这张图做个15秒口播视频配轻快音乐", 1))
	m, e := c.Send(context.Background(), f.project, f.session, raw)
	if e != nil || m.Skill == nil || m.Skill.Name != "口播视频" {
		t.Fatal("automatic skill missing", e)
	}
	none := []byte(strings.TrimSuffix(string(f.input("none")), "}") + `,"skill_id":""}`)
	if _, e = c.Send(context.Background(), f.project, f.session, none); e != nil {
		t.Fatal(e)
	}
	raw = []byte(strings.Replace(string(f.input("disabled")), "hello", "口播视频", 1))
	m, e = c.Send(context.Background(), f.project, f.session, raw)
	if e != nil || m.Skill != nil || !m.SkillDisabled {
		t.Fatal("disabled auto reactivated", e)
	}
	raw = []byte(strings.TrimSuffix(string(f.input("null")), "}") + `,"skill_id":null}`)
	if _, e = c.Send(context.Background(), f.project, f.session, raw); e == nil {
		t.Fatal("null skill selection accepted")
	}
}

func TestSkillActiveNativePromptUsesFrozenPrefix(t *testing.T) {
	f := newChatFixture(t, 0, false)
	s, e := NewSkills(t.TempDir(), skillFixture())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	f.chat.Skills = s
	raw := []byte(strings.Replace(string(f.input("native-skill")), "hello", "口播", 1))
	m, e := f.chat.Send(context.Background(), f.project, f.session, raw)
	if e != nil {
		t.Fatal(e)
	}
	waitChat(t, func() bool { return f.prompts.Load() == 1 })
	if e = os.WriteFile(filepath.Join(s.root, "口播视频", "references", "storyboard.md"), []byte("edited after admission"), 0600); e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	parts := append([]map[string]string(nil), f.promptParts...)
	f.mu.Unlock()
	if len(parts) < 2 || parts[0]["text"] != m.Skill.Prefix() || parts[1]["text"] != "口播" {
		t.Fatal("wrong native prompt parts", parts)
	}
	if !strings.Contains(parts[0]["text"], "Panel frozen reference") || strings.Contains(parts[0]["text"], "edited after admission") {
		t.Fatal("active version drift")
	}
}
