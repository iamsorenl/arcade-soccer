package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// Mailer sends sign-in links. Tests use fakeMailer; production uses smtpMailer;
// local dev (no SMTP_HOST) uses logMailer.
type Mailer interface {
	SendLogin(ctx context.Context, to, link string) error
}

// logMailer prints the link instead of sending it, for local dev.
type logMailer struct{}

func (logMailer) SendLogin(_ context.Context, to, link string) error {
	log.Printf("sign-in link for %s: %s", to, link)
	return nil
}

// smtpMailer sends through an SMTP server that supports STARTTLS (Gmail on 587
// with an app password).
type smtpMailer struct {
	host, user, pass, from string
	port                   int
}

func (m smtpMailer) SendLogin(ctx context.Context, to, link string) error {
	if strings.ContainsAny(to, "\r\n") {
		return fmt.Errorf("bad recipient")
	}
	addr := net.JoinHostPort(m.host, fmt.Sprint(m.port))
	d := net.Dialer{Timeout: 10 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	conn.SetDeadline(time.Now().Add(20 * time.Second))
	c, err := smtp.NewClient(conn, m.host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if err := c.StartTLS(&tls.Config{ServerName: m.host}); err != nil {
		return err
	}
	if err := c.Auth(smtp.PlainAuth("", m.user, m.pass, m.host)); err != nil {
		return err
	}
	if err := c.Mail(m.user); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	wc, err := c.Data()
	if err != nil {
		return err
	}
	msg := "From: " + m.from + "\r\n" +
		"To: " + to + "\r\n" +
		"Subject: Your Arcade Soccer sign-in link\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"Click to sign in to the AI League:\r\n\r\n" + link + "\r\n\r\n" +
		"The link works once and expires in 15 minutes. If you didn't ask for it, ignore this email.\r\n"
	if _, err := wc.Write([]byte(msg)); err != nil {
		return err
	}
	if err := wc.Close(); err != nil {
		return err
	}
	return c.Quit()
}
