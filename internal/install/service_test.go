package install_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vsem-azamat/agora/internal/install"
)

func TestPlainUnit(t *testing.T) {
	unit := install.Unit([]string{bin, "hub"})
	if !strings.HasPrefix(unit, install.UnitHeader+"\n") {
		t.Errorf("unit lacks the header:\n%s", unit)
	}
	for _, line := range []string{
		"[Service]",
		"ExecStart=" + bin + " hub\n",
		"Restart=on-failure\n",
		"[Install]",
		"WantedBy=default.target\n",
	} {
		if !strings.Contains(unit, line) {
			t.Errorf("unit lacks %q:\n%s", line, unit)
		}
	}
}

func TestUnitArgumentsAreQuotedForSystemd(t *testing.T) {
	unit := install.Unit([]string{
		"/opt/my tools/agora", "hub", "--db", "/srv/agora/agora.db",
		"--wake-command", `notify -t "$AGORA_TERMINAL" 100% a\b`,
	})
	want := `ExecStart="/opt/my tools/agora" hub --db /srv/agora/agora.db --wake-command "notify -t \"$$AGORA_TERMINAL\" 100%% a\\b"` + "\n"
	if !strings.Contains(unit, want) {
		t.Fatalf("unit:\n%s\nwant line:\n%s", unit, want)
	}
}

func TestServiceInstallAndUninstall(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "systemd", "user")
	unit := install.Unit([]string{bin, "hub"})
	if o, err := install.InstallService(dir, unit); err != nil || o != install.Created {
		t.Fatalf("install: %v %v", o, err)
	}
	path := filepath.Join(dir, install.ServiceName+".service")
	if b, _ := os.ReadFile(path); string(b) != unit {
		t.Fatalf("unit file: %q", b)
	}
	if o, err := install.InstallService(dir, unit); err != nil || o != install.Unchanged {
		t.Fatalf("second install: %v %v", o, err)
	}
	other := install.Unit([]string{bin, "hub", "--db", "/srv/agora/agora.db"})
	if o, err := install.InstallService(dir, other); err != nil || o != install.Updated {
		t.Fatalf("changed install: %v %v", o, err)
	}
	// what `systemctl --user enable` creates
	wants := filepath.Join(dir, "default.target.wants")
	if err := os.MkdirAll(wants, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, filepath.Join(wants, install.ServiceName+".service")); err != nil {
		t.Fatal(err)
	}
	if o, err := install.UninstallService(dir); err != nil || o != install.Removed {
		t.Fatalf("uninstall: %v %v", o, err)
	}
	for _, p := range []string{path, filepath.Join(wants, install.ServiceName+".service")} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("%s still there: %v", p, err)
		}
	}
	if o, err := install.UninstallService(dir); err != nil || o != install.Absent {
		t.Fatalf("second uninstall: %v %v", o, err)
	}
}

func TestForeignUnitIsLeftAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, install.ServiceName+".service")
	mine := "[Service]\nExecStart=/usr/bin/true\n"
	if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := install.InstallService(dir, install.Unit([]string{bin, "hub"})); err == nil {
		t.Error("overwrote a unit Agora did not write")
	}
	if _, err := install.UninstallService(dir); err == nil {
		t.Error("removed a unit Agora did not write")
	}
	if b, _ := os.ReadFile(path); string(b) != mine {
		t.Fatalf("unit changed: %s", b)
	}
}

func TestDanglingWantsLinkIsRemoved(t *testing.T) {
	dir := t.TempDir()
	wants := filepath.Join(dir, "default.target.wants")
	if err := os.MkdirAll(wants, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(wants, install.ServiceName+".service")
	if err := os.Symlink(filepath.Join(dir, install.ServiceName+".service"), link); err != nil {
		t.Fatal(err)
	}
	if o, err := install.UninstallService(dir); err != nil || o != install.Removed {
		t.Fatalf("uninstall: %v %v", o, err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("link left: %v", err)
	}
}
