package openstackprovider

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"path/filepath"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestTrustOnFirstUsePinsAndRejectsChangedHostKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hosts")
	first := testHostPublicKey(t)
	callback, err := trustOnFirstUseCallback(path)
	if err != nil {
		t.Fatalf("trustOnFirstUseCallback() error = %v", err)
	}
	remote := &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 22}
	if err := callback("192.0.2.10:22", remote, first); err != nil {
		t.Fatalf("first host key was not accepted: %v", err)
	}

	pinned, err := trustOnFirstUseCallback(path)
	if err != nil {
		t.Fatalf("reload known_hosts: %v", err)
	}
	if err := pinned("192.0.2.10:22", remote, first); err != nil {
		t.Fatalf("pinned host key was not accepted: %v", err)
	}
	if err := pinned("192.0.2.10:22", remote, testHostPublicKey(t)); err == nil {
		t.Fatal("changed host key was accepted")
	}
}

func TestTrustOnFirstUseOnlyOneConcurrentFirstKeyWins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hosts")
	firstCallback, err := trustOnFirstUseCallback(path)
	if err != nil {
		t.Fatal(err)
	}
	secondCallback, err := trustOnFirstUseCallback(path)
	if err != nil {
		t.Fatal(err)
	}
	remote := &net.TCPAddr{IP: net.ParseIP("192.0.2.20"), Port: 22}
	keys := []ssh.PublicKey{testHostPublicKey(t), testHostPublicKey(t)}
	callbacks := []ssh.HostKeyCallback{firstCallback, secondCallback}
	start := make(chan struct{})
	results := make(chan error, len(callbacks))
	var ready sync.WaitGroup
	ready.Add(len(callbacks))
	for index := range callbacks {
		go func(index int) {
			ready.Done()
			<-start
			results <- callbacks[index]("192.0.2.20:22", remote, keys[index])
		}(index)
	}
	ready.Wait()
	close(start)

	succeeded := 0
	for range callbacks {
		if err := <-results; err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("concurrent first keys accepted = %d, want exactly 1", succeeded)
	}
}

func testHostPublicKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	return key
}
