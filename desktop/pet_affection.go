// SPDX-License-Identifier: GPL-3.0-only
package desktop

import "time"

// Affection is stored as thousandths of a star, so the half-cent rewards do
// not accumulate floating point error. Each character has its own history.
const petMaxAffection = 10000

type petAffection struct {
	Points  int    `json:"points"`
	Day     string `json:"day,omitempty"`
	Started bool   `json:"started,omitempty"`
	Pats    int    `json:"pats,omitempty"`
	Tasks   int    `json:"tasks,omitempty"`
}

type petPreferences struct {
	Enabled     bool                    `json:"enabled"`
	MuteChatter bool                    `json:"muteChatter,omitempty"`
	Size        int                     `json:"size"`
	HasPosition bool                    `json:"hasPosition,omitempty"`
	X           int                     `json:"x,omitempty"`
	Y           int                     `json:"y,omitempty"`
	Affection   map[string]petAffection `json:"affection,omitempty"`
}

func (p *petPreferences) normalize() {
	if p.Size < 120 || p.Size > 280 {
		p.Size = 180
	}
	for k, v := range p.Affection {
		v.Points = max(0, min(petMaxAffection, v.Points))
		v.Pats = max(0, min(3, v.Pats))
		v.Tasks = max(0, min(3, v.Tasks))
		p.Affection[k] = v
	}
}

func (p petPreferences) clone() petPreferences {
	if p.Affection != nil {
		copyMap := make(map[string]petAffection, len(p.Affection))
		for k, v := range p.Affection {
			copyMap[k] = v
		}
		p.Affection = copyMap
	}
	return p
}

// A usual day with two finished tasks and three pats earns .055 stars,
// reaching ten stars in about 182 days. Daily caps avoid rewarding bursts or
// accidental duplicate events indefinitely.
func (p *petPreferences) reward(style, activity string, now time.Time) bool {
	if p.Affection == nil {
		p.Affection = make(map[string]petAffection)
	}
	v := p.Affection[style]
	old := v
	day := now.Format("2006-01-02")
	if v.Day != day {
		v.Day, v.Started, v.Pats, v.Tasks = day, false, 0, 0
	}
	if !v.Started {
		v.Started = true
		v.Points += 20
	}
	switch activity {
	case "pat":
		if v.Pats < 3 {
			v.Pats++
			v.Points += 5
		}
	case "task":
		if v.Tasks < 3 {
			v.Tasks++
			v.Points += 10
		}
	}
	v.Points = max(0, min(petMaxAffection, v.Points))
	p.Affection[style] = v
	return v != old
}
