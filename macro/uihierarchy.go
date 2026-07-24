package macro

import (
	"context"
	"encoding/xml"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"agent/adb"
)

// UIElement 은 uiautomator 계층에서 뽑아낸 화면 요소 하나를 나타낸다.
// CenterX/CenterY 는 bounds 의 중점으로, 요소 기반 탭의 목표 좌표다.
type UIElement struct {
	ResourceID  string `json:"resourceId"`
	Text        string `json:"text"`
	ContentDesc string `json:"contentDesc"`
	Class       string `json:"class"`
	Clickable   bool   `json:"clickable"`
	CenterX     int    `json:"centerX"`
	CenterY     int    `json:"centerY"`
	// Bounds = [x1, y1, x2, y2] (좌상단, 우하단)
	Bounds [4]int `json:"bounds"`
	// ancestorIDs 는 이 요소의 조상 노드 resource-id 목록(빈 값 제외).
	// 컨테이너 스코프 매칭(element_container_id)에 쓴다. JSON 으로 내보내지 않는다.
	ancestorIDs []string
	// ContainerID 는 이 요소를 담은 "가장 가까운 스크롤 컨테이너"의 resource-id.
	// 없으면 빈 문자열. 유저가 라이브 화면에서 요소를 클릭할 때 컨테이너를 자동
	// 채우기 위한 값 — 유저는 id 를 직접 알 필요가 없다.
	ContainerID string `json:"containerId"`
}

// uiNode 는 uiautomator dump XML 의 <node> 요소를 언마샬링하기 위한 내부 구조체다.
// 계층이 중첩되므로 Children 로 재귀 파싱한다.
type uiNode struct {
	ResourceID  string   `xml:"resource-id,attr"`
	Text        string   `xml:"text,attr"`
	ContentDesc string   `xml:"content-desc,attr"`
	Class       string   `xml:"class,attr"`
	Clickable   string   `xml:"clickable,attr"`
	Scrollable  string   `xml:"scrollable,attr"`
	Bounds      string   `xml:"bounds,attr"`
	Children    []uiNode `xml:"node"`
}

// uiHierarchy 는 dump XML 의 루트(<hierarchy>) 요소다.
type uiHierarchy struct {
	Nodes []uiNode `xml:"node"`
}

// DumpUIElements 는 디바이스의 현재 화면을 uiautomator 로 덤프해
// 파싱된 요소 목록을 반환한다. clickableOnly=true 면 clickable="true" 요소만 남긴다.
//
// dump 실행 패턴은 기존 dumpUITexts / getDeviceUIText 와 동일하다
// (uiautomator dump /sdcard/ui.xml → cat). 시그니처를 건드리지 않기 위해 별도 함수로 둔다.
func DumpUIElements(ctx context.Context, dev *adb.Device, clickableOnly bool) ([]UIElement, error) {
	if _, err := dev.Shell(ctx, "uiautomator dump /sdcard/ui.xml"); err != nil {
		return nil, fmt.Errorf("uiautomator dump: %w", err)
	}
	out, err := dev.Shell(ctx, "cat /sdcard/ui.xml")
	if err != nil {
		return nil, fmt.Errorf("cat ui.xml: %w", err)
	}
	return parseUIElements(out, clickableOnly)
}

