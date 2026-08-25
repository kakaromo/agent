// 이 파일은 자동 생성됩니다. 직접 수정하지 마세요.
// 원본: scenario/steptypes.go (Go 실행부 계약)
// 재생성: go run ./scenario/gen

export interface StepParamSpec {
	name: string;
	required: boolean;
	enum?: string[];
	default?: string;
	desc: string;
}

export interface StepContract {
	type: string;
	label: string;
	desc: string;
	icon: string;
	color: string;
	destructive: boolean;
	requiresTool: boolean;
	aiUsable: boolean;
	params: StepParamSpec[];
}

export const STEP_CONTRACTS: StepContract[] = [
	{
		type: "benchmark",
		label: "Benchmark",
		desc: "fio/iozone/tiotest",
		icon: "play",
		color: "blue",
		destructive: false,
		requiresTool: true,
		aiUsable: true,
		params: [
			{ name: "rw", required: false, enum: ["read", "write", "randread", "randwrite"], desc: "I/O 패턴" },
			{ name: "bs", required: false, desc: "블록 크기 (예 \"4k\")" },
			{ name: "size", required: false, desc: "파일 크기 (예 \"1G\")" },
		]
	},
	{
		type: "iotest",
		label: "I/O Test",
		desc: "syscall I/O 테스트",
		icon: "flask-conical",
		color: "cyan",
		destructive: false,
		requiresTool: false,
		aiUsable: true,
		params: [
			{ name: "config", required: false, desc: "iotest 설정" },
		]
	},
	{
		type: "shell",
		label: "Shell",
		desc: "쉘 명령어",
		icon: "terminal",
		color: "gray",
		destructive: false,
		requiresTool: false,
		aiUsable: true,
		params: [
			{ name: "cmd", required: true, desc: "실행할 셸 명령" },
		]
	},
	{
		type: "cleanup",
		label: "Cleanup",
		desc: "파일 삭제",
		icon: "trash-2",
		color: "orange",
		destructive: true,
		requiresTool: false,
		aiUsable: true,
		params: [
			{ name: "path", required: false, desc: "삭제할 경로 (미지정 시 테스트 디렉토리)" },
			{ name: "delete_files_from_steps", required: false, desc: "해당 step 인덱스들이 만든 파일 삭제 (쉼표 구분)" },
		]
	},
	{
		type: "sleep",
		label: "Sleep",
		desc: "대기",
		icon: "clock",
		color: "yellow",
		destructive: false,
		requiresTool: false,
		aiUsable: true,
		params: [
			{ name: "seconds", required: true, default: "1", desc: "대기 초 (예 \"30\")" },
		]
	},
	{
		type: "trace_start",
		label: "Trace Start",
		desc: "ftrace 시작",
		icon: "scan-search",
		color: "emerald",
		destructive: false,
		requiresTool: false,
		aiUsable: true,
		params: [
			{ name: "trace_type", required: false, enum: ["ufs", "block", "both", "fsio_ufs", "fsio_block"], default: "ufs", desc: "트레이스 종류 (fsio_* 는 eBPF 기반 — root 필요, 파일명/프로세스 귀속 제공)" },
			{ name: "window_seconds", required: false, default: "1", desc: "수집 윈도우 (초)" },
		]
	},
	{
		type: "trace_stop",
		label: "Trace Stop",
		desc: "ftrace 중지",
		icon: "square",
		color: "emerald",
		destructive: false,
		requiresTool: false,
		aiUsable: true,
		params: [
			{ name: "trace_type", required: false, enum: ["ufs", "block", "both", "fsio_ufs", "fsio_block"], default: "ufs", desc: "trace_start 와 같은 값" },
		]
	},
	{
		type: "app_macro",
		label: "App Macro",
		desc: "앱 매크로 실행",
		icon: "smartphone",
		color: "violet",
		destructive: false,
		requiresTool: false,
		aiUsable: false,
		params: []
	},
	{
		type: "install_apk",
		label: "Install APK",
		desc: "APK 설치",
		icon: "download",
		color: "indigo",
		destructive: false,
		requiresTool: false,
		aiUsable: true,
		params: [
			{ name: "apk_filename", required: true, desc: "tools/apks 안의 파일명" },
			{ name: "grant_permissions", required: false, enum: ["true", "false"], desc: "설치 시 권한 자동 허용" },
		]
	},
	{
		type: "uninstall_apk",
		label: "Uninstall APK",
		desc: "앱 제거",
		icon: "package-minus",
		color: "rose",
		destructive: true,
		requiresTool: false,
		aiUsable: true,
		params: [
			{ name: "package_name", required: true, desc: "제거할 패키지" },
			{ name: "keep_data", required: false, enum: ["true", "false"], desc: "데이터 보존 여부" },
		]
	},
	{
		type: "tap_element",
		label: "Tap Element",
		desc: "요소 기반 탭",
		icon: "mouse-pointer-click",
		color: "fuchsia",
		destructive: false,
		requiresTool: false,
		aiUsable: true,
		params: [
			{ name: "element_resource_id", required: false, desc: "리소스 ID" },
			{ name: "element_text", required: false, desc: "화면에 보이는 글자" },
			{ name: "element_content_desc", required: false, desc: "접근성 라벨" },
			{ name: "element_match_mode", required: false, enum: ["exact", "contains", "prefix", "suffix", "regex"], desc: "매칭 방식 (미지정 시 exact)" },
			{ name: "element_container_id", required: false, desc: "탐색 범위를 좁힐 컨테이너" },
			{ name: "element_index", required: false, desc: "여러 개 매칭 시 N번째" },
			{ name: "x", required: false, desc: "폴백 좌표 X" },
			{ name: "y", required: false, desc: "폴백 좌표 Y" },
		]
	},
	{
		type: "tap",
		label: "Tap",
		desc: "좌표 탭",
		icon: "pointer",
		color: "pink",
		destructive: false,
		requiresTool: false,
		aiUsable: true,
		params: [
			{ name: "x", required: true, desc: "픽셀 좌표 X" },
			{ name: "y", required: true, desc: "픽셀 좌표 Y" },
		]
	},
	{
		type: "text",
		label: "Text Input",
		desc: "텍스트 입력",
		icon: "type",
		color: "teal",
		destructive: false,
		requiresTool: false,
		aiUsable: true,
		params: [
			{ name: "input_text", required: true, desc: "입력할 문자열" },
			{ name: "submit", required: false, enum: ["true", "false"], desc: "입력 후 엔터로 실행" },
		]
	},
	{
		type: "scroll",
		label: "Scroll",
		desc: "피드 스크롤",
		icon: "mouse",
		color: "sky",
		destructive: false,
		requiresTool: false,
		aiUsable: true,
		params: [
			{ name: "direction", required: false, enum: ["up", "down"], desc: "스크롤 방향" },
			{ name: "count", required: false, desc: "스크롤 횟수 (예 \"10\")" },
			{ name: "pause", required: false, desc: "각 스크롤 사이 대기 **초** (예 \"1\"=1초 — 밀리초 아님)" },
			{ name: "duration", required: false, desc: "스와이프 동작 시간 밀리초 (예 \"300\")" },
		]
	},
	{
		type: "key",
		label: "Key",
		desc: "뒤로/홈/제어 키",
		icon: "corner-up-left",
		color: "slate",
		destructive: false,
		requiresTool: false,
		aiUsable: true,
		params: [
			{ name: "keycode", required: true, desc: "키코드 (예 \"4\"=BACK, \"3\"=HOME, \"66\"=ENTER)" },
		]
	},
	{
		type: "stop_app",
		label: "Stop App",
		desc: "앱 완전 종료",
		icon: "circle-stop",
		color: "red",
		destructive: false,
		requiresTool: false,
		aiUsable: true,
		params: [
			{ name: "package_name", required: true, desc: "종료할 패키지" },
		]
	},
	{
		type: "launch_app",
		label: "Launch App",
		desc: "앱 초기화+시작",
		icon: "rocket",
		color: "green",
		destructive: false,
		requiresTool: false,
		aiUsable: true,
		params: [
			{ name: "package_name", required: true, desc: "실행할 패키지 (예 \"com.google.android.youtube\")" },
			{ name: "clear_mode", required: false, enum: ["force_stop", "clear", "cache", "none"], default: "force_stop", desc: "실행 전 초기화 방식" },
			{ name: "wait_seconds", required: false, default: "3", desc: "실행 후 대기 초" },
			{ name: "wait_activity", required: false, desc: "이 activity 가 포커스될 때까지 대기 (선택)" },
		]
	},
];

