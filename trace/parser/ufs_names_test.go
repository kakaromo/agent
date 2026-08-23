package parser

import "testing"

// Rust `../trace/src/models/ufs_names.rs` 의 테스트를 그대로 옮긴 것.
// 각 테스트는 과거에 실제로 났던 사고를 고정한다 — 값만 보고 지우면 안 된다.

func TestQueryDisplaySplitsIdnTableByOpcode(t *testing.T) {
	// 같은 idn=0x02 라도 opcode 에 따라 다른 테이블을 봐야 한다.
	if got := QueryDisplay(0x01, 0x02); got != "Read Descriptor(unit)" {
		t.Errorf("descriptor idn: got %q", got)
	}
	if got := QueryDisplay(0x03, 0x02); got != "Read Attribute(bCurrPowerMode)" {
		t.Errorf("attribute idn: got %q", got)
	}
}

func TestQueryDisplayFlagTable(t *testing.T) {
	if got := QueryDisplay(0x06, 0x0e); got != "Set Flag(fWriteBoosterEn)" {
		t.Errorf("got %q", got)
	}
	if got := QueryDisplay(0x01, 0x07); got != "Read Descriptor(geometry)" {
		t.Errorf("got %q", got)
	}
}

func TestQueryDisplayOmitsIdnWhenMeaningless(t *testing.T) {
	if got := QueryDisplay(0x00, 0x00); got != "NOP" {
		t.Errorf("got %q", got)
	}
}

