//go:build dbtest

package repositories_test

import (
	"os"
	"testing"

	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/schemadb"
)

// The scratch schema is built once for the whole package (a few seconds) and dropped here.
func TestMain(m *testing.M) {
	code := m.Run()
	schemadb.Teardown()
	os.Exit(code)
}
