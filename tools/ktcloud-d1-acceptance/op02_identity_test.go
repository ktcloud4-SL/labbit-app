package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/protocol"
)

func TestOP02SSHIdentityHandlesPTYFramingAndANSIWithoutEcho(t *testing.T) {
	tuple := "198.51.100.1 54321 10.0.0.2 22"
	for name, output := range map[string]string{
		"plain":           "OP02-CONNECTION:" + tuple + "\r\n",
		"bracketed-paste": "\x1b[?2004l\rOP02-CONNECTION:" + tuple + "\r\n",
		"colour":          "\x1b[0mOP02-CONNECTION:" + tuple + "\n",
		"prompt":          "ubuntu@workspace:~$ OP02-CONNECTION:" + tuple + "\n",
		"ipv6":            "OP02-CONNECTION:2001:db8::1 12345 2001:db8::2 22\n",
	} {
		t.Run(name, func(t *testing.T) {
			expected := tuple
			if name == "ipv6" {
				expected = "2001:db8::1 12345 2001:db8::2 22"
			}
			// Every possible split, including within the final port, must wait
			// for the newline rather than accepting a partial four-field tuple.
			for split := 0; split < len(output)-1; split++ {
				if _, ok := parseConnectionIdentity([]byte(output[:split])); ok {
					t.Fatalf("accepted unfinished output at split %d", split)
				}
				if got, ok := parseConnectionIdentity([]byte(output[:split] + output[split:])); !ok || got != expected {
					t.Fatalf("complete tuple rejected at split %d", split)
				}
			}
		})
	}
	for name, output := range map[string]string{
		"echo-only":    "printf 'OP02-CON%s:%s\\n' 'NECTION' \"$SSH_CONNECTION\"\r\n",
		"partial-port": "OP02-CONNECTION:198.51.100.1 54321 10.0.0.2 2",
		"invalid-IP":   "OP02-CONNECTION:not-an-ip 12345 10.0.0.2 22\n",
		"extra-field":  "OP02-CONNECTION:" + tuple + " extra\n",
		"zero-port":    "OP02-CONNECTION:198.51.100.1 0 10.0.0.2 22\n",
		"large-port":   "OP02-CONNECTION:198.51.100.1 65536 10.0.0.2 22\n",
		"signed-port":  "OP02-CONNECTION:198.51.100.1 12345 10.0.0.2 +22\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := parseConnectionIdentity([]byte(output)); ok {
				t.Fatal("invalid or echo-only tuple accepted")
			}
		})
	}
}

func TestOP02SSHIdentityConsumesActualTLSDataFrames(t *testing.T) {
	for _, prefix := range []string{"", "\x1b[?2004l\r", "\x1b[0mubuntu@workspace:~$ "} {
		t.Run(prefix, func(t *testing.T) {
			commandValid := make(chan bool, 1)
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				c, err := (&websocket.Upgrader{}).Upgrade(w, req, nil)
				if err != nil {
					return
				}
				defer c.Close()
				kind, cmd, err := c.ReadMessage()
				commandValid <- err == nil && kind == websocket.BinaryMessage && strings.Contains(string(cmd), "OP02-CON%s:%s") && !strings.Contains(string(cmd), "OP02-CONNECTION:")
				for _, frame := range []string{string(cmd), prefix + "OP02-CONNECTION:198.51.100.1 54321 10.0.0.2 2", "2\r\n"} {
					if c.WriteMessage(websocket.BinaryMessage, []byte(frame)) != nil {
						return
					}
				}
			}))
			defer server.Close()
			roots := x509.NewCertPool()
			roots.AddCert(server.Certificate())
			dialer := websocket.Dialer{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
			c, _, err := dialer.Dial("wss"+strings.TrimPrefix(server.URL, "https"), nil)
			if err != nil {
				t.Fatal("TLS fixture dial failed")
			}
			defer c.Close()
			plan := newDataPlan(protocol.BaseEnvelope{}, "fixture-server")
			plan.conn = c
			done := make(chan struct{})
			go func() {
				defer close(done)
				for {
					kind, raw, err := c.ReadMessage()
					plan.events <- dataEvent{kind, raw, err}
					if err != nil {
						return
					}
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			identity, err := connectionIdentity(ctx, plan, false)
			if err != nil || identity != "198.51.100.1 54321 10.0.0.2 22" {
				t.Fatal("actual fragmented TLS output not parsed correctly")
			}
			if !<-commandValid {
				t.Fatal("echo can satisfy probe or invalid input frame")
			}
			c.Close()
			select {
			case <-done:
			case <-ctx.Done():
				t.Fatal("fixture reader did not drain")
			}
		})
	}
}
