package utils

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"strings"
	"time"
)

type Mailer struct{ Host, Port, Username, Password, From string }

func NewMailerFromEnv() (*Mailer, error) {
	host := strings.TrimSpace(os.Getenv("SMTP_HOST"))
	if host == "" {
		return nil, nil
	}
	m := &Mailer{Host: host, Port: os.Getenv("SMTP_PORT"), Username: os.Getenv("SMTP_USERNAME"), Password: os.Getenv("SMTP_PASSWORD"), From: os.Getenv("SMTP_FROM")}
	if m.Port == "" {
		m.Port = "587"
	}
	if _, err := mail.ParseAddress(m.From); err != nil || strings.ContainsAny(m.From, "\r\n") {
		return nil, errors.New("SMTP_FROM must be a valid email address")
	}
	if (m.Username == "") != (m.Password == "") {
		return nil, errors.New("SMTP_USERNAME and SMTP_PASSWORD must be set together")
	}
	return m, nil
}

func (m *Mailer) Send(ctx context.Context, to, subject, body, id string) error {
	from, err := mail.ParseAddress(m.From)
	if err != nil {
		return err
	}
	recipient, err := mail.ParseAddress(to)
	if err != nil {
		return err
	}
	if strings.ContainsAny(m.From+to+subject+id, "\r\n") {
		return errors.New("invalid email header")
	}
	address := net.JoinHostPort(m.Host, m.Port)
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	defer conn.Close()
	deadline := time.Now().Add(20 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	tlsConfig := &tls.Config{ServerName: m.Host, MinVersion: tls.VersionTLS12}
	if m.Port == "465" {
		secure := tls.Client(conn, tlsConfig)
		if err := secure.HandshakeContext(ctx); err != nil {
			return err
		}
		conn = secure
	}
	client, err := smtp.NewClient(conn, m.Host)
	if err != nil {
		return err
	}
	defer client.Close()
	if m.Port != "465" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("SMTP server must support STARTTLS")
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			return err
		}
	}
	if m.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", m.Username, m.Password, m.Host)); err != nil {
			return err
		}
	}
	if err := client.Mail(from.Address); err != nil {
		return err
	}
	if err := client.Rcpt(recipient.Address); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	body = strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n")
	_, err = fmt.Fprintf(writer, "From: %s\r\nTo: %s\r\nSubject: %s\r\nMessage-ID: <%s@%s>\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n", from.String(), recipient.String(), subject, id, strings.Split(from.Address, "@")[1])
	if err != nil {
		return err
	}
	encoded := quotedprintable.NewWriter(writer)
	if _, err := fmt.Fprint(encoded, body); err != nil {
		return err
	}
	if err := encoded.Close(); err != nil {
		return err
	}
	// DATA acknowledgement means accepted; a QUIT failure must not cause a resend.
	return writer.Close()
}
