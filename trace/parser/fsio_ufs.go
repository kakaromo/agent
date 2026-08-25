package parser

import "sort"

// FsioUfsEvent latency 후처리.
// Rust `../trace/src/processors/fsio_ufs.rs` 의 포팅.
//
// 매칭 키 = (lun, tag, opcode), 정렬키 = time.
//
// lun 이 키에 있는 이유 — UFS tag 는 LU 별로 0~31 재사용되고 LBA 주소공간도 LU 마다
// 독립이라, lun 을 빼면 다른 LU 의 동시 요청이 같은 키로 충돌해 dtoc 가 엉뚱하게
// 짝지어지고 continuous 도 오판한다.
// lun 은 bpftrace 가 send 값을 tag 로 stash 해 복원해 준다. 복원까지 실패하면
// `lun=?`(0xff) 로 오고 그때만 (tag,opcode) 폴백이 동작한다.
//
// complete_rsp 후보정: bpftrace 는 요청 UPIU 만 잡고, complete 는 IRQ 문맥이라
// cross-layer 메타도 못 채운다. Response UPIU stash 는 IRQ 핸들러가 무거워져 이벤트를
// 잃으므로 의도적으로 userspace 책임이다. 그래서 UPIU 메타와
// comm/name/syscall/fs/pid/tid/ino 를 send_req pairing 으로 채운다.

// ufsReqState — send_req 에서 complete_rsp 로 넘길 pairing 상태:
// send_time + 요청 UPIU 메타 + cross-layer 메타(complete 후보정용).
type ufsReqState struct {
	sendTime  float64
	txn       *uint8
	upiuFlags *uint8
	upiuFunc  *uint8
	upiuAttr  string
	upiuCp    *uint8
	// cross-layer 후보정용 (모듈 헤더 참고).
	pid     uint32
	tid     uint32
	comm    string
	process string
	syscall string
	fs      string
	ino     uint64
	name    string
}

// mgmtKind — mgmt 이벤트 종류. 페어링 키에 포함해 서로 다른 종류가 같은 tag 로
// 섞이지 않게 한다.
type mgmtKind uint8

const (
	mgmtQuery mgmtKind = iota
	mgmtTM
	mgmtNop
)

type mgmtKey struct {
	kind mgmtKind
	lun  uint8
	tag  uint32
}

type ufsKey struct {
	lun    uint8
	tag    uint32
	opcode uint8
}

// setDtoC — 음수 가드 포함 dtoc 설정 (ms).
func setDtoC(ev *FsioUfsEvent, sendTime float64) {
	if diff := ev.Time - sendTime; diff >= 0 {
		ev.DtoC = diff * MilliSeconds
	} else {
		ev.DtoC = 0
	}
}

// mgmtLatency — mgmt 이벤트의 왕복 latency(dtoc) 계산.
//
// send 쪽은 시각을 기록만 하고, complete 쪽에서 짝을 찾아 dtoc 를 채운다.
// 짝이 없으면(트레이스 시작 전에 send 된 경우 등) dtoc 는 0 으로 남는다.
//
// **데이터 IO 상태(currentQD 등)는 건드리지 않는다** — 호출부 주석 참고.
// qd/ctoc/ctod/continuous 는 mgmt 에 의미가 없어 0/false 로 남긴다.
func mgmtLatency(ev *FsioUfsEvent, mgmtReq map[mgmtKey]float64, uicPending *float64, uicHas *bool) {
	// UIC 는 tag 가 없어 단일 슬롯으로 페어링한다.
	// 방향을 모르는 "uic" (구 producer — dir 키 없음) 는 페어링 불가라 건너뛴다.
	switch ev.Action {
	case "uic_send":
		*uicPending = ev.Time
		*uicHas = true
		return
	case "uic_complete":
		if *uicHas {
			setDtoC(ev, *uicPending)
			*uicHas = false
		}
		return
	}

	var kind mgmtKind
	var isSend bool
	switch ev.Action {
	case "upiu_query_req":
		kind, isSend = mgmtQuery, true
	case "upiu_query_rsp":
		kind, isSend = mgmtQuery, false
	case "upiu_tm_req":
		kind, isSend = mgmtTM, true
	case "upiu_tm_rsp":
		kind, isSend = mgmtTM, false
	case "upiu_nop_out":
		kind, isSend = mgmtNop, true
	case "upiu_nop_in":
		kind, isSend = mgmtNop, false
	default:
		// 짝이 없는 종류(rtt/reject/data_in/exception 등)는 시점만 의미가 있다.
		return
	}

	key := mgmtKey{kind: kind, lun: ev.LUN, tag: ev.Tag}
	if isSend {
		mgmtReq[key] = ev.Time
		return
	}
	if sendTime, ok := mgmtReq[key]; ok {
		delete(mgmtReq, key)
		setDtoC(ev, sendTime)
	}
}

