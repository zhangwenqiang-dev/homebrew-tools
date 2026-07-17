package connectmac

import "testing"

func TestFormatBeijingDisplayTime(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "UTC",
			input: "2026-07-16T08:03:24Z",
			want:  "2026-07-16 16:03:24（北京时间）",
		},
		{
			name:  "crosses midnight",
			input: "2026-07-16T18:30:00Z",
			want:  "2026-07-17 02:30:00（北京时间）",
		},
		{
			name:  "already Beijing time",
			input: "2026-07-16T16:00:00+08:00",
			want:  "2026-07-16 16:00:00（北京时间）",
		},
		{
			name:  "empty",
			input: "",
			want:  "",
		},
		{
			name:  "invalid",
			input: "not-a-time",
			want:  "not-a-time",
		},
		{
			name:  "whitespace-only invalid",
			input: "   ",
			want:  "   ",
		},
		{
			name:  "whitespace-wrapped invalid",
			input: "  not-a-time  ",
			want:  "  not-a-time  ",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := formatBeijingDisplayTime(test.input); got != test.want {
				t.Errorf("formatBeijingDisplayTime(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}
