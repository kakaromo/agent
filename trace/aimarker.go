package trace

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ftrace `trace_marker` 에서 on-device AI 지표를 뽑는다.
//
// **왜 logcat 말고 여기도 필요한가.** logcat 경로에는 못 넘는 벽이 있다:
//   - 런타임이 **stderr 로 뱉으면 logcat 에 아예 안 남는다** (llama.cpp 의
//     `llama_print_timings()` 가 그렇다). 패턴을 아무리 고쳐도 소용없다.
//   - logcat 은 monotonic 축이라 IO(BOOTTIME)와 겹치려면 변환이 필요하다.
//
// trace_marker 는 둘 다 해결한다 — 파일 write 라 stderr 제약이 없고, IO 트레이스와
// **같은 버퍼·같은 clock(boot)** 이라 축 변환이 필요 없다.
//
// **Android atrace 표준 포맷** (실기기 S25 실측, 2026-09-01):
//
//	B|<pid>|<name>              구간 시작
//	E|<pid>                     구간 끝
//	C|<pid>|<name>|<value>      카운터 — **숫자값이 이미 분리돼 있다**
//	I|<pid>|<name>              순간 이벤트
//
// ⚠⚠ **`C|` 가 logcat 대비 가장 큰 이점이다.** logcat 은 자유 문구라 사용자가
// 정규식과 캡처 그룹을 직접 만들어야 하는데(그래서 검증·진단을 그렇게 두껍게 짰다),
// `C|` 는 이름과 값이 파이프로 나뉘어 있어 **캡처 그룹 자체가 필요 없다.**
// 사용자는 "어떤 카운터 이름을 볼지" 만 고르면 된다.
//
// 실측 참고: 설정앱 실행 3초에 `C|` 371건 / 카운터 29종이 잡혔고,
// `Heap size (KB)` 는 92개 시계열이었다 — TPOT 처럼 반복되는 지표와 같은 구조다.
//
// ⚠ 전제: **런타임이 실제로 찍어야 한다.** logcat 과 같은 블랙박스 문제다
// (Java `Trace.setCounter()`, NDK `ATrace_setCounter()`). 그래서 탐색기가 따로 있다.

// MarkerPatterns — 사용자가 "무엇을 볼지" 지정하는 값.
//
// ⚠ logcat 과 달리 **정규식이 선택**이다. `C|` 는 이름이 이미 분리돼 있어서 이름만
// 적으면 되고, 부분 일치가 필요할 때만 regex 를 쓴다. 둘 다 비면 저장 시 막는다.
type MarkerPatterns struct {
	// Counters — `C|pid|<name>|<value>` 에서 값을 뽑을 카운터.
	Counters []MarkerCounter `json:"counters,omitempty"`
	// Sections — `B|pid|<name>` ~ `E|pid` 구간. 시작 시각을 mark 로 쓴다.
	Sections []MarkerSection `json:"sections,omitempty"`
}

// MarkerCounter — 카운터 하나. 값이 반복되면 시계열이 된다 (TPOT 등).
type MarkerCounter struct {
	// Key — 결과에서 쓸 이름 (예: "ttft", "tpot").
	Key string `json:"key"`
	// Name — 기기가 찍는 카운터 이름. 정확히 일치하면 이것만으로 충분하다.
	Name string `json:"name,omitempty"`
	// Regex — 이름이 버전마다 다를 때. Name 이 비어 있으면 이쪽을 쓴다.
	// ⚠ 캡처 그룹은 **필요 없다** — 값은 `C|` 의 마지막 필드에서 온다.
	Regex string `json:"regex,omitempty"`
	Unit  string `json:"unit,omitempty"`
}

// MarkerSection — 구간 이름. prefill/decode 처럼 단계를 감싸는 데 쓴다.
type MarkerSection struct {
	Key   string `json:"key"`
	Name  string `json:"name,omitempty"`
	Regex string `json:"regex,omitempty"`
}

// markerLine — trace_marker 한 줄을 쪼갠 것.
type markerLine struct {
	TimeSec float64
	Kind    byte // 'B' 'E' 'C' 'I'
	PID     int
	Name    string
	Value   float64
	HasVal  bool
}

