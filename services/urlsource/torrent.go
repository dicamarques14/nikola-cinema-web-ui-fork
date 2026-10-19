package urlsource

import (
	"bytes"
	"io"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/pkg/errors"
)

// BuildTorrent hashes r (exactly length bytes) into a single-file torrent
// named name whose only source is the web seed seedURL (BEP 19). It is
// private (BEP 27): the infohash is not announced on the DHT or by PEX, so
// the file stays this instance's. Returns the .torrent bytes and the
// infohash in hex.
//
// seedURL should end in "/": the client then appends the escaped name
// (anacrolix webseed), which the /urlseed handler ignores.
func BuildTorrent(r io.Reader, name string, length int64, seedURL string, progress func(n int64)) ([]byte, string, error) {
	if length <= 0 {
		return nil, "", errors.New("empty file")
	}
	if name == "" {
		return nil, "", errors.New("no name")
	}
	pieceLength := metainfo.ChoosePieceLength(length)
	cr := &countingReader{r: r, progress: progress}
	pieces, err := metainfo.GeneratePieces(io.LimitReader(cr, length), pieceLength, nil)
	if err != nil {
		return nil, "", errors.Wrap(err, "failed to hash pieces")
	}
	if cr.n != length {
		return nil, "", errors.Errorf("hashed %d bytes, expected %d", cr.n, length)
	}
	private := true
	info := metainfo.Info{
		Name:        name,
		Length:      length,
		PieceLength: pieceLength,
		Pieces:      pieces,
		Private:     &private,
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		return nil, "", errors.Wrap(err, "failed to encode info")
	}
	mi := metainfo.MetaInfo{
		InfoBytes:    infoBytes,
		CreationDate: time.Now().Unix(),
		CreatedBy:    "webtor web-ui urlsource",
	}
	if seedURL != "" {
		mi.UrlList = metainfo.UrlList{seedURL}
	}
	var buf bytes.Buffer
	if err := mi.Write(&buf); err != nil {
		return nil, "", errors.Wrap(err, "failed to encode torrent")
	}
	return buf.Bytes(), mi.HashInfoBytes().HexString(), nil
}

type countingReader struct {
	r        io.Reader
	n        int64
	last     int64
	progress func(n int64)
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	if c.progress != nil && (c.n-c.last >= 4<<20 || err != nil) {
		c.last = c.n
		c.progress(c.n)
	}
	return n, err
}