// parseUIElements 는 uiautomator dump XML 문자열을 파싱해 요소 목록으로 변환한다.
// dump 실행과 분리해 두어 단위 테스트로 직접 XML 을 넣어 검증할 수 있다.
func parseUIElements(xmlStr string, clickableOnly bool) ([]UIElement, error) {
	// uiautomator dump 는 앞에 "UI hierchary dumped to: ..." 같은 로그가 붙을 수 있으므로
	// 실제 XML 시작(<?xml 또는 <hierarchy)부터 자른다.
	if idx := strings.Index(xmlStr, "<?xml"); idx > 0 {
		xmlStr = xmlStr[idx:]
	} else if idx := strings.Index(xmlStr, "<hierarchy"); idx > 0 {
		xmlStr = xmlStr[idx:]
	}

	var root uiHierarchy
	if err := xml.Unmarshal([]byte(xmlStr), &root); err != nil {
		return nil, fmt.Errorf("parse ui xml: %w", err)
	}

	var elements []UIElement
	// ancestors: 현재 노드까지 내려오는 경로의 resource-id 스택.
	// container: 가장 가까운 "스크롤 컨테이너(scrollable=true + id 있음)"의 resource-id.
	var walk func(n *uiNode, ancestors []string, container string)
	walk = func(n *uiNode, ancestors []string, container string) {
		clickable := n.Clickable == "true"
		if !clickableOnly || clickable {
			bounds, ok := parseBounds(n.Bounds)
			if ok {
				// 텍스트/resource-id/content-desc 가 하나도 없는 요소는
				// 셀렉터로 재식별할 수 없으므로 제외한다.
				if n.ResourceID != "" || n.Text != "" || n.ContentDesc != "" {
					// ancestors 는 재귀 중 재사용되는 슬라이스라 복사해 보관한다.
					anc := make([]string, len(ancestors))
					copy(anc, ancestors)
					elements = append(elements, UIElement{
						ResourceID:  n.ResourceID,
						Text:        n.Text,
						ContentDesc: n.ContentDesc,
						Class:       n.Class,
						Clickable:   clickable,
						CenterX:     (bounds[0] + bounds[2]) / 2,
						CenterY:     (bounds[1] + bounds[3]) / 2,
						Bounds:      bounds,
						ancestorIDs: anc,
						ContainerID: container,
					})
				}
			}
		}
		// 자식으로 내려갈 때 현재 노드의 resource-id 를 조상 스택에 추가.
		childAncestors := ancestors
		if n.ResourceID != "" {
			childAncestors = append(ancestors, n.ResourceID)
		}
		// 현재 노드가 스크롤 컨테이너이고 id 가 있으면, 자식들의 컨테이너로 갱신.
		childContainer := container
		if n.Scrollable == "true" && n.ResourceID != "" {
			childContainer = n.ResourceID
		}
		for i := range n.Children {
			walk(&n.Children[i], childAncestors, childContainer)
		}
	}
	for i := range root.Nodes {
		walk(&root.Nodes[i], nil, "")
	}
	return elements, nil
}

// parseBounds 는 "[x1,y1][x2,y2]" 형식 문자열을 정수 4개로 파싱한다.
// 형식이 맞지 않으면 ok=false.
func parseBounds(s string) ([4]int, bool) {
	var b [4]int
	// "[x1,y1][x2,y2]" → 대괄호/쉼표를 공백으로 치환 후 필드 4개 파싱
	replacer := strings.NewReplacer("[", " ", "]", " ", ",", " ")
	fields := strings.Fields(replacer.Replace(s))
	if len(fields) != 4 {
		return b, false
	}
	for i, f := range fields {
		v, err := strconv.Atoi(f)
		if err != nil {
			return b, false
		}
		b[i] = v
	}
	return b, true
}

// ElementSelector 는 tap_element 재생 시 요소를 찾기 위한 셀렉터 규칙이다.
// MatchMode 가 "exact"/빈 값이면 기존 동작(완전일치 후 부분일치 폴백),
// 그 외("contains"/"prefix"/"suffix"/"regex")면 Text/ContentDesc 를 패턴으로 해석한다.
// ContainerID 지정 시 해당 resource-id 컨테이너 하위 요소로 검색을 한정한다.
// Index 는 매칭 후보가 여러 개일 때 N번째(0-based)를 고른다.
type ElementSelector struct {
	ResourceID  string
	Text        string
	ContentDesc string
	MatchMode   string // "", "exact", "contains", "prefix", "suffix", "regex"
	Index       int
	ContainerID string
}

