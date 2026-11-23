package trae

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"testing"
	"time"
)

func TestDeviceProofMatchesMagpieShape(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	encoded := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: raw})
	proof, err := deviceProof(epBoundExchange, IDEClientID, "refresh", string(encoded), time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if proof["Signature"] == "" || proof["Nonce"] == "" || proof["Timestamp"] != int64(1700000000) {
		t.Fatalf("unexpected proof: %#v", proof)
	}
}

func TestDeviceProofRejectsInvalidKey(t *testing.T) {
	if _, err := deviceProof(epBoundExchange, IDEClientID, "refresh", "not-a-key", time.Now()); err == nil {
		t.Fatal("expected invalid key rejection")
	}
}
