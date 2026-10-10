package sessions

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/vsem-azamat/agora/internal/queue"
	"github.com/vsem-azamat/agora/internal/rooms"
)

// deliver returns the agent's oldest unread messages as context text and marks the ones shown
// as read; it also returns how many of them address the agent.
func (s *Sessions) deliver(ctx context.Context, agent string) (string, int, error) {
	if s.rooms == nil {
		return "", 0, nil
	}
	msgs, total, err := s.rooms.Take(ctx, agent, false, DeliverAtOnce)
	if err != nil || len(msgs) == 0 {
		return "", 0, err
	}
	lines := []string{fmt.Sprintf("Agora: new board messages for you (%s). Act on what is addressed to you "+
		"(reply: agora post <room> '...' --reply <id>); otherwise note them and carry on.", agent)}
	addressed := 0
	for _, m := range msgs {
		lines = append(lines, rooms.Format(m, DeliverChars))
		if m.Addressed {
			addressed++
		}
	}
	if more := total - len(msgs); more > 0 {
		lines = append(lines, fmt.Sprintf("... %d more: run `agora unread`.", more))
	}
	return strings.Join(lines, "\n"), addressed, nil
}

// waiting returns the text for what needs the agent now (unread messages addressed to it,
// which it marks read, and offered slots), or "" when nothing does. when says when the agent
// should act, e.g. "before you end your turn".
func (s *Sessions) waiting(ctx context.Context, agent, when string) (string, error) {
	var msgs []rooms.Message
	var total int
	if s.rooms != nil {
		var err error
		if msgs, total, err = s.rooms.Take(ctx, agent, true, DeliverAtOnce); err != nil {
			return "", err
		}
	}
	entries, err := s.queue.EntriesOf(ctx, agent)
	if err != nil {
		return "", err
	}
	return attention(when+",", agent, msgs, total-len(msgs), entries), nil
}

// wakeText is what a connector's wait wakes the agent with.
func wakeText(agent string, msgs []rooms.Message, entries []queue.Entry) string {
	return attention("you were woken;", agent, msgs, 0, entries)
}

// attention is the text for what needs the agent now: the messages addressed to it (and how
// many more there are) and its offered slots, or "" for none. opening says when it should act.
func attention(opening, agent string, msgs []rooms.Message, more int, entries []queue.Entry) string {
	var parts []string
	if len(msgs) > 0 {
		lines := []string{fmt.Sprintf("Agora: %s answer what is addressed to you (%s), "+
			"even with \"not me\" or \"later\": agora post <room> '...' --reply <id>.", opening, agent)}
		for _, m := range msgs {
			lines = append(lines, rooms.Format(m, DeliverChars))
		}
		if more > 0 {
			lines = append(lines, fmt.Sprintf("... %d more addressed to you: run `agora unread`.", more))
		}
		parts = append(parts, strings.Join(lines, "\n"))
	}
	if len(offered(entries)) > 0 {
		parts = append(parts, note(agent, entries, nil)+" Claim or release the offered slot now; otherwise it passes to the next agent.")
	}
	return strings.Join(parts, "\n\n")
}

func clock(t time.Time) string { return t.Local().Format("15:04") }

func note(agent string, entries []queue.Entry, lost []string) string {
	return strings.TrimSpace(fmt.Sprintf("Agora: you are %s. %s", agent, places(entries, lost)))
}

// places describes the agent's places in queues and the ones it lost, or "" for none.
func places(entries []queue.Entry, lost []string) string {
	var held, waiting, offered []string
	for _, e := range entries {
		switch e.State {
		case queue.Held:
			held = append(held, fmt.Sprintf("%s until %s", e.Key, clock(e.Expires)))
		case queue.Waiting:
			waiting = append(waiting, fmt.Sprintf("%s at position %d", e.Key, e.Position))
		case queue.Offered:
			offered = append(offered, fmt.Sprintf("%s: claim it by %s with `agora queue renew %s`, or give it up with `agora queue release %s`",
				e.Key, clock(e.Expires), e.Key, e.Key))
		}
	}
	var parts []string
	for _, o := range offered {
		parts = append(parts, fmt.Sprintf("It is your turn on %s.", o))
	}
	if len(held) > 0 {
		parts = append(parts, fmt.Sprintf("You hold %s; release with `agora queue release <key>` when done.", strings.Join(held, ", ")))
	}
	if len(waiting) > 0 {
		parts = append(parts, fmt.Sprintf("You wait for %s.", strings.Join(waiting, ", ")))
	}
	if len(lost) > 0 {
		parts = append(parts, fmt.Sprintf("You no longer hold or wait for %s (lease ended, turn missed or removed).", strings.Join(lost, ", ")))
	}
	return strings.Join(parts, " ")
}
