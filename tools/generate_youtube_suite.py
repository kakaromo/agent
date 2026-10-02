"""YouTube 시나리오를 생성한다. 실행·기기 설정·DB 저장은 수행하지 않는다."""
import copy
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / "docs/examples/youtube-suite"
OUT.mkdir(parents=True, exist_ok=True)
PKG = "com.google.android.youtube"
BASE = json.loads((ROOT / "docs/examples/youtube-quality-360-720-1080.scenario.json").read_text(encoding="utf-8"))
START, END = BASE["steps"][0], BASE["steps"][-1]
VIDEO = BASE["steps"][1:8]

def step(kind, label, **params):
    return {"type": kind, "params": {"label": label, **{k: str(v) for k, v in params.items()}}}

def yt(action, label, **params):
    return step("youtube", label, action=action, **params)

def launch(mode="force_stop", package=PKG):
    return step("launch_app", "앱 실행" if mode == "force_stop" else "앱 복귀", package_name=package, clear_mode=mode, wait_seconds=3)

def stop(): return step("stop_app", "YouTube 종료", package_name=PKG)
def wait(n): return step("sleep", f"{n}초 대기", seconds=n)
def key(n, label): return step("key", label, keycode=n)
def tap(label, pattern): return step("tap_element", label, element_content_desc=pattern, element_match_mode="regex")
def watch(n=30, shorts=False): return yt("watch", f"{'Shorts' if shorts else '본영상'} {n}초 관측 · 광고 제외", seconds=n, surface="shorts" if shorts else "video", ad_timeout=180)
def control(target): return yt("control", {"play":"재생 확인","pause":"일시정지 확인","fullscreen":"전체화면 확인","inline":"일반화면 확인","next":"다음 영상 요청","previous":"이전 영상 요청","minimize":"미니플레이어 전환 요청"}[target], target=target)
def scroll(n=1, direction="down", pause=1): return step("scroll", f"{direction} 방향 {n}회 이동", direction=direction, count=n, pause=pause, duration=400)
def feed(n=1, direction="down"): return yt("feed", f"{direction} 방향 {n}회 탐색 · 광고 카드 기록", count=n, direction=direction)
def quality(q): return yt("quality", f"{q} 선택·확인", quality=q)
def shorts(): return [launch(), tap("Shorts 탭", "^(Shorts|쇼츠)(,.*)?$"), wait(3)]
def search(term): return [launch(), tap("검색창 열기", "^(검색|Search)$"), step("text", f"{term} 검색", input_text=term, submit="true"), wait(3), yt("inspect", "검색 결과 광고 기록")]

catalog = []
scenarios = []
COMMON = " FSIO UFS+DRAM 수집. 재생 시간은 광고·미확인을 제외한 표본 간 관측 시간이며 마지막 조회 간격만큼 늘어날 수 있습니다. 광고 조회가 측정 부하를 추가합니다. 잠금 해제·YouTube 초기 안내 완료가 필요합니다. 반복 중 같은 영상이 다시 나올 수 있습니다. 전체 반복·장시간 실행 검증 전이며 개별 공통 동작의 검증과 구분합니다."

def add(slug, name, category, setup, body, count=1, tail=None, note="", packages=None):
    index = len(scenarios)+1
    steps = [copy.deepcopy(START), *copy.deepcopy(setup)]
    first = len(steps)
    steps.extend(copy.deepcopy(body))
    loops = [{"startStep": first, "endStep": len(steps)-1, "count": count}] if count > 1 else []
    steps.extend(copy.deepcopy(tail or []))
    steps.extend([stop(), copy.deepcopy(END)])
    description = note + f" 반복 블록 {count}회. " + " → ".join(s["params"]["label"] for s in body) + COMMON
    obj = {"schemaVersion": 1, "kind": "scenario", "name": f"YouTube_{index:02}_{name}_DRAM", "description": description, "repeatCount": 1, "steps": steps, "loops": loops, "requirements": {"packages": packages or [PKG], "traceType": "fsio_ufs"}}
    filename = f"{index:02}-{slug}.scenario.json"
    (OUT/filename).write_text(json.dumps(obj, ensure_ascii=False, indent=2)+"\n", encoding="utf-8")
    scenarios.append(obj)
    catalog.append({"id":f"A{index:02}", "name":name, "category":category, "status":"template-created", "file":filename, "deviceValidation":"not-run-full", "preconditions":["잠금 해제", "YouTube 초기 안내 완료", "FSIO·DRAM 지원 기기"], "success":"각 단계 성공 및 trace 종료. next/previous/seek는 요청 전송과 후속 재생만 확인하며 영상 ID·이동 위치는 별도 확인.", "notes":note})

