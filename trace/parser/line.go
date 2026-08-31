package parser

import (
	"strconv"
	"strings"
)

// 안드로이드 ftrace `trace_pipe` 라인 포맷:
//
//	<comm-pid>     [CPU] flags  ts: eventname: key: value, key: value, ...
//
// 예시:
//	          <idle>-0       [003] d.h2. 268879.507791: ufshcd_command: complete_rsp: 1d84000.ufshc: tag: 21, ...
//	    kworker/5:2H-25770   [005] ..... 268879.626314: ufshcd_command: send_req: ...
//
// comm 에 공백/대괄호/슬래시가 모두 등장하므로 정규식 대신 우측 토큰에서 [CPU] 위치를
// 찾아 헤더를 분해한다. payload 의 `key: value, key: value` 는 수동 KV 스캐너로 처리.

// quickUFSCheck — Rust `UFS_QUICK_CHECK` 와 동등. 라인이 UFS 이벤트인지 빠른 prefilter.
func quickUFSCheck(line string) bool {
	return strings.Contains(line, "ufshcd_command:")
}

// quickBlockCheck — Rust `BLOCK_QUICK_CHECK`.
func quickBlockCheck(line string) bool {
	return strings.Contains(line, "blk_") || strings.Contains(line, "block_")
}

// ftraceHeader 는 trace_pipe 라인의 공통 헤더를 분해한 결과.
type ftraceHeader struct {
	Process string // 예: "<idle>-0", "kworker/5:2H-25770"
	CPU     uint32
	Flags   string // 예: "d.h2.", "....."
	Time    float64
	// PayloadStart 는 `eventname: ...` 시작 인덱스 (라인 내).
	PayloadStart int
}

// parseFtraceHeader — `<comm-pid>` ~ `time:` 까지 파싱. 매칭 안 되면 ok=false.
// payload 영역은 호출자에서 따로 파싱한다.
func parseFtraceHeader(line string) (ftraceHeader, bool) {
	var h ftraceHeader

	// 1) `[CPU]` 위치를 찾는다.
	//
	// ⚠⚠ 예전엔 "comm 에 `[` 가 들어가는 케이스는 거의 없다" 고 보고 **첫 `[`** 를
	// 썼는데, 실기기에서 틀렸다. 안드로이드 스레드 이름에 대괄호가 흔하다:
	//
	//	highpool[392]-7685    [002] d.h1. …   ← 첫 `[` 는 스레드 이름의 것
	//	     ^^^^^                  ^^^^^        진짜 CPU 는 뒤쪽
	//
	// 그러면 CPU 를 392 로 읽고, 범위를 넘으면 그 줄을 통째로 버린다. 실측(S25 앱
	// 전환 1회): ufshcd 줄 583건이 이 형태였고 393건이 버려졌다. **send/complete
	// 균형이 깨져 QD 가 회수되지 않고 157까지 누적**됐다(하드웨어 상한은 32×8).
	// 조용히 틀리는 종류다 — 줄이 사라진 것은 안 보이고 QD 그래프만 이상해진다.
	//
	// CPU 토큰은 `[` + 숫자 + `]` 이고 **뒤에 공백이 온다**. 스레드 이름의 대괄호는
	// `highpool[392]-7685` 처럼 뒤에 `-` 나 다른 문자가 붙는다. 그래서 "닫는 대괄호
	// 다음이 공백" 인 것을 CPU 로 본다 — 첫 번째로 그 조건을 만족하는 것을 쓴다.
	// ⚠ comm 이 16자에서 잘려 **여는 대괄호만 남는** 경우가 있다
	// (`IntentService[C-9374`). 그때 `]` 를 못 찾는다고 포기하면 그 줄을 통째로
	// 버리게 되므로, 닫는 짝이 없으면 **다음 `[` 로 계속 넘어간다.**
	openIdx, closeIdx := -1, -1
	for i := 0; i < len(line); i++ {
		if line[i] != '[' {
			continue
		}
		j := strings.IndexByte(line[i:], ']')
		if j <= 0 {
			continue // 닫는 짝 없음 — comm 이 잘린 경우. 다음 후보로.
		}
		j += i
		// CPU 필드의 조건 두 가지를 **모두** 본다:
		//   ① `]` 바로 뒤가 공백      (스레드 이름의 대괄호는 `-` 등이 붙는다)
		//   ② 대괄호 안이 숫자만       (`[C-9374    [005` 같은 잘못된 짝을 배제)
		// ⚠ ②가 없으면 comm 이 잘려 여는 대괄호만 남은 경우
		// (`IntentService[C-9374   [005]`) 에 comm 의 `[` 와 CPU 의 `]` 가 짝지어져
		// 안쪽이 `C-9374    [005` 가 되는데, 뒤가 공백이라 ①만으로는 통과해 버린다.
		if j+1 < len(line) && line[j+1] == ' ' && isAllDigits(line[i+1:j]) {
			openIdx, closeIdx = i, j
			break
		}
	}
	if openIdx <= 0 || closeIdx <= openIdx {
		return h, false
	}
	// 2) `process` = openIdx 이전 trim 한 토큰
	process := strings.TrimSpace(line[:openIdx])
	if process == "" {
		return h, false
	}
	h.Process = process

	// 3) CPU 숫자
	cpuStr := strings.TrimSpace(line[openIdx+1 : closeIdx])
	cpu, err := strconv.ParseUint(cpuStr, 10, 32)
	if err != nil {
		return h, false
	}
	h.CPU = uint32(cpu)

	// 4) `]` 다음에는 ` flags  ts:` — 공백으로 분리하면 [flags, "ts:"...]
	rest := line[closeIdx+1:]
	rest = strings.TrimLeft(rest, " ")
	// flags: 공백 전까지
	spaceIdx := strings.IndexByte(rest, ' ')
	if spaceIdx < 0 {
		return h, false
	}
	h.Flags = rest[:spaceIdx]
	rest = strings.TrimLeft(rest[spaceIdx:], " ")

	// 5) ts: `123.456:` 형식. ":" 위치 찾기.
	colonIdx := strings.IndexByte(rest, ':')
	if colonIdx <= 0 {
		return h, false
	}
	tsStr := rest[:colonIdx]
	ts, err := strconv.ParseFloat(tsStr, 64)
	if err != nil {
		return h, false
	}
	h.Time = ts

	// 6) payload 시작은 ":" 뒤의 첫 비공백
	payload := rest[colonIdx+1:]
	skip := 0
	for skip < len(payload) && payload[skip] == ' ' {
		skip++
	}
	// payload 시작 인덱스 = openIdx + (closeIdx - openIdx) + 1 + ... 직접 계산 대신
	// 라인 내 절대 인덱스로 환산
	h.PayloadStart = closeIdx + 1 + (len(line[closeIdx+1:]) - len(payload[skip:]))

	return h, true
}

