package engine

import "testing"

// Cost drift is the alarm that would have caught the reasoning-token bug on its
// own: a modeled price that ignores reasoning diverges ~290% from what the
// provider actually charged.
func TestCheckCostDrift(t *testing.T) {
	cases := []struct {
		name              string
		modeled, provider float64
		wantWarn          bool
		why               string
	}{
		{
			// The real bug, as it would have surfaced: $0.001558 modeled
			// against xAI's reported $0.006074.
			name:    "reasoning-blind pricing is flagged",
			modeled: 0.001558, provider: 0.006074,
			wantWarn: true,
			why:      "74% of true cost was invisible",
		},
		{
			name:    "matching prices are silent",
			modeled: 0.006074, provider: 0.006074,
			wantWarn: false,
			why:      "model agrees with the vendor",
		},
		{
			name:    "provider reported nothing",
			modeled: 0.5, provider: 0,
			wantWarn: false,
			why:      "no figure to verify against",
		},
		{
			name:    "local model priced at zero",
			modeled: 0, provider: 0,
			wantWarn: false,
			why:      "nothing to compare",
		},
		{
			// Percentage alone is not enough: sub-cent attempts clear 5% on
			// rounding, and an alarm that always fires gets ignored.
			name:    "large percentage below the absolute floor is silent",
			modeled: 0.0000010, provider: 0.0000020,
			wantWarn: false,
			why:      "50% drift but only $0.000001 in absolute terms",
		},
		{
			name:    "small percentage above the floor is silent",
			modeled: 1.010, provider: 1.000,
			wantWarn: false,
			why:      "1% drift is within tolerance despite being $0.01",
		},
		{
			name:    "over-modeling is flagged too",
			modeled: 0.010, provider: 0.001,
			wantWarn: true,
			why:      "drift is absolute; rates.yaml too high is equally stale",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := checkCostDrift("test-preset", c.modeled, c.provider)
			if (got != "") != c.wantWarn {
				t.Fatalf("warn=%v (%q), want warn=%v (%s)", got != "", got, c.wantWarn, c.why)
			}
		})
	}
}
