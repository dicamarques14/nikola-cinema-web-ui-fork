package urlsource

import (
	"bytes"
	"crypto/sha1"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
)

func TestBuildTorrent(t *testing.T) {
	data := bytes.Repeat([]byte("webtor-url-source-"), 100000) // 1.8 MB
	b, hash, err := BuildTorrent(bytes.NewReader(data), "Film.mkv", int64(len(data)), "http://127.0.0.1:8097/urlseed/x/y/", nil)
	if err != nil {
		t.Fatal(err)
	}
	mi, err := metainfo.Load(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if got := mi.HashInfoBytes().HexString(); got != hash {
		t.Fatalf("hash %s, returned %s", got, hash)
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "Film.mkv" || info.Length != int64(len(data)) || info.Private == nil || !*info.Private {
		t.Fatalf("info %+v", info)
	}
	if len(mi.UrlList) != 1 || mi.UrlList[0] != "http://127.0.0.1:8097/urlseed/x/y/" {
		t.Fatalf("url-list %v", mi.UrlList)
	}
	// Every piece hash is the SHA-1 of that piece of the file.
	for i := 0; i < info.NumPieces(); i++ {
		p := info.Piece(i)
		want := sha1.Sum(data[p.Offset() : p.Offset()+p.Length()])
		if !bytes.Equal(p.V1Hash().Unwrap().Bytes(), want[:]) {
			t.Fatalf("piece %d hash mismatch", i)
		}
	}
}

func TestBuildTorrentShortRead(t *testing.T) {
	if _, _, err := BuildTorrent(bytes.NewReader([]byte("abc")), "a", 10, "", nil); err == nil {
		t.Fatal("expected an error for a short file")
	}
}
