package httpapi

import (
	"net/http/httptest"
	"testing"

	"github.com/timisah/101conv/server/internal/catalog"
)

func TestCatalogSourceIsAvailableOnFreshInstall(t *testing.T) {
	catalogService, err := catalog.New("0.1.0-dev")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New("0.1.0-dev", catalogService))
	defer server.Close()

	response, err := server.Client().Get(server.URL + "/api/v1/catalogs")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("status = %d", response.StatusCode)
	}
}
