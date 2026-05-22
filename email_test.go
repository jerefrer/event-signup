package main

import "testing"

func TestFormatFrom(t *testing.T) {
	tests := []struct {
		name string
		addr string
		from string
		want string
	}{
		{"no name", "no-reply@chanteloube.fr", "", "no-reply@chanteloube.fr"},
		{"ascii name", "no-reply@chanteloube.fr", "Chanteloube", `"Chanteloube" <no-reply@chanteloube.fr>`},
		{"non-ascii name encoded", "no-reply@chanteloube.fr", "Évènements", "=?utf-8?q?=C3=89v=C3=A8nements?= <no-reply@chanteloube.fr>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatFrom(tt.addr, tt.from); got != tt.want {
				t.Errorf("formatFrom(%q, %q) = %q, want %q", tt.addr, tt.from, got, tt.want)
			}
		})
	}
}
