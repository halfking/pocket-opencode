package usersetting

import "testing"

func TestDecidePut(t *testing.T) {
	cases := []struct {
		name           string
		client, server int64
		want           Decision
	}{
		{name: "client newer applies", client: 20, server: 10, want: DecisionApply},
		{name: "server newer keeps", client: 10, server: 20, want: DecisionKeep},
		{name: "equal keeps server", client: 7, server: 7, want: DecisionKeep},
		{name: "missing server applies", client: 5, server: 0, want: DecisionApply},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DecidePut(tc.client, tc.server)
			if got != tc.want {
				t.Fatalf("DecidePut(%d,%d)=%v want %v", tc.client, tc.server, got, tc.want)
			}
		})
	}
}
