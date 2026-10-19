package devicelogin

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestStore(t *testing.T) (*Store, *miniredis.Miniredis) {
	t.Helper()
	m := miniredis.RunT(t)
	cl := redis.NewClient(&redis.Options{Addr: m.Addr()})
	t.Cleanup(func() { _ = cl.Close() })
	return New(cl), m
}

func TestCreateApproveClaim(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	req, err := s.Create(ctx, "Tesla/2026")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(req.Token) < 40 || len(req.Code) != codeLen {
		t.Fatalf("unexpected token/code: %q %q", req.Token, req.Code)
	}
	if st, _ := s.Status(ctx, req.Token); st != StatusPending {
		t.Fatalf("status: got %v, want pending", st)
	}
	if err := s.Claim(ctx, req.Token); !errors.Is(err, ErrNotApproved) {
		t.Fatalf("claim before approval: got %v, want ErrNotApproved", err)
	}
	if err := s.Approve(ctx, req.Token, "ZZZZZZ"); !errors.Is(err, ErrCodeMismatch) {
		t.Fatalf("approve with wrong code: got %v", err)
	}
	if err := s.Approve(ctx, req.Token, FormatCode(req.Code)); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if st, _ := s.Status(ctx, req.Token); st != StatusApproved {
		t.Fatalf("status: got %v, want approved", st)
	}
	if err := s.Claim(ctx, req.Token); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := s.Claim(ctx, req.Token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second claim: got %v, want ErrNotFound (single use)", err)
	}
	if _, err := s.TokenByCode(ctx, req.Code); !errors.Is(err, ErrNotFound) {
		t.Fatalf("code must be gone after a claim, got %v", err)
	}
}

func TestTokenByCodeAcceptsTypedForms(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	req, err := s.Create(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	typed := " " + string(req.Code[:3]) + "-" + string(req.Code[3:]) + " "
	tok, err := s.TokenByCode(ctx, typed)
	if err != nil || tok != req.Token {
		t.Fatalf("TokenByCode(%q) = %q, %v", typed, tok, err)
	}
}

func TestRequestsExpire(t *testing.T) {
	s, m := newTestStore(t)
	ctx := context.Background()
	req, err := s.Create(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Approve(ctx, req.Token, req.Code); err != nil {
		t.Fatal(err)
	}
	// Approving keeps the original deadline rather than restarting it.
	if ttl := m.TTL(tokenPrefix + req.Token); ttl <= 0 || ttl > TTL {
		t.Fatalf("approved request TTL = %v, want within (0, %v]", ttl, TTL)
	}
	m.FastForward(TTL + time.Second)
	if st, _ := s.Status(ctx, req.Token); st != StatusExpired {
		t.Fatalf("status after TTL: got %v, want expired", st)
	}
	if err := s.Claim(ctx, req.Token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("claim after TTL: got %v", err)
	}
	if err := s.Approve(ctx, req.Token, req.Code); !errors.Is(err, ErrNotFound) {
		t.Fatalf("approve after TTL: got %v", err)
	}
}

func TestUnknownToken(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.Get(context.Background(), ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty token: %v", err)
	}
	if _, err := s.Get(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown token: %v", err)
	}
}

func TestQRSVG(t *testing.T) {
	svg, err := QRSVG("https://example.com/login/approve?t=abc")
	if err != nil {
		t.Fatal(err)
	}
	if len(svg) < 200 || svg[:4] != "<svg" {
		t.Fatalf("unexpected SVG: %.80q", svg)
	}
}
