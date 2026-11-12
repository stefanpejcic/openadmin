package cluster

import (
	"errors"
	"html"
)

// Palette is the order new servers get their color in, after the last one it starts over, no green or red since those read as up/down
var Palette = []string{"blue", "violet", "amber", "cyan", "fuchsia", "indigo", "sky", "purple", "yellow", "slate"}

// colorClasses is spelled out so tailwind keeps every one of them in output.css
var colorClasses = map[string]string{
	"blue": "bg-blue-500", "violet": "bg-violet-500", "amber": "bg-amber-500", "cyan": "bg-cyan-500", "fuchsia": "bg-fuchsia-500",
	"indigo": "bg-indigo-500", "sky": "bg-sky-500", "purple": "bg-purple-500", "yellow": "bg-yellow-400", "slate": "bg-slate-500",
}

// ColorClass is the background class of a palette color, an unknown one falls back to the first
func ColorClass(color string) string {
	if c, ok := colorClasses[color]; ok {
		return c
	}
	return colorClasses[Palette[0]]
}

// nextColor is the first palette color nobody has yet, or the next one round when all ten are taken
func nextColor(s *State) string {
	used := map[string]bool{}
	for _, c := range allColors(s) {
		used[c] = true
	}
	for _, c := range Palette {
		if !used[c] {
			return c
		}
	}
	return Palette[(len(s.Nodes)+1)%len(Palette)]
}

// allColors is every server's color, ones from before colors existed get the free ones in order
func allColors(s *State) map[string]string {
	out := map[string]string{}
	used := map[string]bool{}
	// a saved color that's no longer in the palette counts as none
	if colorClasses[s.SelfColor] != "" {
		out[LocalNode] = s.SelfColor
		used[s.SelfColor] = true
	}
	for _, n := range s.Nodes {
		if colorClasses[n.Color] != "" {
			out[n.ID] = n.Color
			used[n.Color] = true
		}
	}
	ids := []string{LocalNode}
	for _, n := range s.Nodes {
		ids = append(ids, n.ID)
	}
	next := 0
	for pos, id := range ids {
		if out[id] != "" {
			continue
		}
		for next < len(Palette) && used[Palette[next]] {
			next++
		}
		if next < len(Palette) {
			out[id] = Palette[next]
			used[Palette[next]] = true
		} else {
			out[id] = Palette[pos%len(Palette)]
		}
	}
	return out
}

func colorOf(s *State, id string) string {
	if id == "" {
		id = LocalNode
	}
	if c, ok := allColors(s)[id]; ok {
		return c
	}
	return Palette[0]
}

// ColorOf is the color a server shows with in the Server column
func (m *Manager) ColorOf(id string) string {
	st := m.State()
	return colorOf(&st, id)
}

// Colors maps every server of the cluster to its color, the master is LocalNode
func (m *Manager) Colors() map[string]string {
	st := m.State()
	return allColors(&st)
}

// SetColor saves the color the admin picked for a server
func (m *Manager) SetColor(id, color string) error {
	if _, ok := colorClasses[color]; !ok {
		return errors.New("unknown color")
	}
	return m.update(func(s *State) error {
		if id == LocalNode {
			s.SelfColor = color
			return nil
		}
		for i := range s.Nodes {
			if s.Nodes[i].ID == id {
				s.Nodes[i].Color = color
				return nil
			}
		}
		return errors.New("unknown server")
	})
}

// ServerLabel is a server's name with its color dot in front, for the Server column of merged lists
func (m *Manager) ServerLabel(id, name string) string {
	return serverDot(m.ColorOf(id)) + html.EscapeString(name)
}

func serverDot(color string) string {
	return `<span class="mr-1.5 inline-block size-2 shrink-0 rounded-sm align-middle ` + ColorClass(color) + `" aria-hidden="true"></span>`
}
