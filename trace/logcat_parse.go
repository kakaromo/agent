package trace

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"time"
)

// 프로파일 패턴으로 logcat 에서 지표를 뽑는다.
//
// mark(시점) 와 series(값) 를 나눠 다룬다:
//
//	9573.594 QnnHtp : prefill begin, 512 tokens          → mark   (시각만)
//	9574.044 Genie  : first token emitted — TTFT 2840 ms → series (2840)
//	9574.068 Genie  : decode 24.1 ms/tok                 → series (24.1, 반복 → 시계열)
//
// ⚠ 이 층의 계약: **0건이면 실패다.** 조용히 빈 결과를 내면 화면엔 "측정 완료" 가
// 뜨는데 값이 없다. 측정 도구에서 가장 나쁜 실패 형태다.

// LogcatPatterns — sqlitedb.AILogPatterns 와 같은 모양.
//
// ⚠ trace 패키지가 storage/sqlitedb 를 import 하지 않으려고 구조를 복제한다.
// (benchmark→trace import 를 인터페이스로 끊는 것과 같은 방침. 하위 계층이
// 상위 저장소를 알면 의존이 뒤집힌다.)
type LogcatPatterns struct {
	Tags        []string       `json:"tags,omitempty"`
	MinPriority string         `json:"minPriority,omitempty"`
	Marks       []LogcatMark   `json:"marks,omitempty"`
	Series      []LogcatSeries `json:"series,omitempty"`
}

type LogcatMark struct {
	Key   string `json:"key"`
	Regex string `json:"regex"`
}

type LogcatSeries struct {
	Key   string `json:"key"`
	Regex string `json:"regex"`
	Unit  string `json:"unit,omitempty"`
}

// MarkHit — 패턴이 걸린 시점.
type MarkHit struct {
	Key     string  `json:"key"`
	TimeSec float64 `json:"timeSec"`
	Tag     string  `json:"tag"`
	Raw     string  `json:"raw"`
}

// SeriesPoint — 뽑아낸 값 하나.
type SeriesPoint struct {
	TimeSec float64 `json:"timeSec"`
	Value   float64 `json:"value"`
	Raw     string  `json:"raw"`
}

// SeriesResult — 한 키의 시계열 + 요약.
type SeriesResult struct {
	Key    string        `json:"key"`
	Unit   string        `json:"unit,omitempty"`
	Points []SeriesPoint `json:"points"`
	// 요약 — 토큰마다 나오는 값(TPOT)은 분포가 중요하다. 평균만 보면
	// "뒤로 갈수록 느려지는" 현상을 놓친다.
	Count  int     `json:"count"`
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
	Mean   float64 `json:"mean"`
	Median float64 `json:"median"`
	P99    float64 `json:"p99"`
}

// PatternStat — 패턴별 매칭 결과. 진단의 근거다.
type PatternStat struct {
	Key  string `json:"key"`
	Kind string `json:"kind"` // mark | series
	Hits int    `json:"hits"`
	// ParseFailures — 정규식은 맞았는데 캡처 값이 숫자가 아니었던 횟수.
	// ⚠ 이걸 따로 세는 이유: hits>0 인데 값이 안 나오면 "패턴이 안 맞았다" 가
	// 아니라 "캡처 그룹을 잘못 잡았다" 이다. 원인이 달라 안내가 갈려야 한다.
	ParseFailures int `json:"parseFailures"`
}

