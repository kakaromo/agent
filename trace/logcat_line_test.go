package trace

import "testing"

func TestParseLogcatLine(t *testing.T) {
	ok := []struct {
		in      string
		tag     string
		pid     int
		msgHead string
	}{
		{"1204575.374   871   882 E AndroidRuntime: FATAL EXCEPTION: binder:871_2",
			"AndroidRuntime", 871, "FATAL EXCEPTION"},
		// ⚠ 태그 안에 콜론이 있다 — 첫 콜론으로 자르면 "Moneta" 로 잘린다.
		{"1225523.478  2992 30513 I Moneta::TradingUtil: something happened",
			"Moneta::TradingUtil", 2992, "something"},
		// ⚠ 메시지 안에도 콜론이 있다 — 마지막 콜론으로 자르면 태그가 통째로 틀린다.
		{"1227929.694  1421  1421 D io_stats: !@ Read_top(KB): bmoe:top(27317) 2973460",
			"io_stats", 1421, "!@ Read_top(KB):"},
		// 대괄호/플러스가 든 태그
		{"1227011.403 25100 25167 I [+0900]oneconnect[1.8.47][CORE]: DataConvert",
			"[+0900]oneconnect[1.8.47][CORE]", 25100, "DataConvert"},
		// epoch 형식 (자릿수가 길 뿐 같은 실수)
		{"1756272146.408   871   882 I Foo: bar", "Foo", 871, "bar"},
		// 태그에 공백이 든 실제 사례
		{"1229459.295 17194 17210 I SSS@search user 0: (GeoDbDownloadWorker) x",
			"SSS@search user 0", 17194, "(GeoDbDownloadWorker)"},
	}
	for _, tc := range ok {
		l, got := ParseLogcatLine(tc.in)
		if !got {
			t.Errorf("파싱 실패해선 안 되는 줄: %q", tc.in)
			continue
		}
		if l.Tag != tc.tag {
			t.Errorf("tag = %q, 기대 %q\n  줄: %s", l.Tag, tc.tag, tc.in)
		}
		if l.PID != tc.pid {
			t.Errorf("pid = %d, 기대 %d", l.PID, tc.pid)
		}
		if len(tc.msgHead) > 0 && len(l.Message) >= len(tc.msgHead) &&
			l.Message[:len(tc.msgHead)] != tc.msgHead {
			t.Errorf("message 앞부분 = %q, 기대 %q", l.Message[:len(tc.msgHead)], tc.msgHead)
		}
	}

	// ⚠ 형식이 아닌 줄은 반드시 false 여야 한다. 조용히 0 값을 채우면 그 줄이
	// "시각 0초의 이벤트" 로 둔갑해 집계를 오염시킨다.
	bad := []string{
		"--------- beginning of crash",
		"1204575.374   871   882 E AndroidRuntime: \tat com.foo.Bar(X.java:1)"[:20] + "", // 잘린 줄
		"\tat java.lang.reflect.Method.invoke(Native Method)",
		"",
		"   ",
		"not a log line at all",
		"1204575.374 no pid here",
	}
	for _, s := range bad {
		if l, got := ParseLogcatLine(s); got {
			t.Errorf("파싱되면 안 되는 줄이 통과됐다: %q → %+v", s, l)
		}
	}
}

func TestParseLogcatLine_TimeAndLevel(t *testing.T) {
	l, ok := ParseLogcatLine("1204575.374   871   882 W libc    : Access denied")
	if !ok {
		t.Fatal("파싱 실패")
	}
	if l.TimeSec != 1204575.374 {
		t.Errorf("TimeSec = %v", l.TimeSec)
	}
	if l.Level != "W" {
		t.Errorf("Level = %q", l.Level)
	}
	if l.Tag != "libc" {
		t.Errorf("Tag = %q (뒤 공백이 안 잘렸다)", l.Tag)
	}
	if l.TID != 882 {
		t.Errorf("TID = %d", l.TID)
	}
}
