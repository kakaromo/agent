// Package scenario — 시나리오 step 계약의 단일 진실 소스(single source of truth).
//
// 배경: step 타입 정의가 네 곳에 손으로 복사돼 있었다.
//  1. benchmark/scenario.go  executeStepInner 의 switch (실제 동작)
//  2. ai/prompt.go           AI 프롬프트의 자연어 설명 + schema enum
//  3. server/rest_ai.go      validateStepParams 의 필수 param 검증
//  4. ui/.../NodePalette.svelte + types.ts  팔레트 항목 + 색상
//
// 네 곳이 어긋나도 컴파일이 통과했기 때문에, 어긋남이 항상 실기기 세션 중에야
// 발견됐다. (예: 실행부는 clear_mode="force_stop" 을 기대하는데 프롬프트가 그걸
// 말해주지 않아 AI 가 "none" 으로 생성 → 2회차부터 실패.)
//
// 이 파일이 그 계약을 한 번만 선언한다. 프롬프트 문자열·JSON schema·검증·UI 메타는
// 모두 여기서 파생되며, 실행부 switch 와의 일치는 steptypes_test.go 가 강제한다.
package scenario

import (
	"fmt"
	"sort"
	"strings"
)

// ParamSpec — step param 하나의 계약.
type ParamSpec struct {
	Name     string   // params 맵의 키. 실행부가 읽는 이름과 정확히 일치해야 한다.
	Required bool     // 없으면 step 을 실행할 수 없다 → 검증에서 제외/거부
	Enum     []string // 허용값 목록. 비어있으면 자유 문자열
	Default  string   // 실행부가 적용하는 기본값 (문서화용 — 실행부와 일치해야 함)
	Desc     string   // AI 프롬프트에 들어갈 설명
}

// StepSpec — step 타입 하나의 계약.
type StepSpec struct {
	Type string // params switch 의 case 문자열
	// AIUsable=false 면 자연어 생성 대상에서 제외한다(schema enum 에서 빠짐).
	// 예: app_macro 는 실존 macroId 가 필요한데 AI 가 알 수 없다.
	AIUsable bool
	// Destructive=true 면 사용자가 명시 요청하지 않는 한 AI 가 생성하면 안 된다.
	// (크롬을 지워버린 uninstall_apk 오생성 사고 재발 방지)
	Destructive bool
	// RequiresTool=true 면 step.tool 필드가 필요하다 (benchmark 의 fio/iozone 등).
	RequiresTool bool
	Params       []ParamSpec

	// AI 프롬프트용
	Summary string   // 한 줄 설명
	Notes   []string // 함정/주의사항. 실기기 세션에서 얻은 교훈을 여기 축적한다.

	// UI 팔레트용
	Label string // 영문 라벨
	Desc  string // 한글 짧은 설명
	Icon  string // lucide 아이콘 이름 (kebab-case)
	Color string // tailwind 색상 계열 이름
}

// AnyOfGroup — "이 중 최소 하나는 있어야 한다" 제약.
// tap_element 처럼 식별자 여러 개 중 아무거나 하나면 되는 경우에 쓴다.
type AnyOfGroup struct {
	StepType string
	Params   []string
	Reason   string
}

// anyOfGroups — StepSpec.Params 로 표현할 수 없는 교차 제약.
var anyOfGroups = []AnyOfGroup{
	{
		StepType: "tap_element",
		Params:   []string{"element_resource_id", "element_text", "element_content_desc", "x", "y"},
		Reason:   "요소 식별자(resource_id/text/content_desc) 또는 좌표 필요",
	},
}

