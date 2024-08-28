package transport

import (
	"net/http"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

// TokenTransport is an http.RoundTripper that adds an Authorization header
// with the Bearer token to the request.
type TokenTransport struct {
	token string
	next  http.RoundTripper
}

func NewBearerTokenTransport(token string, next http.RoundTripper) *TokenTransport {
	if next == nil {
		next = http.DefaultTransport
	}

	return &TokenTransport{
		token: token,
		next:  next,
	}
}

func (t *TokenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set(backend.OAuthIdentityTokenHeaderName, "Bearer "+t.token)

	return t.next.RoundTrip(req)
}
