package watch

import (
	"slices"
	"testing"
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
