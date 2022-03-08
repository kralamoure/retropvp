package retropvp

import (
	"testing"

	"github.com/kralamoure/retro"
)

func Test_calcBoost(t *testing.T) {
	type testCase struct {
		current int
		add     int
		boosts  []retro.ClassBoostCost

		wantCost  int
		wantBonus int
	}

	iopStrength := []retro.ClassBoostCost{
		{Quantity: 0, Cost: 1, Bonus: 1},
		{Quantity: 100, Cost: 2, Bonus: 1},
		{Quantity: 200, Cost: 3, Bonus: 1},
		{Quantity: 300, Cost: 4, Bonus: 1},
		{Quantity: 400, Cost: 5, Bonus: 1},
	}

	sacrierVitality := []retro.ClassBoostCost{
		{Quantity: 0, Cost: 1, Bonus: 2},
	}

	wisdom := []retro.ClassBoostCost{
		{Quantity: 0, Cost: 3, Bonus: 1},
	}

	testCases := []testCase{
		{current: 0, add: 1, boosts: iopStrength, wantCost: 1, wantBonus: 1},
		{current: 100, add: 50, boosts: iopStrength, wantCost: 100, wantBonus: 50},
		{current: 101, add: 99, boosts: iopStrength, wantCost: 198, wantBonus: 99},
		{current: 101, add: 100, boosts: iopStrength, wantCost: 201, wantBonus: 100},
		{current: 101, add: 100, boosts: iopStrength, wantCost: 201, wantBonus: 100},
		{current: 0, add: 1000, boosts: sacrierVitality, wantCost: 500, wantBonus: 1000},
		{current: 30, add: 100, boosts: sacrierVitality, wantCost: 50, wantBonus: 100},
		{current: 0, add: 100, boosts: wisdom, wantCost: 300, wantBonus: 100},
		{current: 100, add: 100, boosts: wisdom, wantCost: 300, wantBonus: 100},
	}

	for _, tc := range testCases {
		cost, bonus := calcBoost(tc.current, tc.add, tc.boosts)

		if cost != tc.wantCost {
			t.Errorf("cost: want %d, got %d", tc.wantCost, cost)
		}

		if bonus != tc.wantBonus {
			t.Errorf("bonus: want %d, got %d", tc.wantBonus, bonus)
		}
	}

}
