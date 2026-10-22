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

func TestJunctionCommandQuotesEqualsSpacesAndNativePaths(t *testing.T) {
	link, target := `C:\project with spaces\global=false\.claude\skills\leaf`, `C:\project with spaces\global=false\.agents\skills\leaf`
	want := `cmd /c mklink /J "C:\project with spaces\global=false\.claude\skills\leaf" "C:\project with spaces\global=false\.agents\skills\leaf"`
	if got := junctionCommandLine(link, target); got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
}
