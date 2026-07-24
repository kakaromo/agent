package macro

import "testing"

const sampleUIXML = `<?xml version='1.0' encoding='UTF-8' standalone='yes' ?>
<hierarchy rotation="0">
  <node index="0" text="" resource-id="" class="android.widget.FrameLayout" package="com.google.android.youtube" content-desc="" clickable="false" bounds="[0,0][1080,2400]">
    <node index="0" text="검색" resource-id="com.google.android.youtube:id/menu_search" class="android.widget.Button" content-desc="검색" clickable="true" bounds="[900,100][1000,200]">
    </node>
    <node index="1" text="lofi hip hop radio" resource-id="com.google.android.youtube:id/title" class="android.widget.TextView" content-desc="" clickable="true" bounds="[40,300][1040,400]">
    </node>
    <node index="2" text="" resource-id="" class="android.view.View" content-desc="" clickable="true" bounds="[0,2000][1080,2100]">
    </node>
  </node>
</hierarchy>`

func TestParseUIElements_ClickableOnly(t *testing.T) {
	els, err := parseUIElements(sampleUIXML, true)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	// clickable=true 이면서 셀렉터(text/id/desc) 있는 요소만: 검색 버튼, 제목 TextView
	if len(els) != 2 {
		t.Fatalf("expected 2 elements, got %d: %+v", len(els), els)
	}
	search := els[0]
	if search.Text != "검색" || search.ResourceID != "com.google.android.youtube:id/menu_search" {
		t.Errorf("unexpected search element: %+v", search)
	}
	// bounds [900,100][1000,200] → center (950,150)
	if search.CenterX != 950 || search.CenterY != 150 {
		t.Errorf("expected center (950,150), got (%d,%d)", search.CenterX, search.CenterY)
	}
}

func TestParseUIElements_All(t *testing.T) {
	els, err := parseUIElements(sampleUIXML, false)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	// clickableOnly=false 여도 셀렉터 없는 요소(루트 FrameLayout, index=2 View)는 제외
	if len(els) != 2 {
		t.Fatalf("expected 2 elements, got %d", len(els))
	}
}

func TestFindElementBySelector(t *testing.T) {
	els, _ := parseUIElements(sampleUIXML, true)

	// resource-id 우선
	if e := findElementBySelector(els, "com.google.android.youtube:id/title", "", ""); e == nil || e.Text != "lofi hip hop radio" {
		t.Errorf("resource-id match failed: %+v", e)
	}
	// text 부분일치
	if e := findElementBySelector(els, "", "lofi", ""); e == nil || e.CenterX != 540 {
		t.Errorf("text partial match failed: %+v", e)
	}
	// content-desc 완전일치
	if e := findElementBySelector(els, "", "", "검색"); e == nil || e.CenterX != 950 {
		t.Errorf("content-desc match failed: %+v", e)
	}
	// 매칭 없음
	if e := findElementBySelector(els, "", "존재하지않음", ""); e != nil {
		t.Errorf("expected nil, got %+v", e)
	}
}

// 동적 피드를 흉내낸 XML — 컨테이너(results) 안에 콘텐츠성 desc 카드 2개.
const feedXML = `<?xml version='1.0' encoding='UTF-8' standalone='yes' ?>
<hierarchy rotation="0">
  <node class="A" resource-id="com.yt:id/root" clickable="false" bounds="[0,0][1440,3120]">
    <node class="B" resource-id="com.yt:id/results" clickable="false" bounds="[0,300][1440,3000]">
      <node text="" resource-id="" content-desc="임금님 찜닭 - Shorts 동영상 재생" class="C" clickable="true" bounds="[45,313][705,1303]"></node>
      <node text="" resource-id="" content-desc="자이로드롭 - Shorts 동영상 재생" class="C" clickable="true" bounds="[735,313][1395,1303]"></node>
    </node>
    <node text="" resource-id="com.yt:id/menu_search" content-desc="검색" class="D" clickable="true" bounds="[1300,100][1400,200]"></node>
  </node>
</hierarchy>`

func TestFindElement_SuffixPattern(t *testing.T) {
	els, _ := parseUIElements(feedXML, true)
	// suffix 패턴 "동영상 재생" — 콘텐츠 제목 무관, 첫 번째(Index 0)
	e := findElement(els, ElementSelector{ContentDesc: "동영상 재생", MatchMode: "suffix", Index: 0})
	if e == nil || e.CenterX != (45+705)/2 {
		t.Fatalf("suffix index0 실패: %+v", e)
	}
	// 두 번째 카드
	e2 := findElement(els, ElementSelector{ContentDesc: "동영상 재생", MatchMode: "suffix", Index: 1})
	if e2 == nil || e2.CenterX != (735+1395)/2 {
		t.Fatalf("suffix index1 실패: %+v", e2)
	}
}

func TestFindElement_ContainerScope(t *testing.T) {
	els, _ := parseUIElements(feedXML, true)
	// results 컨테이너 안에서만 검색 → 검색 버튼(컨테이너 밖)은 제외
	e := findElement(els, ElementSelector{
		ContentDesc: "동영상 재생", MatchMode: "suffix",
		ContainerID: "com.yt:id/results", Index: 0,
	})
	if e == nil || e.ContentDesc != "임금님 찜닭 - Shorts 동영상 재생" {
		t.Fatalf("container scope 실패: %+v", e)
	}
	// 컨테이너 밖 검색 버튼은 이 컨테이너 스코프에서 안 잡혀야 함
	miss := findElement(els, ElementSelector{
		ContentDesc: "검색", MatchMode: "contains",
		ContainerID: "com.yt:id/results",
	})
	if miss != nil {
		t.Fatalf("컨테이너 밖 요소가 잡힘: %+v", miss)
	}
}

func TestFindElement_RegexAndExactBackcompat(t *testing.T) {
	els, _ := parseUIElements(feedXML, true)
	// regex
	e := findElement(els, ElementSelector{ContentDesc: "동영상 재생$", MatchMode: "regex", Index: 1})
	if e == nil || e.CenterX != (735+1395)/2 {
		t.Fatalf("regex 실패: %+v", e)
	}
	// exact 모드(빈 MatchMode)는 기존 동작 — 검색 버튼 완전일치
	e2 := findElement(els, ElementSelector{ContentDesc: "검색"})
	if e2 == nil || e2.ResourceID != "com.yt:id/menu_search" {
		t.Fatalf("exact 호환 실패: %+v", e2)
	}
}

func TestParseBounds(t *testing.T) {
	b, ok := parseBounds("[10,20][110,220]")
	if !ok || b != [4]int{10, 20, 110, 220} {
		t.Errorf("parseBounds failed: %v %v", b, ok)
	}
	if _, ok := parseBounds("garbage"); ok {
		t.Errorf("expected parse failure for garbage")
	}
}
