package mcp

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// maxDays ist die groesste Tageszahl, die noch in eine time.Duration passt.
// Ohne die Pruefung ist die Umrechnung ein ueberlaufender float64->int64-Cast
// mit undefiniertem Ergebnis; ein Fenster in die Vergangenheit koennte dadurch
// in der Zukunft landen. Der h/m/s-Pfad von time.ParseDuration prueft selbst.
const maxDays = float64(math.MaxInt64) / float64(24*time.Hour)

// ParseTime nimmt entweder einen RFC-3339-Zeitpunkt oder eine relative Dauer
// wie "24h" oder "7d" und liefert immer UTC (R18).
//
// Go kennt bei Dauern kein "d", deshalb die eigene Behandlung: ohne sie waere
// "7d" ein Fehler, und genau das schreibt ein Modell am ehesten.
func ParseTime(value string, now time.Time) (time.Time, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, fmt.Errorf("empty time value; use RFC 3339 like 2026-09-29T10:00:00Z or a duration like 24h or 7d")
	}

	if parsed, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return parsed.UTC(), nil
	}

	duration, err := parseDuration(trimmed)
	if err != nil {
		return time.Time{}, err
	}
	return now.Add(-duration).UTC(), nil
}

func parseDuration(value string) (time.Duration, error) {
	invalid := fmt.Errorf("cannot read %q as a time; use RFC 3339 like 2026-09-29T10:00:00Z or a duration like 30m, 24h, 7d", value)

	if strings.HasPrefix(value, "-") {
		// Eine relative Angabe zaehlt immer rueckwaerts; ein Minus waere doppelt
		// gemoppelt und mehrdeutig.
		return 0, invalid
	}
	if days, found := strings.CutSuffix(value, "d"); found {
		count, err := strconv.ParseFloat(days, 64)
		if err != nil || count < 0 || math.IsNaN(count) || count > maxDays {
			return 0, invalid
		}
		return time.Duration(count * float64(24*time.Hour)), nil
	}

	duration, err := time.ParseDuration(value)
	if err != nil || duration < 0 {
		return 0, invalid
	}
	return duration, nil
}
