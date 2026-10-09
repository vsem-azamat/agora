package install_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vsem-azamat/agora/internal/install"
)

func TestSkillHasFrontmatter(t *testing.T) {
	head, body, ok := strings.Cut(strings.TrimPrefix(install.Skill, "---\n"), "\n---\n")
	if !strings.HasPrefix(install.Skill, "---\n") || !ok {
		t.Fatalf("no frontmatter:\n%.200s", install.Skill)
	}
	if !strings.Contains(head, "\nname: agora\n") && !strings.HasPrefix(head, "name: agora\n") {
		t.Errorf("frontmatter lacks name: %q", head)
	}
	if !strings.Contains(head, "description: ") {
		t.Errorf("frontmatter lacks description: %q", head)
	}
	for _, topic := range []string{
		"agora join", "agora status", "agora who", "agora post", "--reply", "agora read", "agora unread",
		"@all", "agora queue join", "--wait", "agora lock", "exit code 2", "lease", "agora propose", "agora vote", "agora charter",
		"woken", "CI is green on #", "--as", "agora leave",
	} {
		if !strings.Contains(body, topic) {
			t.Errorf("guide does not cover %q", topic)
		}
	}
}

func TestSkillInstallAndUninstall(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "skills")
	if o, err := install.InstallSkill(dir); err != nil || o != install.Created {
		t.Fatalf("install: %v %v", o, err)
	}
	path := filepath.Join(dir, "agora", "SKILL.md")
	if b, _ := os.ReadFile(path); string(b) != install.Skill {
		t.Fatal("skill not written")
	}
	if o, err := install.InstallSkill(dir); err != nil || o != install.Unchanged {
		t.Fatalf("second install: %v %v", o, err)
	}
	if err := os.WriteFile(path, []byte("old guide"), 0o644); err != nil {
		t.Fatal(err)
	}
	if o, err := install.InstallSkill(dir); err != nil || o != install.Updated {
		t.Fatalf("update: %v %v", o, err)
	}
	if o, err := install.UninstallSkill(dir); err != nil || o != install.Removed {
		t.Fatalf("uninstall: %v %v", o, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "agora")); !os.IsNotExist(err) {
		t.Fatalf("empty skill directory left: %v", err)
	}
	if o, err := install.UninstallSkill(dir); err != nil || o != install.Absent {
		t.Fatalf("second uninstall: %v %v", o, err)
	}
}

func TestSkillUninstallKeepsOtherFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := install.InstallSkill(dir); err != nil {
		t.Fatal(err)
	}
	mine := filepath.Join(dir, "agora", "notes.md")
	if err := os.WriteFile(mine, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := install.UninstallSkill(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "agora", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("SKILL.md left")
	}
	if _, err := os.Stat(mine); err != nil {
		t.Fatalf("user's file removed: %v", err)
	}
}
