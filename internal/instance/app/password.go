package app

import (
	"crypto/rand"
	"math/big"
)

const initialPasswordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789!@#$%*-_"

func generateInitialPassword() (string, error) {
	const length = 18
	out := make([]byte, length)
	max := big.NewInt(int64(len(initialPasswordAlphabet)))
	for i := range out {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = initialPasswordAlphabet[n.Int64()]
	}
	return string(out), nil
}
