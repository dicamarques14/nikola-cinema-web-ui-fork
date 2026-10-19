package devicelogin

import (
	"fmt"
	"strings"

	"github.com/pkg/errors"
	qrcode "github.com/skip2/go-qrcode"
)

// QRSVG renders content as a self-contained SVG QR code: dark modules on a
// white square with the standard quiet zone, which phone cameras need even
// on a dark page. One path keeps the markup small.
func QRSVG(content string) (string, error) {
	q, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		return "", errors.Wrap(err, "failed to encode QR code")
	}
	// Bitmap includes the quiet zone (4 modules on each side).
	bm := q.Bitmap()
	n := len(bm)
	var p strings.Builder
	for y, row := range bm {
		for x, dark := range row {
			if dark {
				fmt.Fprintf(&p, "M%d %dh1v1h-1z", x, y)
			}
		}
	}
	return fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges" role="img"><rect width="%d" height="%d" fill="#fff"/><path d="%s" fill="#000"/></svg>`,
		n, n, n, n, p.String(),
	), nil
}