// Specs — 모든 step 타입의 계약. 이 슬라이스가 단일 진실 소스다.
//
// 새 step 을 추가할 때는 여기에만 추가하면 프롬프트/schema/검증/UI 가 함께 따라온다.
// 실행부 switch 에 case 를 추가하는 것을 잊으면 steptypes_test.go 가 실패한다.
var Specs = []StepSpec{
	{
		Type: "benchmark", AIUsable: true, RequiresTool: true,
		Summary: "스토리지 벤치마크. step.tool 에 \"fio\"/\"iozone\"/\"tiotest\" 지정",
		Params: []ParamSpec{
			{Name: "rw", Enum: []string{"read", "write", "randread", "randwrite"}, Desc: "I/O 패턴"},
			{Name: "bs", Desc: "블록 크기 (예 \"4k\")"},
			{Name: "size", Desc: "파일 크기 (예 \"1G\")"},
		},
		Label: "Benchmark", Desc: "fio/iozone/tiotest", Icon: "play", Color: "blue",
	},
	{
		Type: "iotest", AIUsable: true,
		Summary: "syscall I/O 테스트",
		Params:  []ParamSpec{{Name: "config", Desc: "iotest 설정"}},
		Label:   "I/O Test", Desc: "syscall I/O 테스트", Icon: "flask-conical", Color: "cyan",
	},
	{
		Type: "shell", AIUsable: true,
		Summary: "adb shell 명령",
		Params:  []ParamSpec{{Name: "cmd", Required: true, Desc: "실행할 셸 명령"}},
		Label:   "Shell", Desc: "쉘 명령어", Icon: "terminal", Color: "gray",
	},
	{
		Type: "cleanup", AIUsable: true, Destructive: true,
		Summary: "파일 삭제",
		Notes:   []string{"파일을 지우는 파괴적 동작입니다. 명시 요청이 없으면 넣지 마세요."},
		Params: []ParamSpec{
			{Name: "path", Desc: "삭제할 경로 (미지정 시 테스트 디렉토리)"},
			{Name: "delete_files_from_steps", Desc: "해당 step 인덱스들이 만든 파일 삭제 (쉼표 구분)"},
		},
		Label: "Cleanup", Desc: "파일 삭제", Icon: "trash-2", Color: "orange",
	},
	{
		Type: "sleep", AIUsable: true,
		Summary: "대기",
		Params:  []ParamSpec{{Name: "seconds", Required: true, Default: "1", Desc: "대기 초 (예 \"30\")"}},
		Label:   "Sleep", Desc: "대기", Icon: "clock", Color: "yellow",
	},
	{
		Type: "trace_start", AIUsable: true,
		Summary: "커널 트레이스 시작",
		Notes: []string{
			"trace 는 측정 대상을 **감싸는** 구조입니다. \"~하면서 트레이스\", \"~할 때 trace 수집\" 요청이면 trace_start 를 측정 대상 **앞**에, trace_stop 을 **뒤**에 반드시 쌍으로 넣으세요.",
			"trace_start 만 넣거나 워크로드 뒤에 두면 아무것도 측정되지 않습니다.",
			"**trace_start 를 넣었으면 trace_stop 을 반드시 넣으세요** — 빠지면 트레이스가 중지되지 않아 다음 작업까지 방해합니다.",
			"loops 와 함께 쓸 때 loops 범위는 trace_start/trace_stop 을 제외한 **워크로드 스텝만** 감싸야 합니다 (예: steps=[trace_start, launch_app, stop_app, trace_stop] 이면 loops 는 startStep=1, endStep=2).",
		},
		Params: []ParamSpec{
			{Name: "trace_type", Enum: []string{"ufs", "block", "both", "fsio_ufs", "fsio_block"}, Default: "ufs", Desc: "트레이스 종류 (fsio_* 는 eBPF 기반 — root 필요, 파일명/프로세스 귀속 제공)"},
			{Name: "window_seconds", Default: "1", Desc: "수집 윈도우 (초)"},
		},
		Label: "Trace Start", Desc: "ftrace 시작", Icon: "scan-search", Color: "emerald",
	},
	{
		Type: "trace_stop", AIUsable: true,
		Summary: "트레이스 중지",
		Params: []ParamSpec{
			{Name: "trace_type", Enum: []string{"ufs", "block", "both", "fsio_ufs", "fsio_block"}, Default: "ufs", Desc: "trace_start 와 같은 값"},
		},
		Label: "Trace Stop", Desc: "ftrace 중지", Icon: "square", Color: "emerald",
	},
	{
		// AI 생성 제외 — 실존 macroId 를 모델이 알 수 없다.
		Type: "app_macro", AIUsable: false,
		Summary: "기록된 앱 매크로 실행",
		Label:   "App Macro", Desc: "앱 매크로 실행", Icon: "smartphone", Color: "violet",
	},
	{
		Type: "install_apk", AIUsable: true,
		Summary: "APK 설치",
		Params: []ParamSpec{
			{Name: "apk_filename", Required: true, Desc: "tools/apks 안의 파일명"},
			{Name: "grant_permissions", Enum: []string{"true", "false"}, Desc: "설치 시 권한 자동 허용"},
		},
		Label: "Install APK", Desc: "APK 설치", Icon: "download", Color: "indigo",
	},
	{
		Type: "uninstall_apk", AIUsable: true, Destructive: true,
		Summary: "앱 제거",
		Notes: []string{
			"**주의: 앱을 삭제하는 파괴적 동작입니다.** 사용자가 \"삭제/제거/언인스톨\" 을 명시적으로 요구하지 않았다면 절대 넣지 마세요.",
			"\"종료\", \"강제종료\", \"끄기\" 는 uninstall_apk 가 아니라 **stop_app** 입니다.",
		},
		Params: []ParamSpec{
			{Name: "package_name", Required: true, Desc: "제거할 패키지"},
			{Name: "keep_data", Enum: []string{"true", "false"}, Desc: "데이터 보존 여부"},
		},
		Label: "Uninstall APK", Desc: "앱 제거", Icon: "package-minus", Color: "rose",
	},
	{
		Type: "tap_element", AIUsable: true,
		Summary: "요소 기반 탭",
		Notes: []string{
			"정확한 resource_id 를 모르면 지어내지 말고 element_content_desc(접근성 라벨) 나 element_text(화면에 보이는 글자) 로 지정하세요. 이쪽이 앱 버전에 덜 민감합니다.",
		},
		Params: []ParamSpec{
			{Name: "element_resource_id", Desc: "리소스 ID"},
			{Name: "element_text", Desc: "화면에 보이는 글자"},
			{Name: "element_content_desc", Desc: "접근성 라벨"},
			// macro/uihierarchy.go 의 matchPattern 이 실제로 지원하는 6가지.
			// (처음에 exact/contains 만 적었다가 저장된 시나리오의 "suffix" 가 거부돼 발견 —
			//  계약을 구현보다 좁게 쓰면 멀쩡한 시나리오를 막는다.)
			{Name: "element_match_mode", Enum: []string{"exact", "contains", "prefix", "suffix", "regex"}, Desc: "매칭 방식 (미지정 시 exact)"},
			{Name: "element_container_id", Desc: "탐색 범위를 좁힐 컨테이너"},
			{Name: "element_index", Desc: "여러 개 매칭 시 N번째"},
			{Name: "x", Desc: "폴백 좌표 X"},
			{Name: "y", Desc: "폴백 좌표 Y"},
		},
		Label: "Tap Element", Desc: "요소 기반 탭", Icon: "mouse-pointer-click", Color: "fuchsia",
	},
	{
		Type: "tap", AIUsable: true,
		Summary: "절대 좌표 탭",
		Params: []ParamSpec{
			{Name: "x", Required: true, Desc: "픽셀 좌표 X"},
			{Name: "y", Required: true, Desc: "픽셀 좌표 Y"},
		},
		Label: "Tap", Desc: "좌표 탭", Icon: "pointer", Color: "pink",
	},
	{
		Type: "text", AIUsable: true,
		Summary: "텍스트 입력",
		Notes: []string{
			"**입력창이 이미 활성(포커스)된 상태여야 합니다** — 필요하면 먼저 tap_element 로 입력창/검색을 눌러 진입하세요.",
		},
		Params: []ParamSpec{
			{Name: "input_text", Required: true, Desc: "입력할 문자열"},
			{Name: "submit", Enum: []string{"true", "false"}, Desc: "입력 후 엔터로 실행"},
		},
		Label: "Text Input", Desc: "텍스트 입력", Icon: "type", Color: "teal",
	},
	{
		Type: "scroll", AIUsable: true,
		Summary: "피드 스크롤 (워크로드 재현)",
		Notes: []string{
			"**\"N번 스크롤하며 각 사이 P초 대기\"는 반드시 scroll 하나로 { count:\"N\", pause:\"P\" } 로 표현하세요.** scroll count=1 을 loop 로 N번 반복하면 스크롤 사이 대기(pause)가 적용되지 않습니다.",
		},
		Params: []ParamSpec{
			{Name: "direction", Enum: []string{"up", "down"}, Desc: "스크롤 방향"},
			{Name: "count", Desc: "스크롤 횟수 (예 \"10\")"},
			{Name: "pause", Desc: "각 스크롤 사이 대기 **초** (예 \"1\"=1초 — 밀리초 아님)"},
			{Name: "duration", Desc: "스와이프 동작 시간 밀리초 (예 \"300\")"},
		},
		Label: "Scroll", Desc: "피드 스크롤", Icon: "mouse", Color: "sky",
	},
	{
		Type: "key", AIUsable: true,
		Summary: "키 이벤트",
		Params: []ParamSpec{
			{Name: "keycode", Required: true, Desc: "키코드 (예 \"4\"=BACK, \"3\"=HOME, \"66\"=ENTER)"},
		},
		Label: "Key", Desc: "뒤로/홈/제어 키", Icon: "corner-up-left", Color: "slate",
	},
	{
		Type: "stop_app", AIUsable: true,
		Summary: "앱 종료 (강제종료 / force stop / 앱 죽이기 / 종료 후 재실행)",
		Notes: []string{
			"\"강제종료\"는 **stop_app** 입니다. 앱을 지우는 uninstall_apk 와 혼동하지 마세요.",
			"\"종료하고 다시 켜기\"(cold start) 는 stop_app → launch_app, 또는 launch_app 의 clear_mode=\"force_stop\" 하나로도 됩니다.",
		},
		Params: []ParamSpec{{Name: "package_name", Required: true, Desc: "종료할 패키지"}},
		Label:  "Stop App", Desc: "앱 완전 종료", Icon: "circle-stop", Color: "red",
	},
	{
		Type: "launch_app", AIUsable: true,
		Summary: "앱 실행",
		Notes: []string{
			"**clear_mode 는 기본적으로 \"force_stop\" 을 쓰세요.** 앱이 이미 실행 중이면 이전 화면(검색 결과 등)이 그대로 남아, 뒤따르는 tap_element 가 \"검색\" 같은 첫 화면 요소를 찾지 못해 실패합니다. 같은 시나리오를 반복해도 같은 결과가 나오려면 앱을 초기 상태에서 시작해야 합니다.",
			"\"이어서/현재 화면에서\" 처럼 사용자가 명시적으로 현재 상태 유지를 요구할 때만 \"none\" 을 쓰세요.",
			"**wait_seconds 는 항상 넣으세요**(\"3\" 권장). 앱 로딩 전에 탭하면 요소를 못 찾습니다.",
		},
		Params: []ParamSpec{
			{Name: "package_name", Required: true, Desc: "실행할 패키지 (예 \"com.google.android.youtube\")"},
			{Name: "clear_mode", Enum: []string{"force_stop", "clear", "cache", "none"}, Default: "force_stop", Desc: "실행 전 초기화 방식"},
			{Name: "wait_seconds", Default: "3", Desc: "실행 후 대기 초"},
			{Name: "wait_activity", Desc: "이 activity 가 포커스될 때까지 대기 (선택)"},
		},
		Label: "Launch App", Desc: "앱 초기화+시작", Icon: "rocket", Color: "green",
	},
}