// findElementBySelector 는 기존 호출부 호환용 얇은 래퍼 — exact 모드로 위임한다.
func findElementBySelector(elements []UIElement, resourceID, text, contentDesc string) *UIElement {
	return findElement(elements, ElementSelector{
		ResourceID: resourceID, Text: text, ContentDesc: contentDesc,
	})
}

// findElement 는 셀렉터 규칙으로 요소를 찾는다. 매칭 없으면 nil.
//
// 동작:
//  1. ContainerID 지정 시, 조상에 그 id 를 가진 요소만 후보로 남긴다.
//  2. exact 모드: resource-id → text → content-desc 순, 각각 완전일치 후 부분일치.
//     (기존 동작 유지 — Index 는 무시하고 첫 매칭 반환)
//  3. 패턴 모드(contains/prefix/suffix/regex): resource-id 는 항상 완전일치로 먼저
//     좁히고(있으면), text/content-desc 는 패턴으로 매칭한다. 매칭된 후보 중 Index 번째.
func findElement(elements []UIElement, sel ElementSelector) *UIElement {
	// 1) 컨테이너 스코프
	pool := elements
	if sel.ContainerID != "" {
		scoped := pool[:0:0]
		for i := range pool {
			if containsStr(pool[i].ancestorIDs, sel.ContainerID) {
				scoped = append(scoped, pool[i])
			}
		}
		pool = scoped
	}

	mode := sel.MatchMode
	if mode == "" || mode == "exact" {
		return findExact(pool, sel.ResourceID, sel.Text, sel.ContentDesc)
	}

	// 패턴 모드 — resource-id 로 먼저 좁히고 text/desc 패턴으로 매칭.
	var candidates []*UIElement
	for i := range pool {
		el := &pool[i]
		if sel.ResourceID != "" && el.ResourceID != sel.ResourceID {
			continue
		}
		matched := false
		if sel.Text != "" && el.Text != "" && matchPattern(el.Text, sel.Text, mode) {
			matched = true
		}
		if !matched && sel.ContentDesc != "" && el.ContentDesc != "" && matchPattern(el.ContentDesc, sel.ContentDesc, mode) {
			matched = true
		}
		// text/desc 패턴이 둘 다 비었고 resource-id 만으로 좁힌 경우도 후보로.
		if sel.Text == "" && sel.ContentDesc == "" && sel.ResourceID != "" {
			matched = true
		}
		if matched {
			candidates = append(candidates, el)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	idx := sel.Index
	if idx < 0 || idx >= len(candidates) {
		idx = 0
	}
	return candidates[idx]
}

// findExact 는 기존 우선순위 매칭(완전일치 후 부분일치)이다.
func findExact(elements []UIElement, resourceID, text, contentDesc string) *UIElement {
	if resourceID != "" {
		for i := range elements {
			if elements[i].ResourceID == resourceID {
				return &elements[i]
			}
		}
	}
	if text != "" {
		for i := range elements {
			if elements[i].Text == text {
				return &elements[i]
			}
		}
		for i := range elements {
			if elements[i].Text != "" && strings.Contains(elements[i].Text, text) {
				return &elements[i]
			}
		}
	}
	if contentDesc != "" {
		for i := range elements {
			if elements[i].ContentDesc == contentDesc {
				return &elements[i]
			}
		}
		for i := range elements {
			if elements[i].ContentDesc != "" && strings.Contains(elements[i].ContentDesc, contentDesc) {
				return &elements[i]
			}
		}
	}
	return nil
}

// matchPattern 은 value 가 pattern 에 mode 방식으로 매칭되는지 반환한다.
func matchPattern(value, pattern, mode string) bool {
	switch mode {
	case "contains":
		return strings.Contains(value, pattern)
	case "prefix":
		return strings.HasPrefix(value, pattern)
	case "suffix":
		return strings.HasSuffix(value, pattern)
	case "regex":
		re, err := regexp.Compile(pattern)
		if err != nil {
			return false
		}
		return re.MatchString(value)
	default: // exact
		return value == pattern
	}
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