func TestUnknownKeepsRawHex(t *testing.T) {
	// 모르는 값이 조용히 "Unknown" 으로 뭉개지면 안 된다.
	cases := []struct{ got, want string }{
		{QueryDisplay(0xfe, 0x00), "Unknown(0xfe)"},
		{QueryDisplay(0x01, 0x3f), "Read Descriptor(Unknown(0x3f))"},
		{TMDisplay(0x77), "Unknown(0x77)"},
		{UICDisplay(0xab), "Unknown(0xab)"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

func TestKnownNames(t *testing.T) {
	if got := TMDisplay(0x08); got != "Logical Unit Reset" {
		t.Errorf("got %q", got)
	}
	if got := UICDisplay(0x17); got != "DME_HIBER_ENTER" {
		t.Errorf("got %q", got)
	}
	if got := UICDisplay(0x18); got != "DME_HIBER_EXIT" {
		t.Errorf("got %q", got)
	}
}

// attribute IDN 표를 "자주 쓰는 것만" 추리면 안 된다.
//
// 실기기에서 `Read Attribute(Unknown(0x1))` 이 나와서 발견됐다. 값이
// 띄엄띄엄 있어(0x11 다음 0x14, 0x1a 다음 0x1c, 0x2a/0x30/0x34) 눈으로
// 훑으면 빠뜨리기 쉽다 — 커널 enum 전수를 옮겼는지 확인한다.
func TestAttributeIdnTableIsComplete(t *testing.T) {
	cases := []struct {
		idn  uint8
		want string
	}{
		{0x01, "Read Attribute(bMaxHPBSingleCmd)"},
		{0x11, "Read Attribute(dCorrPrgBlkNum)"},
		// WriteBooster 계열 — 성능 분석에서 실제로 자주 본다
		{0x1c, "Read Attribute(bWBBufFlushStatus)"},
		{0x1d, "Read Attribute(bAvailableWBBufSize)"},
		{0x1e, "Read Attribute(bWBBufLifeTimeEst)"},
		{0x1f, "Read Attribute(dCurrentWBBufSize)"},
		// 띄엄띄엄 떨어져 있는 뒤쪽 값들
		{0x2a, "Read Attribute(bExtIIDEn)"},
		{0x30, "Read Attribute(qTimestamp)"},
		{0x34, "Read Attribute(dDevLvlExceptionID)"},
		// 0x1b 는 커널 enum 에 없다 — 진짜 미지값은 Unknown 이어야 한다
		{0x1b, "Read Attribute(Unknown(0x1b))"},
	}
	for _, c := range cases {
		if got := QueryDisplay(0x03, c.idn); got != c.want {
			t.Errorf("idn 0x%02x: got %q, want %q", c.idn, got, c.want)
		}
	}
}

// TM function 값은 0x80/0x81 이다.
//
// 예전에 Query Task=0x10 / Query Task Set=0x20 / Target Reset=0x80 으로
// 적혀 있었다. 그러면 0x80 이 "Target Reset" 으로 **오표시**되고(값은
// 그럴듯해서 안 틀린 것처럼 보인다) 0x81 은 Unknown 이 된다.
func TestTMFuncCodesMatchKernelEnum(t *testing.T) {
	if got := TMDisplay(0x80); got != "Query Task" {
		t.Errorf("got %q", got)
	}
	if got := TMDisplay(0x81); got != "Query Task Set" {
		t.Errorf("got %q", got)
	}
	// 예전 오답 값들은 정의되지 않은 코드다
	if got := TMDisplay(0x10); got != "Unknown(0x10)" {
		t.Errorf("got %q", got)
	}
	if got := TMDisplay(0x20); got != "Unknown(0x20)" {
		t.Errorf("got %q", got)
	}
}

// 예약값(RFU)도 이름을 준다 — Unknown 으로 두면 "표가 빠진 건가?" 를
// 매번 다시 확인하게 된다.
func TestReservedValuesAreNamed(t *testing.T) {
	cases := []struct{ got, want string }{
		{QueryDisplay(0x01, 0x03), "Read Descriptor(rfu_0)"},
		{QueryDisplay(0x01, 0x06), "Read Descriptor(rfu_1)"},
		{QueryDisplay(0x05, 0x07), "Read Flag(reserved2)"},
		{QueryDisplay(0x05, 0x0a), "Read Flag(reserved3)"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

// UIC 표가 한 칸 밀리는 사고 재발 방지.
//
// 0x13 은 비어 있고 RESET 이 0x14 다. 이걸 0x13 으로 적으면 이후가 통째로
// 밀려서 **LINK_STARTUP 이 HIBER_ENTER 로 보인다** — 값은 그럴듯하고
// 이름만 틀려서 조용히 잘못된 분석으로 이어진다.
func TestUICControlCodesAreNotShifted(t *testing.T) {
	if got := uicCmdName(0x13); got != "Unknown" {
		t.Errorf("0x13 은 비어 있어야 한다: got %q", got)
	}
	cases := []struct {
		cmd  uint32
		want string
	}{
		{0x14, "DME_RESET"},
		{0x16, "DME_LINK_STARTUP"},
		{0x17, "DME_HIBER_ENTER"},
		{0x18, "DME_HIBER_EXIT"},
	}
	for _, c := range cases {
		if got := uicCmdName(c.cmd); got != c.want {
			t.Errorf("0x%02x: got %q, want %q", c.cmd, got, c.want)
		}
	}
}

// UICCMD 는 상위 비트에 다른 필드가 올 수 있어 하위 8비트만 본다.
func TestUICMasksHighBits(t *testing.T) {
	if got := uicCmdName(0x0000_0017); got != "DME_HIBER_ENTER" {
		t.Errorf("got %q", got)
	}
	if got := uicCmdName(0x1234_0017); got != "DME_HIBER_ENTER" {
		t.Errorf("got %q", got)
	}
}

// 같은 idn 값이 opcode 문맥에 따라 다른 표를 봐야 한다 — producer 가
// 지적한 케이스(0x05 가 flag 면 LifeSpan, attr 이면 BkOpsStatus).
func TestIdnNamespaceDiffersByOpcode(t *testing.T) {
	if got := QueryDisplay(0x05, 0x05); got != "Read Flag(fLifeSpanModeEn)" {
		t.Errorf("got %q", got)
	}
	if got := QueryDisplay(0x03, 0x05); got != "Read Attribute(bBackgroundOpStatus)" {
		t.Errorf("got %q", got)
	}
}
