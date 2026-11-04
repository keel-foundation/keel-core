package federation

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
)

func parseRSAPrivateKey(value string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(value))
	if block == nil {
		return nil, fmt.Errorf("federation private key is not PEM")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		typed, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("federation private key is not RSA")
		}
		return typed, nil
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	return nil, fmt.Errorf("federation private key is not RSA")
}

func parseRSAPublicKey(value string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(value))
	if block == nil {
		return nil, fmt.Errorf("federation public key is not PEM")
	}
	if key, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		typed, ok := key.(*rsa.PublicKey)
		if !ok {
			return nil, fmt.Errorf("federation public key is not RSA")
		}
		return typed, nil
	}
	if key, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
		return key, nil
	}
	return nil, fmt.Errorf("federation public key is not RSA")
}

func encodePrivateKey(key *rsa.PrivateKey) (string, error) {
	body, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: body})), nil
}

func encodePublicKey(key *rsa.PublicKey) (string, error) {
	body, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: body})), nil
}
