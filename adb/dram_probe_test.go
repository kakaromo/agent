package adb

import "testing"

func TestParseDramProbe(t *testing.T) {
	cases := []struct {
		out      string
		ok       bool
		reasonIn string
	}{
		{"event=1 inst=1 write=1\n", true, ""},
		// user 빌드: 보이지만 instance 를 만들 수 없다
		{"event=1 inst=1 write=0\n", false, "root 가 필요"},
		{"event=1 inst=0 write=0\n", false, "instance"},
		{"event=0 inst=1 write=1\n", false, "s5e9945 에는 DRAM"},
		{"/system/bin/sh: Permission denied\n", false, "해석하지 못했어요"},
	}
	for _, c := range cases {
		ok, reason := parseDramProbe(c.out, "s5e9945")
		if ok != c.ok {
			t.Errorf("%q: ok=%v want %v", c.out, ok, c.ok)
		}
		if c.reasonIn != "" && !contains(reason, c.reasonIn) {
			t.Errorf("%q: reason=%q, %q 를 포함해야 한다", c.out, reason, c.reasonIn)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
