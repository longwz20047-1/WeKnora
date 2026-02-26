package utils

import (
	"strings"
	"testing"
	"time"
)

func TestGenerateAndValidate(t *testing.T) {
	secret := "test-secret"
	kid := "knowledge-123"
	tid := uint64(42)

	token := GenerateHMACToken(secret, kid, tid, 5*time.Minute)
	gotKID, gotTID, err := ValidateHMACToken(secret, token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotKID != kid {
		t.Errorf("knowledgeID = %q, want %q", gotKID, kid)
	}
	if gotTID != tid {
		t.Errorf("tenantID = %d, want %d", gotTID, tid)
	}
}

func TestExpiredToken(t *testing.T) {
	token := GenerateHMACToken("secret", "kid", 1, 0)
	time.Sleep(1 * time.Second)
	_, _, err := ValidateHMACToken("secret", token)
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected expired error, got: %v", err)
	}
}

func TestTamperedKnowledgeID(t *testing.T) {
	token := GenerateHMACToken("secret", "original-id", 1, 5*time.Minute)
	parts := strings.SplitN(token, ":", 4)
	parts[0] = "tampered-id"
	tampered := strings.Join(parts, ":")
	_, _, err := ValidateHMACToken("secret", tampered)
	if err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("expected signature error, got: %v", err)
	}
}

func TestTamperedTenantID(t *testing.T) {
	token := GenerateHMACToken("secret", "kid", 1, 5*time.Minute)
	parts := strings.SplitN(token, ":", 4)
	parts[1] = "999"
	tampered := strings.Join(parts, ":")
	_, _, err := ValidateHMACToken("secret", tampered)
	if err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("expected signature error, got: %v", err)
	}
}

func TestTamperedExpiry(t *testing.T) {
	token := GenerateHMACToken("secret", "kid", 1, 5*time.Minute)
	parts := strings.SplitN(token, ":", 4)
	parts[2] = "9999999999"
	tampered := strings.Join(parts, ":")
	_, _, err := ValidateHMACToken("secret", tampered)
	if err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("expected signature error, got: %v", err)
	}
}

func TestInvalidFormat(t *testing.T) {
	_, _, err := ValidateHMACToken("secret", "not-a-valid-token")
	if err == nil || !strings.Contains(err.Error(), "invalid token format") {
		t.Fatalf("expected format error, got: %v", err)
	}
}

func TestWrongSecret(t *testing.T) {
	token := GenerateHMACToken("secret-A", "kid", 1, 5*time.Minute)
	_, _, err := ValidateHMACToken("secret-B", token)
	if err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("expected signature error, got: %v", err)
	}
}

func TestEmptyToken(t *testing.T) {
	_, _, err := ValidateHMACToken("secret", "")
	if err == nil {
		t.Fatal("expected error for empty token")
	}
}
