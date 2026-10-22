package torznab_indexer

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/pkg/errors"
	jackett "github.com/webtor-io/go-jackett"
)

func TestClassifyAddError(t *testing.T) {
	const secret = "s3cr3tkey"
	caps := func(err error) error {
		// The shape services/torznab.Client.Caps produces.
		return errors.Wrap(fmt.Errorf("get caps: %w", err), "indexer did not answer a capabilities request")
	}
	cases := []struct {
		name    string
		err     error
		wantKey string
		wantNum int
	}{
		{"egress guard", caps(fmt.Errorf("invoke fetch request: %w", errors.New("dial tcp 192.168.1.5:9117: refusing to connect to private address 192.168.1.5"))), "error.indexerPrivateAddress", 0},
		{"proxied guard", errors.New("refusing to reach private address 10.0.0.2"), "error.indexerPrivateAddress", 0},
		{"jackett invalid key", caps(&jackett.IndexerError{Code: "100", Description: "Invalid API Key"}), "error.indexerInvalidApiKey", 0},
		{"key by description", caps(&jackett.IndexerError{Description: "Missing apikey parameter"}), "error.indexerInvalidApiKey", 0},
		{"other torznab code", caps(&jackett.IndexerError{Code: "203", Description: "Function not available"}), "error.indexerRejected", 203},
		{"torznab no code", caps(&jackett.IndexerError{Description: "boom"}), "error.indexerRejected", 900},
		{"prowlarr 401", caps(fmt.Errorf("fetch http://prowlarr:9696/1/api?apikey=%s&t=caps: unexpected status 401", secret)), "error.indexerInvalidApiKey", 0},
		{"http 404", caps(fmt.Errorf("fetch http://jackett:9117/api?apikey=%s: unexpected status 404", secret)), "error.indexerHttpStatus", 404},
		{"http 502", caps(errors.New("fetch http://x/api: unexpected status 502")), "error.indexerHttpStatus", 502},
		{"html page", caps(errors.New("unmarshal response data: http://x/: XML syntax error on line 1")), "error.indexerNotTorznab", 0},
		{"no search mode", errors.New("indexer advertises no usable search mode"), "error.indexerNoSearch", 0},
		{"refused", caps(errors.New("dial tcp 1.2.3.4:9117: connect: connection refused")), "error.indexerUnreachable", 0},
		{"timeout", caps(context.DeadlineExceeded), "error.indexerUnreachable", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ue := classifyAddError(tc.err)
			if ue == nil {
				t.Fatalf("got nil, want %s", tc.wantKey)
			}
			if ue.Key != tc.wantKey || ue.Num != tc.wantNum {
				t.Errorf("got %s/%d, want %s/%d", ue.Key, ue.Num, tc.wantKey, tc.wantNum)
			}
			// Only the key and a number reach the page: the error text
			// (with the URL and key in it) must not be the message.
			if strings.Contains(ue.Error(), secret) {
				t.Errorf("user-facing message leaks the API key: %q", ue.Error())
			}
		})
	}
}

func TestClassifyAddErrorLeavesUnknownErrorsAlone(t *testing.T) {
	if ue := classifyAddError(errors.New("no db")); ue != nil {
		t.Errorf("got %s, want nil (generic message)", ue.Key)
	}
	if ue := classifyAddError(nil); ue != nil {
		t.Errorf("nil error classified as %s", ue.Key)
	}
}
