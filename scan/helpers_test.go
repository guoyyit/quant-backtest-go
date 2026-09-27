package scan

import "time"

var base = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

func t0plus(i int) time.Time { return base.AddDate(0, 0, i) }