// controlOnlyTypes — 실행부 switch 에는 있지만 Specs 에 넣지 않는 제어 전용 타입.
//
// condition 은 캔버스 DAG 의 분기 노드라 params 계약이 다른 step 과 구조가 다르고
// (좌/우 분기 edge 로 표현), 팔레트에도 별도 Control 섹션에 있다. AI 생성 대상도 아니다.
// 여기 명시해 두는 이유는 steptypes_test 가 "실행부에 있는데 Specs 에 없다"고
// 실패하는 것을 막으면서도, 새 타입이 조용히 빠지는 것은 계속 잡기 위해서다.
var controlOnlyTypes = map[string]bool{
	"condition": true,
}

// IsControlOnly — 제어 전용(일반 step 계약 밖) 타입인지.
func IsControlOnly(stepType string) bool { return controlOnlyTypes[stepType] }

// specByType — 조회용 인덱스.
var specByType = func() map[string]StepSpec {
	m := make(map[string]StepSpec, len(Specs))
	for _, s := range Specs {
		m[s.Type] = s
	}
	return m
}()

// Lookup — 타입으로 spec 을 찾는다.
//
// 현재 프로덕션 호출자는 없고 계약 테스트(steptypes_test.go)에서만 쓴다. 이 파일이
// step 계약의 단일 진실 소스이므로 조회 API 는 표면으로 남겨둔다 — 드리프트 가드가
// 주 소비자인 것이 설계 의도다.
func Lookup(stepType string) (StepSpec, bool) {
	s, ok := specByType[stepType]
	return s, ok
}

