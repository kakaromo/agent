package parser

import "fmt"

// UFS 프로토콜 코드 → 사람이 읽는 이름.
//
// Query UPIU / Task Management UPIU / UIC(DME) 커맨드는 SCSI opcode 가 없어서
// `0x00` 으로 뭉뚱그려지면 종류를 구분할 수 없다. 통계·차트에서 바로 쓸 표시
// 문자열을 여기서 만든다.
//
// Rust `../trace/src/models/ufs_names.rs` 의 포팅. 값 출처는 UFS 스펙(JESD220) 의
// UPIU / Query Request 표와 UFSHCI 의 UIC command 표이며, 두 구현이 같은 로그를
// 두 이름으로 보여주지 않도록 **항상 같이 고쳐야 한다**.
//
// 모르는 값은 조용히 사라지지 않도록 `Unknown(0x..)` 로 hex 를 함께 보여준다.

// queryOpcodeName — Query UPIU opcode (Query Request UPIU 의 Opcode 필드).
func queryOpcodeName(op uint8) string {
	switch op {
	case 0x00:
		return "NOP"
	case 0x01:
		return "Read Descriptor"
	case 0x02:
		return "Write Descriptor"
	case 0x03:
		return "Read Attribute"
	case 0x04:
		return "Write Attribute"
	case 0x05:
		return "Read Flag"
	case 0x06:
		return "Set Flag"
	case 0x07:
		return "Clear Flag"
	case 0x08:
		return "Toggle Flag"
	default:
		return "Unknown"
	}
}

// descriptorIdnName — Descriptor IDN. `Read/Write Descriptor` 일 때의 대상.
//
// 예약값(rfu)도 이름을 준다 — Unknown 으로 두면 표 누락인지 예약값인지
// 구분이 안 되어 매번 다시 확인하게 된다.
func descriptorIdnName(idn uint8) string {
	switch idn {
	case 0x00:
		return "device"
	case 0x01:
		return "configuration"
	case 0x02:
		return "unit"
	case 0x03:
		return "rfu_0"
	case 0x04:
		return "interconnect"
	case 0x05:
		return "string"
	case 0x06:
		return "rfu_1"
	case 0x07:
		return "geometry"
	case 0x08:
		return "power"
	case 0x09:
		return "health"
	default:
		return "Unknown"
	}
}

// attributeIdnName — Attribute IDN. `Read/Write Attribute` 일 때의 대상 (bAttr*).
//
// descriptor IDN 과 **값 공간이 다르다** — 같은 0x02 가 descriptor 면 unit,
// attribute 면 bCurrPowerMode 다. 반드시 query opcode 로 갈라 쓸 것.
//
// ⚠ 값이 **띄엄띄엄** 있다(0x11 다음 0x14, 0x1a 다음 0x1c, 그리고 0x2a/0x30/0x34).
// "연속이겠거니" 추측하지 말고 커널 `include/ufs/ufs.h` 의 enum 을 그대로 옮길 것.
func attributeIdnName(idn uint8) string {
	switch idn {
	case 0x00:
		return "bBootLunEn"
	case 0x01:
		return "bMaxHPBSingleCmd"
	case 0x02:
		return "bCurrPowerMode"
	case 0x03:
		return "bActiveIccLevel"
	case 0x04:
		return "bOutOfOrderDataEn"
	case 0x05:
		return "bBackgroundOpStatus"
	case 0x06:
		return "bPurgeStatus"
	case 0x07:
		return "bMaxDataInSize"
	case 0x08:
		return "bMaxDataOutSize"
	case 0x09:
		return "dDynCapNeeded"
	case 0x0a:
		return "bRefClkFreq"
	case 0x0b:
		return "bConfigDescrLock"
	case 0x0c:
		return "bMaxNumOfRTT"
	case 0x0d:
		return "wExceptionEventControl"
	case 0x0e:
		return "wExceptionEventStatus"
	case 0x0f:
		return "dSecondsPassed"
	case 0x10:
		return "wContextConf"
	case 0x11:
		return "dCorrPrgBlkNum"
	case 0x12:
		return "reserved2"
	case 0x13:
		return "reserved3"
	case 0x14:
		return "bDeviceFFUStatus"
	case 0x15:
		return "bPSAState"
	case 0x16:
		return "dPSADataSize"
	case 0x17:
		return "bRefClkGatingWaitTime"
	case 0x18:
		return "bDeviceCaseRoughTemp"
	case 0x19:
		return "bDeviceTooHighTempBound"
	case 0x1a:
		return "bDeviceTooLowTempBound"
	case 0x1c:
		return "bWBBufFlushStatus"
	case 0x1d:
		return "bAvailableWBBufSize"
	case 0x1e:
		return "bWBBufLifeTimeEst"
	case 0x1f:
		return "dCurrentWBBufSize"
	case 0x2a:
		return "bExtIIDEn"
	case 0x30:
		return "qTimestamp"
	case 0x34:
		return "dDevLvlExceptionID"
	default:
		return "Unknown"
	}
}

