package sqlitedb

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// AILogPatterns — ai_log_profiles.patterns_json 의 구조.
//
// 런타임이 logcat 에 찍는 문구에서 지표를 뽑는 규칙 묶음이다. 예시:
//
//	9573.594 QnnHtp : prefill begin, 512 tokens          → Mark   (시각만 씀)
//	9574.044 Genie  : first token emitted — TTFT 2840 ms → Series (숫자를 뽑음)
//	9574.068 Genie  : decode 24.1 ms/tok                 → Series (토큰마다 → 시계열)
//
// ⚠ Mark(시점) 와 Series(값) 를 나눈 이유: mark 는 걸린 줄의 **시각**만 써서 구간
// 경계로 쓰고, series 는 캡처 그룹에서 **숫자**를 뽑아 값으로 쓴다. 성격이 달라
// 한 종류로 뭉치면 파싱이 지저분해진다.
type AILogPatterns struct {
	// Tags — 측정 시 `logcat -s <tags>` 로 좁힐 태그 목록.
	// 비우면 전체를 받는데, 그 자체가 IO/CPU 를 써서 측정 대상을 흔든다.
	Tags []string `json:"tags,omitempty"`
	// MinPriority — logcat 우선순위 하한 (V/D/I/W/E). 비우면 기본값.
	MinPriority string `json:"minPriority,omitempty"`

	Marks  []AILogMark   `json:"marks,omitempty"`
	Series []AILogSeries `json:"series,omitempty"`
}

// AILogMark — 걸린 줄의 **시각**을 구간 경계로 쓰는 패턴.
type AILogMark struct {
	Key   string `json:"key"`
	Regex string `json:"regex"`
}

// AILogSeries — 캡처 그룹에서 **숫자**를 뽑는 패턴.
// 같은 키가 여러 줄에 걸리면 시계열이 된다 (예: 토큰마다 나오는 ms/tok).
type AILogSeries struct {
	Key   string `json:"key"`
	Regex string `json:"regex"`
	Unit  string `json:"unit,omitempty"`
}

// AI 지표를 읽어오는 소스.
//
// ⚠ 두 소스는 patterns_json 의 **필드 이름이 다르다**:
//
//	logcat: {"marks":[{key,regex}], "series":[{key,regex,unit}]}
//	marker: {"counters":[{key,name|regex,unit}], "sections":[{key,name|regex}]}
//
// 섞으면 JSON 파싱은 통과하는데 매칭이 **조용히 0건**이 된다 — 사용자는 정규식을
// 고쳐가며 헛수고한다. 그래서 프로파일에 소스를 박아 두고 파싱 전에 막는다.
const (
	AISourceLogcat = "logcat"
	AISourceMarker = "marker"
)

// normalizeAISource — 빈 값은 기존 동작(logcat)으로 본다.
// 이 컬럼이 없던 시절 저장된 행과 호환되어야 한다.
func normalizeAISource(s string) string {
	if s == AISourceMarker {
		return AISourceMarker
	}
	return AISourceLogcat
}

// ValidateMarkerPatternsJSON — marker 패턴을 저장 전에 검증한다.
//
// ⚠ logcat 과 검증 항목이 다르다. 저쪽은 "series 에 캡처 그룹이 있는가" 가 핵심인데,
// marker 의 `C|` 는 값이 이미 분리돼 있어 **캡처 그룹 자체가 필요 없다.** 대신
// "무엇을 찾을지"(name 또는 regex)가 있는지를 본다.
func ValidateMarkerPatternsJSON(s string) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("patternsJson required")
	}
	var p struct {
		Counters []struct {
			Key, Name, Regex string
		} `json:"counters"`
		Sections []struct {
			Key, Name, Regex string
		} `json:"sections"`
	}
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		return fmt.Errorf("patternsJson: %w", err)
	}
	if len(p.Counters) == 0 && len(p.Sections) == 0 {
		return fmt.Errorf("counters 또는 sections 중 최소 하나는 있어야 한다 " +
			"(없으면 매칭이 항상 0건이다)")
	}
	seen := map[string]bool{}
	check := func(kind, key, name, expr string) error {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("%s: key 가 비어 있다", kind)
		}
		if seen[key] {
			// 나중 것이 앞의 것을 덮어써 한쪽이 조용히 사라진다.
			return fmt.Errorf("key 중복: %q", key)
		}
		seen[key] = true
		if strings.TrimSpace(name) == "" && strings.TrimSpace(expr) == "" {
			return fmt.Errorf("%s %q: name 또는 regex 중 하나는 있어야 한다", kind, key)
		}
		if expr != "" {
			if _, err := regexp.Compile(expr); err != nil {
				// 여기서 안 막으면 **측정 시점에** 터진다 — 그때는 기기를 붙들고 있다.
				return fmt.Errorf("%s %q: regex: %w", kind, key, err)
			}
		}
		return nil
	}
	for _, c := range p.Counters {
		if err := check("counter", c.Key, c.Name, c.Regex); err != nil {
			return err
		}
	}
	for _, c := range p.Sections {
		if err := check("section", c.Key, c.Name, c.Regex); err != nil {
			return err
		}
	}
	return nil
}