// parseKV — `tag: 21, size: 4096, opcode: 0x28 (READ_10), ...` 같은 페이로드에서
// 주어진 key 의 값을 찾는다. value 는 다음 콤마(`,`) 또는 라인 끝까지.
// 키는 정확 매치하고, value 앞뒤 공백은 trim 한다. key 가 여러 번 등장하면 첫 번째.
func parseKV(payload, key string) (string, bool) {
	// 키 앞에는 보통 공백 또는 라인 시작이 온다. 단순 Contains 로 잘못 매칭(예: "ag:"
	// 가 "tag:" 안에 잡히는 케이스) 을 피하기 위해 ` key:` / `,key:` 패턴을 우선 시도.
	probe := key + ":"
	idx := 0
	for {
		rel := strings.Index(payload[idx:], probe)
		if rel < 0 {
			return "", false
		}
		absolute := idx + rel
		// 시작점이 라인 시작이거나, 앞 글자가 공백/콤마면 OK
		if absolute == 0 {
			return extractValue(payload, absolute+len(probe)), true
		}
		prev := payload[absolute-1]
		if prev == ' ' || prev == ',' {
			return extractValue(payload, absolute+len(probe)), true
		}
		idx = absolute + len(probe)
		if idx >= len(payload) {
			return "", false
		}
	}
}

func extractValue(payload string, start int) string {
	// value: 앞 공백 skip → 콤마 또는 끝까지 (단, `0x28 (READ_10)` 같이 괄호가 끼는
	// 경우는 콤마까지를 통째로 가져온다 — opcode 파서가 첫 토큰만 쓰면 된다)
	for start < len(payload) && payload[start] == ' ' {
		start++
	}
	end := start
	for end < len(payload) && payload[end] != ',' {
		end++
	}
	return strings.TrimSpace(payload[start:end])
}

