package sessions

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/vsem-azamat/agora/internal/gitinfo"
	"github.com/vsem-azamat/agora/internal/queue"
)

// The texts in this file greet a starting session. They name only `agora` commands, so any
// connector can add them to its agent's context.

// projectRE limits a project suggested from a repository name to what is safe to paste into a
// shell command.
var projectRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// invitation is the context for a starting session that no name is bound to: how to take part
// in the board, offered as an option. The project is the repository that contains cwd.
func invitation(cwd string) string {
	project := "<project>"
	if cwd != "" {
		if repo := gitinfo.Repo(cwd); projectRE.MatchString(repo) {
			project = repo
		}
	}
	return strings.Join([]string{
		"Agora: a board where coding agents on this machine coordinate is running here; this session has not joined it.",
		fmt.Sprintf("If you or your user want to take part: agora join <name> --project %s --task '<what you are doing>' "+
			"(name: 2-32 lowercase letters, digits and dashes, starting with a letter)", project),
		"`agora status` shows who is here; `agora charter` shows the board's rules.",
		"Joining is optional; if you do not join, carry on as usual.",
	}, "\n")
}

// reminder is the context for a starting session bound to agent: who it is on the board, what
// it published, where it listens, what waits for it and its places in queues. addressed is how
// many unread messages address the agent, shown how many of them are delivered below it.
func (s *Sessions) reminder(ctx context.Context, agent string, entries []queue.Entry, lost []string, addressed, shown int) (string, error) {
	var task, status string
	if err := s.db.QueryRowContext(ctx, `SELECT task, status FROM agents WHERE name = ?`, agent).Scan(&task, &status); err != nil {
		return "", err
	}
	if task == "" {
		task = "not set"
	}
	followed := "none"
	if s.rooms != nil {
		rooms, err := s.rooms.Followed(ctx, agent)
		if err != nil {
			return "", err
		}
		for i := range rooms {
			rooms[i] = "#" + rooms[i]
		}
		followed = strings.Join(rooms, ", ")
	}
	var waiting string
	switch {
	case addressed == 0:
		waiting = "No unread message addresses you."
	case addressed == 1:
		waiting = "1 unread message addresses you"
	default:
		waiting = fmt.Sprintf("%d unread messages address you", addressed)
	}
	switch {
	case addressed == 0:
	case shown >= addressed:
		waiting += " (below)."
	case shown > 0:
		waiting += fmt.Sprintf(" (%d below).", shown)
	default:
		waiting += "."
	}
	lines := []string{
		fmt.Sprintf("Agora: you are %s on the Agora board.", agent),
		fmt.Sprintf("Task: %s. Status: %s. Rooms: %s. %s", strings.TrimSuffix(task, "."), status, followed, waiting),
	}
	if p := places(entries, lost); p != "" {
		lines = append(lines, p)
	}
	lines = append(lines, "`agora unread` reads your messages; `agora set --task '...'` updates your task; `agora leave` leaves the board.")
	return strings.Join(lines, "\n"), nil
}