// LogcatParseResult — 파싱 결과.
type LogcatParseResult struct {
	TotalLines  int      `json:"totalLines"`
	ParsedLines int      `json:"parsedLines"`
	MatchedTags []string `json:"matchedTags"`

	Marks  []MarkHit               `json:"marks"`
	Series map[string]SeriesResult `json:"series"`

	Stats []PatternStat `json:"stats"`
	// TotalHits — 전체 매칭 수. 0 이면 실패다.
	TotalHits int `json:"totalHits"`
	// Partial — 패턴 일부만 맞았다.
	// ⚠ 성공으로 처리하면 화면에 **반쪽 지표가 정상처럼** 뜬다.
	// (TTFT 는 나오는데 TPOT 은 안 나오는 식)
	Partial bool `json:"partial"`
	// MissingKeys — 한 번도 안 걸린 패턴 키.
	MissingKeys []string `json:"missingKeys"`
	Diagnosis   []string `json:"diagnosis"`
}

// 안전장치 — 정규식은 사용자 입력이다.
const (
	// maxParseLines — 줄 수 상한. 링버퍼 폭주나 거대 파일에서 무한정 돌지 않게 한다.
	maxParseLines = 5_000_000
	// parseTimeout — 전체 파싱 시간 상한. catastrophic backtracking 패턴 하나가
	// 파싱을 멈추는 것을 막는다.
	parseTimeout = 60 * time.Second
	// deadlineCheckEvery — 몇 줄마다 시간 상한을 보는가.
	// ⚠ 이 값보다 짧은 파일은 상한 검사를 **한 번도 안 탄다.** 크게 잡으면
	// 짧은 파일에서 상한이 무력해진다 (9999줄 + 느린 정규식 = 무한정).
	deadlineCheckEvery = 1000
)

// ParseLogcatPatternsJSON — 프로파일 JSON 을 읽고 컴파일한다.
func ParseLogcatPatternsJSON(s string) (*LogcatPatterns, error) {
	var p LogcatPatterns
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		return nil, fmt.Errorf("patternsJson: %w", err)
	}
	return &p, nil
}

type compiled struct {
	key, kind, unit string
	re              *regexp.Regexp
}

