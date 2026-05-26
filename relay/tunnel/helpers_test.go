package tunnel

import (
	"net/http/httptest"
)

// testFrontDomain is the domain-front host for httptest servers (must include port).
func testFrontDomain(srv *httptest.Server) string {
	return srv.Listener.Addr().String()
}
