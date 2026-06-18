package engine

import "testing"

func TestValidateOrder(t *testing.T) {
	tests := []struct {
		name    string
		order   Order
		wantErr bool
	}{
		{name: "price 0 rejected", order: Order{OwnerID: "u1", Price: 0, Quantity: 1}, wantErr: true},
		{name: "price 100 rejected", order: Order{OwnerID: "u1", Price: 100, Quantity: 1}, wantErr: true},
		{name: "negative price rejected", order: Order{OwnerID: "u1", Price: -5, Quantity: 1}, wantErr: true},
		{name: "price 1 valid", order: Order{OwnerID: "u1", Price: 1, Quantity: 1}, wantErr: false},
		{name: "price 99 valid", order: Order{OwnerID: "u1", Price: 99, Quantity: 1}, wantErr: false},
		{name: "quantity 0 rejected", order: Order{OwnerID: "u1", Price: 50, Quantity: 0}, wantErr: true},
		{name: "negative quantity rejected", order: Order{OwnerID: "u1", Price: 50, Quantity: -1}, wantErr: true},
		{name: "missing ownerID rejected", order: Order{OwnerID: "", Price: 50, Quantity: 1}, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateOrder(&tc.order)
			if tc.wantErr && err == nil {
				t.Errorf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("expected no error, got: %v", err)
			}
		})
	}
}
