package utils

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// GenerateHMACToken creates a time-limited HMAC-SHA256 token.
// Format: "{knowledgeID}:{tenantID}:{expiry_unix}:{signature_hex}"
func GenerateHMACToken(secret, knowledgeID string, tenantID uint64, ttl time.Duration) string {
	expiry := time.Now().Add(ttl).Unix()
	payload := fmt.Sprintf("%s:%d:%d", knowledgeID, tenantID, expiry)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf("%s:%s", payload, sig)
}

// ValidateHMACToken validates a token and returns (knowledgeID, tenantID, error).
func ValidateHMACToken(secret, token string) (string, uint64, error) {
	parts := strings.SplitN(token, ":", 4)
	if len(parts) != 4 {
		return "", 0, fmt.Errorf("invalid token format")
	}
	knowledgeID := parts[0]
	tenantID, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return "", 0, fmt.Errorf("invalid tenant ID")
	}
	expiry, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return "", 0, fmt.Errorf("invalid expiry")
	}
	if time.Now().Unix() > expiry {
		return "", 0, fmt.Errorf("token expired")
	}
	payload := fmt.Sprintf("%s:%d:%d", knowledgeID, tenantID, expiry)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(parts[3]), []byte(expected)) {
		return "", 0, fmt.Errorf("invalid signature")
	}
	return knowledgeID, tenantID, nil
}
