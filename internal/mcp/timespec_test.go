package mcp_test

import (
	"testing"
	"time"

	"github.com/Fuchsi94/stateinspector/internal/mcp"
)

var reference = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

func TestParseTimeAcceptsRFC3339AndRelativeDurations(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want time.Time
	}{
		{"RFC 3339", "2026-09-28T10:00:00Z", time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)},
		{"Stunden", "24h", reference.Add(-24 * time.Hour)},
		{"Minuten", "30m", reference.Add(-30 * time.Minute)},
		{"Tage", "7d", reference.Add(-7 * 24 * time.Hour)},
		{"ein Tag", "1d", reference.Add(-24 * time.Hour)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := mcp.ParseTime(tc.in, reference)
			if err != nil {
				t.Fatalf("ParseTime(%q): %v", tc.in, err)
			}
			if !got.Equal(tc.want) {
				t.Errorf("ParseTime(%q) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseTimeRejectsGarbage(t *testing.T) {
	for _, in := range []string{"gestern", "7x", "", "letzten Montag", "d", "-3d",
		// Ueberlauf: time.Duration fasst rund 106751 Tage. Ohne Pruefung ist die
		// Umrechnung ein undefinierter float64->int64-Cast, der ein Fenster in
		// die Vergangenheit in der Zukunft landen lassen kann.
		"200000d", "1e30d", "NaNd"} {
		t.Run(in, func(t *testing.T) {
			if _, err := mcp.ParseTime(in, reference); err == nil {
				t.Errorf("ParseTime(%q) lieferte keinen Fehler", in)
			}
		})
	}
}

// Ergebnisse sind immer UTC, damit ein Modell keine Zeitzone raten muss.
func TestParseTimeNormalisesToUTC(t *testing.T) {
	got, err := mcp.ParseTime("2026-09-28T12:00:00+02:00", reference)
	if err != nil {
		t.Fatalf("ParseTime(): %v", err)
	}
	if got.Location() != time.UTC {
		t.Errorf("Location = %s, want UTC", got.Location())
	}
	if got.Hour() != 10 {
		t.Errorf("Stunde = %d, want 10 nach Umrechnung", got.Hour())
	}
}