add("cold-start", "앱종료_재실행_5회", "기본", [], [launch(), wait(5), yt("inspect", "홈 광고 기록"), stop()], 5, note="앱 데이터·캐시는 삭제하지 않습니다. 프로세스 재시작 테스트입니다.")
add("background", "홈이동_복귀_5회", "기본", VIDEO, [watch(), key(3,"홈 화면 이동"), wait(10), launch("none"), control("play"), watch()], 5)
add("other-app", "Chrome이동_복귀_5회", "기본", VIDEO, [watch(), launch("none", "com.android.chrome"), wait(10), launch("none"), control("play"), watch()], 5, packages=[PKG,"com.android.chrome"], note="Chrome 현재 화면을 열고 돌아옵니다. 웹 탐색이나 Chrome 강제 종료는 하지 않습니다.")
add("long-background", "백그라운드_2분후복귀", "기본", VIDEO, [watch(), key(3,"홈 화면 이동"), wait(120), launch("none"), control("play"), watch(60)])
add("pause", "일시정지_재생_5회", "기본", VIDEO, [watch(20), control("pause"), wait(10), control("play")], 5, [watch(20)])
add("fullscreen", "전체화면_전환_5회", "기본", VIDEO, [control("fullscreen"), watch(20), control("inline"), watch(20)], 5)
add("seek-both", "앞뒤_구간이동_5회", "기본", VIDEO, [key(90,"앞으로 이동 키 요청"), watch(20), key(89,"뒤로 이동 키 요청"), watch(20)], 5, note="키 이벤트를 보내고 재생을 확인합니다. 실제 위치 이동 여부·이동 초 수는 자동 검증하지 않습니다.")
add("next-video", "다음영상_5회", "기본", VIDEO, [control("next"), watch(30)], 5, note="현재 플레이어의 다음 영상 버튼을 사용합니다.")
add("previous-next", "이전_다음영상_3회", "기본", VIDEO, [control("next"), watch(20), control("previous"), watch(20)], 3, note="이전 영상 버튼이 제공되는 재생 이력이 필요합니다.")
add("miniplayer-feed", "미니플레이어_피드탐색", "기본", VIDEO, [watch(), control("minimize"), feed(10), feed(5,"up")], note="미니플레이어 전환 요청 후 피드를 탐색합니다. 축소 상태의 실제 백그라운드 재생은 검증하지 않습니다.")
add("share-dismiss", "공유메뉴_열기닫기_3회", "기본", VIDEO, [tap("공유 메뉴 열기", "^(공유|Share)$"), wait(3), key(4,"공유 메뉴 닫기"), watch(10)], 3, note="공유 대상 선택이나 메시지 전송은 하지 않습니다.")
add("long-10m", "연속재생_10분", "반복·장시간", VIDEO, [watch(600)])
add("long-30m", "연속재생_30분", "반복·장시간", VIDEO, [watch(1800)])
add("same-selection", "같은영상_재선택_3회", "반복·장시간", [], [*VIDEO, watch(60), stop()], 3, note="같은 검색 제목의 영상을 다시 선택합니다. 재생 위치 초기화나 영상 ID 동일성은 보장하지 않습니다.")
add("rapid-next", "빠른영상전환_20회", "반복·장시간", VIDEO, [control("next"), watch(6)], 20)
for q in ("360p","720p","1080p"):
    add(f"fixed-{q}", f"고정화질_{q}_3분", "반복·장시간", VIDEO, [quality(q), watch(180)], note="요청 화질을 지원하는 영상과 필요 시 Tesseract eng/kor가 필요합니다.")
