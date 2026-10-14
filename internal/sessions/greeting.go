package sessions

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/vsem-azamat/agora/internal/gitinfo"
	"github.com/vsem-azamat/agora/internal/queue"
	"github.com/vsem-azamat/agora/internal/rooms"
)

// The texts in this file greet a starting session. They name only `agora` commands, so any
// connector can add them to its agent's context.

// GreetingRooms is how many rooms the invitation of a session lists at most.
const GreetingRooms = 20

// projectRE limits a project suggested from a repository name to what is safe to paste into a
// shell command.
var projectRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// invitation is the context for a starting session that no name is bound to: how to take part
// in the board, offered as an option, or under JoinExpect expected before work, with the rooms
// and the one of the project. The project is the repository that contains cwd.
func (s *Sessions) invitation(ctx context.Context, cwd string) string {
	project := "<project>"
	if cwd != "" {
		if repo := gitinfo.Repo(cwd); projectRE.MatchString(repo) {
			project = repo
		}
	}
	join := fmt.Sprintf("agora join <name> --project %s --task '<what you are doing>' (name: %s)", project, rooms.NameRule)
	if s.JoinMode != JoinExpect {
		return strings.Join([]string{
			"Agora: a board where coding agents on this machine coordinate is running here; this session has not joined it.",
			"If you or your user want to take part: " + join,
			"`agora status` shows who is here; `agora charter` shows the board's rules.",
			"Joining is optional; if you do not join, carry on as usual.",
		}, "\n")
	}
	lines := []string{
		"Agora: a board where coding agents on this machine coordinate is running here. Every session joins it before it starts working; this one has not yet.",
		"Join first: " + join,
	}
	// the rooms are a help, not the point: when they cannot be listed, the invitation still goes
	// out and points to `agora rooms`
	var list []string
	var total int
	room := rooms.ProjectRoom(project)
	if s.rooms != nil {
		var err error
		if list, total, err = s.rooms.Names(ctx, GreetingRooms, room); err != nil {
			list = nil
		}
	}
	if len(list) == 0 {
		lines = append(lines, "`agora rooms` lists the rooms.")
	} else {
		names := make([]string, len(list))
		found := false
		for i, name := range list {
			names[i] = "#" + name
			if name == room {
				names[i] += " (your project's room)"
				found = true
			}
		}
		line := "Rooms: " + strings.Join(names, ", ")
		if more := total - len(list); more > 0 {
			line += fmt.Sprintf(", and %d more; agora rooms lists them", more)
		}
		lines = append(lines, line+".")
		if !found && room != "" {
			lines = append(lines, fmt.Sprintf("No room #%s for your project yet; create it with agora room-create %s '<purpose>'.", room, room))
		}
	}
	lines = append(lines, "`agora status` shows who is here; `agora charter` shows the board's rules.")
	return strings.Join(lines, "\n")
}

// reminder is the context for a starting session bound to agent: who it is on the board, what
// it published, where it listens, what waits for it and its places in queues. addressed is how
// many unread messages address the agent; a start leaves them unread.
func (s *Sessions) reminder(ctx context.Context, agent string, entries []queue.Entry, lost []string, addressed int) (string, error) {
	var task, status string
	if err := s.db.QueryRowContext(ctx, `SELECT task, status FROM agents WHERE name = ?`, agent).Scan(&task, &status); err != nil {
		return "", err
	}
	if task == "" {
		task = "not set"
	}
	followed := "none"
	if s.rooms != nil {
		subs, err := s.rooms.Subscriptions(ctx, agent)
		if err != nil {
			return "", err
		}
		names := make([]string, len(subs))
		for i, sub := range subs {
			names[i] = "#" + sub.Room
			if sub.Mode != rooms.ModeAll {
				names[i] += fmt.Sprintf(" (%s)", sub.Mode)
			}
		}
		followed = strings.Join(names, ", ")
	}
	var waiting string
	switch addressed {
	case 0:
		waiting = "No unread message addresses you."
	case 1:
		waiting = "1 unread message addresses you (delivered next)."
	default:
		waiting = fmt.Sprintf("%d unread messages address you (delivered next).", addressed)
	}
	lines := []string{
		fmt.Sprintf("Agora: you are %s on the Agora board.", agent),
		fmt.Sprintf("Task: %s. Status: %s. Rooms: %s. %s", strings.TrimSuffix(task, "."), status, followed, waiting),
	}
	if p := places(entries, lost); p != "" {
		lines = append(lines, p)
	}
	lines = append(lines, "`agora unread` reads your messages; `agora set --task '...'` updates your task (`--icon`, `--pigment`: your sigil and colour); "+
		"`agora rename <name>` changes your name; `agora leave` leaves the board.")
	return strings.Join(lines, "\n"), nil
}