// isNameless — `name` 이 실제 파일 경로가 아닌가? (빈 값 / `-` / `ino:N` / `(...)` 라벨)
//
// `ino:N` 과 `(...)` 는 bpftrace 가 파일명을 못 얻었을 때 대신 채우는 값이다.
// 원칙적으로는 생산자(bpftrace)가 걸러 보내는 게 맞고, 그렇게 바뀌면 이 판정의
// 해당 분기는 그냥 안 걸리게 될 뿐이라 두어도 해가 없다.
func isNameless(name string) bool {
	if name == "" || name == "-" {
		return true
	}
	if len(name) >= 4 && name[:4] == "ino:" {
		return true
	}
	return len(name) >= 2 && name[0] == '(' && name[len(name)-1] == ')'
}

// backfillCrossLayer — complete_rsp 의 빈 cross-layer 필드를 짝지어진 send_req 값으로 채운다.
//
// 덮어쓰기가 아니라 빈 값일 때만 채운다 — complete 가 실제 값을 갖고 오는 경우
// 그 값을 잃지 않기 위해서. pid/tid/ino 는 0, 문자열은 빈 값 또는 `-` 가 '빔'.
func backfillCrossLayer(ev *FsioUfsEvent, req *ufsReqState) {
	if ev.PID == 0 {
		ev.PID = req.pid
	}
	if ev.TID == 0 {
		ev.TID = req.tid
	}
	if ev.Ino == 0 {
		ev.Ino = req.ino
	}
	// complete 의 comm 은 IRQ 문맥의 swapper/kworker — 빈 값이 아니라 **틀린 값**이라
	// 여기서는 send 값을 우선한다. process 는 comm 과 같은 값의 별칭 필드.
	if req.comm != "" {
		ev.Comm = req.comm
		ev.Process = req.process
	}
	if ev.Syscall == "" || ev.Syscall == "-" {
		ev.Syscall = req.syscall
	}
	if ev.FS == "" {
		ev.FS = req.fs
	}
	// name 은 "빈 값" 판정이 까다롭다. bpftrace 는 파일명을 못 얻으면 빈칸이 아니라
	//   - `ino:N`           dentry 를 못 얻음 (f2fs node/저널처럼 애초에 파일이 아닌 것 포함)
	//   - `(flush:barrier)` 류 라벨   왜 파일이 없는지
	// 를 채워 보낸다. 이것들은 "실제 값" 이 아니라 **파일명 부재의 표현**이라,
	// send 쪽에 진짜 경로가 있으면 그걸로 덮어야 한다. 빈 문자열만 보면 라벨/ino 가
	// 들어찬 complete row 가 후보정을 건너뛰어 파일명을 영영 잃는다.
	if isNameless(ev.Name) && !isNameless(req.name) {
		ev.Name = req.name
	}
}

