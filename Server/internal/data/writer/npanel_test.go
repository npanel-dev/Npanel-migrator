package writer

import (
	"strings"
	"testing"
)

func TestNPanelConfigDSNUseUTC(t *testing.T) {
	dsn := (NPanelConfig{
		Host:     "127.0.0.1",
		Port:     3306,
		Database: "npanel",
		Username: "root",
		Password: "secret",
	}).DSN()

	if !strings.Contains(dsn, "loc=UTC") {
		t.Fatalf("DSN must serialize target DATETIME values in UTC: %s", dsn)
	}
	if strings.Contains(dsn, "loc=Local") {
		t.Fatalf("DSN must not depend on the migrator host timezone: %s", dsn)
	}
}
