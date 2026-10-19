package auth

import (
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// AdminRenewedAtKey records (unix seconds) when the admin session was last
// re-saved by RenewAdminSession. A sign-in sets it too.
const AdminRenewedAtKey = "admin-renewed-at"

// AdminRenewInterval is how often an admin session in use is re-saved.
const AdminRenewInterval = 24 * time.Hour

// RenewAdminSession slides a signed-in administrator's session forward: once
// a day, a request that carries it re-saves the session, which resets both
// the cookie's Max-Age and the store's TTL to the full SESSION_MAX_AGE. A
// device that is used at least once per lifetime then never has to sign in
// again, while one that is abandoned still expires.
//
// Re-saving at most daily rather than on every request keeps a page full of
// XHRs from writing the session once per call. It has to run after the
// session middleware (handlers/session) and is harmless without a password:
// AdminSessionKey is only ever set by a sign-in.
func RenewAdminSession() gin.HandlerFunc {
	return renewAdminSession(time.Now)
}

func renewAdminSession(now func() time.Time) gin.HandlerFunc {
	return func(c *gin.Context) {
		s := sessions.Default(c)
		if active, ok := s.Get(AdminSessionKey).(bool); !ok || !active {
			c.Next()
			return
		}
		t := now()
		last, _ := s.Get(AdminRenewedAtKey).(int64)
		if t.Sub(time.Unix(last, 0)) >= AdminRenewInterval {
			s.Set(AdminRenewedAtKey, t.Unix())
			if err := s.Save(); err != nil {
				// Not worth failing the page over: the session is still
				// valid, it just did not get its extension this time.
				log.WithError(err).Warn("failed to renew admin session")
			}
		}
		c.Next()
	}
}
