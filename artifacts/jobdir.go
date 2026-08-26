package artifacts

import (
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// 잡 산출물 폴더 규칙.
//
// **왜 한 폴더인가.** 예전엔 같은 잡의 결과가 두 트리로 갈라져 있었다:
//
//	~/agent_trace/<traceJobId>/                        trace.log · parquet · clocksync
//	~/.agent-standalone/archive/auto/<날짜>/<jobId>/    result.json
//
// 디렉토리 이름이 서로 다른 ID(trace job vs scenario job)라 **사람이 눈으로 잇는 것조차
// 어려웠고**, 잡 하나를 통째로 넘기려면 두 곳을 찾아 합쳐야 했다. 이제 한 폴더 아래
// 모은다 — 폴더째 복사하면 그것만으로 재현·공유가 된다.
//
//	<archiveBase>/jobs/20260826-222841_scenario_UI검증/
//	  ├─ result.json                 잡 결과 (디바이스별)
//	  └─ trace/<traceJobId>/         trace.log · result_*.parquet · clocksync.json
//
// **왜 UUID 를 안 쓰는가.** `48b10aec-5aad-…` 는 사람이 못 읽는다. 어느 잡이 무엇이었는지
// 폴더 이름만 보고 알 수 있어야 파일을 뒤질 수 있다. 시각을 앞에 둬서 시간순 정렬이
// 되고, 잡 이름이 비어도(그런 경우가 흔하다) 시각+타입만으로 구분된다.

// jobDirUnsafeRe — 파일명에 쓰면 곤란한 문자. 한글·영숫자·일부 기호는 남긴다
// (읽히는 게 목적이라 과하게 깎지 않는다).
var jobDirUnsafeRe = regexp.MustCompile(`[^\p{L}\p{N}_.-]+`)

// sanitizeJobDirPart — 경로 조각 하나를 안전하게 만든다.
//
// ⚠ 경로 구분자와 상위 참조를 반드시 없앤다 — jobName 은 사용자 입력이라
// "../../etc" 같은 값이 들어오면 archiveBase 밖으로 쓰게 된다.
func sanitizeJobDirPart(s string, max int) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, string(filepath.Separator), "_")
	s = strings.ReplaceAll(s, "/", "_")
	s = jobDirUnsafeRe.ReplaceAllString(s, "_")
	s = strings.Trim(s, "._-")
	// ".." 잔재 제거 (위 Trim 이 양끝만 보므로 가운데는 남을 수 있다)
	for strings.Contains(s, "..") {
		s = strings.ReplaceAll(s, "..", "_")
	}
	if r := []rune(s); len(r) > max {
		s = string(r[:max])
		// ⚠ 자른 자리가 '.' 이면 다시 떼어낸다. Windows 는 디렉토리 이름의 끝
		// '.' 을 조용히 버려서, 그 뒤만 다른 두 잡 이름이 **한 폴더로 합쳐진다.**
		// (이 저장소는 windows-amd64 를 빌드해 배포한다)
		s = strings.Trim(s, "._-")
	}
	return s
}

// JobDirName — 잡 산출물 폴더 이름. 형식: 20260826-222841_scenario_이름
//
// startedAt 이 zero 면 호출 시각을 쓴다 — 이름이 없는 것보다 대략의 시각이 낫다.
func JobDirName(startedAt time.Time, jobType, jobName, jobID string) string {
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	// ⚠ **UTC 로 고정한다.** 이 이름을 만드는 곳이 둘인데(시나리오는 time.Now() =
	// 로컬, hook 은 DB 시각 = UTC) 서로 다른 존을 쓰면 **같은 잡이 두 폴더로 갈린다.**
	// 실제로 KST 환경에서 9시간 차이로 갈라졌다. 저장 형식은 한 존으로 못 박는다.
	parts := []string{startedAt.UTC().Format("20060102-150405")}

	if t := sanitizeJobDirPart(jobType, 16); t != "" {
		parts = append(parts, t)
	}
	if n := sanitizeJobDirPart(jobName, 40); n != "" {
		parts = append(parts, n)
	}
	// 이름·타입이 다 비면 시각만 남는데, 같은 초에 두 잡이 끝나면 충돌한다.
	// 그때만 jobId 앞자리를 붙여 구분한다 (평소엔 안 붙여 이름을 짧게 유지).
	if len(parts) == 1 && jobID != "" {
		parts = append(parts, shortJobID(jobID))
	}
	return strings.Join(parts, "_")
}

// shortJobID — UUID 앞 8자. 로그·API 의 jobId 와 대조할 때 쓴다.
func shortJobID(jobID string) string {
	if len(jobID) > 8 {
		return jobID[:8]
	}
	return jobID
}

// JobArtifactDir — 잡 산출물 루트. <archiveBase>/jobs/<이름>
func JobArtifactDir(archiveBase string, startedAt time.Time, jobType, jobName, jobID string) string {
	return filepath.Join(archiveBase, "jobs", JobDirName(startedAt, jobType, jobName, jobID))
}

// JobTraceSubdir — 잡 폴더 안에서 trace 들이 들어갈 부모 디렉토리 이름.
//
// 실제 산출물은 그 아래 <traceJobId>/ 로 들어간다(트레이서가 붙인다). 한 시나리오가
// trace 를 여러 번 켤 수 있어 구분자가 필요하고, traceJobId 는 API·로그에 그대로
// 나오는 식별자라 대조가 된다.
const JobTraceSubdir = "trace"
