package main

import "testing"

func TestParseCombos(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{
			name: "direct-style",
			in:   "То есть **3 кофе и 2 чая**:\nТо есть **5 какао**:\n(3, 2, 0) и (0, 0, 5)",
			want: 2,
		},
		{
			name: "strict",
			in:   "кофе 3, чай 2, какао 0\nкофе 0, чай 0, какао 5",
			want: 2,
		},
		{
			name: "latex-tuple",
			in:   "\\[(3,\\ 2,\\ 0) \\quad \\text{и} \\quad (0,\\ 0,\\ 5)\\]",
			want: 2,
		},
		{
			name: "real-direct",
			in:   "1. **3 кофе, 2 чая, 0 какао**\n2. **0 кофе, 0 чая, 5 какао**\nОтвет: (3;2;0),(0;0;5)",
			want: 2,
		},
		{
			name: "plain-bullet",
			in:   "- кофе 0, чай 0, какао 5\n- кофе 3, чай 2, какао 0",
			want: 2,
		},
		{
			name: "full-direct-json",
			in: "Решение даёт такие неотрицательные целые варианты:\n\n" +
				"1. **3 кофе, 2 чая, 0 какао**  \n" +
				"   \\(3\\cdot200 + 2\\cdot150 = 600+300=900\\)\n\n" +
				"2. **0 кофе, 0 чая, 5 какао**  \n" +
				"   \\(5\\cdot180=900\\)\n\n" +
				"Ответ:  \n\\[\n(3;2;0),\\quad (0;0;5)\n\\]",
			want: 2,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseCombos(c.in)
			if len(got) != c.want {
				t.Fatalf("parseCombos(%q) = %d (%v), want %d", c.in, len(got), got, c.want)
			}
		})
	}
}

func TestValidCombo(t *testing.T) {
	valid := []string{"кофе 0, чай 0, какао 5", "кофе 3, чай 2, какао 0"}
	invalid := []string{"кофе 1, чай 3, какао 2", "кофе 2, чай 0, какао 0", "кофе 5, чай 0, какао 0"}
	for _, v := range valid {
		if !validCombo(v) {
			t.Errorf("validCombo(%q) = false, want true", v)
		}
	}
	for _, v := range invalid {
		if validCombo(v) {
			t.Errorf("validCombo(%q) = true, want false", v)
		}
	}
}
