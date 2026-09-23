package token

import (
	"crypto/aes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

type Payload struct {
	UID  int     `json:"uid"`
	Role int     `json:"role"`
	Exp  float64 `json:"exp"`
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	padLen := blockSize - len(data)%blockSize
	pad := make([]byte, padLen)
	for i := range pad {
		pad[i] = byte(padLen)
	}
	return append(data, pad...)
}

func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, fmt.Errorf("invalid padded length: %d", len(data))
	}
	padLen := int(data[len(data)-1])
	if padLen == 0 || padLen > blockSize || padLen > len(data) {
		return nil, fmt.Errorf("invalid padding byte: %d", padLen)
	}
	for i := 0; i < padLen; i++ {
		if data[len(data)-1-i] != byte(padLen) {
			return nil, fmt.Errorf("invalid padding sequence")
		}
	}
	return data[:len(data)-padLen], nil
}

func encryptECB(plaintext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes new cipher: %w", err)
	}
	blockSize := block.BlockSize()
	padded := pkcs7Pad(plaintext, blockSize)
	ciphertext := make([]byte, len(padded))
	for i := 0; i < len(padded); i += blockSize {
		block.Encrypt(ciphertext[i:i+blockSize], padded[i:i+blockSize])
	}
	return ciphertext, nil
}

func decryptECB(ciphertext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes new cipher: %w", err)
	}
	blockSize := block.BlockSize()
	if len(ciphertext)%blockSize != 0 {
		return nil, fmt.Errorf("ciphertext length %d is not multiple of block size %d", len(ciphertext), blockSize)
	}
	plaintext := make([]byte, len(ciphertext))
	for i := 0; i < len(ciphertext); i += blockSize {
		block.Decrypt(plaintext[i:i+blockSize], ciphertext[i:i+blockSize])
	}
	return pkcs7Unpad(plaintext, blockSize)
}

func Create(uid, role int, sessionTimeoutSec int, cypherKey string) (string, error) {
	payload := Payload{
		UID:  uid,
		Role: role,
		Exp:  float64(time.Now().Unix()) + float64(sessionTimeoutSec),
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}
	encrypted, err := encryptECB(payloadBytes, []byte(cypherKey))
	if err != nil {
		return "", fmt.Errorf("encrypt: %w", err)
	}
	return base64.URLEncoding.EncodeToString(encrypted), nil
}

func Decode(tokenStr string, cypherKey []byte) (*Payload, error) {
	if len(cypherKey) == 0 {
		return nil, fmt.Errorf("cypher_key is empty")
	}
	ciphertext, err := base64.URLEncoding.DecodeString(tokenStr)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	plaintext, err := decryptECB(ciphertext, cypherKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	var payload Payload
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		return nil, fmt.Errorf("unmarshal payload: %w", err)
	}
	if float64(time.Now().Unix()) > payload.Exp {
		return nil, fmt.Errorf("token expired")
	}
	return &payload, nil
}