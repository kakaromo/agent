package main

import (
	"embed"
	"io/fs"
)

//go:embed all:ui/build
var uiBuildFS embed.FS

// uiFS 는 ui/build/ 하위만 노출하는 sub-FS 다.
// SvelteKit adapter-static 산출물(index.html, _app/, favicon.svg) 를 정적 서빙한다.
// standalone=false 일 때는 사용되지 않는다 (HTTP router 가 EnableUI=false 로 마운트).
func uiFS() fs.FS {
	sub, err := fs.Sub(uiBuildFS, "ui/build")
	if err != nil {
		// 빌드 시점에 확정된 경로이므로 실패는 dev 환경 문제 → panic 으로 빠르게 노출.
		panic("uiFS sub: " + err.Error())
	}
	return sub
}
