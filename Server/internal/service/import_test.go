package service

import "testing"

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
