package fpl

import "testing"

// Positions, so pairing has something to match on. 1 GKP, 2 DEF, 3 MID, 4 FWD.
var testTypes = map[int]int{
	1: 1, 2: 1, // keepers
	10: 2, 11: 2, 12: 2, // defenders
	20: 3, 21: 3, 22: 3, // midfielders
	30: 4, 31: 4, // forwards
}

// tf builds one raw row. The API hands these back newest first, so the
// fixtures below are written newest first too.
func tf(event, in, out int) Transfer {
	return Transfer{Event: event, ElementIn: in, ElementOut: out}
}

func pairs(got []TransferOut) [][2]int {
	p := make([][2]int, len(got))
	for i, t := range got {
		p[i] = [2]int{t.Out, t.In}
	}
	return p
}

func equal(a, b [][2]int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestNetTransfers(t *testing.T) {
	cases := []struct {
		name string
		raw  []Transfer // newest first, as the API returns them
		want [][2]int   // {out, in}
	}{{
		name: "plain gameweek passes through untouched",
		raw:  []Transfer{tf(3, 21, 20)},
		want: [][2]int{{20, 21}},
	}, {
		name: "a round trip cancels to nothing",
		// oldest: 20 out for 21; newest: 21 out for 20 again.
		raw:  []Transfer{tf(3, 20, 21), tf(3, 21, 20)},
		want: [][2]int{},
	}, {
		name: "a chain collapses to its endpoints",
		// oldest 20->21, then 21->22: the manager ends on 22, having started
		// on 20, and 21 never belonged to the squad at the deadline.
		raw:  []Transfer{tf(3, 22, 21), tf(3, 21, 20)},
		want: [][2]int{{20, 22}},
	}, {
		name: "other gameweeks are not counted",
		raw:  []Transfer{tf(3, 21, 20), tf(2, 31, 30)},
		want: [][2]int{{20, 21}},
	}, {
		name: "survivors pair by position, not by order",
		// A keeper and a forward move. Paired by arrival order alone the
		// keeper would be shown replacing the forward.
		raw:  []Transfer{tf(3, 31, 30), tf(3, 2, 1)},
		want: [][2]int{{1, 2}, {30, 31}},
	}, {
		name: "wildcard churn reduces to the real change",
		// Newest first: the net effect is 10 -> 12 and 20 -> 22, with a
		// keeper swapped out and back, and 11 bought then sold again.
		raw: []Transfer{
			tf(3, 22, 21), // 21 -> 22
			tf(3, 21, 20), // 20 -> 21
			tf(3, 1, 2),   // keeper back
			tf(3, 2, 1),   // keeper away
			tf(3, 12, 11), // 11 -> 12
			tf(3, 11, 10), // 10 -> 11
		},
		want: [][2]int{{10, 12}, {20, 22}},
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := pairs(netTransfers(c.raw, 3, testTypes))
			if !equal(got, c.want) {
				t.Errorf("netTransfers() = %v, want %v", got, c.want)
			}
		})
	}
}

// A squad keeps its shape across a gameweek, so the two lists must come out
// the same length: every incoming player replaced someone.
func TestNetTransfersPairsEveryone(t *testing.T) {
	raw := []Transfer{tf(3, 22, 21), tf(3, 12, 11), tf(3, 21, 20), tf(3, 11, 10)}
	for _, got := range netTransfers(raw, 3, testTypes) {
		if got.Out == 0 {
			t.Errorf("transfer in %d was left without the player it replaced", got.In)
		}
	}
}
