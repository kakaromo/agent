package scenario

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestSpecsMatchExecutionSwitch — Specs 와 실행부 switch 가 일치하는지 검사한다.
//
// 이 테스트가 이 패키지의 존재 이유다. 예전엔 step 계약이 네 곳에 복사돼 있고
// 어긋나도 컴파일이 통과해서, 불일치가 항상 실기기 세션 중에야 드러났다.
// 여기서 실제 소스를 파싱해 비교하므로 이제는 빌드 단계에서 잡힌다.
func TestSpecsMatchExecutionSwitch(t *testing.T) {
	execTypes := parseExecutionSwitch(t)

	for _, spec := range Specs {
		if !execTypes[spec.Type] {
			t.Errorf("Specs 에 %q 가 있는데 benchmark/scenario.go 의 executeStepInner switch 에 case 가 없습니다.\n"+
				"→ 실행부에 case 를 추가하거나 Specs 에서 제거하세요.", spec.Type)
		}
	}

	for typ := range execTypes {
		if IsControlOnly(typ) {
			continue // condition 등 DAG 제어 전용 — 의도적으로 Specs 밖
		}
		if _, ok := Lookup(typ); !ok {
			t.Errorf("실행부 switch 에 case %q 가 있는데 Specs 에 없습니다.\n"+
				"→ scenario/steptypes.go 의 Specs 에 추가하세요. "+
				"(추가하지 않으면 AI 프롬프트·검증·UI 팔레트에서 이 step 이 누락됩니다.)", typ)
		}
	}
}

// parseExecutionSwitch — benchmark/scenario.go 의 executeStepInner switch 에서 case 문자열을 뽑는다.
//
// 소스 파싱이라 다소 거칠지만, 목적은 "두 목록이 어긋났다"는 신호를 내는 것이고
// 실행부 switch 는 형식이 안정적이다. 파싱이 깨지면 테스트가 실패해 알 수 있다.
func parseExecutionSwitch(t *testing.T) map[string]bool {
	t.Helper()

	path := filepath.Join("..", "benchmark", "scenario.go")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("실행부 소스를 읽을 수 없습니다 (%s): %v", path, err)
	}

	lines := strings.Split(string(src), "\n")
	start := -1
	for i, l := range lines {
		if strings.Contains(l, "func (o *Orchestrator) executeStepInner(") {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatal("executeStepInner 함수를 찾을 수 없습니다 — 시그니처가 바뀌었다면 이 테스트도 갱신하세요.")
	}

	// switch step.Type { 을 찾고, 그 블록의 최상위 case 만 수집한다.
	switchLine := -1
	for i := start; i < len(lines) && i < start+40; i++ {
		if strings.Contains(lines[i], "switch step.Type") {
			switchLine = i
			break
		}
	}
	if switchLine < 0 {
		t.Fatal("executeStepInner 안에서 'switch step.Type' 을 찾을 수 없습니다.")
	}

	// case 행의 들여쓰기 깊이로 최상위 case 를 구분한다 (중첩 switch 의 case 제외).
	caseRe := regexp.MustCompile(`^(\s*)case\s+(.+):`)
	strRe := regexp.MustCompile(`"([a-z_]+)"`)

	baseIndent := ""
	got := make(map[string]bool)
	for i := switchLine + 1; i < len(lines); i++ {
		line := lines[i]
		// 함수 끝(최상위 닫는 중괄호)에서 중단.
		if line == "}" {
			break
		}
		m := caseRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		indent := m[1]
		if baseIndent == "" {
			baseIndent = indent
		}
		if indent != baseIndent {
			continue // 중첩 switch (clear_mode 등)
		}
		for _, sm := range strRe.FindAllStringSubmatch(m[2], -1) {
			got[sm[1]] = true
		}
	}

	if len(got) == 0 {
		t.Fatal("switch 에서 case 를 하나도 추출하지 못했습니다 — 파싱 로직을 확인하세요.")
	}
	return got
}

// TestGeneratedUIContractIsFresh — 생성된 UI 계약 파일이 Specs 와 동기인지 검사한다.
//
// `go run ./scenario/gen` 을 잊고 커밋하면 UI 팔레트가 조용히 옛 계약을 쓰게 된다.
// 여기서 잡아 "재생성하세요" 라고 알려준다.
func TestGeneratedUIContractIsFresh(t *testing.T) {
	path := filepath.Join("..", "ui", "src", "routes", "agent", "scenario-canvas", "step-contract.ts")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("생성 파일이 없습니다 (%s): %v\n→ go run ./scenario/gen 을 실행하세요.", path, err)
	}
	got := string(data)

	for _, s := range Specs {
		if !strings.Contains(got, `type: "`+s.Type+`"`) {
			t.Errorf("step-contract.ts 에 %q 가 없습니다 — go run ./scenario/gen 으로 재생성하세요", s.Type)
		}
		if !strings.Contains(got, `label: "`+s.Label+`"`) {
			t.Errorf("step-contract.ts 의 %q 라벨이 낡았습니다 — go run ./scenario/gen 으로 재생성하세요", s.Type)
		}
	}
}

