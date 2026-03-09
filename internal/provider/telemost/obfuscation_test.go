package telemost

import (
	"bytes"
	"testing"
)

func TestObfuscationRoundTrip(t *testing.T) {
	key := DeriveObfuscationKey("test-secret")
	data := []byte{0x01, 0x00, 0x42, 0x00, 0x01, 0x01, 0x01, 0xDE, 0xAD}

	frame := buildVP8Frame(data, key)

	// Verify vpnMagic is NOT visible in the obfuscated frame.
	if bytes.Contains(frame[10:], vpnMagic) {
		t.Fatal("vpnMagic should be obfuscated in the frame")
	}

	extracted := extractVP8Data(frame, key)
	if !bytes.Equal(extracted, data) {
		t.Fatalf("round-trip failed: got %x, want %x", extracted, data)
	}
}

func TestObfuscationWrongKey(t *testing.T) {
	key1 := DeriveObfuscationKey("secret-1")
	key2 := DeriveObfuscationKey("secret-2")

	data := []byte{0x01, 0x02, 0x03}
	frame := buildVP8Frame(data, key1)

	// Wrong key should fail to extract (magic won't match).
	extracted := extractVP8Data(frame, key2)
	if extracted != nil {
		t.Fatal("extraction with wrong key should return nil")
	}
}

func TestObfuscationDefaultKey(t *testing.T) {
	// Empty passphrase should still obfuscate (using default key).
	key := DeriveObfuscationKey("")
	data := []byte{0xFF, 0xFE, 0xFD}

	frame := buildVP8Frame(data, key)
	if bytes.Contains(frame[10:], vpnMagic) {
		t.Fatal("default key should still obfuscate vpnMagic")
	}

	extracted := extractVP8Data(frame, key)
	if !bytes.Equal(extracted, data) {
		t.Fatalf("round-trip with default key failed: got %x, want %x", extracted, data)
	}
}

func TestObfuscationLargePayload(t *testing.T) {
	key := DeriveObfuscationKey("large-test")
	data := make([]byte, maxVP8Data)
	for i := range data {
		data[i] = byte(i)
	}

	frame := buildVP8Frame(data, key)
	extracted := extractVP8Data(frame, key)
	if !bytes.Equal(extracted, data) {
		t.Fatal("large payload round-trip failed")
	}
}
