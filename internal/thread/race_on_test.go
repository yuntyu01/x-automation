//go:build race

package thread

// 레이스 검사기를 켜면 실행이 10배 넘게 느려져 시간 기준 검사를 하지 않는다
const raceEnabled = true
