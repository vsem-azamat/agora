package install

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ServiceName is the name of the hub's systemd user unit, without the .service suffix.
const ServiceName = "agora-hub"

// ServiceDir is where systemd looks for the user's own units: $XDG_CONFIG_HOME/systemd/user,
// else ~/.config/systemd/user.
func ServiceDir() (string, error) {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "systemd", "user"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "systemd", "user"), nil
}

// Unit is the systemd user unit that runs argv (the agora binary, "hub" and its flags).
func Unit(argv []string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = systemdQuote(a)
	}
	return "[Unit]\n" +
		"Description=Agora hub\n" +
		"Documentation=https://github.com/vsem-azamat/agora\n" +
		"\n" +
		"[Service]\n" +
		"ExecStart=" + strings.Join(quoted, " ") + "\n" +
		"Restart=on-failure\n" +
		"RestartSec=2\n" +
		"\n" +
		"[Install]\n" +
		"WantedBy=default.target\n"
}

// systemdQuote makes s one argument of a unit's command line that systemd passes on exactly:
// plain words stay as they are, anything else is double-quoted with C escapes, and `$` and `%`
// are doubled so systemd neither expands variables nor specifiers in it.
func systemdQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_/.,:+=@-") == "" {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '$':
			b.WriteString("$$")
		case '%':
			b.WriteString("%%")
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// InstallService writes unit as the hub's unit file in dir.
func InstallService(dir, unit string) (Outcome, error) {
	return writeIfChanged(filepath.Join(dir, ServiceName+".service"), []byte(unit))
}

// UninstallService removes the hub's unit file from dir and the link that enabling it made.
func UninstallService(dir string) (Outcome, error) {
	link := filepath.Join(dir, "default.target.wants", ServiceName+".service")
	if fi, err := os.Lstat(link); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		if err := os.Remove(link); err != nil {
			return 0, err
		}
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return 0, err
	}
	return removeIfExists(filepath.Join(dir, ServiceName+".service"))
}
