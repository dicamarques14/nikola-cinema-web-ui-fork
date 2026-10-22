package torznab_indexer

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	jackett "github.com/webtor-io/go-jackett"
	"github.com/webtor-io/web-ui/services/torznab"
	"github.com/webtor-io/web-ui/services/web"
)

// statusRe pulls the HTTP status out of go-jackett's "unexpected status N"
// error. Only the number travels to the user; the URL around it never does.
var statusRe = regexp.MustCompile(`unexpected status (\d{3})`)

// classifyAddError turns a failed add-time probe into the message the form
// shows, or nil when the error is not one we can explain better than the
// generic message.
//
// Nothing here quotes the error text itself: the request URL (and with it,
// for feeds that keep the key in the path, the credentials) is part of most
// of these messages. Only fixed i18n keys and a bare number reach the page.
func classifyAddError(err error) *web.UserError {
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())

	// The egress guard (services/egress) refused the dial. First, because it
	// is also "unreachable" below, and only this one is fixed by a setting.
	if strings.Contains(msg, "refusing to connect to private address") ||
		strings.Contains(msg, "refusing to reach private address") {
		return web.NewUserError("error.indexerPrivateAddress", err)
	}

	var ie *jackett.IndexerError
	if errors.As(err, &ie) {
		code, _ := strconv.Atoi(strings.TrimSpace(ie.Code))
		desc := strings.ToLower(ie.Description)
		// Torznab 100-102: incorrect credentials, account suspended,
		// insufficient privileges. Jackett answers a wrong key with
		// code 100 "Invalid API Key" and HTTP 200.
		if (code >= 100 && code <= 102) || strings.Contains(desc, "api key") || strings.Contains(desc, "apikey") {
			return web.NewUserError("error.indexerInvalidApiKey", err)
		}
		if code < 1 || code > 999 {
			code = 900 // Torznab "unknown error"
		}
		return web.NewUserErrorNum("error.indexerRejected", code, err)
	}

	if m := statusRe.FindStringSubmatch(msg); m != nil {
		status, _ := strconv.Atoi(m[1])
		// Prowlarr (and Jackett's dashboard routes) answer a wrong or
		// missing key with a plain 401.
		if status == 401 || status == 403 {
			return web.NewUserError("error.indexerInvalidApiKey", err)
		}
		return web.NewUserErrorNum("error.indexerHttpStatus", status, err)
	}

	// 200 with something that is not a caps document: a login page, the
	// indexer's web UI, a reverse proxy's landing page.
	if strings.Contains(msg, "unmarshal response data") || strings.Contains(msg, "xml syntax error") {
		return web.NewUserError("error.indexerNotTorznab", err)
	}

	if strings.Contains(msg, "advertises no usable search mode") {
		return web.NewUserError("error.indexerNoSearch", err)
	}

	if torznab.IsUnreachable(err) {
		return web.NewUserError("error.indexerUnreachable", err)
	}
	return nil
}
