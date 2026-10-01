package control

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"sync"
	"time"
)

// secretBox encrypts secrets stored in the database (provider keys, alert
// tokens) with AES-256-GCM. The key is derived from LANEPOOL_SECRET.
type secretBox struct{ aead cipher.AEAD }

func newSecretBox(secret string) *secretBox {
	key := sha256.Sum256([]byte("lanepool:" + secret))
	block, _ := aes.NewCipher(key[:])
	aead, _ := cipher.NewGCM(block)
	return &secretBox{aead: aead}
}

func (b *secretBox) seal(plain []byte) []byte {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(err)
	}
	return b.aead.Seal(nonce, nonce, plain, nil)
}

func (b *secretBox) open(sealed []byte) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(sealed) < n {
		return nil, errors.New("secret too short")
	}
	plain, err := b.aead.Open(nil, sealed[:n], sealed[n:], nil)
	if err != nil {
		return nil, errors.New("cannot decrypt secret (was LANEPOOL_SECRET changed?)")
	}
	return plain, nil
}

// rateLimiter counts failures per key within a window (login brute-force protection).
type rateLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string][]time.Time
}

func newRateLimiter(max int, window time.Duration) *rateLimiter {
	return &rateLimiter{max: max, window: window, hits: map[string][]time.Time{}}
}

func (r *rateLimiter) blocked(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prune(key)
	return len(r.hits[key]) >= r.max
}

func (r *rateLimiter) fail(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prune(key)
	r.hits[key] = append(r.hits[key], time.Now())
}

func (r *rateLimiter) reset(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.hits, key)
}

func (r *rateLimiter) prune(key string) {
	cutoff := time.Now().Add(-r.window)
	h := r.hits[key]
	i := 0
	for i < len(h) && h[i].Before(cutoff) {
		i++
	}
	if i == len(h) {
		delete(r.hits, key)
		return
	}
	r.hits[key] = h[i:]
}