add("quality-alternate", "저고화질_교차_3회", "반복·장시간", VIDEO, [quality("360p"),watch(30),quality("1080p"),watch(30)], 3)
add("search-terms", "검색어_3종탐색", "기본", [], [s for term in ("lofi hip hop", "nature documentary", "space documentary") for s in [*search(term),feed(3)]], note="검색어마다 앱을 재실행하여 검색창 상태를 초기화합니다.")
add("search-scroll", "검색결과_30회탐색", "반복·장시간", search("lofi hip hop"), [feed(30)])
add("home-scroll", "홈피드_장시간탐색", "반복·장시간", [launch(),tap("홈 탭", "^(홈|Home)(,.*)?$")], [feed(20),feed(10,"up")], 3)
add("refresh", "홈_새로고침요청_10회", "기본", [launch(),tap("홈 탭", "^(홈|Home)(,.*)?$")], [scroll(1,"up"),wait(3),yt("inspect","피드 광고 기록")], 10, note="최상단에서 아래로 당기는 입력을 보냅니다. 서버의 새 추천 응답 여부는 검증하지 않습니다.")
add("shorts-fast", "Shorts_빠르게넘기기_30회", "반복·장시간", shorts(), [feed(30)], note="각 카드의 광고 표시를 확인하고 넘깁니다. UI 조회 시간 때문에 일정한 1초 간격은 아닙니다.")
add("shorts-back", "Shorts_이전다음_10회", "반복·장시간", shorts(), [scroll(),watch(6,True),scroll(1,"up"),watch(6,True)], 10)
add("shorts-pause", "Shorts_일시정지_5회", "기본", shorts(), [watch(10,True),control("pause"),wait(5),control("play")], 5, [watch(10,True)])
add("shorts-background", "Shorts_홈이동복귀_5회", "기본", shorts(), [watch(10,True),key(3,"홈 화면 이동"),wait(10),launch("none"),control("play"),watch(10,True)], 5)
add("shorts-loop", "Shorts_한카드_10분", "반복·장시간", shorts(), [watch(600,True)], note="다음 카드 입력 없이 현재 Shorts를 관측합니다. 광고 카드는 자동으로 넘깁니다.")
add("video-shorts", "일반영상_Shorts교차_3회", "반복·장시간", [], [*VIDEO,watch(15),*shorts(),watch(15,True)], 3)
add("network-video", "네트워크_끊김복구", "복구", VIDEO, [watch(30),yt("network_cycle","네트워크 10초 차단·원복",seconds=10),wait(5),control("play"),watch(60)], note="Wi-Fi·모바일 데이터 원래 상태를 저장하고 취소·실패 시에도 복원을 시도합니다. 캐시로 재생이 지속될 수 있어 버퍼링 발생 자체를 성공 조건으로 쓰지 않습니다.")
add("network-shorts", "Shorts_네트워크복구", "복구", shorts(), [watch(10,True),yt("network_cycle","네트워크 10초 차단·원복",seconds=10),wait(5),control("play"),watch(30,True)])
add("network-repeat", "네트워크_끊김복구_3회", "복구", VIDEO, [yt("network_cycle","네트워크 10초 차단·원복",seconds=10),wait(5),control("play"),watch(30)], 3)
add("fullscreen-pause", "전체화면_일시정지_5회", "기본", VIDEO, [control("fullscreen"),watch(20),control("pause"),wait(10),control("play"),watch(20),control("inline")], 5)
add("fullscreen-seek", "전체화면_구간이동_5회", "기본", VIDEO, [control("fullscreen"),key(90,"앞으로 이동 키 요청"),watch(20),key(89,"뒤로 이동 키 요청"),watch(20),control("inline")], 5, note="구간 이동 키를 보내고 재생을 확인하며 실제 이동 위치는 별도 확인합니다.")

