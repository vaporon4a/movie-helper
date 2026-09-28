package featurevote

import (
	"crypto/rand"
	"encoding/hex"
)

func RandomToken() (string, error) {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
