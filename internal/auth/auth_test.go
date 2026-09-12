package auth

import "testing"

func TestGenerateTokenAndHash(t *testing.T) {
	token, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken returned error: %v", err)
	}
	if token == "" {
		t.Fatal("GenerateToken returned empty token")
	}
	if HashToken(token) == token {
		t.Fatal("HashToken returned raw token")
	}
	if HashToken(token) != HashToken(token) {
		t.Fatal("HashToken is not stable")
	}
}
