package app

import (
	"io/fs"
	"path"
	"strings"

	"ally-dev/internal/builtin_skills"
)

// builtinSkillEntries returns SkillDefinitions for all embedded built-in
// skills, with embeddedContent populated so readers skip disk I/O. The
// embedded filesystem itself lives in the leaf package
// ally-dev/internal/builtin_skills; this function only parses it into
// SkillDefinition values that the app layer can consume.
//
// Add a new built-in skill by creating
// `internal/builtin_skills/skills/<name>/SKILL.md` with YAML frontmatter
// (name, description, whenToUse). No other change is needed.
func builtinSkillEntries() []SkillDefinition {
	var out []SkillDefinition
	root := builtin_skills.Root
	_ = fs.WalkDir(builtin_skills.FS, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.EqualFold(path.Base(p), "SKILL.md") {
			return nil
		}
		data, rerr := builtin_skills.FS.ReadFile(p)
		if rerr != nil {
			return nil
		}
		text := string(data)
		meta := parseSkillContent(p, text)
		if meta.Name == "" {
			return nil
		}
		meta.Source = "builtin"
		meta.Path = "builtin://" + strings.TrimPrefix(p, root+"/")
		dir := path.Dir(p)
		if dir == root {
			dir = ""
		}
		meta.Dir = dir
		meta.embeddedContent = text
		out = append(out, meta)
		return nil
	})
	return out
}
