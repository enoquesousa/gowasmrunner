package gateway

import (
	"crypto/hmac"
	"crypto/sha256"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

type authorization struct {
	configured bool
	digest     [sha256.Size]byte
}

func (a authorization) check(r *http.Request) int {
	values := r.Header.Values("Authorization")
	if !a.configured {
		if len(values) != 0 {
			return http.StatusUnauthorized
		}
		if !loopbackPeer(r.RemoteAddr) {
			return http.StatusForbidden
		}
		return 0
	}
	if len(values) != 1 {
		return http.StatusUnauthorized
	}
	if r.TLS == nil && !loopbackPeer(r.RemoteAddr) {
		return http.StatusForbidden
	}
	return a.checkBearer(values[0])
}

func (a authorization) checkBearer(value string) int {
	scheme, token, found := strings.Cut(value, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") || !validToken(token) {
		return http.StatusUnauthorized
	}
	digest := sha256.Sum256([]byte(token))
	if !hmac.Equal(a.digest[:], digest[:]) {
		return http.StatusUnauthorized
	}
	return 0
}

func validToken(token string) bool {
	if len(token) < 32 || len(token) > 4096 {
		return false
	}
	for index := range len(token) {
		character := token[index]
		if !asciiAlphanumeric(character) && !strings.ContainsRune("-._~+/=", rune(character)) {
			return false
		}
	}
	return true
}

func asciiAlphanumeric(character byte) bool {
	return character >= 'a' && character <= 'z' ||
		character >= 'A' && character <= 'Z' || character >= '0' && character <= '9'
}

func loopbackPeer(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.Zone() == "" && ip.IsLoopback()
}

func localHost(authority string) bool {
	host := authority
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	} else if strings.Contains(authority, ":") {
		var port string
		var err error
		host, port, err = net.SplitHostPort(authority)
		if err != nil || validatePort(port) != nil {
			return false
		}
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.Zone() == "" && ip.IsLoopback()
}