// ProcessFsioUFS — FsioUfsEvent 리스트에 dtoc/ctoc/ctod/qd/continuous 를 채운다.
// 기존 UFS latency 로직과 동치.
func ProcessFsioUFS(list []FsioUfsEvent) []FsioUfsEvent {
	if len(list) == 0 {
		return list
	}

	sort.SliceStable(list, func(i, j int) bool { return list[i].Time < list[j].Time })

	// (lun, tag, opcode) → send_req 상태. lun 이 키에 있는 이유는 모듈 헤더 참고.
	reqTimes := NewInFlight[ufsKey, ufsReqState](len(list) / 3)
	// tag → 현재 그 tag 로 in-flight 인 키.
	//
	// 미완결 보정에 필요하다. reqTimes 의 키에는 opcode 가 들어 있어서, 같은 tag 에
	// **다른 opcode** 로 새 send 가 오면 키가 달라 이전 건을 못 닫는다. UFS tag 는
	// 컨트롤러 전역 유일이라 tag 만으로 "이전 건은 끝났다" 를 판정할 수 있으므로
	// 별도 색인을 둔다.
	tagOwner := make(map[uint32]ufsKey)
	// 미완결로 닫힌 건수 — 리포트에 드러낸다(숨기면 원인 추적이 안 된다).
	var unfinished uint64
	var currentQD uint32
	var lastCompleteTime float64
	var hasLastComplete bool
	var lastCompleteQD0Time float64
	var hasLastCompleteQD0 bool
	var firstC bool
	var firstCompleteTime float64
	// continuous 비교용: 직전 send_req 의 (lun, lba, size, opcode).
	// LU 마다 LBA 주소공간이 독립이라 lun 이 다르면 섹터가 이어져도 연속이 아니다.
	var prevLun, prevOp uint8
	var prevLBA uint64
	var prevSize uint32
	var hasPrevSend bool

	// mgmt(Query/TM/NOP UPIU) 페어링 — 데이터 IO 의 reqTimes 와 **분리**한다.
	// 데이터 IO 키는 (lun, tag, opcode) 인데 mgmt 는 opcode 가 없어 항상 0 이라,
	// 같은 맵을 쓰면 같은 tag 의 데이터 IO 와 충돌한다.
	mgmtReq := make(map[mgmtKey]float64)
	// UIC 는 tag 가 없다. 대신 커널이 ufshcd_send_uic_cmd 에서 uic_cmd_mutex 를
	// send→complete 전 구간 잡으므로 호스트당 동시 1개만 outstanding 이다.
	// 그래서 맵이 아니라 단일 슬롯이 맞다. (uic_cmd 값으로 페어링하면 재시도된
	// hibern8 enter/exit 쌍에서 오매칭된다.)
	var uicPending float64
	var uicHas bool

	for i := range list {
		ev := &list[i]

		// mgmt 이벤트는 데이터 IO 의 큐 상태 기계에 **일절 관여하지 않는다.**
		//
		// 같은 시간순 리스트에 섞여 들어오므로, 여기서 currentQD/prevSend/
		// lastCompleteTime 을 건드리면 mgmt 행 자신이 아니라 그 앞뒤 **데이터 IO 행의**
		// qd/ctoc/ctod 가 틀어진다. 조용히 틀리는 종류라 회귀 테스트로 고정해 뒀다
		// (TestFsioUFSMgmtRowsDoNotDisturbDataIOMetrics).
		if ev.IsMgmt {
			mgmtLatency(ev, mgmtReq, &uicPending, &uicHas)
			// qd/ctoc/ctod/continuous 는 mgmt 에 의미가 없다 — 0/false 로 남긴다.
			// (아래 공통 `ev.QD = currentQD` 를 타면 데이터 IO 의 큐 깊이를
			//  물려받으므로 여기서 끝낸다.)
			continue
		}

		switch ev.Action {
		case "send_req":
			if hasPrevSend {
				prevEnd := prevLBA + ceilDiv64(uint64(prevSize), 4096)
				ev.Continuous = ev.LUN == prevLun && ev.LBA == prevEnd && ev.Opcode == prevOp
			}
			prevLun, prevLBA, prevSize, prevOp = ev.LUN, ev.LBA, ev.Size, ev.Opcode
			hasPrevSend = true

			// ── 미완결 보정 ①: tag 재사용으로 닫기 ──
			//
			// UFS task tag 는 UTRD 슬롯 번호라 **호스트 컨트롤러 전역으로 유일**하다.
			// 같은 시점에 같은 tag 가 두 개 떠 있을 수 없으므로, 같은 tag 에 새 send 가
			// 왔다는 건 이전 건이 이미 끝났다는 뜻이다 — complete 를 우리가 못 받았을 뿐.
			//
			// reqTimes 의 키에는 opcode/lun 이 들어 있어 insert 만으로는 안 닫힌다
			// (같은 tag 라도 opcode 가 다르면 키가 다르다). tagOwner 색인으로
			// 실제 키를 찾아 지운다.
			//
			// ⚠ 단, **같은 lun 일 때만** 닫는다. 서로 다른 LU 가 같은 tag 로 동시
			// in-flight 인 상황은 정상이고(TestFsioUFSDtoCPairsSameTagDifferentLun),
			// lun 미상(0xff) 로 오는 행도 있어 섣불리 닫으면 멀쩡한 짝을 깨서 dtoc 를
			// 잃는다. 놓친 complete 를 못 닫고 남기는 쪽이, 정상 IO 를 미완결로 만드는
			// 것보다 낫다 — 남는 건 아래 시간 만료가 결국 걷어간다.
			curKey := ufsKey{lun: ev.LUN, tag: ev.Tag, opcode: ev.Opcode}
			if prevKey, ok := tagOwner[ev.Tag]; ok && prevKey.lun == ev.LUN {
				if _, existed := reqTimes.Remove(prevKey); existed {
					// 그 send 의 +1 은 영영 안 돌아온다 — 여기서 되돌린다.
					currentQD = satSub(currentQD, 1)
					unfinished++
				}
			}
			tagOwner[ev.Tag] = curKey

			// ── 미완결 보정 ②: 시간 만료 ──
			//
			// tag 가 재사용되지 않은 채 트레이스가 흘러가는 경우(그 tag 로 더 이상
			// IO 가 안 나감)를 위한 보조 장치. ①만으로는 그런 건을 못 닫는다.
			if swept := reqTimes.Sweep(ev.Time); swept > 0 {
				currentQD = satSub(currentQD, uint32(swept))
				unfinished += swept
			}

			// 같은 키(lun/opcode 까지 동일) 재사용은 위 tagOwner 경로에서 이미
			// 닫혔으므로 여기 교체 반환값은 항상 false 다.
			reqTimes.Insert(curKey, ev.Time, ufsReqState{
				sendTime:  ev.Time,
				txn:       ev.Txn,
				upiuFlags: ev.UpiuFlags,
				upiuFunc:  ev.UpiuFunc,
				upiuAttr:  ev.UpiuAttr,
				upiuCp:    ev.UpiuCp,
				pid:       ev.PID,
				tid:       ev.TID,
				comm:      ev.Comm,
				process:   ev.Process,
				syscall:   ev.Syscall,
				fs:        ev.FS,
				ino:       ev.Ino,
				name:      ev.Name,
			})

			currentQD++
			if currentQD == 1 {
				if hasLastCompleteQD0 {
					if diff := ev.Time - lastCompleteQD0Time; diff >= 0 {
						ev.CtoD = diff * MilliSeconds
					}
				}
				firstC = true
				firstCompleteTime = ev.Time
			}

		case "complete_rsp":
			ev.Continuous = false
			currentQD = satSub(currentQD, 1)

			// 1차: (lun, tag, opcode) 정확 매칭 — 정상 경로.
			req, paired := reqTimes.Remove(ufsKey{lun: ev.LUN, tag: ev.Tag, opcode: ev.Opcode})

			// 2차: lun 미상(0xff)일 때만 (tag, opcode) 폴백. 미상일 때로 한정하는 게
			// 중요하다 — 무조건 폴백하면 다른 LU 의 동시 in-flight 요청을 잘못 문다.
			if !paired && ev.LUN == LunUnknown {
				for _, k := range reqTimes.Keys() {
					if k.tag == ev.Tag && k.opcode == ev.Opcode {
						// 미상이므로 send 쪽 lun 을 채택 — LU 별 집계가 한쪽으로 쏠리지 않게.
						ev.LUN = k.lun
						req, paired = reqTimes.Remove(k)
						break
					}
				}
			}

			// 정상 완료했으니 tag 점유를 푼다. 안 풀면 그 tag 의 **다음** send 가
			// 이미 끝난 건을 미완결로 잘못 세고 qd 를 한 번 더 깎는다.
			delete(tagOwner, ev.Tag)

			if paired {
				if diff := ev.Time - req.sendTime; diff >= 0 {
					ev.DtoC = diff * MilliSeconds
				}
				// 요청 UPIU 메타를 complete_rsp 에 복사.
				ev.Txn = req.txn
				ev.UpiuFlags = req.upiuFlags
				ev.UpiuFunc = req.upiuFunc
				ev.UpiuAttr = req.upiuAttr
				ev.UpiuCp = req.upiuCp
				// cross-layer 메타도 send 값으로 후보정 (모듈 헤더 참고).
				backfillCrossLayer(ev, &req)
			}

			if firstC {
				if diff := ev.Time - firstCompleteTime; diff >= 0 {
					ev.CtoC = diff * MilliSeconds
				}
				firstC = false
			} else if hasLastComplete {
				if diff := ev.Time - lastCompleteTime; diff >= 0 {
					ev.CtoC = diff * MilliSeconds
				}
			}

			if currentQD == 0 {
				lastCompleteQD0Time = ev.Time
				hasLastCompleteQD0 = true
			}
			lastCompleteTime = ev.Time
			hasLastComplete = true
		}

		ev.QD = currentQD
	}

	// 트레이스 끝에 남은 in-flight. 트레이스가 IO 도중 끝나면 정상적으로 남는 것이라
	// producer 유실과는 성격이 다르지만, 미완결인 건 맞으므로 같이 센다.
	unfinished += reqTimes.Finish()

	// 미완결 send 에 표시를 남긴다.
	//
	// 완료된 send 는 위 루프에서 complete 쪽이 짝을 가져갔으므로, 여기서 다시 훑어
	// "complete 를 받지 못한 send" 를 가려낸다. dtoc 는 0 인 채로 두고 플래그만 켠다
	// — 0 으로 채우면 "0ms 에 끝났다" 로 읽혀 지연 통계가 조용히 오염된다.
	if unfinished > 0 {
		markUnfinishedUFS(list)
	}

	return list
}

