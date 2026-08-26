package trace

import (
	"strconv"
	"strings"
)

// LogcatLine — logcat 한 줄을 쪼갠 것.
//
// 지원 형식 (`-v` 옵션에 따라 앞의 시각 표현만 다르다):
//
//	monotonic: 1204575.374   871   882 E AndroidRuntime: FATAL EXCEPTION
//	epoch:     1756272146.408 871   882 E AndroidRuntime: FATAL EXCEPTION
//
// 둘 다 "초.밀리초" 실수라 같은 코드로 읽힌다. 어느 축인지는 수집할 때 정하고
// (LogcatFormat), 여기서는 숫자만 그대로 담는다 — 축 변환은 이 층의 일이 아니다.
type LogcatLine struct {
	// TimeSec — 앞머리 타임스탬프. 축(MONOTONIC/EPOCH)은 수집 설정이 정한다.
	TimeSec float64
	PID     int
	TID     int
	Level   string // V D I W E F
	Tag     string
	Message string
	// Raw — 원문. 탐색 결과에 근거로 같이 보여주기 위해 남긴다.
	// (사람이 "이게 진짜 우리가 찾는 줄인가" 를 판단하려면 원문이 필요하다.)
	Raw string
}

// ParseLogcatLine — 한 줄을 쪼갠다. 형식이 아니면 ok=false.
//
// ⚠ 계속 줄(stack trace 처럼 앞에 공백이 붙는 줄)과 `--------- beginning of ...`
// 같은 구분선은 형식이 아니므로 false 를 준다. 조용히 0 값을 채우면 그 줄이
// **시각 0초의 이벤트**로 둔갑해 집계를 오염시킨다.
func ParseLogcatLine(s string) (LogcatLine, bool) {
	var l LogcatLine
	l.Raw = s

	// 1) 타임스탬프
	rest := strings.TrimLeft(s, " ")
	if rest == "" || rest[0] < '0' || rest[0] > '9' {
		return l, false
	}
	sp := strings.IndexByte(rest, ' ')
	if sp < 0 {
		return l, false
	}
	ts, err := strconv.ParseFloat(rest[:sp], 64)
	if err != nil {
		return l, false
	}
	l.TimeSec = ts
	rest = rest[sp:]

	// 2) pid, tid — 자리수 맞춤 때문에 공백이 여러 개 들어간다.
	pid, rest, ok := nextInt(rest)
	if !ok {
		return l, false
	}
	tid, rest, ok := nextInt(rest)
	if !ok {
		return l, false
	}
	l.PID, l.TID = pid, tid

	// 3) level — 한 글자
	rest = strings.TrimLeft(rest, " ")
	if rest == "" {
		return l, false
	}
	lv := rest[0]
	if !strings.ContainsRune("VDIWEFS", rune(lv)) {
		return l, false
	}
	if len(rest) < 2 || rest[1] != ' ' {
		return l, false
	}
	l.Level = string(lv)
	rest = rest[2:]

	// 4) tag: message — 태그는 콜론 앞까지. 태그 안에 콜론이 든 경우
	//    (예: `Moneta::TradingUtil`, `[+0900]oneconnect[..][CORE]`) 가 있어서
	//    **": " (콜론+공백) 을 경계**로 본다. 마지막 콜론으로 자르면 메시지 안의
	//    콜론에 끌려가고, 첫 콜론으로 자르면 `Moneta::X` 가 잘린다.
	if i := strings.Index(rest, ": "); i >= 0 {
		l.Tag = strings.TrimSpace(rest[:i])
		l.Message = rest[i+2:]
	} else {
		// "tag:" 로 끝나고 메시지가 빈 경우
		t := strings.TrimSpace(rest)
		if !strings.HasSuffix(t, ":") {
			return l, false
		}
		l.Tag = strings.TrimSuffix(t, ":")
	}
	if l.Tag == "" {
		return l, false
	}
	return l, true
}

// nextInt — 앞쪽 공백을 건너뛰고 정수 하나를 읽는다.
func nextInt(s string) (int, string, bool) {
	s = strings.TrimLeft(s, " ")
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, s, false
	}
	n, err := strconv.Atoi(s[:i])
	if err != nil {
		return 0, s, false
	}
	return n, s[i:], true
}
