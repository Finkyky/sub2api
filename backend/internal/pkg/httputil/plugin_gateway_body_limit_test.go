package httputil

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
	"net/http"
	"testing"
)

func TestPluginGatewayBodyLimitEncodedAndPreread(t *testing.T) {
	for _, encoding := range []string{"identity", "gzip", "deflate", "zstd", "preread"} {
		t.Run(encoding, func(t *testing.T) {
			raw := bytes.Repeat([]byte("x"), 4096)
			var encoded bytes.Buffer
			switch encoding {
			case "gzip":
				w := gzip.NewWriter(&encoded)
				_, _ = w.Write(raw)
				require.NoError(t, w.Close())
			case "deflate":
				w := zlib.NewWriter(&encoded)
				_, _ = w.Write(raw)
				require.NoError(t, w.Close())
			case "zstd":
				w, e := zstd.NewWriter(&encoded)
				require.NoError(t, e)
				_, _ = w.Write(raw)
				require.NoError(t, w.Close())
			default:
				_, _ = encoded.Write(raw)
			}
			req, e := http.NewRequest("POST", "/v1/messages", &encoded)
			require.NoError(t, e)
			if encoding == "preread" {
				req.Body = NewPrereadBody(raw)
			} else {
				req.Header.Set("Content-Encoding", encoding)
			}
			req = req.WithContext(WithRequestBodyReadLimit(req.Context(), 128))
			body, e := ReadRequestBodyWithPrealloc(req)
			var maximum *http.MaxBytesError
			require.ErrorAs(t, e, &maximum)
			require.Nil(t, body)
			require.EqualValues(t, 128, maximum.Limit)
		})
	}
}
func TestPluginGatewayBodyLimitExactBoundary(t *testing.T) {
	req, e := http.NewRequest("POST", "/v1/messages", bytes.NewBuffer(make([]byte, 128)))
	require.NoError(t, e)
	req = req.WithContext(WithRequestBodyReadLimit(req.Context(), 128))
	body, e := ReadRequestBodyWithPrealloc(req)
	require.NoError(t, e)
	require.Len(t, body, 128)
}
