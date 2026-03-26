package screen

import (
	"encoding/binary"
	"math"
)

// scrcpy control message types
const (
	ControlMsgTypeInjectKeycode    = 0
	ControlMsgTypeInjectText       = 1
	ControlMsgTypeInjectTouchEvent = 2
	ControlMsgTypeInjectScrollEvent = 3
	ControlMsgTypeBackOrScreenOn   = 4
	ControlMsgTypeExpandNotificationPanel = 5
	ControlMsgTypeExpandSettingsPanel     = 6
	ControlMsgTypeCollapseNotificationPanel = 7
	ControlMsgTypeGetClipboard     = 8
	ControlMsgTypeSetClipboard     = 9
	ControlMsgTypeSetScreenPowerMode = 10
	ControlMsgTypeRotateDevice     = 11
)

// Android key event actions
const (
	ActionDown = 0
	ActionUp   = 1
	ActionMove = 2
)

// TouchEvent represents a touch input from the browser.
type TouchEvent struct {
	Action   int     `json:"action"`   // 0=down, 1=up, 2=move
	X        float64 `json:"x"`        // 0.0 ~ 1.0 (normalized)
	Y        float64 `json:"y"`        // 0.0 ~ 1.0 (normalized)
	Width    int     `json:"width"`    // device screen width
	Height   int     `json:"height"`   // device screen height
	Pressure float64 `json:"pressure"` // 0.0 ~ 1.0
	PointerID int    `json:"pointer_id"`
}

// KeyEvent represents a key input from the browser.
type KeyEvent struct {
	Action   int `json:"action"`    // 0=down, 1=up
	Keycode  int `json:"keycode"`   // Android keycode
	Repeat   int `json:"repeat"`
	MetaState int `json:"meta_state"`
}

// ScrollEvent represents a scroll input from the browser.
type ScrollEvent struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  int     `json:"width"`
	Height int     `json:"height"`
	HScroll int    `json:"h_scroll"` // horizontal scroll amount
	VScroll int    `json:"v_scroll"` // vertical scroll amount
}

// EncodeInjectTouchEvent encodes a touch event for scrcpy control protocol.
func EncodeInjectTouchEvent(evt TouchEvent) []byte {
	buf := make([]byte, 32)
	buf[0] = ControlMsgTypeInjectTouchEvent
	buf[1] = byte(evt.Action)

	// pointer id (8 bytes, big endian)
	binary.BigEndian.PutUint64(buf[2:10], uint64(evt.PointerID))

	// position x (4 bytes)
	x := int32(evt.X * float64(evt.Width))
	binary.BigEndian.PutUint32(buf[10:14], uint32(x))

	// position y (4 bytes)
	y := int32(evt.Y * float64(evt.Height))
	binary.BigEndian.PutUint32(buf[14:18], uint32(y))

	// screen width (2 bytes)
	binary.BigEndian.PutUint16(buf[18:20], uint16(evt.Width))

	// screen height (2 bytes)
	binary.BigEndian.PutUint16(buf[20:22], uint16(evt.Height))

	// pressure (2 bytes, fixed point)
	pressure := uint16(evt.Pressure * float64(math.MaxUint16))
	binary.BigEndian.PutUint16(buf[22:24], pressure)

	// action button (4 bytes) - primary button for mouse
	if evt.Action == ActionDown || evt.Action == ActionMove {
		binary.BigEndian.PutUint32(buf[24:28], 1) // AMOTION_EVENT_BUTTON_PRIMARY
	}

	// buttons (4 bytes)
	if evt.Action == ActionDown || evt.Action == ActionMove {
		binary.BigEndian.PutUint32(buf[28:32], 1)
	}

	return buf
}

// EncodeInjectKeycode encodes a key event for scrcpy control protocol.
func EncodeInjectKeycode(evt KeyEvent) []byte {
	buf := make([]byte, 14)
	buf[0] = ControlMsgTypeInjectKeycode
	buf[1] = byte(evt.Action)

	// keycode (4 bytes)
	binary.BigEndian.PutUint32(buf[2:6], uint32(evt.Keycode))

	// repeat (4 bytes)
	binary.BigEndian.PutUint32(buf[6:10], uint32(evt.Repeat))

	// meta state (4 bytes)
	binary.BigEndian.PutUint32(buf[10:14], uint32(evt.MetaState))

	return buf
}

// EncodeInjectScrollEvent encodes a scroll event for scrcpy control protocol.
func EncodeInjectScrollEvent(evt ScrollEvent) []byte {
	buf := make([]byte, 21)
	buf[0] = ControlMsgTypeInjectScrollEvent

	// position x (4 bytes)
	x := int32(evt.X * float64(evt.Width))
	binary.BigEndian.PutUint32(buf[1:5], uint32(x))

	// position y (4 bytes)
	y := int32(evt.Y * float64(evt.Height))
	binary.BigEndian.PutUint32(buf[5:9], uint32(y))

	// screen width (2 bytes)
	binary.BigEndian.PutUint16(buf[9:11], uint16(evt.Width))

	// screen height (2 bytes)
	binary.BigEndian.PutUint16(buf[11:13], uint16(evt.Height))

	// horizontal scroll (4 bytes, fixed point 16.16)
	binary.BigEndian.PutUint32(buf[13:17], encodeScrollAmount(evt.HScroll))

	// vertical scroll (4 bytes, fixed point 16.16)
	binary.BigEndian.PutUint32(buf[17:21], encodeScrollAmount(evt.VScroll))

	return buf
}

func encodeScrollAmount(amount int) uint32 {
	// scrcpy uses fixed point 16.16
	return uint32(int32(amount) << 16)
}

// EncodeBackOrScreenOn encodes a back/screen-on event.
func EncodeBackOrScreenOn(action int) []byte {
	buf := make([]byte, 2)
	buf[0] = ControlMsgTypeBackOrScreenOn
	buf[1] = byte(action)
	return buf
}
