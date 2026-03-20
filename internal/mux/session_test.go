package mux

import (
	"bytes"
	"testing"

	"github.com/google/uuid"
)

func TestWriteValidateAuthToken(t *testing.T) {
	var buf bytes.Buffer
	token := "test-secret-token"

	if err := WriteAuthToken(&buf, token); err != nil {
		t.Fatalf("WriteAuthToken: %v", err)
	}

	// Should be 16 (nonce) + 32 (HMAC) = 48 bytes
	if buf.Len() != 48 {
		t.Fatalf("expected 48 bytes, got %d", buf.Len())
	}

	r := bytes.NewReader(buf.Bytes())
	if err := ValidateAuthToken(r, token); err != nil {
		t.Fatalf("ValidateAuthToken with correct token: %v", err)
	}
}

func TestValidateAuthTokenWrongToken(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteAuthToken(&buf, "correct-token"); err != nil {
		t.Fatalf("WriteAuthToken: %v", err)
	}

	r := bytes.NewReader(buf.Bytes())
	if err := ValidateAuthToken(r, "wrong-token"); err == nil {
		t.Fatal("expected error for wrong token")
	}
}

func TestAuthTokenNotReplayable(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	token := "same-token"

	WriteAuthToken(&buf1, token)
	WriteAuthToken(&buf2, token)

	// Two auth payloads for same token must differ (different nonce)
	if bytes.Equal(buf1.Bytes(), buf2.Bytes()) {
		t.Fatal("two auth payloads are identical — nonce not working")
	}
}

func TestWriteReadSessionID(t *testing.T) {
	id := uuid.New()
	var buf bytes.Buffer

	if err := WriteSessionID(&buf, id); err != nil {
		t.Fatalf("WriteSessionID: %v", err)
	}

	r := bytes.NewReader(buf.Bytes())
	got, err := ReadSessionID(r)
	if err != nil {
		t.Fatalf("ReadSessionID: %v", err)
	}

	if got != id {
		t.Fatalf("session ID mismatch: got %v, want %v", got, id)
	}
}
