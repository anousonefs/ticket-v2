package jdb

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"math/big"
	"os"

	"golang.org/x/crypto/bcrypt"
)

func randomNumber() (string, error) {
	min := big.NewInt(100000000000000000)
	max := big.NewInt(999999999999999999)
	n, err := rand.Int(rand.Reader, new(big.Int).Sub(max, min))
	if err != nil {
		return "", err
	}

	n.Add(n, min)
	return n.String(), nil
}

func CompareHash(oldHash, newHash []byte) bool {
	if err := bcrypt.CompareHashAndPassword([]byte(oldHash), []byte(newHash)); err != nil {
		return false
	}
	return true
}

func GenerateHash(hash []byte) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(hash), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func ConfirmHash(hash, confirm string) bool {
	if hash != confirm {
		return false
	}
	return hash == confirm
}

// Hmac256 is a function to generate HMAC256 hash.
func Hmac256(body, key []byte) string {
	hash := hmac.New(sha256.New, key)
	hash.Write(body)
	return hex.EncodeToString(hash.Sum(nil))
}

func GetExternalUserID(userID string) string {
	externalUserID := Hmac256([]byte(userID), []byte(os.Getenv("ONESIGNAL_REST_KEY")))
	return externalUserID
}

// VerifyHMACAndRetrieveMessage is a function to verify HMAC and retrieve the UUIDKey if valid.
func VerifyHMACAndRetrieveUUIDKey(key, uuidKey, receivedHMAC string) (string, bool) {
	expectedHMAC := Hmac256([]byte(uuidKey), []byte(key))
	if hmac.Equal([]byte(receivedHMAC), []byte(expectedHMAC)) {
		return uuidKey, true
	}

	return "", false
}
