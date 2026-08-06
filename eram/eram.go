package eram

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/juliusplatzer/reds/util"
)

type LatLon struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

type Sector struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	VisualCenter LatLon  `json:"visualCenter"`
	Frequency    float64 `json:"freq"`
}

func (s Sector) Label() string {
	name := sectorDisplayName(s.ID, s.Name)
	if name == "" {
		return s.ID
	}
	return s.ID + " - " + name
}

type Facility struct {
	DefaultCenter LatLon   `json:"defaultCenter"`
	Sectors       []Sector `json:"sectors"`
}

func LoadFacility(artcc string) (Facility, error) {
	artcc = strings.ToUpper(strings.TrimSpace(artcc))
	if artcc == "" {
		return Facility{}, fmt.Errorf("ERAM: empty ARTCC")
	}

	path := "resources/configs/eram/" + artcc + ".json"
	if !util.ResourceExists(path) {
		return Facility{}, fmt.Errorf("ERAM: facility config %s not found", path)
	}

	var facility Facility
	if err := json.Unmarshal(util.LoadResourceBytes(path), &facility); err != nil {
		return Facility{}, fmt.Errorf("ERAM: decode %s: %w", path, err)
	}

	return facility, nil
}

func sectorDisplayName(id, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}

	ids := sectorIDDisplayForms(id)
	for _, value := range ids {
		quoted := regexp.QuoteMeta(value)

		// Common ERAM names include redundant parenthesized IDs such as
		// "(02) WICHITA HI" or "Sector 2 (R2)".
		name = regexp.MustCompile(`\(\s*[[:alpha:]]*`+quoted+`\s*\)`).ReplaceAllString(name, "")

		// Remove the ID when it appears as its own numeric token at the
		// beginning, middle, or end of the name.
		token := regexp.MustCompile(`(^|[^[:alnum:]])` + quoted + `([^[:alnum:]]|$)`)
		for {
			next := token.ReplaceAllString(name, "${1}${2}")
			if next == name {
				break
			}
			name = next
		}
	}

	name = strings.ReplaceAll(name, "_", " ")
	name = strings.Join(strings.Fields(name), " ")
	return strings.Trim(name, " -_")
}

func sectorIDDisplayForms(id string) []string {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}

	out := []string{id}
	trimmed := strings.TrimLeft(id, "0")
	if trimmed == "" {
		trimmed = "0"
	}
	if trimmed != id {
		out = append(out, trimmed)
	}
	return out
}
