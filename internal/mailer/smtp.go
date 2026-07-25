package mailer

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"
)

type Client struct {
	host     string
	address  string
	username string
	password string
	from     mail.Address
	tlsMode  string
	timeout  time.Duration
}

type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	TLSMode  string
	Timeout  time.Duration
}

type Message struct {
	To       string `json:"to"`
	Subject  string `json:"subject"`
	TextBody string `json:"text_body"`
	HTMLBody string `json:"html_body"`
}

func New(config Config) (*Client, error) {
	from, err := mail.ParseAddress(config.From)
	if err != nil {
		return nil, fmt.Errorf("parse SMTP_FROM: %w", err)
	}
	if config.Host == "" || config.Port < 1 {
		return nil, errors.New("SMTP host and port are required")
	}
	if config.TLSMode == "" {
		config.TLSMode = "starttls"
	}
	if config.TLSMode != "starttls" && config.TLSMode != "tls" {
		return nil, errors.New("SMTP_TLS_MODE must be starttls or tls")
	}
	if config.Timeout <= 0 {
		config.Timeout = 30 * time.Second
	}
	return &Client{
		host:     config.Host,
		address:  net.JoinHostPort(config.Host, fmt.Sprintf("%d", config.Port)),
		username: config.Username,
		password: config.Password,
		from:     *from,
		tlsMode:  config.TLSMode,
		timeout:  config.Timeout,
	}, nil
}

func (c *Client) Send(ctx context.Context, message Message) error {
	to, err := mail.ParseAddress(message.To)
	if err != nil {
		return fmt.Errorf("parse recipient: %w", err)
	}
	if strings.ContainsAny(message.Subject, "\r\n") {
		return errors.New("email subject contains a newline")
	}
	body, err := buildMessage(c.from, *to, message)
	if err != nil {
		return err
	}

	dialer := net.Dialer{Timeout: c.timeout}
	var connection net.Conn
	if c.tlsMode == "tls" {
		connection, err = tls.DialWithDialer(&dialer, "tcp", c.address, &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: c.host,
		})
	} else {
		connection, err = dialer.DialContext(ctx, "tcp", c.address)
	}
	if err != nil {
		return fmt.Errorf("connect to SMTP server: %w", err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(c.timeout))

	client, err := smtp.NewClient(connection, c.host)
	if err != nil {
		return fmt.Errorf("start SMTP client: %w", err)
	}
	defer client.Close()
	if c.tlsMode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("SMTP server does not support STARTTLS")
		}
		if err := client.StartTLS(&tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: c.host,
		}); err != nil {
			return fmt.Errorf("start SMTP TLS: %w", err)
		}
	}
	if c.username != "" {
		if err := client.Auth(smtp.PlainAuth("", c.username, c.password, c.host)); err != nil {
			return fmt.Errorf("authenticate to SMTP: %w", err)
		}
	}
	if err := client.Mail(c.from.Address); err != nil {
		return fmt.Errorf("set SMTP sender: %w", err)
	}
	if err := client.Rcpt(to.Address); err != nil {
		return fmt.Errorf("set SMTP recipient: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("open SMTP message: %w", err)
	}
	if _, err := writer.Write(body); err != nil {
		writer.Close()
		return fmt.Errorf("write SMTP message: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("finish SMTP message: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("quit SMTP session: %w", err)
	}
	return nil
}

func buildMessage(from, to mail.Address, message Message) ([]byte, error) {
	var body bytes.Buffer
	mixed := multipart.NewWriter(&body)
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return nil, fmt.Errorf("generate email message ID: %w", err)
	}
	messageDomain := "localhost"
	if _, domain, ok := strings.Cut(from.Address, "@"); ok && domain != "" {
		messageDomain = domain
	}
	headers := textproto.MIMEHeader{}
	headers.Set("From", from.String())
	headers.Set("To", to.String())
	headers.Set("Subject", message.Subject)
	headers.Set("Date", time.Now().UTC().Format(time.RFC1123Z))
	headers.Set("Message-ID", "<"+hex.EncodeToString(random)+"@"+messageDomain+">")
	headers.Set("MIME-Version", "1.0")
	headers.Set("Content-Type", `multipart/alternative; boundary="`+mixed.Boundary()+`"`)
	for _, key := range []string{
		"From",
		"To",
		"Subject",
		"Date",
		"Message-ID",
		"MIME-Version",
		"Content-Type",
	} {
		fmt.Fprintf(&body, "%s: %s\r\n", key, headers.Get(key))
	}
	body.WriteString("\r\n")

	textHeaders := textproto.MIMEHeader{}
	textHeaders.Set("Content-Type", `text/plain; charset="UTF-8"`)
	textHeaders.Set("Content-Transfer-Encoding", "quoted-printable")
	textPart, err := mixed.CreatePart(textHeaders)
	if err != nil {
		return nil, fmt.Errorf("create text email part: %w", err)
	}
	textEncoder := quotedprintable.NewWriter(textPart)
	if _, err := textEncoder.Write([]byte(message.TextBody)); err != nil {
		return nil, fmt.Errorf("write text email part: %w", err)
	}
	if err := textEncoder.Close(); err != nil {
		return nil, fmt.Errorf("close text email part: %w", err)
	}
	htmlHeaders := textproto.MIMEHeader{}
	htmlHeaders.Set("Content-Type", `text/html; charset="UTF-8"`)
	htmlHeaders.Set("Content-Transfer-Encoding", "quoted-printable")
	htmlPart, err := mixed.CreatePart(htmlHeaders)
	if err != nil {
		return nil, fmt.Errorf("create HTML email part: %w", err)
	}
	htmlEncoder := quotedprintable.NewWriter(htmlPart)
	if _, err := htmlEncoder.Write([]byte(message.HTMLBody)); err != nil {
		return nil, fmt.Errorf("write HTML email part: %w", err)
	}
	if err := htmlEncoder.Close(); err != nil {
		return nil, fmt.Errorf("close HTML email part: %w", err)
	}
	if err := mixed.Close(); err != nil {
		return nil, fmt.Errorf("close email body: %w", err)
	}
	return body.Bytes(), nil
}
