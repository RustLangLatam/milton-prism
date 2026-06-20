package application

import analysisdomain "milton_prism/core/services/analysis/domain"

// MergeTechnologies merges incoming into existing by the composite key
// (Name, Category).
//
// Rules applied per matching pair:
//   - DetectedVersion: filled from incoming only when the existing entry is empty.
//   - LatestVersion:   filled from incoming only when the existing entry is empty.
//   - Status:          filled from incoming only when the existing status is UNSPECIFIED.
//   - All other fields on existing entries are always preserved.
//   - Entries in incoming whose key has no match in existing are appended.
//   - No entry from existing is ever removed.
//
// MergeTechnologies never modifies its input slices; it returns a new slice.
// Calling it with a nil or empty existing is safe and returns a copy of incoming.
func MergeTechnologies(existing, incoming []*analysisdomain.Technology) []*analysisdomain.Technology {
	type tkey struct{ name, category string }

	result := make([]*analysisdomain.Technology, len(existing))
	index := make(map[tkey]int, len(existing))

	for i, t := range existing {
		result[i] = cloneTechnology(t)
		index[tkey{t.GetName(), t.GetCategory()}] = i
	}

	for _, t := range incoming {
		k := tkey{t.GetName(), t.GetCategory()}
		if i, ok := index[k]; ok {
			e := result[i]
			if e.DetectedVersion == "" && t.GetDetectedVersion() != "" {
				e.DetectedVersion = t.GetDetectedVersion()
			}
			if e.LatestVersion == "" && t.GetLatestVersion() != "" {
				e.LatestVersion = t.GetLatestVersion()
			}
			if e.Status == analysisdomain.TechnologyStatusUnspecified &&
				t.GetStatus() != analysisdomain.TechnologyStatusUnspecified {
				e.Status = t.GetStatus()
			}
		} else {
			index[k] = len(result)
			result = append(result, cloneTechnology(t))
		}
	}

	return result
}

func cloneTechnology(t *analysisdomain.Technology) *analysisdomain.Technology {
	if t == nil {
		return nil
	}
	return &analysisdomain.Technology{
		Name:            t.GetName(),
		DetectedVersion: t.GetDetectedVersion(),
		LatestVersion:   t.GetLatestVersion(),
		Status:          t.GetStatus(),
		Category:        t.GetCategory(),
		Slug:            t.GetSlug(),
	}
}