// parseUFSLine — 한 라인을 파싱해 UFSEvent 를 만든다. UFS 라인이 아니면 ok=false.
// action 은 "send_req" 또는 "complete_rsp". 그 외 액션은 reject.
func parseUFSLine(line string) (UFSEvent, bool) {
	if !quickUFSCheck(line) {
		return UFSEvent{}, false
	}
	h, ok := parseFtraceHeader(line)
	if !ok {
		return UFSEvent{}, false
	}
	payload := line[h.PayloadStart:]

	// payload 형식: `ufshcd_command: <action>: 1d84000.ufshc: key: value, ...`
	// 첫 콜론까지가 이벤트명, 그 다음이 action.
	const eventName = "ufshcd_command:"
	if !strings.HasPrefix(payload, eventName) {
		return UFSEvent{}, false
	}
	rest := strings.TrimLeft(payload[len(eventName):], " ")
	actionEnd := strings.IndexByte(rest, ':')
	if actionEnd <= 0 {
		return UFSEvent{}, false
	}
	action := strings.TrimSpace(rest[:actionEnd])
	if action != "send_req" && action != "complete_rsp" {
		return UFSEvent{}, false
	}
	kvPart := rest[actionEnd+1:]

	tagStr, _ := parseKV(kvPart, "tag")
	sizeStr, _ := parseKV(kvPart, "size")
	lbaStr, _ := parseKV(kvPart, "LBA")
	opcodeRaw, _ := parseKV(kvPart, "opcode")
	groupStr, _ := parseKV(kvPart, "group_id")
	hwqStr, _ := parseKV(kvPart, "hwq_id")

	tag, _ := strconv.ParseUint(tagStr, 10, 32)

	// size 는 음수일 수 있음 (Rust: unsigned_abs() 사용). 우리도 부호 무시하고 절대값을 4KB units 로.
	var rawSize int64
	if sizeStr != "" {
		if s, err := strconv.ParseInt(sizeStr, 10, 64); err == nil {
			rawSize = s
			if rawSize < 0 {
				rawSize = -rawSize
			}
		}
	}
	sizeIn4KB := uint32((rawSize + 4095) / 4096) // ceil

	rawLBA, _ := strconv.ParseUint(lbaStr, 10, 64)
	lba := rawLBA
	if lba == UFSDebugLBA || lba > MaxValidUFSLBA {
		lba = 0
	}

	// opcode: 첫 토큰만 (예: "0x28 (READ_10)" → "0x28")
	opcode := opcodeRaw
	if sp := strings.IndexByte(opcode, ' '); sp > 0 {
		opcode = opcode[:sp]
	}

	// group_id: `0x` prefix 없이 hex 만 들어옴 (Rust: `0x(?P<group_id>[0-9a-f]+)`).
	// 하지만 실제 로그는 `0x0` 처럼 prefix 포함도 있음 — 양쪽 대응.
	groupStr = strings.TrimPrefix(strings.TrimPrefix(groupStr, "0x"), "0X")
	groupID, _ := strconv.ParseUint(groupStr, 16, 32)

	// hwq_id: -1 같은 음수도 가능.
	var hwq int64
	if hwqStr != "" {
		hwq, _ = strconv.ParseInt(hwqStr, 10, 32)
	}

	ev := UFSEvent{
		Time:    h.Time,
		Process: h.Process,
		CPU:     h.CPU,
		Action:  action,
		Tag:     uint32(tag),
		Opcode:  opcode,
		LBA:     lba,
		Size:    sizeIn4KB,
		GroupID: uint32(groupID),
		HWQID:   int32(hwq),
		Aligned: isUFSAligned(lba),
	}
	return ev, true
}

