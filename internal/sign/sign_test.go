package sign

import (
	"strings"
	"testing"
)

func TestFrameFitsAndTurns(t *testing.T) {
	var seen []string
	for i := 0; i < 60; i++ {
		grid, bang := Frame(float64(i)*0.11, 32, 11)
		if len(grid) != 11 {
			t.Fatalf("%d rows", len(grid))
		}
		var sb strings.Builder
		marked := false
		for y, row := range grid {
			if len(row) != 32 {
				t.Fatalf("row %d is %d wide", y, len(row))
			}
			sb.WriteString(string(row) + "\n")
			for x := range row {
				marked = marked || bang[y][x]
			}
		}
		if strings.TrimSpace(sb.String()) == "" {
			t.Fatalf("frame %d is blank", i)
		}
		seen = append(seen, sb.String())
		_ = marked
	}
	if seen[0] == seen[10] {
		t.Fatal("the sign does not turn")
	}
	// Edge-on frames must still show something: the prism has depth.
	grid, _ := Frame(1.5708, 32, 11)
	nonblank := 0
	for _, row := range grid {
		nonblank += len(strings.TrimSpace(string(row)))
	}
	if nonblank < 10 {
		t.Fatalf("edge-on frame nearly blank: %d cells", nonblank)
	}
}
