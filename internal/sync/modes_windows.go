//go:build windows

package sync

import "github.com/Rush1994/SkillPort/internal/skill"

func preserveModes(s *skill.Snapshot, modes map[string]int64) {
	for i := range s.Files {
		if mode, ok := modes[s.Files[i].Path]; ok {
			s.Files[i].Mode = mode
		}
	}
}
