package filters

import (
	"strings"
	"testing"

	"github.com/stephenywilson/xit/internal/runner"
)

func TestFilterSearch(t *testing.T) {
	input := `src/main.go:10:func main() {
src/main.go:20:fmt.Println("hello")
internal/util.go:5:func helper() {}
internal/util.go:15:func helper2() {}
internal/util.go:25:func helper3() {}
internal/util.go:35:func helper4() {}
`
	res := &runner.Result{Stdout: []byte(input), ExitCode: 0, DurationMs: 10}
	s, err := filterSearch([]string{"rg", "func"}, res)
	if err != nil {
		t.Fatal(err)
	}
	if s.KeyFacts["files_matched"].(int) != 2 {
		t.Errorf("files_matched = %v, want 2", s.KeyFacts["files_matched"])
	}
	render := s.Render("human")
	if !strings.Contains(render, "truncated") {
		t.Error("expected truncation hint for >3 matches per file")
	}
}