// parseBlockLine — block_rq_issue / block_rq_complete 라인 파서.
// 페이로드 형식:
//
//	block_rq_issue: <major>,<minor> <io_type> [<extra>] () <sector> + <size> [<flags>] [<comm>]
//
// 실 라인이 부족해 정규식과 동일 의미로 파싱. action 이 block_rq_issue/complete 만 받는다.
func parseBlockLine(line string) (BlockEvent, bool) {
	if !quickBlockCheck(line) {
		return BlockEvent{}, false
	}
	h, ok := parseFtraceHeader(line)
	if !ok {
		return BlockEvent{}, false
	}
	payload := line[h.PayloadStart:]

	// payload: "<action>: <dev>,<dev> <iotype> ... <sector> + <size> ... [<comm>]"
	// 액션 토큰 추출
	actionEnd := strings.IndexByte(payload, ':')
	if actionEnd <= 0 {
		return BlockEvent{}, false
	}
	action := strings.TrimSpace(payload[:actionEnd])
	if action != "block_rq_issue" && action != "block_rq_complete" {
		return BlockEvent{}, false
	}
	rest := strings.TrimLeft(payload[actionEnd+1:], " ")

	// dev = "major,minor"
	spIdx := strings.IndexByte(rest, ' ')
	if spIdx <= 0 {
		return BlockEvent{}, false
	}
	dev := rest[:spIdx]
	commaIdx := strings.IndexByte(dev, ',')
	if commaIdx <= 0 {
		return BlockEvent{}, false
	}
	devMajor, _ := strconv.ParseUint(dev[:commaIdx], 10, 32)
	devMinor, _ := strconv.ParseUint(dev[commaIdx+1:], 10, 32)
	rest = strings.TrimLeft(rest[spIdx:], " ")

	// io_type = 영문 대문자 시퀀스
	ioEnd := 0
	for ioEnd < len(rest) {
		c := rest[ioEnd]
		if c < 'A' || c > 'Z' {
			break
		}
		ioEnd++
	}
	if ioEnd == 0 {
		return BlockEvent{}, false
	}
	ioType := rest[:ioEnd]
	rest = rest[ioEnd:]

	// 선택적 extra: 공백 후 숫자
	rest = strings.TrimLeft(rest, " ")
	var extra uint32
	if len(rest) > 0 && rest[0] >= '0' && rest[0] <= '9' {
		numEnd := 0
		for numEnd < len(rest) && rest[numEnd] >= '0' && rest[numEnd] <= '9' {
			numEnd++
		}
		if v, err := strconv.ParseUint(rest[:numEnd], 10, 32); err == nil {
			extra = uint32(v)
		}
		rest = rest[numEnd:]
	}

	// `() <sector> + <size>` 부분 — `()` 또는 `(...)` 형태 모두 skip
	if idx := strings.Index(rest, "()"); idx >= 0 {
		rest = rest[idx+2:]
	} else if idx := strings.Index(rest, ") "); idx >= 0 {
		rest = rest[idx+2:]
	}
	rest = strings.TrimLeft(rest, " ")

	// <sector> + <size>
	plusIdx := strings.Index(rest, " + ")
	if plusIdx < 0 {
		return BlockEvent{}, false
	}
	rawSector, _ := strconv.ParseUint(strings.TrimSpace(rest[:plusIdx]), 10, 64)
	afterPlus := rest[plusIdx+3:]
	sizeEnd := 0
	for sizeEnd < len(afterPlus) && afterPlus[sizeEnd] >= '0' && afterPlus[sizeEnd] <= '9' {
		sizeEnd++
	}
	rawSize, _ := strconv.ParseUint(afterPlus[:sizeEnd], 10, 32)
	rest = afterPlus[sizeEnd:]

	// 마지막 `[comm]` — 끝에서 역으로 찾는다 (선행 [flags] 가 있을 수 있어 마지막 대괄호).
	comm := ""
	if open := strings.LastIndexByte(rest, '['); open >= 0 {
		if close := strings.IndexByte(rest[open:], ']'); close > 0 {
			comm = strings.TrimSpace(rest[open+1 : open+close])
		}
	}

	sector := normalizeSector(rawSector, uint32(rawSize), ioType)
	return BlockEvent{
		Time:     h.Time,
		Process:  h.Process,
		CPU:      h.CPU,
		Flags:    h.Flags,
		Action:   action,
		DevMajor: uint32(devMajor),
		DevMinor: uint32(devMinor),
		IOType:   ioType,
		Extra:    extra,
		Sector:   sector,
		Size:     uint32(rawSize),
		Comm:     comm,
		Aligned:  isBlockAligned(sector),
	}, true
}

// parseUFSCustomLine — `opcode,lba,size,start_time,end_time` CSV 라인 파서.
// 헤더/주석은 ok=false. Rust `parse_ufscustom_event` 와 동일.
func parseUFSCustomLine(line string) (UFSCustomEvent, bool) {
	if strings.HasPrefix(line, "opcode,lba,size,start_time,end_time") {
		return UFSCustomEvent{}, false
	}
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") {
		return UFSCustomEvent{}, false
	}
	parts := strings.Split(trimmed, ",")
	if len(parts) < 5 {
		return UFSCustomEvent{}, false
	}
	opcode := strings.TrimSpace(parts[0])
	if !strings.HasPrefix(opcode, "0x") {
		return UFSCustomEvent{}, false
	}
	lba, err := strconv.ParseUint(strings.TrimSpace(parts[1]), 10, 64)
	if err != nil {
		return UFSCustomEvent{}, false
	}
	size, err := strconv.ParseUint(strings.TrimSpace(parts[2]), 10, 32)
	if err != nil {
		return UFSCustomEvent{}, false
	}
	startTime, err := strconv.ParseFloat(strings.TrimSpace(parts[3]), 64)
	if err != nil {
		return UFSCustomEvent{}, false
	}
	endTime, err := strconv.ParseFloat(strings.TrimSpace(parts[4]), 64)
	if err != nil {
		return UFSCustomEvent{}, false
	}
	dtoc := 0.0
	if endTime >= startTime {
		dtoc = (endTime - startTime) * MilliSeconds
	}
	return UFSCustomEvent{
		Opcode:    opcode,
		LBA:       lba,
		Size:      uint32(size),
		StartTime: startTime,
		EndTime:   endTime,
		DtoC:      dtoc,
		Aligned:   isUFSAligned(lba),
	}, true
}

// isAllDigits — 빈 문자열이 아니고 전부 0-9 인가. CPU 토큰 판정용.
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
