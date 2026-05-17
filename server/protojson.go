package server

import (
	"strconv"

	"google.golang.org/protobuf/encoding/protojson"
)

// protojson 직렬화 옵션 — enum 문자열, unpopulated 필드도 출력.
// portal frontend 가 string enum 을 기대하는 곳(예: WS metrics raw push)에서 사용.
var marshalOpts = protojson.MarshalOptions{
	UseEnumNumbers:  false,
	EmitUnpopulated: true,
}

// parseFloat64 — querystring 등 string → float64. 실패 시 default.
func parseFloat64(s string, def float64) float64 {
	if s == "" {
		return def
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return def
	}
	return n
}