export const STEP_CONTRACT_BY_TYPE: Record<string, StepContract> = Object.fromEntries(
	STEP_CONTRACTS.map((c) => [c.type, c])
);

// tailwind 색상 계열 → 실제 클래스. tailwind 는 동적 문자열을 purge 하므로
// 클래스 전체를 리터럴로 나열해야 한다.
export const STEP_TYPE_COLORS: Record<string, { bg: string; text: string }> = {
	benchmark: { bg: 'bg-blue-100', text: 'text-blue-700' },
	iotest: { bg: 'bg-cyan-100', text: 'text-cyan-700' },
	shell: { bg: 'bg-gray-100', text: 'text-gray-700' },
	cleanup: { bg: 'bg-orange-100', text: 'text-orange-700' },
	sleep: { bg: 'bg-yellow-100', text: 'text-yellow-700' },
	trace_start: { bg: 'bg-emerald-100', text: 'text-emerald-700' },
	trace_stop: { bg: 'bg-emerald-100', text: 'text-emerald-700' },
	app_macro: { bg: 'bg-violet-100', text: 'text-violet-700' },
	install_apk: { bg: 'bg-indigo-100', text: 'text-indigo-700' },
	uninstall_apk: { bg: 'bg-rose-100', text: 'text-rose-700' },
	tap_element: { bg: 'bg-fuchsia-100', text: 'text-fuchsia-700' },
	tap: { bg: 'bg-pink-100', text: 'text-pink-700' },
	text: { bg: 'bg-teal-100', text: 'text-teal-700' },
	scroll: { bg: 'bg-sky-100', text: 'text-sky-700' },
	key: { bg: 'bg-slate-100', text: 'text-slate-700' },
	stop_app: { bg: 'bg-red-100', text: 'text-red-700' },
	launch_app: { bg: 'bg-green-100', text: 'text-green-700' },
};