// parseMarkerLine — ftrace 줄에서 `tracing_mark_write:` payload 를 읽는다.
//
// ⚠ ftrace 헤더(comm/CPU/시각) 파싱은 하지 않고 **필요한 것만** 뽑는다. 이 층은
// logcat_line.go 처럼 "형식이 아니면 false" 를 지켜서, 형식 아닌 줄이 시각 0의
// 이벤트로 둔갑해 집계를 오염시키는 것을 막는다.
func parseMarkerLine(s string) (markerLine, bool) {
	var m markerLine
	i := strings.Index(s, markerEventTag)
	if i < 0 {
		return m, false
	}
	// 시각 — `... <ts>: tracing_mark_write: ...` 에서 앞쪽 마지막 ": " 직전 숫자.
	head := s[:i]
	colon := strings.LastIndexByte(head, ':')
	if colon <= 0 {
		return m, false
	}
	tsStr := head[:colon]
	if sp := strings.LastIndexByte(tsStr, ' '); sp >= 0 {
		tsStr = tsStr[sp+1:]
	}
	ts, err := strconv.ParseFloat(strings.TrimSpace(tsStr), 64)
	if err != nil {
		return m, false
	}
	m.TimeSec = ts

	payload := strings.TrimSpace(s[i+len(markerEventTag):])
	if payload == "" {
		return m, false
	}
	m.Kind = payload[0]
	switch m.Kind {
	case 'B', 'E', 'C', 'I':
	default:
		return m, false
	}
	parts := strings.Split(payload, "|")
	if len(parts) < 2 {
		return m, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return m, false
	}
	m.PID = pid
	if len(parts) >= 3 {
		m.Name = strings.TrimSpace(parts[2])
	}
	// ⚠ 이름에 `|` 가 들어가면 필드가 밀린다. C 는 **마지막 필드가 값**이므로
	// 그 규칙으로 되찾는다 (이름 안의 파이프는 이름으로 되붙인다).
	if m.Kind == 'C' && len(parts) >= 4 {
		last := strings.TrimSpace(parts[len(parts)-1])
		if v, err := strconv.ParseFloat(last, 64); err == nil {
			m.Value, m.HasVal = v, true
			m.Name = strings.TrimSpace(strings.Join(parts[2:len(parts)-1], "|"))
		}
	}
	return m, true
}

const markerEventTag = "tracing_mark_write:"

// 안전장치 — logcat 파서와 같은 이유(정규식·파일 크기가 외부 입력).
const (
	maxMarkerLines         = 5_000_000
	markerParseTimeout     = 60 * time.Second
	markerDeadlineCheckPer = 1000
)

// ParseMarkerPatterns — trace.log 에서 지정한 카운터/구간을 뽑는다.
//
// 결과 타입은 **logcat 과 같은 LogcatParseResult** 다. 화면·진단·요약 로직을 그대로
// 재사용하기 위해서다 (지표 성격이 같으므로 굳이 다른 모양을 만들 이유가 없다).
func ParseMarkerPatterns(r io.Reader, p *MarkerPatterns) (LogcatParseResult, error) {
	res := LogcatParseResult{Series: map[string]SeriesResult{}}
	if p == nil || (len(p.Counters) == 0 && len(p.Sections) == 0) {
		return res, fmt.Errorf("패턴이 비어 있다 (counters/sections 중 하나는 있어야 한다)")
	}

	type cmp struct {
		key, unit string
		name      string
		re        *regexp.Regexp
		section   bool
	}
	var cs []cmp
	seen := map[string]bool{}
	add := func(key, name, expr, unit string, section bool) error {
		if key == "" {
			return fmt.Errorf("key 가 비어 있다")
		}
		// ⚠ 키 중복은 통계가 조용히 틀어지는 원인이라 여기서 막는다
		// (logcat 파서와 같은 이유 — stat map 에서 한쪽이 덮인다).
		if seen[key] {
			return fmt.Errorf("패턴 key 중복: %q — counters/sections 를 통틀어 유일해야 한다", key)
		}
		seen[key] = true
		if name == "" && expr == "" {
			return fmt.Errorf("%q: name 또는 regex 중 하나는 있어야 한다", key)
		}
		var re *regexp.Regexp
		if expr != "" {
			var err error
			if re, err = regexp.Compile(expr); err != nil {
				return fmt.Errorf("%q: regex: %w", key, err)
			}
		}
		cs = append(cs, cmp{key: key, unit: unit, name: name, re: re, section: section})
		return nil
	}
	for _, c := range p.Counters {
		if err := add(c.Key, c.Name, c.Regex, c.Unit, false); err != nil {
			return res, err
		}
	}
	for _, s := range p.Sections {
		if err := add(s.Key, s.Name, s.Regex, "", true); err != nil {
			return res, err
		}
	}

	stat := map[string]*PatternStat{}
	for _, c := range cs {
		kind := "series"
		if c.section {
			kind = "mark"
		}
		stat[c.key] = &PatternStat{Key: c.key, Kind: kind}
	}
	points := map[string][]SeriesPoint{}
	nameSeen := map[string]bool{}
	deadline := time.Now().Add(markerParseTimeout)

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		res.TotalLines++
		if res.TotalLines > maxMarkerLines {
			res.Diagnosis = append(res.Diagnosis, fmt.Sprintf(
				"줄 수 상한(%d)에 걸려 이후를 읽지 않았다 — 결과가 일부만 반영됐다.", maxMarkerLines))
			break
		}
		if res.TotalLines%markerDeadlineCheckPer == 0 && time.Now().After(deadline) {
			res.Diagnosis = append(res.Diagnosis, fmt.Sprintf(
				"파싱 시간 상한(%s)을 넘겨 중단했다 — 결과가 일부만 반영됐다.", markerParseTimeout))
			break
		}

		ml, ok := parseMarkerLine(sc.Text())
		if !ok {
			continue
		}
		res.ParsedLines++
		if ml.Name != "" {
			nameSeen[ml.Name] = true
		}

		for _, c := range cs {
			// 카운터는 C|, 구간은 B| 만 본다.
			if c.section && ml.Kind != 'B' {
				continue
			}
			if !c.section && ml.Kind != 'C' {
				continue
			}
			if !markerNameMatches(c.name, c.re, ml.Name) {
				continue
			}
			st := stat[c.key]
			st.Hits++
			if c.section {
				res.Marks = append(res.Marks, MarkHit{Key: c.key, TimeSec: ml.TimeSec})
				continue
			}
			if !ml.HasVal {
				// ⚠ 이름은 맞았는데 값이 숫자가 아니다 — "패턴이 틀렸다" 가 아니라
				// "이 카운터는 값을 안 싣는다" 이므로 안내가 갈려야 한다.
				st.ParseFailures++
				continue
			}
			points[c.key] = append(points[c.key], SeriesPoint{TimeSec: ml.TimeSec, Value: ml.Value})
		}
	}
	if err := sc.Err(); err != nil {
		return res, fmt.Errorf("read: %w", err)
	}

	for _, c := range cs {
		if c.section {
			continue
		}
		if pts := points[c.key]; len(pts) > 0 {
			res.Series[c.key] = summarize(c.key, c.unit, pts)
		}
	}
	for n := range nameSeen {
		res.MatchedTags = append(res.MatchedTags, n)
	}
	sort.Strings(res.MatchedTags)
	sort.Slice(res.Marks, func(i, j int) bool { return res.Marks[i].TimeSec < res.Marks[j].TimeSec })

	for _, c := range cs {
		st := stat[c.key]
		res.Stats = append(res.Stats, *st)
		res.TotalHits += st.Hits
		if st.Hits == 0 {
			res.MissingKeys = append(res.MissingKeys, c.key)
		}
	}
	res.Partial = res.TotalHits > 0 && len(res.MissingKeys) > 0
	res.Diagnosis = append(res.Diagnosis, diagnoseMarkerParse(res, len(nameSeen))...)
	return res, nil
}

