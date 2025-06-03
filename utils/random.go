package utils

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
)

func GenerateCode(n int) (string, error) {
	// Make a slice of nBytes random bytes.
	byt := make([]byte, n)

	// Read into the slice.
	if _, err := rand.Read(byt); err != nil {
		return "", err
	}

	// Return the hexadecimal string.
	return strings.ToUpper(hex.EncodeToString(byt)), nil
}

func GenerateOTP(length int) (string, error) {
	// OTPCharset is the default charset used for OTP generation.
	const charset = "0123456789"

	// Make a slice of length random bytes.
	code := make([]byte, length)

	// Read into the slice.
	if _, err := rand.Read(code); err != nil {
		return "", err
	}

	// Convert bytes to string.
	for i := 0; i < length; i++ {
		code[i] = charset[int(code[i])%len(charset)]
	}

	return string(code), nil
}
