package controller

import "testing"

func TestParseSubscriptionUserInfo(t *testing.T) {
	cases := []struct {
		header string
		used   int64
		total  int64
		hasExp bool
	}{
		{"upload=1024; download=2048; total=107374182400; expire=1798761600", 3072, 107374182400, true},
		{"upload=0;download=0;total=0;expire=0", 0, 0, false},
		{"", 0, 0, false},
		{"total=5368709120", 0, 5368709120, false},
	}
	for _, c := range cases {
		got := parseSubscriptionUserInfo(c.header)
		if got.Used != c.used || got.Total != c.total {
			t.Fatalf("header %q: used=%d total=%d, want used=%d total=%d", c.header, got.Used, got.Total, c.used, c.total)
		}
		if (got.ExpireAt != nil) != c.hasExp {
			t.Fatalf("header %q: expire presence=%v, want %v", c.header, got.ExpireAt != nil, c.hasExp)
		}
	}
}
