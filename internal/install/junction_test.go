package install

import (
	"testing"
)

func TestJunctionRejectsShellCharacters(t *testing.T) {
	const want = "cannot create a junction for a path containing one of & | < > ^ % ! \""
	for _, char := range "&|<>^%!\"" {
		for _, target := range []bool{false, true} {
			link, dest := `C:\Users\User Name\project`, `C:\Users\User Name\skill`
			if target {
				dest += string(char)
			} else {
				link += string(char)
			}
			err := validateJunctionPaths(link, dest)
			if err == nil || err.Error() != want {
				t.Fatalf("link=%q target=%q: error = %v", link, dest, err)
			}
		}
	}
	if err := validateJunctionPaths(`C:\Users\User Name\project`, `C:\Users\User Name\skill`); err != nil {
		t.Fatal(err)
	}
}
