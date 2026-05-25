package base62

import "errors"

const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

var reverse [256]int64

func init() {
	for i := range reverse {
		reverse[i] = -1
	}
	for i, b := range []byte(alphabet) {
		reverse[b] = int64(i)
	}
}

func Encode(n uint64) string {
	if n == 0 {
		return "0"
	}

	buf := make([]byte, 0, 11)
	for n > 0 {
		buf = append(buf, alphabet[n%62])
		n /= 62
	}

	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}

func Decode(s string) (uint64, error) {
	if s == "" {
		return 0, errors.New("empty base62 string")
	}

	var n uint64
	for i := 0; i < len(s); i++ {
		b := s[i]
		if reverse[b] < 0 {
			return 0, errors.New("invalid base62 character")
		}
		n = n*62 + uint64(reverse[b])
	}
	return n, nil
}