// validateBySource — 소스에 맞는 검증기를 고른다.
//
// ⚠ 여기서 안 갈라주면 marker 패턴이 logcat 검증(캡처 그룹 필수)에 걸려 저장이
// 거부되거나, 반대로 logcat 패턴이 marker 로 저장돼 측정 시 0건이 된다.
func validateBySource(source, patternsJSON string) error {
	if normalizeAISource(source) == AISourceMarker {
		return ValidateMarkerPatternsJSON(patternsJSON)
	}
	return ValidatePatternsJSON(patternsJSON)
}

// ParseAILogPatterns — patterns_json 을 구조체로 푼다.
//
// 저장 시 ValidatePatternsJSON 을 통과한 값이라는 전제지만, DB 를 손으로 고친
// 경우를 대비해 에러를 그대로 올린다 (조용히 빈 값을 주면 measure 가 explore 로
// 떨어져 측정 대상을 흔든다).
func ParseAILogPatterns(s string) (*AILogPatterns, error) {
	var p AILogPatterns
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		return nil, fmt.Errorf("patternsJson: %w", err)
	}
	return &p, nil
}

// ValidatePatternsJSON — patterns_json 을 저장 전에 검증한다.
//
// ⚠ 정규식은 사용자 입력이다. 여기서 안 막으면 잘못된 패턴이 DB 에 들어앉아
// **측정 시점에** 터진다 — 그때는 기기를 붙들고 실행 중이라 되돌리는 비용이 크다.
// 저장은 사람이 앉아 있는 시점이라 고치기 쉽다.
func ValidatePatternsJSON(s string) error {
	if s == "" {
		return fmt.Errorf("patternsJson required")
	}
	var p AILogPatterns
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		return fmt.Errorf("patternsJson: %w", err)
	}
	if len(p.Marks) == 0 && len(p.Series) == 0 {
		// 패턴이 하나도 없으면 매칭이 항상 0건이라 잡이 무조건 실패한다.
		// 저장 시점에 막는 편이 낫다.
		return fmt.Errorf("patternsJson: marks 또는 series 중 최소 하나는 있어야 한다")
	}

	seen := map[string]bool{}
	for _, m := range p.Marks {
		if err := validateOne("mark", m.Key, m.Regex, seen, false); err != nil {
			return err
		}
	}
	for _, s := range p.Series {
		if err := validateOne("series", s.Key, s.Regex, seen, true); err != nil {
			return err
		}
	}
	return nil
}

// validateOne — 키 중복과 정규식 컴파일을 본다.
// needCapture 면 캡처 그룹이 있어야 한다 (series 는 숫자를 뽑아야 하므로).
func validateOne(kind, key, expr string, seen map[string]bool, needCapture bool) error {
	if key == "" {
		return fmt.Errorf("%s: key required", kind)
	}
	// ⚠ 키가 겹치면 나중 것이 앞의 것을 덮어써 **조용히 한쪽이 사라진다.**
	// marks 와 series 를 통틀어 유일해야 결과 map 에서 충돌하지 않는다.
	if seen[key] {
		return fmt.Errorf("%s: key 중복 %q", kind, key)
	}
	seen[key] = true

	if expr == "" {
		return fmt.Errorf("%s %q: regex required", kind, key)
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return fmt.Errorf("%s %q: regex: %w", kind, key, err)
	}
	if needCapture && re.NumSubexp() < 1 {
		// 캡처 그룹이 없으면 뽑을 숫자가 없다. 매칭은 되는데 값이 안 나오는
		// **조용히 비어 있는** 결과가 되므로 저장 시점에 막는다.
		return fmt.Errorf("%s %q: 값을 뽑을 캡처 그룹 () 이 필요하다 (예: `TTFT ([0-9.]+) ms`)", kind, key)
	}
	return nil
}
