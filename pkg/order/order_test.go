package order

import "testing"

func TestSideString(t *testing.T) {
	if Buy.String() != "buy" {
		t.Fatalf("Buy=%q", Buy.String())
	}
	if Sell.String() != "sell" {
		t.Fatalf("Sell=%q", Sell.String())
	}
}
