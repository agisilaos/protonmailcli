package bridge

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This server accepts only a loopback connection and never delivers mail.
type smtpReceived struct {
	raw        string
	recipients []string
}

func smtpTestServer(t *testing.T, quitReply, dataReply string, serverTLS ...*tls.Config) (SMTPConfig, <-chan smtpReceived) {
	t.Helper()
	return smtpTestServerWithDataChallenge(t, quitReply, "354 send data", dataReply, serverTLS...)
}

func smtpTestServerWithDataChallenge(t *testing.T, quitReply, challenge, dataReply string, serverTLS ...*tls.Config) (SMTPConfig, <-chan smtpReceived) {
	t.Helper()
	return smtpTestServerWithRawDataReplies(t, quitReply, challenge+"\r\n", dataReply+"\r\n", serverTLS...)
}

func smtpTestServerWithRawDataReplies(t *testing.T, quitReply, challenge, dataReply string, serverTLS ...*tls.Config) (SMTPConfig, <-chan smtpReceived) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	messages := make(chan smtpReceived, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		r := bufio.NewReader(conn)
		tlsStarted := false
		var recipients []string
		fmt.Fprint(conn, "220 localhost synthetic SMTP\r\n")
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			switch {
			case strings.HasPrefix(line, "EHLO "):
				if len(serverTLS) > 0 && !tlsStarted {
					fmt.Fprint(conn, "250-localhost\r\n250 STARTTLS\r\n")
				} else {
					fmt.Fprint(conn, "250 localhost\r\n")
				}
			case line == "STARTTLS\r\n" && len(serverTLS) > 0:
				fmt.Fprint(conn, "220 begin TLS\r\n")
				tlsConn := tls.Server(conn, serverTLS[0])
				if err := tlsConn.Handshake(); err != nil {
					return
				}
				conn, r, tlsStarted = tlsConn, bufio.NewReader(tlsConn), true
			case strings.HasPrefix(line, "MAIL FROM:"):
				fmt.Fprint(conn, "250 ok\r\n")
			case strings.HasPrefix(line, "RCPT TO:"):
				recipients = append(recipients, strings.TrimSpace(strings.TrimPrefix(line, "RCPT TO:")))
				fmt.Fprint(conn, "250 ok\r\n")
			case line == "DATA\r\n":
				fmt.Fprint(conn, challenge)
				if !strings.HasSuffix(challenge, "\r\n") {
					return
				}
				raw, err := textproto.NewReader(r).ReadDotBytes()
				if err != nil {
					return
				}
				messages <- smtpReceived{raw: string(raw), recipients: recipients}
				fmt.Fprint(conn, dataReply)
				if !strings.HasSuffix(dataReply, "\r\n") {
					return
				}
			case line == "QUIT\r\n":
				fmt.Fprint(conn, quitReply+"\r\n")
				return
			default:
				fmt.Fprint(conn, "500 unsupported\r\n")
			}
		}
	}()
	t.Cleanup(func() { ln.Close(); <-done })
	return SMTPConfig{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port}, messages
}

func smtpTestCertificate(t *testing.T, validHostname bool) (*tls.Config, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "synthetic Bridge"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage:    x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    []string{"wrong.example.invalid"},
	}
	if validHostname {
		cert.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "bridge-cert.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}, path
}

func TestSendTrustsOnlyExplicitBridgeCertificate(t *testing.T) {
	for _, trusted := range []bool{false, true} {
		t.Run(fmt.Sprint(trusted), func(t *testing.T) {
			serverTLS, certFile := smtpTestCertificate(t, true)
			cfg, _ := smtpTestServer(t, "221 bye", "250 accepted", serverTLS)
			if trusted {
				cfg.TLSCertFile = certFile
			}
			err := Send(cfg, SendInput{From: "sender@example.invalid", To: []string{"recipient@example.invalid"}, Body: "test"})
			if trusted && err != nil {
				t.Fatalf("explicitly trusted self-signed Bridge certificate rejected: %v", err)
			}
			if !trusted && err == nil {
				t.Fatal("untrusted certificate accepted")
			}
		})
	}
}

func TestSendExplicitTrustStillVerifiesHostname(t *testing.T) {
	serverTLS, certFile := smtpTestCertificate(t, false)
	cfg, _ := smtpTestServer(t, "221 bye", "250 accepted", serverTLS)
	cfg.TLSCertFile = certFile
	if err := Send(cfg, SendInput{From: "sender@example.invalid", To: []string{"recipient@example.invalid"}}); err == nil {
		t.Fatal("certificate for wrong host accepted")
	}
}

func TestSendExplicitTrustRequiresSTARTTLS(t *testing.T) {
	_, certFile := smtpTestCertificate(t, true)
	cfg, _ := smtpTestServer(t, "221 bye", "250 accepted")
	cfg.TLSCertFile = certFile
	if err := Send(cfg, SendInput{From: "sender@example.invalid", To: []string{"recipient@example.invalid"}}); err == nil {
		t.Fatal("explicit TLS trust silently downgraded to plaintext")
	}
}