// markerNameMatches — 이름 매칭. name 이 있으면 정확 일치, 없으면 regex.
func markerNameMatches(name string, re *regexp.Regexp, got string) bool {
	if name != "" {
		return name == got
	}
	if re != nil {
		return re.MatchString(got)
	}
	return false
}

// diagnoseMarkerParse — 0건일 때 **왜 그런지**를 가른다.
//
// ⚠ logcat 과 원인이 다르다. 여기서 0건이면 "런타임이 stderr 로 뱉는다" 가 아니라
// **atrace 카테고리가 안 켜졌거나 앱이 marker 를 안 찍는다** 쪽이다. 안내가 틀리면
// 사용자가 엉뚱한 곳을 고치며 시간을 쓴다.
func diagnoseMarkerParse(res LogcatParseResult, distinctNames int) []string {
	var out []string
	switch {
	case res.TotalLines == 0:
		out = append(out, "로그가 비어 있다 — trace 수집 자체를 확인할 것.")
	case res.ParsedLines == 0:
		out = append(out, "trace_marker 줄이 하나도 없다. ftrace 버퍼에 marker 가 안 들어간 것이다 — "+
			"수집 중 `tracing_on=1` 이었는지, 앱 마커가 필요하면 atrace 카테고리를 켰는지 확인할 것.")
	case res.TotalHits == 0:
		out = append(out, fmt.Sprintf(
			"marker 는 %d줄 있는데(카운터/구간 이름 %d종) 지정한 패턴에 하나도 안 걸렸다. "+
				"이름이 다를 수 있으니 탐색으로 실제 이름을 먼저 확인할 것.", res.ParsedLines, distinctNames))
	}
	if res.Partial {
		out = append(out, fmt.Sprintf(
			"⚠ 일부만 걸렸다 — 안 걸린 키: %s. 나머지 수치만 보고 정상이라 판단하면 안 된다.",
			strings.Join(res.MissingKeys, ", ")))
	}
	for _, st := range res.Stats {
		if st.Hits > 0 && st.ParseFailures == st.Hits {
			out = append(out, fmt.Sprintf(
				"%q 는 이름이 맞았지만 값이 숫자가 아니다 — 이 카운터는 값을 안 싣는 것으로 보인다 "+
					"(구간이라면 sections 로 옮길 것).", st.Key))
		}
	}
	return out
}