// flagIdnName — Flag IDN. `Read/Set/Clear/Toggle Flag` 일 때의 대상 (fFlag*).
//
// 이름은 producer(`fsiotrace.c` 의 `ufs_query_idn_name`)와 맞춘다 — 다르면
// 같은 로그가 두 이름으로 보인다.
func flagIdnName(idn uint8) string {
	switch idn {
	case 0x01:
		return "fDeviceInit"
	case 0x02:
		return "fPermanentWPEn"
	case 0x03:
		return "fPowerOnWPEn"
	case 0x04:
		return "fBackgroundOpsEn"
	case 0x05:
		return "fLifeSpanModeEn"
	case 0x06:
		return "fPurgeEnable"
	case 0x07:
		return "reserved2"
	case 0x08:
		return "fPhyResourceRemoval"
	case 0x09:
		return "fBusyRTC"
	case 0x0a:
		return "reserved3"
	case 0x0b:
		return "fPermDisableFwUpdate"
	case 0x0e:
		return "fWriteBoosterEn"
	case 0x0f:
		return "fWBBufFlushEn"
	case 0x10:
		return "fWBBufFlushDuringHibern8"
	case 0x11:
		return "fHPBReset"
	case 0x12:
		return "fHPBEn"
	default:
		return "Unknown"
	}
}

// tmFuncName — Task Management function (TM Request UPIU 의 Task Management Function 필드).
//
// ⚠ 정본은 커널 `include/ufs/ufs.h` 의 `enum { UFS_ABORT_TASK ... }` 다.
// Query Task 는 0x80, Query Task Set 은 0x81 이고 Target Reset 은 이 enum 에 없다.
// 0x10/0x20/0x80 으로 잘못 적으면 0x80 이 "Target Reset" 으로 **오표시**되고
// (값은 그럴듯해서 안 틀린 것처럼 보인다) 0x81 은 Unknown 으로 빠진다.
func tmFuncName(fn uint8) string {
	switch fn {
	case 0x01:
		return "Abort Task"
	case 0x02:
		return "Abort Task Set"
	case 0x04:
		return "Clear Task Set"
	case 0x08:
		return "Logical Unit Reset"
	case 0x80:
		return "Query Task"
	case 0x81:
		return "Query Task Set"
	default:
		return "Unknown"
	}
}

// uicCmdName — UIC / DME 커맨드 (UFSHCI 의 UICCMD).
//
// 정본은 커널 `include/ufs/ufshci.h` 의 `enum uic_cmd_dme` 다.
// producer(`fsiotrace.c` 의 `ufs_uic_cmd_name`)와 **값이 같아야 한다**.
//
// ⚠ 0x13 은 비어 있고 RESET 이 0x14 다. 여기를 0x13 으로 적으면 이후가
// 통째로 한 칸씩 밀려 LINK_STARTUP 이 HIBER_ENTER 로 보인다(실제로 그랬다).
//
// 하위 8비트만 본다 — UICCMD 레지스터는 상위 비트에 다른 필드가 있다.
func uicCmdName(cmd uint32) string {
	switch cmd & 0xff {
	// configuration
	case 0x01:
		return "DME_GET"
	case 0x02:
		return "DME_SET"
	case 0x03:
		return "DME_PEER_GET"
	case 0x04:
		return "DME_PEER_SET"
	// control
	case 0x10:
		return "DME_POWERON"
	case 0x11:
		return "DME_POWEROFF"
	case 0x12:
		return "DME_ENABLE"
	case 0x14:
		return "DME_RESET"
	case 0x15:
		return "DME_END_PT_RST"
	case 0x16:
		return "DME_LINK_STARTUP"
	case 0x17:
		return "DME_HIBER_ENTER"
	case 0x18:
		return "DME_HIBER_EXIT"
	case 0x1a:
		return "DME_TEST_MODE"
	default:
		return "Unknown"
	}
}

// orHex — `Unknown` 일 때 hex 를 붙여 원본 값이 사라지지 않게 한다.
func orHex(name string, raw uint32) string {
	if name == "Unknown" {
		return fmt.Sprintf("Unknown(0x%02x)", raw)
	}
	return name
}

// QueryDisplay — Query 한 건의 표시 이름. `"Read Descriptor(geometry)"` / `"Set Flag(fWriteBoosterEn)"`.
//
// opcode 에 따라 idn 테이블이 갈린다 (descriptor / attribute / flag).
func QueryDisplay(opcode, idn uint8) string {
	op := orHex(queryOpcodeName(opcode), uint32(opcode))
	var idnName string
	switch {
	case opcode == 0x01 || opcode == 0x02:
		idnName = descriptorIdnName(idn)
	case opcode == 0x03 || opcode == 0x04:
		idnName = attributeIdnName(idn)
	case opcode >= 0x05 && opcode <= 0x08:
		idnName = flagIdnName(idn)
	default:
		// NOP 등 idn 이 의미 없는 opcode 는 이름만.
		return op
	}
	return fmt.Sprintf("%s(%s)", op, orHex(idnName, uint32(idn)))
}

// TMDisplay — Task Management 한 건의 표시 이름. `"Abort Task"`.
func TMDisplay(fn uint8) string {
	return orHex(tmFuncName(fn), uint32(fn))
}

// UICDisplay — UIC 한 건의 표시 이름. `"DME_HIBER_EXIT"`.
func UICDisplay(cmd uint32) string {
	return orHex(uicCmdName(cmd), cmd)
}
