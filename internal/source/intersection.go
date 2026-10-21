package source

import "golang.org/x/mod/semver"

// ConstraintsOverlap reports whether some release version could satisfy every
// constraint, independently of which versions have actually been published.
func ConstraintsOverlap(constraints []string) (bool, error) {
	parsed := make([]constraint, 0, len(constraints))
	witness := "0.0.0"
	for _, text := range constraints {
		c, err := parseConstraint(text)
		if err != nil {
			return false, err
		}
		parsed = append(parsed, c)
		floor := c.exact
		if c.caret {
			floor = c.floor[1:]
		}
		if floor != "" && semver.Compare("v"+floor, "v"+witness) > 0 {
			witness = floor
		}
	}
	for _, c := range parsed {
		if !c.contains(witness) {
			return false, nil
		}
	}
	return true, nil
}
