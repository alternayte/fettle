package agent

import (
	"testing"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/bots"
)

func TestMentions(t *testing.T) {
	mk := func(name string) *bots.Bot {
		b := &bots.Bot{Name: name}
		b.SetID(uuid.New())
		return b
	}
	ada, adaChief, writer := mk("Ada"), mk("Ada Chief"), mk("Writer")
	members := []*bots.Bot{ada, adaChief, writer}
	cases := []struct {
		body     string
		want     []*bots.Bot
		everyone bool
	}{
		{"@Ada Chief plan the week", []*bots.Bot{adaChief}, false},
		{"@ada and @writer, hello", []*bots.Bot{ada, writer}, false},
		{"mail ada@acme.test about it", nil, false},
		{"write to team@ada.test", nil, false},
		{"@Writers is not a bot", nil, false},
		{"@everyone stand up", nil, true},
		{"@everyoneelse", nil, false},
	}
	for _, c := range cases {
		got, every := Mentions(c.body, members)
		if every != c.everyone || len(got) != len(c.want) {
			t.Fatalf("%q: got %d bots, everyone %t", c.body, len(got), every)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("%q: bot %d is %s", c.body, i, got[i].Name)
			}
		}
	}
}
