package install

import (
	_ "embed"
	"os"
	"path/filepath"
)

// Skill is the agent guide in the Agent Skills format.
//
//go:embed skill/SKILL.md
var Skill string

// SkillDir is the default skills directory: Claude Code's, $CLAUDE_CONFIG_DIR/skills, else
// ~/.claude/skills.
func SkillDir() (string, error) {
	dir, err := claudeConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "skills"), nil
}

// SkillPath is where the guide goes in the skills directory dir.
func SkillPath(dir string) string { return filepath.Join(dir, "agora", "SKILL.md") }

// InstallSkill writes the guide into the skills directory dir.
func InstallSkill(dir string) (Outcome, error) {
	return writeIfChanged(SkillPath(dir), []byte(Skill))
}

// UninstallSkill removes the guide from the skills directory dir, and its directory when
// nothing else is in it.
func UninstallSkill(dir string) (Outcome, error) {
	o, err := removeIfExists(SkillPath(dir))
	if err != nil || o == Absent {
		return o, err
	}
	d := filepath.Dir(SkillPath(dir))
	if entries, err := os.ReadDir(d); err == nil && len(entries) == 0 {
		if err := os.Remove(d); err != nil {
			return o, err
		}
	}
	return o, nil
}
