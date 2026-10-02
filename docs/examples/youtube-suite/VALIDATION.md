# YouTube 확장 시나리오 검증

검증일: 2026-10-02. 기기: OnePlus CPH2745, Android 16, 한국어 YouTube.

## 통과

- 신규 실행 템플릿 36개: 단계 타입·파라미터, 반복 범위, trace 시작/종료 쌍, DRAM 활성화, 광고 대기 한도 검사.
- Agent에 36개 등록 후 저장된 steps/loops 원본 일치 확인. 기존 템플릿의 이름·설명·steps·loops·반복 횟수 유지 확인.
- `go test ./benchmark ./scenario ./macro ./server -count=1`.
- `TestYoutubeDeviceControls`: 재생, 일시정지, 전체화면, 일반화면, 재생 복귀.
- `TestYoutubeDevicePanels`: 댓글 열기·닫기, 설명 열기·닫기. 실제 패널 제목 확인.
- 통합 실행 `b067d2ca-154f-4e6d-b44c-598c0a65a8a7`: 앱 실행·검색·영상 선택·광고 대기 → 홈 이동·복귀 → 일시정지·재생 → 전체화면 → 댓글·설명 스크롤 → 네트워크 3초 차단·복구 → 본영상 재생 → 앱 종료·trace 종료. 성공, DRAM 7,441개 표본 저장.
- Shorts 실행 `940a61d4-ea49-44de-8c6c-add1514bcfc3`: 앱 실행·Shorts 진입 → 재생·일시정지 → 재생 → 홈 이동·복귀 → 재생 → 앱 종료·trace 종료. 성공.
- 네트워크 테스트 전후 Wi-Fi 및 모바일 데이터 설정 일치. 취소·명령 실패 시 원복 시도와 원복 오류 반환은 단위 테스트로 확인.
- UI production 빌드와 Windows Agent 빌드 성공.

## 검증 범위

대표 통합 실행은 짧은 재생 구간과 1회 동작으로 구성했습니다. 36개 각각의 전체 반복 횟수, 10분·30분 실행, 모든 광고 형식과 기기·계정 조합을 완료한 것은 아닙니다. `catalog.json`의 `deviceValidation: not-run-full`은 이 구분을 유지합니다.

`npm run check`는 기존 오류 76개·경고 153개가 남아 실패합니다. 이번 변경으로 추가된 오류는 없습니다.

조건부 절차 50개는 준비 조건·동작 순서·성공 조건·자동화에 남은 작업을 작성한 사례입니다. 실행 가능한 템플릿으로 등록하지 않았으며 실기기 검증도 하지 않았습니다. `catalog.json`의 `automationBlocker`를 확인하세요.

재생 시간은 화면·미디어 상태를 표본으로 관측한 시간입니다. 짧은 광고나 버퍼링을 놓칠 수 있고, UI/OCR 조회 자체가 I/O·DRAM 부하를 추가합니다. `next`, `previous`, 구간 이동 키 요청은 실제 영상 ID나 탐색 위치를 자동 검증하지 않습니다.
