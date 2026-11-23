package drcontrol

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

type artifactFunc func(context.Context, Reference) ([]byte, error)

func (f artifactFunc) Read(c context.Context, r Reference) ([]byte, error) { return f(c, r) }

func TestPreparedArtifactExactReferencesCopiesAndClear(t *testing.T) {
	f := setup(t)
	r := f.i.Approval
	raw := append([]byte(nil), f.art.values[r.VersionID]...)
	calls := 0
	p, e := PrepareArtifacts(t.Context(), f.p, artifactFunc(func(c context.Context, r Reference) ([]byte, error) {
		calls++
		d, ok := c.Deadline()
		if !ok || time.Until(d) > 8*time.Second {
			t.Fatal("unbounded preparation")
		}
		return raw, nil
	}), r, r)
	if e != nil || calls != 1 {
		t.Fatal(e, calls)
	}
	raw[0] ^= 1
	b, e := p.Read(t.Context(), r)
	if e != nil || Hash(b) != r.SHA256 {
		t.Fatal("reader retained mutable input")
	}
	b[0] ^= 1
	b, e = p.Read(t.Context(), r)
	if e != nil || Hash(b) != r.SHA256 {
		t.Fatal("reader returned shared buffer")
	}
	for _, change := range []func(*Reference){
		func(v *Reference) { v.Bucket += "x" }, func(v *Reference) { v.Key += "x" },
		func(v *Reference) { v.VersionID += "x" }, func(v *Reference) { v.SHA256 = strings.Repeat("a", 64) },
		func(v *Reference) { v.Size++ },
	} {
		other := r
		change(&other)
		if _, err := p.Read(t.Context(), other); err == nil {
			t.Fatal("reference alias accepted")
		}
	}
	c, cancel := context.WithCancel(t.Context())
	cancel()
	if _, e := p.Read(c, r); e == nil {
		t.Fatal("cancel ignored")
	}
	p.Clear()
	p.Clear()
	if _, e := p.Read(t.Context(), r); e == nil {
		t.Fatal("cleared data retained")
	}
}

func TestPrepareArtifactsRejectsBeforeUse(t *testing.T) {
	for _, name := range []string{"bad-first", "bad-second", "corrupt", "size", "read-error", "cancel-after-read", "partial-second"} {
		t.Run(name, func(t *testing.T) {
			f := setup(t)
			c, cancel := context.WithCancel(t.Context())
			defer cancel()
			refs := []Reference{f.i.Approval, f.i.Evidence}
			if name == "bad-first" {
				refs[0].Bucket = "foreign"
			}
			if name == "bad-second" {
				refs[1].Bucket = "foreign"
			}
			calls := 0
			p, e := PrepareArtifacts(c, f.p, artifactFunc(func(c context.Context, r Reference) ([]byte, error) {
				calls++
				raw := append([]byte(nil), f.art.values[r.VersionID]...)
				switch name {
				case "corrupt":
					raw[0] ^= 1
				case "size":
					raw = append(raw, 'x')
				case "read-error":
					return nil, errors.New("not logged")
				case "cancel-after-read":
					cancel()
				case "partial-second":
					if calls == 2 {
						return nil, ErrEvidence
					}
				}
				return raw, nil
			}), refs...)
			if e == nil || p != nil {
				t.Fatal("bad preparation published")
			}
			if strings.HasPrefix(name, "bad-") && calls != 0 {
				t.Fatal("invalid reference made IO")
			}
		})
	}
}

func TestPreparedCallbacksKeepTwoSecondBudgetAndFreshTime(t *testing.T) {
	for _, expired := range []bool{false, true} {
		f := setup(t)
		reader := artifactFunc(func(c context.Context, r Reference) ([]byte, error) {
			if expired {
				f.now = f.now.Add(time.Minute)
			}
			return f.art.Read(c, r)
		})
		_, e := Execute(t.Context(), f.p, f.i, f.id, reader, f.s, func() time.Time { return f.now })
		if expired {
			if e == nil || f.s.begins != 0 {
				t.Fatal("preload froze TTL decision")
			}
		} else if e != nil || f.art.calls != 2 || f.s.applies != 1 {
			t.Fatal(e)
		}
	}
	// Model slow download outside callback. A callback with <2s remaining only
	// reads immutable bytes; it cannot make another network request.
	f := setup(t)
	calls := 0
	p, e := PrepareArtifacts(t.Context(), f.p, artifactFunc(func(c context.Context, r Reference) ([]byte, error) {
		calls++
		if calls == 1 {
			select {
			case <-time.After(2100 * time.Millisecond):
			case <-c.Done():
				return nil, c.Err()
			}
		}
		return f.art.Read(c, r)
	}), f.i.Approval, f.i.Evidence)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Clear()
	v, e := NewVerifier(f.p, f.i, p, func() time.Time { return f.now })
	if e != nil {
		t.Fatal(e)
	}
	c, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if _, e = v.AuthorizeResume(c, f.i.Request); e != nil {
		t.Fatal(e)
	}
	if _, e = v.RecheckResume(c, f.i.Request); e != nil || c.Err() != nil || calls != 2 {
		t.Fatal("callback repeated IO", e)
	}
	f.now = f.now.Add(time.Minute)
	if _, e = v.AuthorizeResume(c, f.i.Request); e == nil {
		t.Fatal("cached approval authorization")
	}
}

