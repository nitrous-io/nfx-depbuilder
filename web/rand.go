package web

import "crypto/rand"

const dict = "0123456789abcdefghijklmnopqrstuvwxyz"

func randomID(n uint) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	bl := byte(len(dict))
	for k, v := range b {
		b[k] = dict[v%bl]
	}
	return string(b), nil
}