// markUnfinishedUFS — complete 를 못 받은 send 에 IsUnfinished 를 켠다.
//
// 짝짓기는 이미 끝난 뒤라, 여기서는 (lun, tag, opcode) 별로 send/complete 개수를
// 세어 남는 send 를 뒤에서부터 표시한다. 뒤에서부터인 이유 — 같은 tag 가 여러 번
// 쓰였다면 짝을 못 찾은 건 **나중 것**일 가능성이 높다(앞의 것들은 complete 가 왔다).
func markUnfinishedUFS(list []FsioUfsEvent) {
	completes := make(map[ufsKey]int)
	for i := range list {
		ev := &list[i]
		if ev.IsMgmt || ev.Action != "complete_rsp" {
			continue
		}
		completes[ufsKey{lun: ev.LUN, tag: ev.Tag, opcode: ev.Opcode}]++
	}
	for i := len(list) - 1; i >= 0; i-- {
		ev := &list[i]
		if ev.IsMgmt || ev.Action != "send_req" {
			continue
		}
		key := ufsKey{lun: ev.LUN, tag: ev.Tag, opcode: ev.Opcode}
		if n := completes[key]; n > 0 {
			completes[key] = n - 1 // 짝이 있다
		} else {
			ev.IsUnfinished = true
		}
	}
}

// satSub — uint32 포화 뺄셈.
func satSub(a, b uint32) uint32 {
	if a < b {
		return 0
	}
	return a - b
}

// ceilDiv64 — 올림 나눗셈.
func ceilDiv64(a, b uint64) uint64 {
	if b == 0 {
		return 0
	}
	return (a + b - 1) / b
}