// TestValidateParams — 필수/enum/anyOf 제약.
func TestValidateParams(t *testing.T) {
	tests := []struct {
		name     string
		stepType string
		tool     string
		params   map[string]string
		wantOK   bool
	}{
		{"launch_app 정상", "launch_app", "", map[string]string{"package_name": "com.android.chrome"}, true},
		{"launch_app package 누락", "launch_app", "", map[string]string{}, false},
		{"launch_app clear_mode 오타", "launch_app", "", map[string]string{"package_name": "x", "clear_mode": "forcestop"}, false},
		{"launch_app clear_mode 정상", "launch_app", "", map[string]string{"package_name": "x", "clear_mode": "force_stop"}, true},
		{"clear_mode 빈값은 기본값 적용", "launch_app", "", map[string]string{"package_name": "x", "clear_mode": ""}, true},

		{"benchmark tool 필요", "benchmark", "", map[string]string{}, false},
		{"benchmark tool 있음", "benchmark", "fio", map[string]string{}, true},
		{"benchmark rw 오타", "benchmark", "fio", map[string]string{"rw": "randomread"}, false},

		{"tap 좌표 필요", "tap", "", map[string]string{"x": "100"}, false},
		{"tap 좌표 정상", "tap", "", map[string]string{"x": "100", "y": "200"}, true},

		{"tap_element 식별자 없음", "tap_element", "", map[string]string{}, false},
		{"tap_element content_desc", "tap_element", "", map[string]string{"element_content_desc": "검색"}, true},
		{"tap_element 좌표 폴백", "tap_element", "", map[string]string{"x": "10", "y": "20"}, true},

		{"trace_start 타입 오타", "trace_start", "", map[string]string{"trace_type": "usf"}, false},
		{"trace_start 정상", "trace_start", "", map[string]string{"trace_type": "ufs"}, true},

		{"scroll 방향 오타", "scroll", "", map[string]string{"direction": "downward"}, false},
		{"scroll 정상", "scroll", "", map[string]string{"direction": "down", "count": "10"}, true},

		{"key keycode 필요", "key", "", map[string]string{}, false},
		{"text input_text 필요", "text", "", map[string]string{}, false},
		{"shell cmd 필요", "shell", "", map[string]string{}, false},

		{"알 수 없는 타입", "teleport", "", map[string]string{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidateParams(tt.stepType, tt.tool, tt.params)
			if tt.wantOK && got != "" {
				t.Errorf("통과해야 하는데 거부됨: %s", got)
			}
			if !tt.wantOK && got == "" {
				t.Error("거부해야 하는데 통과됨")
			}
		})
	}
}

// TestPromptReferenceCoversContract — 프롬프트가 계약의 핵심을 실제로 담고 있는지.
//
// 실기기에서 비싸게 얻은 교훈(clear_mode=force_stop 기본, 강제종료≠삭제 등)이
// 프롬프트에서 조용히 빠지는 것을 막는다.
func TestPromptReferenceCoversContract(t *testing.T) {
	ref := PromptStepReference()

	for _, s := range Specs {
		if !s.AIUsable {
			continue
		}
		if !strings.Contains(ref, "- "+s.Type+":") {
			t.Errorf("프롬프트에 step type %q 가 없습니다", s.Type)
		}
		for _, p := range s.Params {
			if p.Required && !strings.Contains(ref, p.Name) {
				t.Errorf("프롬프트에 %s 의 필수 param %q 가 없습니다", s.Type, p.Name)
			}
		}
		for _, n := range s.Notes {
			// note 의 앞부분만 확인 (마크다운 강조 등으로 완전 일치가 깨질 수 있음)
			head := n
			if len(head) > 20 {
				head = head[:20]
			}
			if !strings.Contains(ref, head) {
				t.Errorf("프롬프트에 %s 의 주의사항이 누락됐습니다: %q", s.Type, head)
			}
		}
	}

	// app_macro 는 AI 대상이 아니지만 "쓰지 말라"는 안내는 남아야 한다.
	if !strings.Contains(ref, "app_macro") {
		t.Error("프롬프트에 app_macro 금지 안내가 없습니다 — 없으면 모델이 지어냅니다")
	}
}

// TestAITypesExcludeNonUsable — schema enum 에 app_macro 가 새어나가지 않는지.
func TestAITypesExcludeNonUsable(t *testing.T) {
	for _, typ := range AITypes() {
		spec, _ := Lookup(typ)
		if !spec.AIUsable {
			t.Errorf("AITypes() 가 AIUsable=false 인 %q 를 포함합니다", typ)
		}
	}
	if len(AITypes()) >= len(AllTypes()) {
		t.Error("AITypes() 가 AllTypes() 를 걸러내지 못했습니다")
	}
}

// TestDestructiveTypesFlagged — 파괴적 step 이 표시돼 있는지.
// 크롬이 실제로 삭제된 사고(uninstall_apk 오생성)의 회귀 방지.
func TestDestructiveTypesFlagged(t *testing.T) {
	d := DestructiveTypes()
	want := map[string]bool{"uninstall_apk": true, "cleanup": true}
	for w := range want {
		found := false
		for _, g := range d {
			if g == w {
				found = true
			}
		}
		if !found {
			t.Errorf("%q 가 Destructive 로 표시돼야 합니다", w)
		}
	}
}