add("comments", "댓글탐색_3회", "기본", VIDEO, [yt("panel","댓글 패널 열기·확인",target="comments"),scroll(3),yt("panel","댓글 패널 닫기·확인",target="close_panel"),watch(20)], 3, note="댓글을 읽고 스크롤만 합니다. 댓글 작성·좋아요 입력은 없습니다. OCR에 Tesseract eng/kor가 필요할 수 있습니다.")
add("description", "설명탐색_3회", "기본", VIDEO, [yt("panel","설명 패널 열기·확인",target="description"),scroll(2),yt("panel","설명 패널 닫기·확인",target="close_panel"),watch(20)], 3, note="설명을 스크롤하고 닫습니다. 외부 링크는 선택하지 않습니다.")

# 계정·영상·OS·실험 장비 조건을 아직 충족하지 않은 사례도 절차와 판정을 작성한다.
# 실행 단계가 구현되지 않은 사례를 import 가능한 파일로 위장하지 않는다.
CONDITIONAL = [
 ("최초실행", "테스트 전용 신규 프로필", "초기 안내 확인 → 필요한 권한 선택 → 홈 진입", "홈 진입·초기 안내 재등장 없음", "계정·앱 데이터 초기화가 필요해 자동 실행 제외"),
 ("화면꺼짐_복귀", "기기 잠금 정책과 해제 방법 확인", "30초 재생 → 화면 끄기 10초 → 켜기·잠금 해제 → 30초 재생", "복귀 후 본영상 재생", "잠금 해제 자동화 미구현"),
 ("회전_세로가로", "회전 잠금 원래 값 확보", "30초 재생 → 가로 20초 → 세로 20초, 5회 → 원래 설정 복원", "방향·재생 복귀 및 설정 원복", "OS별 회전 제어·실패 시 원복 구현 필요"),
 ("댓글탐색", "댓글 허용 영상·댓글 패널 선택자 확인", "영상 선택 → 댓글 열기 → 아래로 10회 → 닫기 → 재생", "댓글 패널 표시·복귀 후 재생", "현재 영상의 댓글 UI 선택자 확인 필요"),
 ("설명탐색", "설명 확장 UI 선택자 확인", "30초 재생 → 설명 펼치기 → 아래위 탐색 → 닫기", "설명 표시·재생 화면 복귀", "현재 YouTube 버전의 설명 선택자 확인 필요"),
 ("추천목록_재생중탐색", "영상 아래 추천 목록 접근 확인", "재생 → 추천 목록 10회 스크롤 → 플레이어 복귀", "재생 유지 또는 복귀 후 재개", "패널·목록 스크롤 범위 구분 필요"),
 ("검색필터", "현재 검색 필터 항목 확인", "검색 → 필터 열기 → 길이·정렬 변경 → 결과 탐색", "요청 필터 표시와 결과 로딩", "버전별 필터 UI 선택자 확인 필요"),
 ("채널탐색", "테스트 채널의 일반 영상·탭 확인", "채널 진입 → 동영상·Shorts 탭 이동 → 영상 선택", "선택한 채널의 영상 재생", "채널 카드·탭 선택자 확인 필요"),
 ("구독피드", "구독 채널이 있는 테스트 계정", "구독 탭 → 10회 스크롤 → 영상 재생", "구독 피드 표시·재생", "계정 구독 상태 확인 필요"),
 ("시청기록", "시청 기록이 켜진 테스트 계정", "기록 열기 → 이전 영상 선택 → 30초 재생", "기록의 영상 선택과 재생", "기록 메뉴·계정 상태 확인 필요"),
 ("짧은영상_끝까지", "길이·ID가 확인된 짧은 영상 5개", "각 영상을 끝까지 시청 → 다음 영상 선택", "5개 영상의 종료 상태 확인", "종료 상태·영상 ID 검증 구현 필요"),
 ("영상끝_다시재생", "짧은 영상·다시 재생 버튼", "종료까지 시청 → 다시 재생, 3회", "같은 영상의 종료·시작 위치 확인", "종료 감지·위치 검증 구현 필요"),
 ("자동재생", "자동재생 제공 영상·기존 설정 확보", "자동재생 켜기 → 영상 종료 → 다음 영상 확인 → 설정 원복", "다른 영상 ID로 전환", "설정 원복·영상 ID 검증 구현 필요"),
 ("재생목록_연속", "공개 테스트 재생목록", "목록 첫 영상 → 종료 → 다음 영상, 3개", "목록 순서대로 ID 전환", "재생목록 선택·종료 검증 구현 필요"),
 ("재생목록_선택변경", "3개 이상 영상의 테스트 목록", "1번 → 3번 → 2번 선택, 각각 30초", "선택한 목록 위치와 영상 ID 일치", "목록 UI 확인 필요"),
 ("탐색바_먼구간", "탐색 가능한 긴 영상", "초반 → 50% → 90% → 10% 이동", "실제 재생 위치가 요청 구간에 도달", "탐색바 범위·재생 위치 검증 구현 필요"),
 ("같은구간_반복", "탐색 가능한 영상·대상 시간", "60초 위치 → 20초 재생, 5회", "매회 시작 위치와 재생 구간 일치", "정확한 위치 검증 구현 필요"),
 ("자동화질", "자동 화질 메뉴와 기존 설정 확인", "자동 선택 → 3분 재생 → 표시 화질 기록", "자동 설정 유지·재생", "자동 메뉴 선택·원래 설정 복원 구현 필요"),
 ("재생속도", "속도 변경 지원 영상", "0.5배 → 1배 → 2배 각각 30초 → 원복", "메뉴에 요청 속도 표시", "속도 메뉴 선택자·원복 구현 필요"),
 ("자막_켜기끄기", "자막 있는 영상·기존 설정 확보", "자막 켜기·끄기 각각 20초, 5회 → 원복", "자막 상태 표시와 재생", "현재 기준 영상은 자막 미제공"),
 ("자막언어", "2개 언어 이상 자막 영상", "언어 A → B → A 각각 30초 → 원복", "요청 언어 표시", "지원 언어·메뉴 확인 필요"),
 ("오디오트랙", "다중 오디오 영상", "트랙 A → B → A 각각 30초 → 원복", "요청 오디오 트랙 표시", "다중 오디오 영상·선택자 필요"),
 ("미니플레이어_복귀", "미니플레이어 확장 선택자 확인", "축소 → 피드 탐색 → 확장, 5회", "같은 영상 화면으로 복귀", "확장 컨트롤·영상 동일성 확인 필요"),
 ("PiP", "PiP 지원 계정·지역·OS 설정", "재생 → 홈 → PiP 20초 → 앱 복귀, 5회", "PiP 창 표시와 복귀", "권한·지원 여부 확인 필요"),
 ("분할화면", "분할 화면 지원 OS·다른 앱", "YouTube+Chrome 분할 → 각각 조작 → 단일 화면 복귀", "분할 상태·재생·복귀", "OS별 분할 UI·원복 구현 필요"),
 ("Shorts_댓글", "댓글 허용 Shorts", "시청 → 댓글 열기·스크롤 → 닫기, 5회", "댓글 표시·Shorts 재개", "Shorts 댓글 선택자 확인 필요"),
 ("Shorts_채널복귀", "채널 링크가 있는 Shorts", "제작자 채널 열기 → 탐색 → 뒤로 복귀", "원래 Shorts 복귀", "채널 링크·영상 ID 확인 필요"),
 ("광고_건너뛸수없음", "건너뛰기 없는 광고가 실제 노출됨", "광고 전체 관측 → 본영상 시작 확인", "광고와 본영상 구간 분리", "광고 노출 비결정적; watch 공통 동작으로 관측 가능"),
 ("광고_연속", "2개 이상 연속 광고 실제 노출", "연속 광고 관측 → 가능한 버튼만 건너뛰기 → 본영상", "각 광고 경계·본영상 복귀", "광고 개별 ID·연속 개수 판별 미구현"),
 ("광고_중간", "중간 광고 실제 노출되는 긴 영상", "본영상 → 광고 → 본영상 계속 관측", "광고 구간 분리와 본영상 재개", "광고 노출 비결정적; 장시간 watch에서 관측 가능"),
 ("광고중_앱복귀", "현재 광고 상태가 확인됨", "광고 확인 → 홈 10초 → 복귀 → 종료 대기", "광고 상태와 본영상 복귀", "광고 시작에 맞춘 동작 트리거 구현 필요"),
 ("광고중_화면전환", "현재 광고 상태·전체화면 제공", "광고 확인 → 전체화면 → 일반화면 → 본영상", "전환 및 광고 구간 기록", "광고 트리거·광고별 컨트롤 확인 필요"),
 ("오프라인_앱실행", "실패 시 네트워크 복원 절차", "오프라인 전환 → 앱 실행 → 온라인 복귀 → 재시도", "오프라인 표시·재연결 후 로딩", "시나리오 전체 범위의 네트워크 원복 구현 필요"),
 ("WiFi_모바일전환", "활성 SIM·데이터 이용 가능·기존 망 상태 확보", "Wi-Fi 재생 → 모바일망 → Wi-Fi 원복", "망 변경·재생 복구", "현재 기기의 SIM·데이터망 사용 가능 여부 확인 필요"),
 ("느린네트워크", "속도 제한 가능한 AP 또는 테스트 프록시", "대역폭 제한 → 3분 재생 → 제한 해제", "실제 제한 속도와 재생·버퍼링 기록", "외부 망 제어 장비 필요"),
 ("대역폭감소_고화질", "속도 제한 장비·고화질 영상", "고화질 재생 → 대역폭 감소 → 회복", "실제 망 속도·표시 화질·버퍼링 변화", "외부 망 제어 및 버퍼링 검출 필요"),
 ("버퍼링중_조작", "버퍼링을 확인할 수 있는 제한망", "버퍼링 확인 → 정지·탐색·다른 영상 선택", "각 조작 후 복구 상태", "버퍼링 트리거·망 제어 필요"),
 ("라이브_연속", "현재 방송 중인 공개 라이브", "라이브 선택 → 10분 시청", "라이브 상태 유지·재생", "유효한 방송·라이브 UI 확인 필요"),
 ("라이브_전환", "현재 방송 중인 라이브 3개", "각 방송 30초 시청 → 다음 방송", "방송 ID 변경·재생", "동시 방송 목록 확보 필요"),
 ("라이브_채팅", "채팅 제공 라이브", "채팅 열기·스크롤·닫기, 5회", "채팅 표시와 재생 유지", "라이브 채팅 UI 확인 필요; 메시지 전송 없음"),
 ("라이브_화질", "여러 화질 제공 라이브", "지원 화질 낮음 → 높음 → 낮음", "요청 화질 표시·방송 재생", "라이브 화질 메뉴·지원 해상도 확인 필요"),
 ("라이브_과거현재", "되감기 가능한 라이브", "과거 시점 이동 → 현재로 복귀", "현재 방송 시점 복귀", "라이브 탐색 위치·실시간 버튼 확인 필요"),
 ("라이브_연결복구", "방송 중인 라이브·망 상태 확보", "30초 시청 → network_cycle 10초 → 60초 시청", "재연결 후 방송 복귀", "라이브 선택·재생 상태 판별 확인 필요"),
 ("라이브_방송종료", "종료 시점을 제어하는 테스트 방송", "방송 재생 → 송출 종료 → 종료 화면 관측", "종료 상태로 전환", "테스트 송출 환경 필요"),
 ("계정전환", "테스트 계정 2개", "A 계정 → B 계정 → A 계정 복원", "표시 계정과 피드 변경·원복", "로그인된 테스트 계정과 계정 선택자 확인 필요"),
 ("좋아요_설정해제", "테스트 전용 영상·원래 상태 확보", "좋아요 설정 → 상태 확인 → 원래 상태 복원", "좋아요 상태 변화·원복", "계정 데이터 변경; 전용 테스트 대상 필요"),
 ("구독_설정해제", "테스트 전용 채널·원래 상태 확보", "구독 → 표시 확인 → 원래 상태 복원", "구독 상태 변화·원복", "계정 데이터 변경; 전용 테스트 대상 필요"),
 ("나중에볼영상", "테스트 계정·원래 저장 상태 확보", "저장 → 목록 확인 → 원래 상태 복원", "대상 영상 저장·원복", "계정 목록 변경; 테스트 대상 필요"),
 ("재생목록_추가제거", "테스트 전용 목록", "영상 추가 → 목록에서 확인 → 추가분 제거", "목록 반영과 원복", "계정 목록 변경; 기존 항목 보존 필요"),
 ("오프라인저장", "오프라인 저장 지원 계정·영상·공간", "다운로드 → 오프라인 전환 → 재생 → 네트워크 복원", "저장 완료·오프라인 재생", "계정 기능·저장 공간·원복 절차 필요"),
 ("외부링크_진입", "검증된 영상 링크·앱 연결 설정", "다른 앱에서 링크 선택 → YouTube 진입 → 재생", "지정 영상 ID로 재생", "외부 앱 링크·앱 연결 설정 확인 필요"),
 ("외부기기_전송", "동일 망의 테스트 수신 기기", "전송 기기 선택 → 1분 재생 → 연결 해제", "수신 기기 재생과 연결 해제", "테스트 수신 장비 필요"),
]

