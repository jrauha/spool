package mailer

import (
	"strings"
	"testing"
)

func TestSMTPPasswordResetMessage(t *testing.T) {
	sender, err := NewSMTPPasswordResetSender(
		"smtp.example.com:587", "user", "password", "Spool <spool@example.com>", "https://spool.example/app",
	)
	if err != nil {
		t.Fatalf("NewSMTPPasswordResetSender returned error: %v", err)
	}
	message := string(sender.message("user@example.com", "secret/token"))
	for _, want := range []string{
		"To: user@example.com\r\n",
		"Subject: Reset your Spool password\r\n",
		"https://spool.example/app/reset?token=secret%2Ftoken",
		"expires in one hour",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("message missing %q: %q", want, message)
		}
	}
}

func TestSMTPPasswordResetSenderRequiresHTTPS(t *testing.T) {
	_, err := NewSMTPPasswordResetSender(
		"smtp.example.com:587", "", "", "spool@example.com", "http://spool.example",
	)
	if err == nil {
		t.Fatal("NewSMTPPasswordResetSender returned nil error")
	}
}