func TestPreparationConcurrentReceiptDoesNotApplyAgain(t *testing.T) {
	f := setup(t)
	if _, e := f.execute(); e != nil {
		t.Fatal(e)
	}
	receipt := f.s.record
	f.s = &storeFake{now: f.now}
	reader := artifactFunc(func(c context.Context, r Reference) ([]byte, error) {
		f.s.found, f.s.record = true, receipt
		return f.art.Read(c, r)
	})
	if _, e := Execute(t.Context(), f.p, f.i, f.id, reader, f.s, func() time.Time { return f.now }); e != nil || f.s.begins+f.s.applies != 0 {
		t.Fatal("racing receipt reapplied", e)
	}
}

func TestRestrictionPreparationExpiryAndHistoricalZeroIO(t *testing.T) {
	f, i, s := restrictionFixture(t)
	reader := artifactFunc(func(context.Context, Reference) ([]byte, error) { t.Fatal("historical IO"); return nil, ErrEvidence })
	if _, e := ExecuteRestriction(t.Context(), f.p, i, f.id, f.art, s, func() time.Time { return f.now }); e != nil {
		t.Fatal(e)
	}
	f.now = f.now.Add(time.Hour)
	if _, e := ExecuteRestriction(t.Context(), f.p, i, f.id, reader, s, func() time.Time { return f.now }); e != nil {
		t.Fatal(e)
	}
	f, i, s = restrictionFixture(t)
	reader = artifactFunc(func(c context.Context, r Reference) ([]byte, error) {
		f.now = f.now.Add(time.Hour)
		return f.art.Read(c, r)
	})
	if _, e := ExecuteRestriction(t.Context(), f.p, i, f.id, reader, s, func() time.Time { return f.now }); e == nil || s.begins+s.applies != 0 {
		t.Fatal("expired restriction applied")
	}
}

func TestDatabaseTunnelPreservesTLSIdentityAndExactRoute(t *testing.T) {
	f := setup(t)
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	f.p.DatabaseTunnelPort = uint16(l.Addr().(*net.TCPAddr).Port)
	dsn := "postgres://fixture:dummy@fixture.example.test:5432/labbit?sslmode=verify-full"
	c, e := DatabaseConfig(f.p, dsn)
	if e != nil {
		t.Fatal(e)
	}
	v := c.ConnConfig
	if v.Host != f.p.Database.Host || v.Port != 5432 || v.TLSConfig.ServerName != v.Host || v.TLSConfig.InsecureSkipVerify || len(v.Fallbacks) != 0 || v.ConnectTimeout != 2*time.Second {
		t.Fatal("TLS or target weakened")
	}
	addresses, e := v.LookupFunc(t.Context(), v.Host)
	if e != nil || len(addresses) != 1 || addresses[0] != v.Host {
		t.Fatal("DNS target changed")
	}
	if _, e = v.LookupFunc(t.Context(), "other"); e == nil {
		t.Fatal("foreign DNS accepted")
	}
	if _, e = v.DialFunc(t.Context(), "tcp4", net.JoinHostPort(v.Host, "5432")); e == nil {
		t.Fatal("network alias accepted")
	}
	if _, e = v.DialFunc(t.Context(), "tcp", "127.0.0.1:"+strconv.Itoa(int(f.p.DatabaseTunnelPort))); e == nil {
		t.Fatal("localhost target accepted")
	}
	dial, e := v.DialFunc(t.Context(), "tcp", net.JoinHostPort(v.Host, "5432"))
	if e != nil {
		t.Fatal(e)
	}
	dial.Close()
	accepted, e := l.Accept()
	if e != nil {
		t.Fatal(e)
	}
	accepted.Close()
	cc, cancel := context.WithCancel(t.Context())
	cancel()
	if _, e = v.DialFunc(cc, "tcp", net.JoinHostPort(v.Host, "5432")); e == nil {
		t.Fatal("cancel ignored")
	}
	f.p.DatabaseTunnelPort = 1
	if _, e = DatabaseConfig(f.p, dsn); e == nil {
		t.Fatal("privileged tunnel allowed")
	}
	f.p.DatabaseTunnelPort = 35432
	if _, e = DatabaseConfig(f.p, strings.Replace(dsn, "verify-full", "require", 1)); e == nil {
		t.Fatal("tunnel bypassed TLS")
	}
}