CONDITIONAL = [case for case in CONDITIONAL if case[0] not in {"댓글탐색", "설명탐색"}]
for i,(name,pre,procedure,expected,reason) in enumerate(CONDITIONAL,1):
    catalog.append({"id":f"C{i:02}", "name":name, "category":"조건부", "status":"procedure-only", "preconditions":[pre], "procedure":procedure.split(" → "), "success":expected, "automationBlocker":reason, "deviceValidation":"not-run", "measurement":"준비 완료 뒤 trace 시작, 동작 전체 FSIO·DRAM 수집, 원래 상태 복구 뒤 trace 종료. 광고는 별도 관측 구간으로 기록."})

(OUT/"suite.scenariopack.json").write_text(json.dumps(scenarios,ensure_ascii=False,indent=2)+"\n",encoding="utf-8")
(OUT/"catalog.json").write_text(json.dumps(catalog,ensure_ascii=False,indent=2)+"\n",encoding="utf-8")
lines=["# YouTube 시나리오 목록", "", f"신규 실행 템플릿 {len(scenarios)}개와 조건부 절차 {len(CONDITIONAL)}개입니다. 기존 6개 시나리오는 유지합니다.", "", "실행 템플릿은 `suite.scenariopack.json`으로 일괄 가져올 수 있습니다. 조건부 절차는 자동 실행 파일이 아니며 필요한 환경과 구현 작업을 catalog.json에 명시했습니다.", "", "전체 템플릿의 실제 반복 실행·장시간 검증은 별도입니다. 공통 동작의 실기기 검증 성공을 모든 시나리오의 실행 성공으로 간주하지 않습니다.", "", "## 실행 템플릿", "", "| 순서 | 시나리오 | 분류 |", "|---|---|---|"]
for row in catalog:
    if "file" in row: lines.append(f"| {row['id']} | [{row['name']}]({row['file']}) | {row['category']} |")
lines += ["", "## 조건부 절차", "", "| 순서 | 시나리오 | 준비 조건 | 절차 | 성공 조건 |", "|---|---|---|---|---|"]
for row in catalog:
    if "procedure" in row: lines.append(f"| {row['id']} | {row['name']} | {'; '.join(row['preconditions'])} | {' → '.join(row['procedure'])} | {row['success']} |")
lines += ["", "## 검증", "", "[실기기 및 빌드 검증 기록](VALIDATION.md)을 참고하세요. 전체 반복 실행 여부와 공통 동작 검증을 구분합니다.", "", "## 재생성", "", "`python tools/generate_youtube_suite.py`는 파일만 생성합니다. 실행하거나 기기 설정을 변경하지 않습니다.", ""]
(OUT/"README.md").write_text("\n".join(lines),encoding="utf-8")
print(f"created {len(scenarios)} templates, {len(CONDITIONAL)} conditional procedures")
