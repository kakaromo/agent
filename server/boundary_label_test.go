package server

import (
	"encoding/json"
	"strings"
	"testing"
)

const twoLoops = `[
 {"stepIndex":4,"loopIndex":1,"repeatIndex":1,"type":"scroll","label":"스크롤 down ×10","startedMono":10,"finishedMono":20,"success":true},
 {"stepIndex":4,"loopIndex":2,"repeatIndex":1,"type":"scroll","label":"스크롤 down ×10","startedMono":30,"finishedMono":40,"success":true}
]`

func parse(t *testing.T, s string) []map[string]any {
	t.Helper()
	var arr []map[string]any
	if err := json.Unmarshal([]byte(s), &arr); err != nil {
		t.Fatalf("결과 JSON 파싱 실패: %v", err)
	}
	return arr
}

// 이름을 바꾸면 그 구간만 바뀌고, 원본 label 은 남는다(되돌리기 가능).
func TestApplyBoundaryLabel(t *testing.T) {
	out, ok, err := applyBoundaryLabel(twoLoops, 4, 1, 1, "영상 재생 구간")
	if err != nil || !ok {
		t.Fatalf("적용 실패: ok=%v err=%v", ok, err)
	}
	arr := parse(t, out)
	if got := arr[0]["labelOverride"]; got != "영상 재생 구간" {
		t.Errorf("labelOverride = %v", got)
	}
	if got := arr[0]["label"]; got != "스크롤 down ×10" {
		t.Errorf("원본 label 이 덮어써졌다: %v — 되돌릴 수 없게 된다", got)
	}
	// ⚠ loop 2 는 같은 stepIndex 지만 **다른 구간**이다.
	if _, has := arr[1]["labelOverride"]; has {
		t.Error("loop=2 구간까지 같이 바뀌었다 — (step,loop,repeat) 로 구분해야 한다")
	}
	// 모르는 키를 잃지 않는지
	if arr[0]["startedMono"] == nil || arr[0]["success"] == nil {
		t.Error("다른 필드가 사라졌다")
	}
}

// 빈 문자열이면 키를 지운다 = 원래 이름 복귀.
func TestApplyBoundaryLabelReset(t *testing.T) {
	once, _, _ := applyBoundaryLabel(twoLoops, 4, 1, 1, "임시 이름")
	out, ok, err := applyBoundaryLabel(once, 4, 1, 1, "")
	if err != nil || !ok {
		t.Fatalf("해제 실패: ok=%v err=%v", ok, err)
	}
	if _, has := parse(t, out)[0]["labelOverride"]; has {
		t.Error("빈 값인데 labelOverride 가 남았다 — '빈 이름' 과 '해제' 가 구분 안 된다")
	}
}

// 없는 구간이면 applied=false (호출부가 404 를 준다).
func TestApplyBoundaryLabelNotFound(t *testing.T) {
	if _, ok, err := applyBoundaryLabel(twoLoops, 99, 0, 1, "x"); ok || err != nil {
		t.Errorf("없는 구간인데 ok=%v err=%v", ok, err)
	}
}

// 구간이 없는 잡 / 깨진 JSON 은 에러 메시지로 알린다.
func TestApplyBoundaryLabelBadInput(t *testing.T) {
	if _, _, err := applyBoundaryLabel("", 0, 0, 1, "x"); err == nil {
		t.Error("빈 입력인데 에러가 없다")
	}
	_, _, err := applyBoundaryLabel("{not json", 0, 0, 1, "x")
	if err == nil || !strings.Contains(err.Error(), "읽지 못했습니다") {
		t.Errorf("깨진 JSON 에러가 부적절: %v", err)
	}
}
