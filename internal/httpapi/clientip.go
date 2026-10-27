package httpapi

import (
	"net"
	"net/http"
	"strings"
)

// ClientIP는 신뢰한 프록시 대역에서 온 요청만 X-Forwarded-For를 따른다.
// 임의 헤더로 로그인 시도 제한을 우회하지 못하게 한다.
func ClientIP(r *http.Request, trusted []*net.IPNet) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil || !inNets(ip, trusted) {
		return host
	}
	// 오른쪽부터 신뢰하지 않는 첫 주소가 실제 클라이언트다
	parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		p := strings.TrimSpace(parts[i])
		pip := net.ParseIP(p)
		if pip == nil {
			break
		}
		if !inNets(pip, trusted) {
			return pip.String()
		}
	}
	return host
}

func inNets(ip net.IP, nets []*net.IPNet) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
