package service

import (
	"strings"
	"testing"

	"npanel-migrator/internal/adapter/xiaov2board"
)

func TestValidateModuleDependencies(t *testing.T) {
	tests := []struct {
		name    string
		modules []string
		wantErr bool
	}{
		{name: "empty means full migration", modules: nil},
		{name: "users with subscriptions", modules: []string{ModuleUsers, ModuleSubscriptions}},
		{name: "independent notices", modules: []string{ModuleNotices}},
		{name: "subscriptions require users", modules: []string{ModuleSubscriptions}, wantErr: true},
		{name: "orders require users", modules: []string{ModuleOrders}, wantErr: true},
		{name: "tickets require users", modules: []string{ModuleTickets}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateModuleDependencies(tt.modules)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateModuleDependencies(%v) error = %v, wantErr %v", tt.modules, err, tt.wantErr)
			}
		})
	}
}

func TestDryRunBlockingError(t *testing.T) {
	t.Run("allows successful report", func(t *testing.T) {
		report := &xiaov2board.DryRunReport{
			Summary: xiaov2board.DryRunSummary{CanProceed: true},
			Issues: []xiaov2board.Issue{
				{Severity: xiaov2board.SeverityWarning, Message: "warning only"},
			},
		}
		if err := dryRunBlockingError(report); err != nil {
			t.Fatalf("dryRunBlockingError() error = %v, want nil", err)
		}
	})

	t.Run("blocks error issues", func(t *testing.T) {
		report := &xiaov2board.DryRunReport{
			Summary: xiaov2board.DryRunSummary{CanProceed: false, ErrorCount: 1},
			Issues: []xiaov2board.Issue{
				{
					Severity: xiaov2board.SeverityError,
					Message:  "存在不支持的密码哈希",
					Count:    3,
				},
			},
		}
		err := dryRunBlockingError(report)
		if err == nil {
			t.Fatal("dryRunBlockingError() error = nil, want blocker")
		}
		if !strings.Contains(err.Error(), "存在不支持的密码哈希（3 条）") {
			t.Fatalf("dryRunBlockingError() = %q, want blocker details", err)
		}
	})

	t.Run("blocks missing report", func(t *testing.T) {
		if err := dryRunBlockingError(nil); err == nil {
			t.Fatal("dryRunBlockingError(nil) error = nil, want blocker")
		}
	})
}
