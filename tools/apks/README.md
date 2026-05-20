# tools/apks

에이전트가 디바이스에 설치할 수 있는 APK 번들 폴더.

여기에 `.apk` 파일을 떨어뜨리면 다음 두 곳에서 자동으로 노출된다:

- **UI Macro 탭 → 앱 설치·제거** 패널에서 ad-hoc 설치
- **Scenario 빌더 → Install APK** step 의 select 옵션

## 파일 추가

```sh
cp ~/Downloads/com.antutu.ABenchMark.apk tools/apks/
```

에이전트 재시작 불필요 — 매 요청마다 폴더를 스캔한다.

## 동작

- `GET  /api/agent/apks` → 폴더의 `.apk` 파일 목록 (filename, size, mtime)
- `POST /api/agent/apks/install` → `adb install -r <local>` 로 push + pm install
- `POST /api/agent/apks/uninstall` → `adb uninstall <package>` (디바이스 측 패키지 기준)

scenario step `install_apk` 는 `params.apk_filename` 으로 이 폴더 안의 bare 파일명만 받는다.
`../` 같은 traversal 은 거부.

## 권장 네이밍

가능하면 `<package_name>.apk` 형태로 두면 (예: `com.antutu.ABenchMark.apk`) UI 가 패키지명을
파일명에서 추론해 보여줄 수 있다. 일치하지 않아도 설치 자체는 정상 동작한다.

## Git 정책

이 폴더의 `.apk` 파일은 binary 라 커밋하지 않는다. `.gitignore` 에 `tools/apks/*.apk` 가
포함되어 있다 (이 README 만 추적).