// ParseLogcat — 로그를 훑어 marks/series 를 뽑는다.
func ParseLogcat(r io.Reader, p *LogcatPatterns) (LogcatParseResult, error) {
	res := LogcatParseResult{Series: map[string]SeriesResult{}}
	if p == nil || (len(p.Marks) == 0 && len(p.Series) == 0) {
		return res, fmt.Errorf("패턴이 비어 있다 (marks/series 중 하나는 있어야 한다)")
	}

	var cs []compiled
	for _, m := range p.Marks {
		re, err := regexp.Compile(m.Regex)
		if err != nil {
			return res, fmt.Errorf("mark %q: regex: %w", m.Key, err)
		}
		cs = append(cs, compiled{key: m.Key, kind: "mark", re: re})
	}
	for _, s := range p.Series {
		re, err := regexp.Compile(s.Regex)
		if err != nil {
			return res, fmt.Errorf("series %q: regex: %w", s.Key, err)
		}
		if re.NumSubexp() < 1 {
			// 값을 뽑을 수 없는데 매칭만 되면 **조용히 빈 시계열**이 된다.
			return res, fmt.Errorf("series %q: 캡처 그룹 () 이 없다 — 값을 뽑을 수 없다", s.Key)
		}
		cs = append(cs, compiled{key: s.Key, kind: "series", unit: s.Unit, re: re})
	}

	// ⚠ 키 중복을 여기서 막는다. stat 이 map[key] 라 mark 와 series 가 키를 공유하면
	// 한쪽이 다른 쪽을 덮어써 **Stats 에 같은 항목이 두 번 실리고 TotalHits 가 부풀려진다**
	// (매칭 2줄인데 hits 4 로 보고되는 식). 저장 경로(ValidatePatternsJSON)는 이미 막지만
	// `POST /logcat/parse` 의 inline patternsJson 은 그 검증을 안 타므로 여기서도 막아야 한다.
	// 조용히 통과시키면 화면의 매칭 통계가 근거 없이 틀린다 — 진단의 근거로 쓰이는 값이다.
	stat := map[string]*PatternStat{}
	for _, c := range cs {
		if _, dup := stat[c.key]; dup {
			return res, fmt.Errorf("패턴 key 중복: %q — mark/series 를 통틀어 유일해야 한다 "+
				"(중복이면 한쪽이 조용히 사라지고 매칭 통계가 틀어진다)", c.key)
		}
		stat[c.key] = &PatternStat{Key: c.key, Kind: c.kind}
	}
	points := map[string][]SeriesPoint{}
	tagSeen := map[string]bool{}
	deadline := time.Now().Add(parseTimeout)

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		res.TotalLines++
		if res.TotalLines > maxParseLines {
			res.Diagnosis = append(res.Diagnosis, fmt.Sprintf(
				"줄 수 상한(%d)에 걸려 이후를 읽지 않았다 — 결과가 일부만 반영됐다.",
				maxParseLines))
			break
		}
		// ⚠ 매 줄 시각을 재면 느리므로 주기적으로만 본다.
		//
		// ⚠⚠ 주기를 1000 으로 낮췄다. 10000 이면 **1만 줄 미만 파일에서 상한이 아예
		// 안 걸린다** — 9999줄짜리 로그에 catastrophic backtracking 정규식 하나면
		// 무한정 돈다. 정규식은 사용자 입력이고, `POST /logcat/parse` 는
		// 사무실 모드(0.0.0.0·인증 없음)에서도 등록되므로 요청 하나로 goroutine 을
		// 붙잡을 수 있다. 짧은 파일일수록 검사가 촘촘해야 하는 쪽이 맞다.
		if res.TotalLines%deadlineCheckEvery == 0 && time.Now().After(deadline) {
			res.Diagnosis = append(res.Diagnosis, fmt.Sprintf(
				"파싱 시간 상한(%s)을 넘겨 중단했다 — 정규식이 과도하게 느릴 수 있다. "+
					"결과가 일부만 반영됐다.", parseTimeout))
			break
		}

		raw := sc.Text()
		l, ok := ParseLogcatLine(raw)
		if !ok {
			continue
		}
		res.ParsedLines++

		for _, c := range cs {
			mm := c.re.FindStringSubmatch(l.Message)
			if mm == nil {
				continue
			}
			st := stat[c.key]
			if c.kind == "mark" {
				st.Hits++
				tagSeen[l.Tag] = true
				res.Marks = append(res.Marks, MarkHit{
					Key: c.key, TimeSec: l.TimeSec, Tag: l.Tag, Raw: raw})
				continue
			}
			// series — 첫 캡처 그룹을 숫자로.
			v, err := strconv.ParseFloat(mm[1], 64)
			if err != nil {
				st.ParseFailures++
				continue
			}
			st.Hits++
			tagSeen[l.Tag] = true
			points[c.key] = append(points[c.key], SeriesPoint{
				TimeSec: l.TimeSec, Value: v, Raw: raw})
		}
	}
	if err := sc.Err(); err != nil {
		return res, fmt.Errorf("read: %w", err)
	}

	for _, c := range cs {
		if c.kind != "series" {
			continue
		}
		res.Series[c.key] = summarize(c.key, c.unit, points[c.key])
	}
	for _, c := range cs {
		st := stat[c.key]
		res.Stats = append(res.Stats, *st)
		res.TotalHits += st.Hits
		if st.Hits == 0 {
			res.MissingKeys = append(res.MissingKeys, c.key)
		}
	}
	sort.Slice(res.Stats, func(i, j int) bool { return res.Stats[i].Key < res.Stats[j].Key })
	sort.Strings(res.MissingKeys)
	for t := range tagSeen {
		res.MatchedTags = append(res.MatchedTags, t)
	}
	sort.Strings(res.MatchedTags)
	sort.Slice(res.Marks, func(i, j int) bool { return res.Marks[i].TimeSec < res.Marks[j].TimeSec })

	res.Partial = res.TotalHits > 0 && len(res.MissingKeys) > 0
	res.Diagnosis = append(res.Diagnosis, diagnoseParse(res, stat)...)
	return res, nil
}

