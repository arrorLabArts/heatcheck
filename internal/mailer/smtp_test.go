package mailer

import (
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
)

func TestBuildMessageCreatesPlainTextAndHTMLParts(t *testing.T) {
	from := mail.Address{Name: "HeatCheck", Address: "no-reply@heatcheck.example"}
	to := mail.Address{Address: "player@example.test"}
	encoded, err := buildMessage(from, to, Message{
		Subject:  "Verify email",
		TextBody: "plain body",
		HTMLBody: "<p>HTML body</p>",
	})
	if err != nil {
		t.Fatal(err)
	}
	message, err := mail.ReadMessage(strings.NewReader(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	mediaType, parameters, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	if mediaType != "multipart/alternative" {
		t.Fatalf("media type = %q, want multipart/alternative", mediaType)
	}
	reader := multipart.NewReader(message.Body, parameters["boundary"])
	var parts []string
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(part)
		if err != nil {
			t.Fatal(err)
		}
		parts = append(parts, string(body))
	}
	if len(parts) != 2 || parts[0] != "plain body" || parts[1] != "<p>HTML body</p>" {
		t.Fatalf("unexpected MIME parts: %#v", parts)
	}
}

func TestNewRejectsInsecureTLSMode(t *testing.T) {
	_, err := New(Config{
		Host:    "smtp.example.test",
		Port:    587,
		From:    "HeatCheck <no-reply@heatcheck.example>",
		TLSMode: "none",
	})
	if err == nil {
		t.Fatal("New() error = nil, want unsupported TLS mode error")
	}
}
