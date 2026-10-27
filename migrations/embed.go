// Package migrations는 순서 있는 SQL 마이그레이션을 바이너리에 포함한다.
// 파일명은 NNNN_설명.sql 이고, 한 번 적용한 파일은 고치지 않는다 (체크섬으로 검사).
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
