package manifest

import "fmt"

// Resolution is the set of components to run on one platform, in dependency order.
type Resolution struct {
	// Order lists component ids, prerequisites first.
	Order []string
	// AddedBecause maps an auto-added prerequisite to the picked component that needed it.
	AddedBecause map[string]string
	// Unsupported lists picked ids that do not run on this platform.
	Unsupported []string
}

// Resolve adds the missing prerequisites of picked and orders everything for platform.
// A requires entry naming a component that does not run on this platform is dropped, so one
// component can require [core-cli, linux-core] and get whichever exists here.
func (m *Manifest) Resolve(platform string, picked []string) (Resolution, error) {
	res := Resolution{AddedBecause: map[string]string{}}
	selected := map[string]bool{}
	for _, id := range picked {
		c := m.Component(id)
		if c == nil {
			return res, fmt.Errorf("unknown component %q", id)
		}
		if !c.Supports(platform) {
			res.Unsupported = append(res.Unsupported, id)
			continue
		}
		selected[id] = true
	}

	// pull in prerequisites, breadth by manifest order for stable "added because" owners
	queue := make([]string, 0, len(selected))
	for _, c := range m.Components {
		if selected[c.ID] {
			queue = append(queue, c.ID)
		}
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, req := range m.Component(id).Requires {
			rc := m.Component(req)
			if rc == nil || !rc.Supports(platform) || selected[req] {
				continue
			}
			selected[req] = true
			res.AddedBecause[req] = id
			queue = append(queue, req)
		}
	}

	// depth-first post-order over manifest order: prerequisites before dependents
	done := map[string]bool{}
	var visit func(id string)
	visit = func(id string) {
		if done[id] {
			return
		}
		done[id] = true
		for _, req := range m.Component(id).Requires {
			if selected[req] {
				visit(req)
			}
		}
		res.Order = append(res.Order, id)
	}
	for _, c := range m.Components {
		if selected[c.ID] {
			visit(c.ID)
		}
	}
	return res, nil
}

// PlatformComponents returns the ids of all components that run on platform, in manifest order.
func (m *Manifest) PlatformComponents(platform string) []string {
	var out []string
	for _, c := range m.Components {
		if c.Supports(platform) {
			out = append(out, c.ID)
		}
	}
	return out
}
