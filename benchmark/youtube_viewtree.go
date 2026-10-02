package benchmark

import (
	"regexp"
	"strconv"
	"strings"

	"agent/macro"
)

// YouTube 재생 화면의 광고 판정 — `dumpsys activity top` 의 뷰 트리로 한다.
//
// 재생 중에는 화면이 계속 움직여 `uiautomator dump` 가 idle 을 못 잡고 **12초를
// 기다린 뒤 실패**한다(SM-S938N 재현). 그동안 기기에서 uiautomator 가 계속 돌아
// 측정 부하가 되고, 표본 간격이 3초가 아니라 ~17초로 벌어져 짧은 광고를 놓쳤다.
// dumpsys 는 idle 을 기다리지 않아 0.1초 안에 끝난다.
//
// 판정 기준 (로그아웃 계정 실기기, 광고 2개 연속 → 본영상 16표본 전부 화면과 일치):
//   - 광고 중      = ad_progress_text 가 보인다
//   - 건너뛰기 가능 = skip_ad_button 이 보인다
//
// ⚠ "보인다" 는 **조상까지 전부 VISIBLE** 이어야 한다. 버튼이 사라진 뒤에도
// skip_ad_button_container/_text 는 자기 플래그가 V 로 남아 있었다 — 자기 플래그만
// 보면 광고가 끝났는데도 광고로 판정한다.
// ⚠ 좌표는 **부모 기준**이다. 탭하려면 조상 좌표를 더해 화면 절대좌표로 바꾼다.

const youtubePackage = "com.google.android.youtube"

// youtubeView — 뷰 트리의 한 노드. Bounds 는 화면 절대좌표, Visible 은 조상 포함 실효값.
type youtubeView struct {
	ID      string
	Visible bool
	Bounds  [4]int
}

// 예: `  android.widget.FrameLayout{d043a4f VFE...... ........ 0,128-1440,938 #7f0b18ba app:id/watch_player}`
var youtubeViewLineRE = regexp.MustCompile(`^(\s*)\S+\{[0-9a-f]+ ([VIG])\S* \S+ (-?\d+),(-?\d+)-(-?\d+),(-?\d+)([^}]*)\}`)
var youtubeViewIDRE = regexp.MustCompile(`app:id/([\w.]+)`)

// parseYoutubeViewTree — dumpsys activity top 출력에서 YouTube activity 의 뷰 트리만 읽는다.
// 다른 앱(런처 등) activity 도 같이 나오므로 패키지로 고른다. 못 찾으면 false.
func parseYoutubeViewTree(out string) ([]youtubeView, bool) {
	lines := strings.Split(out, "\n")
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "ACTIVITY "+youtubePackage+"/") {
			start = i
		}
	}
	if start < 0 {
		return nil, false
	}
	// ⚠ activity top 은 멈춘(stopped) activity 도 출력한다. YouTube 가 백그라운드에
	// 있을 때 그 뷰 트리로 "재생 중" 을 판정하면 안 되므로 resumed 일 때만 쓴다.
	resumed := false
	for _, l := range lines[start+1:] {
		if strings.Contains(l, "mResumed=") {
			resumed = strings.Contains(l, "mResumed=true")
			break
		}
	}
	if !resumed {
		return nil, false
	}
	type frame struct {
		indent, left, top int
		visible           bool
	}
	var stack []frame
	var views []youtubeView
	headerIndent := -1 // "View Hierarchy:" 줄의 들여쓰기. 이보다 얕은 줄이 나오면 트리 끝
	for _, l := range lines[start+1:] {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "ACTIVITY ") || strings.HasPrefix(trimmed, "TASK ") {
			break
		}
		lineIndent := len(l) - len(strings.TrimLeft(l, " "))
		if trimmed == "View Hierarchy:" {
			headerIndent = lineIndent
			continue
		}
		if headerIndent < 0 || trimmed == "" {
			continue
		}
		if lineIndent <= headerIndent {
			break // 뷰 트리가 끝났다 (Looper 등 다음 섹션)
		}
		m := youtubeViewLineRE.FindStringSubmatch(l)
		if m == nil {
			// ⚠ 트리 중간에 뷰가 아닌 부가 줄(`82116979(0,0,1440,810)` 등)이 낀다.
			// 여기서 끝내면 트리 앞부분만 읽어 광고 뷰를 통째로 놓친다.
			continue
		}
		indent := len(m[1])
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		parent := frame{visible: true}
		if len(stack) > 0 {
			parent = stack[len(stack)-1]
		}
		l0, _ := strconv.Atoi(m[3])
		t0, _ := strconv.Atoi(m[4])
		r0, _ := strconv.Atoi(m[5])
		b0, _ := strconv.Atoi(m[6])
		f := frame{indent: indent, left: parent.left + l0, top: parent.top + t0,
			visible: parent.visible && m[2] == "V"}
		stack = append(stack, f)
		v := youtubeView{Visible: f.visible, Bounds: [4]int{f.left, f.top, parent.left + r0, parent.top + b0}}
		if id := youtubeViewIDRE.FindStringSubmatch(m[7]); id != nil {
			v.ID = id[1]
		}
		views = append(views, v)
	}
	return views, len(views) > 0
}

// youtubeTreeState — 뷰 트리로 본 일반 영상 재생 화면 상태.
//
//	"ad"     : 재생 광고 중 (skip 이 nil 이 아니면 건너뛰기 버튼 위치)
//	"player" : 광고 없이 플레이어가 보인다 — 실제 재생 여부는 호출자가 media_session 으로 본다
//	""       : 판정 불가 (플레이어가 없음 등) — 호출자가 기존 경로로 넘어간다
func youtubeTreeState(views []youtubeView) (string, *macro.UIElement) {
	visible := func(id string) *youtubeView {
		for i := range views {
			v := &views[i]
			if v.ID == id && v.Visible && v.Bounds[2] > v.Bounds[0] && v.Bounds[3] > v.Bounds[1] {
				return v
			}
		}
		return nil
	}
	if visible("ad_progress_text") != nil {
		var skip *macro.UIElement
		if b := visible("skip_ad_button"); b != nil {
			skip = &macro.UIElement{ResourceID: youtubePackage + ":id/skip_ad_button", Bounds: b.Bounds,
				CenterX: (b.Bounds[0] + b.Bounds[2]) / 2, CenterY: (b.Bounds[1] + b.Bounds[3]) / 2}
		}
		return "ad", skip
	}
	if visible("watch_player") != nil {
		return "player", nil
	}
	return "", nil
}