func TestSendAcknowledgedDataSucceedsWhenQuitFails(t *testing.T) {
	cfg, received := smtpTestServer(t, "500 cannot quit", "250 accepted")
	err := Send(cfg, SendInput{From: "sender@example.invalid", To: []string{"recipient@example.invalid"}, Subject: "synthetic acceptance", Body: "test"})
	if err != nil {
		t.Fatalf("server accepted DATA, but Send returned a resendable failure: %v", err)
	}
	if raw := (<-received).raw; !strings.Contains(raw, "Subject: synthetic acceptance") {
		t.Fatalf("wrong accepted message: %q", raw)
	}
}

func TestSendEnvelopeOverridePreservesVisibleRecipients(t *testing.T) {
	cfg, received := smtpTestServer(t, "221 bye", "250 accepted")
	err := Send(cfg, SendInput{From: "self@example.invalid", To: []string{"intended@example.invalid"}, EnvelopeTo: []string{"self@example.invalid"}, Subject: "draft", Body: "test"})
	if err != nil {
		t.Fatal(err)
	}
	message := <-received
	if len(message.recipients) != 1 || message.recipients[0] != "<self@example.invalid>" {
		t.Fatalf("wrong delivery recipients: %#v", message.recipients)
	}
	if !strings.Contains(message.raw, "To: intended@example.invalid\n") {
		t.Fatalf("visible draft recipients were replaced: %q", message.raw)
	}
}

func TestSendUnacknowledgedDataReturnsError(t *testing.T) {
	cfg, _ := smtpTestServer(t, "221 bye", "")
	if err := Send(cfg, SendInput{From: "sender@example.invalid", To: []string{"recipient@example.invalid"}, Body: "test"}); !errors.Is(err, ErrSMTPDataUncertain) {
		t.Fatalf("missing final DATA acknowledgement: %v", err)
	}
}

func TestValidateMessageHeadersRejectsAddressLineInjection(t *testing.T) {
	for _, tc := range []struct {
		from string
		to   []string
	}{
		{from: "self@example.invalid\r\nBcc: hidden@example.invalid"},
		{to: []string{"intended@example.invalid\nBcc: hidden@example.invalid"}},
	} {
		if err := ValidateMessageHeaders(tc.from, tc.to, "ordinary subject", nil); err == nil {
			t.Fatal("address header injection accepted")
		}
	}
	if err := ValidateMessageHeaders("self@example.invalid", []string{"intended@example.invalid"}, "ordinary subject", map[string]string{"In-Reply-To": "<id@example.invalid>"}); err != nil {
		t.Fatalf("valid headers rejected: %v", err)
	}
}

func TestSendRejectedDataReturnsError(t *testing.T) {
	for _, code := range []int{451, 550} {
		cfg, _ := smtpTestServer(t, "221 bye", fmt.Sprintf("%d rejected", code))
		err := Send(cfg, SendInput{From: "sender@example.invalid", To: []string{"recipient@example.invalid"}, Body: "test"})
		var reply *textproto.Error
		if !errors.As(err, &reply) || reply.Code != code || errors.Is(err, ErrSMTPDataUncertain) {
			t.Fatalf("SMTP %d rejection: %v", code, err)
		}
	}
}

func TestSendHonorsTimeoutBeforeServerGreeting(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Bound the old behavior too, so a regression does not hang the suite.
		conn.SetReadDeadline(time.Now().Add(700 * time.Millisecond))
		var b [1]byte
		conn.Read(b[:])
	}()
	cfg := SMTPConfig{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, Timeout: 40 * time.Millisecond}
	start := time.Now()
	err = Send(cfg, SendInput{From: "sender@example.invalid", To: []string{"recipient@example.invalid"}})
	elapsed := time.Since(start)
	ln.Close()
	<-done
	if err == nil {
		t.Fatal("server never greeted, but Send succeeded")
	}
	if elapsed > 400*time.Millisecond {
		t.Fatalf("configured 40ms timeout ignored: elapsed=%v err=%v", elapsed, err)
	}
}

func TestSendRejectsInjectedMessageHeaders(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input SendInput
	}{
		{"subject", SendInput{Subject: "innocent\r\nBcc: hidden@example.invalid"}},
		{"extra name", SendInput{ExtraHeaders: map[string]string{"X-Test\r\nBcc": "hidden@example.invalid"}}},
		{"extra value", SendInput{ExtraHeaders: map[string]string{"X-Test": "ok\n\nreplacement body"}}},
		{"colon in name", SendInput{ExtraHeaders: map[string]string{"X-Test: Bcc": "hidden@example.invalid"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, _ := smtpTestServer(t, "221 bye", "250 accepted")
			in := tc.input
			in.From, in.To = "sender@example.invalid", []string{"recipient@example.invalid"}
			if err := Send(cfg, in); err == nil {
				t.Fatal("SMTP accepted message with injected headers")
			}
		})
	}
}

