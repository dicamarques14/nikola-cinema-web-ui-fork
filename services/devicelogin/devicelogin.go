// Package devicelogin lets a browser that cannot comfortably type a password
// (a car's touchscreen, a TV) be signed in from a device that already is.
//
// The waiting browser asks for a request: an unguessable token, which it
// keeps in its own session and shows as a QR code, and a short code that a
// person can read off the screen. A signed-in device opens the QR link (or
// types the code), checks the code matches the screen, and approves. The
// waiting browser then claims the approval and gets the admin session.
//
// The token never travels in the claim: the claimant presents it from its
// own session, so a leaked QR link lets someone approve, never sign in.
// Everything lives in redis with a short TTL and a claim deletes it, so a
// request is single-use.
package devicelogin

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/redis/go-redis/v9"
)

const (
	// TTL is how long a request waits for approval.
	TTL = 5 * time.Minute

	tokenPrefix = "devlogin:t:"
	codePrefix  = "devlogin:c:"
	// codeAlphabet leaves out characters that read alike on a screen
	// (0/O, 1/I/L).
	codeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	codeLen      = 6
)

type Status string

const (
	StatusPending  Status = "pending"
	StatusApproved Status = "approved"
	StatusExpired  Status = "expired"
)

var (
	ErrNotFound     = errors.New("device login request not found or expired")
	ErrCodeMismatch = errors.New("device login code does not match")
	ErrNotApproved  = errors.New("device login request is not approved")
)

// Request is what redis keeps for one waiting browser.
type Request struct {
	Token     string `json:"-"`
	Code      string `json:"code"`
	Status    Status `json:"status"`
	UserAgent string `json:"ua"`
	CreatedAt int64  `json:"created_at"`
}

// FormattedCode renders the code as "ABC-DEF" for display.
func (r *Request) FormattedCode() string {
	return FormatCode(r.Code)
}

// FormatCode renders a code as "ABC-DEF".
func FormatCode(code string) string {
	if len(code) != codeLen {
		return code
	}
	return code[:3] + "-" + code[3:]
}

// NormalizeCode turns what a person typed into the stored form.
func NormalizeCode(s string) string {
	s = strings.ToUpper(s)
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(codeAlphabet, r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

type Store struct {
	cl  redis.UniversalClient
	ttl time.Duration
	now func() time.Time
}

func New(cl redis.UniversalClient) *Store {
	return &Store{cl: cl, ttl: TTL, now: time.Now}
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func randomCode() (string, error) {
	var b strings.Builder
	max := big.NewInt(int64(len(codeAlphabet)))
	for i := 0; i < codeLen; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b.WriteByte(codeAlphabet[n.Int64()])
	}
	return b.String(), nil
}

// Create starts a new request. ua is only shown on the approve page, so the
// approver can tell which browser is asking.
func (s *Store) Create(ctx context.Context, ua string) (*Request, error) {
	if len(ua) > 200 {
		ua = ua[:200]
	}
	token, err := randomToken()
	if err != nil {
		return nil, errors.Wrap(err, "failed to generate token")
	}
	req := &Request{Token: token, Status: StatusPending, UserAgent: ua, CreatedAt: s.now().Unix()}
	// A code collision with another live request is rare (31^6 values, a
	// handful live at once) but would let one approval land on the wrong
	// browser, so the code key is claimed with SETNX and retried.
	for i := 0; i < 5; i++ {
		code, err := randomCode()
		if err != nil {
			return nil, errors.Wrap(err, "failed to generate code")
		}
		ok, err := s.cl.SetNX(ctx, codePrefix+code, token, s.ttl).Result()
		if err != nil {
			return nil, errors.Wrap(err, "failed to store device login code")
		}
		if ok {
			req.Code = code
			break
		}
	}
	if req.Code == "" {
		return nil, errors.New("failed to allocate a unique device login code")
	}
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if err := s.cl.Set(ctx, tokenPrefix+token, b, s.ttl).Err(); err != nil {
		return nil, errors.Wrap(err, "failed to store device login request")
	}
	return req, nil
}

// Get returns the request for a token, or ErrNotFound once it has expired or
// been claimed.
func (s *Store) Get(ctx context.Context, token string) (*Request, error) {
	if token == "" {
		return nil, ErrNotFound
	}
	b, err := s.cl.Get(ctx, tokenPrefix+token).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, errors.Wrap(err, "failed to read device login request")
	}
	var req Request
	if err := json.Unmarshal(b, &req); err != nil {
		return nil, errors.Wrap(err, "failed to decode device login request")
	}
	req.Token = token
	return &req, nil
}

// TokenByCode resolves a typed code to its request's token.
func (s *Store) TokenByCode(ctx context.Context, code string) (string, error) {
	code = NormalizeCode(code)
	if len(code) != codeLen {
		return "", ErrNotFound
	}
	t, err := s.cl.Get(ctx, codePrefix+code).Result()
	if errors.Is(err, redis.Nil) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", errors.Wrap(err, "failed to read device login code")
	}
	return t, nil
}

// Status reports where a token's request stands; a missing one is expired.
func (s *Store) Status(ctx context.Context, token string) (Status, error) {
	req, err := s.Get(ctx, token)
	if errors.Is(err, ErrNotFound) {
		return StatusExpired, nil
	}
	if err != nil {
		return "", err
	}
	return req.Status, nil
}

// Approve marks the request approved. code must be the one shown on the
// waiting screen: it is what the approver confirmed by eye. The request keeps
// its remaining TTL.
func (s *Store) Approve(ctx context.Context, token, code string) error {
	req, err := s.Get(ctx, token)
	if err != nil {
		return err
	}
	if NormalizeCode(code) != req.Code {
		return ErrCodeMismatch
	}
	req.Status = StatusApproved
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	ok, err := s.cl.SetArgs(ctx, tokenPrefix+token, b, redis.SetArgs{KeepTTL: true, Mode: "XX"}).Result()
	if errors.Is(err, redis.Nil) || (err == nil && ok != "OK") {
		return ErrNotFound
	}
	if err != nil {
		return errors.Wrap(err, "failed to approve device login request")
	}
	return nil
}

// Claim consumes an approved request. Only one caller can win: the request
// is deleted, and whoever's DEL removed it is the one who signs in.
func (s *Store) Claim(ctx context.Context, token string) error {
	req, err := s.Get(ctx, token)
	if err != nil {
		return err
	}
	if req.Status != StatusApproved {
		return ErrNotApproved
	}
	n, err := s.cl.Del(ctx, tokenPrefix+token).Result()
	if err != nil {
		return errors.Wrap(err, "failed to claim device login request")
	}
	if n == 0 {
		return ErrNotFound
	}
	_ = s.cl.Del(ctx, codePrefix+req.Code).Err()
	return nil
}
