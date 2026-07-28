// Package builtin_skills holds skill Markdown files embedded into the Ally
// binary. It is a leaf package with no dependencies on internal/app, so the
// app layer can import it freely without cycles.
//
// To add a built-in skill, create `skills/<name>/SKILL.md` under this
// directory with YAML frontmatter (name, description, whenToUse). No other
// change is needed — internal/app/builtin_skills.go walks the tree at runtime.
package builtin_skills

import "embed"

// FS is the embedded filesystem rooted at the `skills` directory.
//
//go:embed all:skills
var FS embed.FS

// Root is the path prefix within FS where skill directories live.
const Root = "skills"
