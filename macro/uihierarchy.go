package macro

import (
	"context"
	"encoding/xml"
	"fmt"
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
}

// uiNode 는 uiautomator dump XML 의 <node> 요소를 언마샬링하기 위한 내부 구조체다.
// 계층이 중첩되므로 Children 로 재귀 파싱한다.
type uiNode struct {
	ResourceID  string   `xml:"resource-id,attr"`
	Text        string   `xml:"text,attr"`
	ContentDesc string   `xml:"content-desc,attr"`
	Class       string   `xml:"class,attr"`
	Clickable   string   `xml:"clickable,attr"`
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
	var walk func(n *uiNode)
	walk = func(n *uiNode) {
		clickable := n.Clickable == "true"
		if !clickableOnly || clickable {
			bounds, ok := parseBounds(n.Bounds)
			if ok {
				// 텍스트/resource-id/content-desc 가 하나도 없는 요소는
				// 셀렉터로 재식별할 수 없으므로 제외한다.
				if n.ResourceID != "" || n.Text != "" || n.ContentDesc != "" {
					elements = append(elements, UIElement{
						ResourceID:  n.ResourceID,
						Text:        n.Text,
						ContentDesc: n.ContentDesc,
						Class:       n.Class,
						Clickable:   clickable,
						CenterX:     (bounds[0] + bounds[2]) / 2,
						CenterY:     (bounds[1] + bounds[3]) / 2,
						Bounds:      bounds,
					})
				}
			}
		}
		for i := range n.Children {
			walk(&n.Children[i])
		}
	}
	for i := range root.Nodes {
		walk(&root.Nodes[i])
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

// findElementBySelector 는 셀렉터 우선순위(resource-id → text → content-desc)로
// 요소 목록에서 첫 매칭을 찾는다. 매칭 없으면 nil.
//
// 각 셀렉터는 완전일치 우선, 없으면 부분일치(Contains)로 fallback 한다.
// resourceID/text/contentDesc 중 비어있지 않은 것만 조건으로 사용한다.
func findElementBySelector(elements []UIElement, resourceID, text, contentDesc string) *UIElement {
	// 우선순위 1: resource-id 완전일치
	if resourceID != "" {
		for i := range elements {
			if elements[i].ResourceID == resourceID {
				return &elements[i]
			}
		}
	}
	// 우선순위 2: text 완전일치 → 부분일치
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
	// 우선순위 3: content-desc 완전일치 → 부분일치
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
