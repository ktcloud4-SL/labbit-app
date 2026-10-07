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
	identity, err := sshHostKeyIdentity("provider-1", "server-1")
	if err != nil {
		t.Fatal(err)
	}
	callback, err := trustOnFirstUseCallback(path, identity)
	if err != nil {
		t.Fatalf("trustOnFirstUseCallback() error = %v", err)
	}
	remote := &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 22}
	if err := callback("192.0.2.10:22", remote, first); err != nil {
		t.Fatalf("first host key was not accepted: %v", err)
	}

	pinned, err := trustOnFirstUseCallback(path, identity)
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

func TestTrustOnFirstUseRepinsNewServerIdentityAtReusedIP(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hosts")
	remote := &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 22}
	firstKey := testHostPublicKey(t)
	secondKey := testHostPublicKey(t)
	firstIdentity, _ := sshHostKeyIdentity("provider-1", "server-generation-1")
	secondIdentity, _ := sshHostKeyIdentity("provider-1", "server-generation-2")

	first, err := trustOnFirstUseCallback(path, firstIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if err := first("192.0.2.10:22", remote, firstKey); err != nil {
		t.Fatalf("first identity pin failed: %v", err)
	}
	second, err := trustOnFirstUseCallback(path, secondIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if err := second("192.0.2.10:22", remote, secondKey); err != nil {
		t.Fatalf("new server identity at reused IP was rejected: %v", err)
	}
	if err := second("192.0.2.10:22", remote, firstKey); err == nil {
		t.Fatal("new server identity accepted a different key after pinning")
	}
}

func TestTrustOnFirstUseOnlyOneConcurrentFirstKeyWins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hosts")
	identity, err := sshHostKeyIdentity("provider-1", "server-1")
	if err != nil {
		t.Fatal(err)
	}
	firstCallback, err := trustOnFirstUseCallback(path, identity)
	if err != nil {
		t.Fatal(err)
	}
	secondCallback, err := trustOnFirstUseCallback(path, identity)
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

func TestSSHHostKeyIdentityIsStableAndBoundToServer(t *testing.T) {
	first, err := sshHostKeyIdentity(" provider-1 ", " server-1 ")
	if err != nil {
		t.Fatal(err)
	}
	again, _ := sshHostKeyIdentity("provider-1", "server-1")
	otherServer, _ := sshHostKeyIdentity("provider-1", "server-2")
	otherProvider, _ := sshHostKeyIdentity("provider-2", "server-1")
	if first != again || first == otherServer || first == otherProvider {
		t.Fatalf("identities first=%q again=%q otherServer=%q otherProvider=%q", first, again, otherServer, otherProvider)
	}
	if _, err := sshHostKeyIdentity("provider-1", ""); err == nil {
		t.Fatal("empty server identity was accepted")
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
