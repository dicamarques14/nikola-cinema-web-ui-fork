package session

import (
	"net/http"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	gsessions "github.com/gorilla/sessions"
	"github.com/snowdreamtech/redistore"
)

// gorillaSession is implemented by gin-contrib's session wrapper; it exposes
// the underlying gorilla session whose ID the redis store keys on.
type gorillaSession interface {
	Session() *gsessions.Session
}

// discardWriter swallows the expiring cookie redistore's Delete writes: the
// rotated session's own Save sets the replacement cookie, and a second
// Set-Cookie for the same name in one response is needless ambiguity.
type discardWriter struct{ h http.Header }

func (d *discardWriter) Header() http.Header {
	if d.h == nil {
		d.h = http.Header{}
	}
	return d.h
}
func (d *discardWriter) Write(b []byte) (int, error) { return len(b), nil }
func (d *discardWriter) WriteHeader(int)             {}

// Rotate gives the current session a fresh ID while keeping its values, and
// deletes the old one from the store. Call it when a session gains
// privileges (a sign-in), so an ID that was known before -- planted by
// someone else, or read off a shared screen -- does not become an admin
// session. The caller still has to Save.
//
// The cookie store has no server-side ID: its cookie is the whole session,
// re-sealed on every Save, so there is nothing to rotate and this is a no-op.
func Rotate(c *gin.Context) error {
	s := sessions.Default(c)
	gs, ok := s.(gorillaSession)
	if !ok {
		return nil
	}
	g := gs.Session()
	if g == nil || g.ID == "" {
		return nil
	}
	rs, ok := g.Store().(*redistore.RediStore)
	if !ok {
		return nil
	}
	values := make(map[any]any, len(g.Values))
	for k, v := range g.Values {
		values[k] = v
	}
	if err := rs.Delete(c.Request, &discardWriter{}, g); err != nil {
		return err
	}
	g.ID = ""
	g.IsNew = true
	for k, v := range values {
		// Set, not a direct map write: it also marks the wrapper written, so
		// the caller's Save actually reaches the store.
		s.Set(k, v)
	}
	return nil
}