// AllTypes — 실행부가 인식하는 모든 step 타입 (condition 제외 — DAG 전용).
func AllTypes() []string {
	out := make([]string, 0, len(Specs))
	for _, s := range Specs {
		out = append(out, s.Type)
	}
	return out
}

// AITypes — 자연어 생성이 쓸 수 있는 타입만 (schema enum 용).
func AITypes() []string {
	out := make([]string, 0, len(Specs))
	for _, s := range Specs {
		if s.AIUsable {
			out = append(out, s.Type)
		}
	}
	return out
}

// AnyOfFor — 해당 타입의 "최소 하나" 제약을 반환한다.
func AnyOfFor(stepType string) (AnyOfGroup, bool) {
	for _, g := range anyOfGroups {
		if g.StepType == stepType {
			return g, true
		}
	}
	return AnyOfGroup{}, false
}

// ValidateParams — step 의 필수 param / enum / anyOf 제약을 검사한다.
//
// 빈 문자열 반환이면 통과, 아니면 사람이 읽을 수 있는 위반 사유.
// AI 생성 검증(rest_ai)과 캔버스 수동 편집 검증이 같은 함수를 쓴다 —
// 예전엔 AI 경로에만 검증이 있어 수동 편집으로 만든 시나리오는 무검증이었다.
func ValidateParams(stepType, tool string, params map[string]string) string {
	spec, ok := specByType[stepType]
	if !ok {
		return fmt.Sprintf("알 수 없는 type '%s'", stepType)
	}

	if spec.RequiresTool && strings.TrimSpace(tool) == "" {
		return fmt.Sprintf("%s 는 tool(fio 등) 필요", stepType)
	}

	for _, p := range spec.Params {
		v := strings.TrimSpace(params[p.Name])
		if p.Required && v == "" {
			return fmt.Sprintf("%s 필요", p.Name)
		}
		// 값이 있을 때만 enum 검사 — 빈 값은 실행부가 Default 를 적용한다.
		if v != "" && len(p.Enum) > 0 && !contains(p.Enum, v) {
			return fmt.Sprintf("%s 는 %s 중 하나여야 합니다 (받은 값: %q)",
				p.Name, strings.Join(p.Enum, "|"), v)
		}
	}

	if g, ok := AnyOfFor(stepType); ok {
		found := false
		for _, name := range g.Params {
			if strings.TrimSpace(params[name]) != "" {
				found = true
				break
			}
		}
		if !found {
			return g.Reason
		}
	}

	return ""
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// PromptStepReference — AI 프롬프트의 "사용 가능한 step type" 절을 생성한다.
//
// 손으로 쓴 설명과 실제 계약이 어긋나는 것을 막기 위해 Specs 에서 만든다.
func PromptStepReference() string {
	var b strings.Builder
	for _, s := range Specs {
		if !s.AIUsable {
			continue
		}
		b.WriteString("- " + s.Type + ": " + s.Summary)
		if len(s.Params) > 0 {
			b.WriteString(". params: " + paramLine(s))
		}
		b.WriteString("\n")
		for _, n := range s.Notes {
			b.WriteString("  " + n + "\n")
		}
	}
	// AI 가 쓰면 안 되는 타입도 이유와 함께 명시 — 안 그러면 지어낸다.
	for _, s := range Specs {
		if s.AIUsable {
			continue
		}
		b.WriteString("- " + s.Type + ": **직접 생성하지 마세요.** 기록된 매크로 참조가 필요한데 그 ID 를 알 수 없습니다.\n")
		b.WriteString("  탭/텍스트/스크롤이 필요하면 tap / tap_element / text / scroll / launch_app 같은 직접 step 으로 표현하세요.\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func paramLine(s StepSpec) string {
	parts := make([]string, 0, len(s.Params))
	for _, p := range s.Params {
		seg := p.Name
		var attrs []string
		if p.Required {
			attrs = append(attrs, "필수")
		}
		if len(p.Enum) > 0 {
			quoted := make([]string, len(p.Enum))
			for i, e := range p.Enum {
				quoted[i] = `"` + e + `"`
			}
			attrs = append(attrs, strings.Join(quoted, "|"))
		}
		if p.Desc != "" {
			attrs = append(attrs, p.Desc)
		}
		if len(attrs) > 0 {
			seg += "(" + strings.Join(attrs, ", ") + ")"
		}
		parts = append(parts, seg)
	}
	return strings.Join(parts, ", ")
}

// DestructiveTypes — 파괴적 step 타입 목록 (정렬됨).
func DestructiveTypes() []string {
	var out []string
	for _, s := range Specs {
		if s.Destructive {
			out = append(out, s.Type)
		}
	}
	sort.Strings(out)
	return out
}
