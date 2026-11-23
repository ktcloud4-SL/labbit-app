package drcontrol

import (
	"context"
	"time"
)

// PreparedArtifacts holds exact immutable bytes for one authenticated invocation.
// It caches no identity, signature decision, TTL decision, proof or journal result.
// Download is outside the Store's 2s callback budget, bounded to 8s in total.
// Revoked access/deletion after download is not re-queried by this invocation.
type PreparedArtifacts struct{ values map[Reference][]byte }

func PrepareArtifacts(ctx context.Context, p Policy, reader ArtifactReader, refs ...Reference) (*PreparedArtifacts, error) {
	if p.Validate() != nil || reader == nil || len(refs) < 1 || len(refs) > 2 || ctx.Err() != nil {
		return nil, ErrEvidence
	}
	for _, ref := range refs {
		if p.CheckReference(ref) != nil {
			return nil, ErrEvidence
		}
	}
	c, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	prepared := &PreparedArtifacts{values: make(map[Reference][]byte)}
	ok := false
	defer func() {
		if !ok {
			prepared.Clear()
		}
	}()
	for _, ref := range refs {
		if _, exists := prepared.values[ref]; exists {
			continue
		}
		raw, err := reader.Read(c, ref)
		if err != nil || c.Err() != nil || int64(len(raw)) != ref.Size {
			return nil, ErrEvidence
		}
		copy := append([]byte(nil), raw...)
		if Hash(copy) != ref.SHA256 {
			clear(copy)
			return nil, ErrEvidence
		}
		prepared.values[ref] = copy
	}
	if c.Err() != nil {
		return nil, ErrEvidence
	}
	ok = true
	return prepared, nil
}

func (p *PreparedArtifacts) Read(ctx context.Context, ref Reference) ([]byte, error) {
	if p == nil || ctx.Err() != nil {
		return nil, ErrEvidence
	}
	raw, ok := p.values[ref]
	if !ok {
		return nil, ErrEvidence
	}
	return append([]byte(nil), raw...), nil
}

// Clear is owner-only, after the synchronous invocation and all callbacks return.
func (p *PreparedArtifacts) Clear() {
	if p == nil {
		return
	}
	for ref, raw := range p.values {
		clear(raw)
		delete(p.values, ref)
	}
}
