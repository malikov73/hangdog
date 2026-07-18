package watch

import (
	"slices"
	"testing"
	"time"
)

func TestInjectFlags(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "bare go test",
			in:   []string{"go", "test", "./..."},
			want: []string{"go", "test", "-json", "-timeout", "0", "./..."},
		},
		{
			name: "already has -json",
			in:   []string{"go", "test", "-json", "./..."},
			want: []string{"go", "test", "-timeout", "0", "-json", "./..."},
		},
		{
			name: "user set -timeout is respected",
			in:   []string{"go", "test", "-timeout", "5m", "./..."},
			want: []string{"go", "test", "-json", "-timeout", "5m", "./..."},
		},
		{
			name: "user set -timeout=form is respected",
			in:   []string{"go", "test", "-timeout=30s", "./..."},
			want: []string{"go", "test", "-json", "-timeout=30s", "./..."},
		},
		{
			name: "nothing to inject",
			in:   []string{"go", "test", "-json", "-timeout", "1m", "./..."},
			want: []string{"go", "test", "-json", "-timeout", "1m", "./..."},
		},
		{
			name: "flags preserved after packages",
			in:   []string{"go", "test", "./...", "-race", "-count=1"},
			want: []string{"go", "test", "-json", "-timeout", "0", "./...", "-race", "-count=1"},
		},
		{
			name: "not a go test command is untouched",
			in:   []string{"echo", "hello"},
			want: []string{"echo", "hello"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := InjectFlags(tt.in)
			if !slices.Equal(got, tt.want) {
				t.Errorf("InjectFlags(%v)\n got = %v\nwant = %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestTriggerReason(t *testing.T) {
	const idle = 2 * time.Second
	const budget = 10 * time.Second
	tests := []struct {
		name       string
		cfg        Config
		idle       time.Duration
		elapsed    time.Duration
		anyRunning bool
		want       string
	}{
		{"idle fires while a test is in flight", Config{Idle: idle}, 3 * time.Second, 3 * time.Second, true, "idle 2s"},
		{"idle suppressed with no test in flight", Config{Idle: idle}, 3 * time.Second, 3 * time.Second, false, ""},
		{"budget fires with no test in flight", Config{Budget: budget}, 0, 11 * time.Second, false, "budget 10s"},
		{"budget fires regardless of the idle gate", Config{Budget: budget}, 0, 11 * time.Second, true, "budget 10s"},
		{"idle takes precedence when both would fire", Config{Idle: idle, Budget: budget}, 3 * time.Second, 11 * time.Second, true, "idle 2s"},
		{"budget wins when idle is gated out", Config{Idle: idle, Budget: budget}, 3 * time.Second, 11 * time.Second, false, "budget 10s"},
		{"idle disabled", Config{Idle: 0, Budget: budget}, 100 * time.Second, 5 * time.Second, true, ""},
		{"budget disabled and under idle", Config{Idle: idle, Budget: 0}, 1 * time.Second, 100 * time.Second, true, ""},
		{"neither configured", Config{}, 100 * time.Second, 100 * time.Second, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := triggerReason(tt.cfg, tt.idle, tt.elapsed, tt.anyRunning); got != tt.want {
				t.Errorf("triggerReason(%+v, %s, %s, anyRunning=%v) = %q, want %q",
					tt.cfg, tt.idle, tt.elapsed, tt.anyRunning, got, tt.want)
			}
		})
	}
}
