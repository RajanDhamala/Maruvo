package utils

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"math/big"
	"strings"
)

const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

func WalletKey(address string) (ed25519.PublicKey, error) {
	if len(address) < 32 || len(address) > 44 {
		return nil, errors.New("invalid wallet address")
	}

	number := new(big.Int)

	for _, c := range address {
		digit := strings.IndexRune(base58Alphabet, c)
		if digit < 0 {
			return nil, errors.New("invalid wallet address")
		}

		number.Mul(number, big.NewInt(58)).Add(number, big.NewInt(int64(digit)))
	}

	data := append(make([]byte, len(address)-len(strings.TrimLeft(address, "1"))), number.Bytes()...)
	if len(data) != ed25519.PublicKeySize {
		return nil, errors.New("invalid wallet address")
	}

	return ed25519.PublicKey(data), nil
}

func Base58(data []byte) string {
	number, radix, remainder := new(big.Int).SetBytes(data), big.NewInt(58), new(big.Int)
	result := ""

	for number.Sign() > 0 {
		number.QuoRem(number, radix, remainder)
		result = string(base58Alphabet[remainder.Int64()]) + result
	}

	for _, b := range data {
		if b != 0 {
			break
		}

		result = "1" + result
	}

	return result
}

func TransactionSignature(unsigned, signed, address string) (string, error) {
	expected, e1 := base64.StdEncoding.DecodeString(unsigned)
	actual, e2 := base64.StdEncoding.DecodeString(signed)

	key, e3 := WalletKey(address)
	if e1 != nil || e2 != nil || e3 != nil || len(actual) <= 65 || len(actual) > 1232 ||
		len(expected) != len(actual) ||
		actual[0] != 1 ||
		expected[0] != 1 ||
		!bytes.Equal(expected[65:], actual[65:]) ||
		!ed25519.Verify(key, actual[65:], actual[1:65]) {
		return "", errors.New("signed transaction does not match the prepared transaction")
	}

	return Base58(actual[1:65]), nil
}