// summarize — 시계열 요약. TPOT 처럼 반복되는 값은 분포가 중요하다.
func summarize(key, unit string, pts []SeriesPoint) SeriesResult {
	r := SeriesResult{Key: key, Unit: unit, Points: pts, Count: len(pts)}
	if len(pts) == 0 {
		return r
	}
	vals := make([]float64, len(pts))
	sum := 0.0
	for i, p := range pts {
		vals[i] = p.Value
		sum += p.Value
	}
	sort.Float64s(vals)
	r.Min, r.Max = vals[0], vals[len(vals)-1]
	r.Mean = sum / float64(len(vals))
	r.Median = percentile(vals, 50)
	r.P99 = percentile(vals, 99)
	return r
}

// percentile — vals 는 정렬돼 있어야 한다. nearest-rank.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted))*p/100 + 0.5)
	if idx < 1 {
		idx = 1
	}
	if idx > len(sorted) {
		idx = len(sorted)
	}
	return sorted[idx-1]
}

// diagnoseParse — 0건/부분매칭의 원인을 구분해 알린다.
//
// ⚠ "줄 자체가 0" 과 "줄은 있는데 매칭 0" 은 원인이 완전히 다르다.
// 전자는 수집(태그/권한/기기), 후자는 패턴 문제다. 뭉뚱그리면 사용자가
// 엉뚱한 곳을 고치며 시간을 쓴다.
func diagnoseParse(res LogcatParseResult, stat map[string]*PatternStat) []string {
	var d []string
	if res.TotalLines == 0 {
		return []string{
			"logcat 파일이 비어 있다 (0줄).",
			"→ 패턴이 아니라 **수집**이 실패한 것이다. 태그·권한·기기 연결을 확인할 것.",
		}
	}
	if res.ParsedLines == 0 {
		return []string{
			fmt.Sprintf("줄은 %d개 있으나 logcat 형식으로 파싱되지 않았다.", res.TotalLines),
			"→ `-v` 형식이 예상과 다를 수 있다 (monotonic/epoch 를 기대한다).",
		}
	}

	// 캡처 그룹은 잘못됐지만 정규식은 맞은 경우 — 원인이 다르다.
	for _, s := range res.Stats {
		if s.Hits == 0 && s.ParseFailures > 0 {
			d = append(d, fmt.Sprintf(
				"series %q: 정규식은 %d줄에 걸렸으나 캡처 값이 숫자가 아니다. "+
					"→ 패턴이 아니라 **캡처 그룹 위치**를 고칠 것.", s.Key, s.ParseFailures))
		}
	}

	if res.TotalHits == 0 {
		d = append(d,
			fmt.Sprintf("패턴 %d개 중 **0건 매칭** (logcat %d줄 수집됨).", len(res.Stats), res.ParsedLines),
			"가능한 원인:",
			"  · 태그/문자열이 이 런타임·버전과 다르다 → 탐색 모드로 재확인",
			"  · 런타임이 stderr 로 출력한다 → Android 는 stderr 를 logcat 으로 보내지 않으므로 잡을 수 없다",
			"  · 벤더가 태그를 막아뒀다 → `getprop | grep log.tag` 확인, `setprop log.tag.<TAG> VERBOSE`",
			"  · 수집 구간에 추론이 실제로 실행되지 않았다",
		)
		return d
	}
	if len(res.MissingKeys) > 0 {
		d = append(d, fmt.Sprintf(
			"⚠ 부분 매칭: 패턴 %d개 중 %d개가 한 번도 안 걸렸다 (%v).",
			len(res.Stats), len(res.MissingKeys), res.MissingKeys),
			"→ 이 지표들은 **값이 없다.** 화면의 나머지 수치만 보고 정상이라 판단하지 말 것.")
	}
	return d
}
