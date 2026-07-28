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

func TestStableOptionsIsOrderIndependentAndExcludesCredentials(t *testing.T) {
	first := &ImportRequest{
		SourcePassword: "source-secret",
		TargetPassword: "target-secret",
		Modules:        []string{ModuleOrders, ModuleUsers},
		PlanMappings: []PlanMapping{
			{
				SourcePlanID: 2, TargetSubscribeID: 20,
				PeriodMappings: []PeriodMapping{
					{SourcePeriod: "year_price", TargetPriceOptionID: 202},
					{SourcePeriod: "month_price", TargetPriceOptionID: 201},
				},
			},
			{SourcePlanID: 1, TargetSubscribeID: 10},
		},
		TrialAssignment: TrialAssignment{
			TargetSubscribeID: 99, DurationUnit: "Day", DurationValue: 7,
		},
	}
	second := &ImportRequest{
		SourcePassword: "different-source-secret",
		TargetPassword: "different-target-secret",
		Modules:        []string{ModuleUsers, ModuleOrders},
		PlanMappings: []PlanMapping{
			{SourcePlanID: 1, TargetSubscribeID: 10},
			{
				SourcePlanID: 2, TargetSubscribeID: 20,
				PeriodMappings: []PeriodMapping{
					{SourcePeriod: "month_price", TargetPriceOptionID: 201},
					{SourcePeriod: "year_price", TargetPriceOptionID: 202},
				},
			},
		},
		TrialAssignment: first.TrialAssignment,
	}
	firstJSON, firstHash, err := stableOptions(first)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, secondHash, err := stableOptions(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstHash != secondHash || firstJSON != secondJSON {
		t.Fatalf("stable options differ:\n%s\n%s", firstJSON, secondJSON)
	}
	if strings.Contains(firstJSON, "secret") {
		t.Fatalf("stored options contain credentials: %s", firstJSON)
	}
}

func TestConfigFingerprintNormalization(t *testing.T) {
	first := configFingerprint("XiaoV2Board", "DB.EXAMPLE.COM ", 3306, "V2BOARD")
	second := configFingerprint("xiaov2board", "db.example.com", 3306, "v2board")
	if first != second {
		t.Fatalf("normalized fingerprints differ: %s != %s", first, second)
	}
	if first == configFingerprint("xiaov2board", "db.example.com", 3306, "other") {
		t.Fatal("different database produced the same fingerprint")
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
