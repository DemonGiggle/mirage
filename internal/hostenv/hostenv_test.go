package hostenv

import "testing"

func TestDetect(t *testing.T) {
	tests := []struct {
		name string
		euid int
		want Kind
	}{
		{name: "root", euid: 0, want: Root},
		{name: "ordinary user", euid: 1000, want: Rootless},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Detect(tc.euid); got != tc.want {
				t.Fatalf("Detect(%d) = %q, want %q", tc.euid, got, tc.want)
			}
		})
	}
}
