package telegraph

import (
	"time"

	"github.com/robfig/cron/v3"
)

// cronParser uses standard 5-field cron expressions (minute, hour, dom, month, dow).
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// nextCronDuration parses a 5-field cron expression and returns the duration
// until the next fire time. Returns 0 on parse error.
func nextCronDuration(expr string) time.Duration {
	sched, err := cronParser.Parse(expr)
	if err != nil {
		return 0
	}
	next := sched.Next(time.Now())
	d := time.Until(next)
	if d < 0 {
		return 0
	}
	return d
}