func TestSendCodeOnlyFinalDataReplies(t *testing.T) {
	for _, tc := range []struct {
		reply string
		code  int
	}{
		{"250", 250}, {"250 ", 250}, {"451", 451}, {"550", 550},
		{"250-first line\r\n250", 250}, {"451-first line\r\n451", 451},
	} {
		t.Run(tc.reply, func(t *testing.T) {
			cfg, received := smtpTestServer(t, "221 bye", tc.reply)
			err := Send(cfg, SendInput{From: "sender@example.invalid", To: []string{"recipient@example.invalid"}, Body: "first\n.leading\nlast"})
			if tc.code == 250 {
				if err != nil {
					t.Fatalf("code-only acceptance was not confirmed: %v", err)
				}
			} else {
				var reply *textproto.Error
				if !errors.As(err, &reply) || reply.Code != tc.code || errors.Is(err, ErrSMTPDataUncertain) {
					t.Fatalf("code-only rejection became uncertain: %v", err)
				}
			}
			if message := <-received; !strings.Contains(message.raw, "\n.leading\n") {
				t.Fatalf("changed SMTP dot encoding: %q", message.raw)
			}
		})
	}
}

func TestSendMalformedFinalDataRepliesRemainUncertain(t *testing.T) {
	for _, reply := range []string{"250x", "451x", "25", "999", "250-first\r\n451 rejected", "250-first\r\n250x"} {
		t.Run(reply, func(t *testing.T) {
			cfg, _ := smtpTestServer(t, "221 bye", reply)
			err := Send(cfg, SendInput{From: "sender@example.invalid", To: []string{"recipient@example.invalid"}, Body: "fixture"})
			if !errors.Is(err, ErrSMTPDataUncertain) {
				t.Fatalf("malformed final response became definite: %v", err)
			}
		})
	}
}

func TestSendCodeOnlyDataChallenge(t *testing.T) {
	for _, challenge := range []string{"354", "354 ", "354-ready\r\n354"} {
		t.Run(challenge, func(t *testing.T) {
			cfg, received := smtpTestServerWithDataChallenge(t, "221 bye", challenge, "250")
			if err := Send(cfg, SendInput{From: "sender@example.invalid", To: []string{"recipient@example.invalid"}, Body: "fixture"}); err != nil {
				t.Fatalf("code-only DATA challenge rejected: %v", err)
			}
			if message := <-received; !strings.Contains(message.raw, "fixture") {
				t.Fatalf("missing message body: %q", message.raw)
			}
		})
	}
}

func TestSendDataChallengeFailureIsNotDeliveryUncertainty(t *testing.T) {
	for _, challenge := range []string{"451", "354x"} {
		t.Run(challenge, func(t *testing.T) {
			cfg, received := smtpTestServerWithDataChallenge(t, "221 bye", challenge, "250")
			err := Send(cfg, SendInput{From: "sender@example.invalid", To: []string{"recipient@example.invalid"}, Body: "fixture"})
			if err == nil || errors.Is(err, ErrSMTPDataUncertain) {
				t.Fatalf("pre-body error classified as delivery uncertainty: %v", err)
			}
			if challenge == "451" {
				var reply *textproto.Error
				if !errors.As(err, &reply) || reply.Code != 451 {
					t.Fatalf("lost explicit rejection code: %v", err)
				}
			}
			select {
			case message := <-received:
				t.Fatalf("sent body after rejected challenge: %q", message.raw)
			default:
			}
		})
	}
}

func TestSendRequiresCompleteFinalSMTPReplyLine(t *testing.T) {
	for _, reply := range []string{"250", "451", "550", "250\r", "250\n", "451\n", "250-first\r\n250"} {
		t.Run(fmt.Sprintf("%q", reply), func(t *testing.T) {
			cfg, received := smtpTestServerWithRawDataReplies(t, "221 bye", "354\r\n", reply)
			err := Send(cfg, SendInput{From: "sender@example.invalid", To: []string{"recipient@example.invalid"}, Body: "fixture"})
			if !errors.Is(err, ErrSMTPDataUncertain) {
				t.Fatalf("unterminated final reply became definitive: %v", err)
			}
			if message := <-received; !strings.Contains(message.raw, "fixture") {
				t.Fatalf("missing received body: %q", message.raw)
			}
		})
	}
}

func TestSendRequiresCompleteSMTPChallengeLine(t *testing.T) {
	for _, challenge := range []string{"354", "354\r", "354\n", "354-first\r\n354"} {
		t.Run(fmt.Sprintf("%q", challenge), func(t *testing.T) {
			cfg, received := smtpTestServerWithRawDataReplies(t, "221 bye", challenge, "250\r\n")
			err := Send(cfg, SendInput{From: "sender@example.invalid", To: []string{"recipient@example.invalid"}, Body: "fixture"})
			if err == nil || errors.Is(err, ErrSMTPDataUncertain) {
				t.Fatalf("unterminated challenge was accepted: %v", err)
			}
			select {
			case message := <-received:
				t.Fatalf("body sent after incomplete challenge: %q", message.raw)
			default:
			}
		})
	}
}
