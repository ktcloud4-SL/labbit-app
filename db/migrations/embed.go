// Package migrations는 Git SSOT SQL Migration을 실행 파일에 포함한다.
//
// Migration runner와 labbit-server readiness가 같은 build의 같은 파일 집합을 기준으로
// 적용·호환성을 판단하도록 별도 경로 설정 없이 embed한다.
package migrations

import "embed"

//go:embed *.sql
var Files embed.FS
