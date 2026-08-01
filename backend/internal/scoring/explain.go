package scoring

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Reason codes. Explanations are generated from these by a lookup table, never
// by a language model: the same inputs must always produce the same sentence,
// and every sentence must be traceable to a number a reader can check.
const (
	CodeStarGain         = "star_gain"
	CodeAccelerating     = "accelerating"
	CodeGrowthRestarted  = "growth_restarted"
	CodeRelativeGrowth   = "relative_growth"
	CodeForkGain         = "fork_gain"
	CodeRecentPush       = "recent_push"
	CodeRecentRelease    = "recent_release"
	CodeLimitedHistory   = "limited_history"
	CodeSteadyGrowth     = "steady_growth"
	CodeNoRecentMovement = "no_recent_movement"
)

// reasonPriority fixes the order in which reasons are considered. Explanations
// are emitted in this order and truncated to MaxReasons, so the list is stable
// across runs and across processes.
//
// CodeLimitedHistory leads deliberately. It is only ever generated for a
// provisional entry, and when it applies it is the most important thing a
// reader needs to know — a caveat that gets crowded out by three impressive
// growth numbers is a caveat that failed to do its job.
var reasonPriority = []string{
	CodeLimitedHistory,
	CodeStarGain,
	CodeAccelerating,
	CodeGrowthRestarted,
	CodeRelativeGrowth,
	CodeForkGain,
	CodeRecentRelease,
	CodeRecentPush,
	CodeSteadyGrowth,
	CodeNoRecentMovement,
}

// MaxReasons is how many explanations a repository shows.
const MaxReasons = 3

// Thresholds that decide whether a reason is worth stating. They are named
// constants so the boundaries can be tested directly.
const (
	minStarsForGainReason      = 1
	minAccelerationRatio       = 1.5
	minRelativeGrowthForReason = 0.15
	minForksForReason          = 3
	recentPushWindow           = 24 * time.Hour
	recentReleaseWindow        = 14 * 24 * time.Hour
)

// Explanation is one deterministic sentence with its machine-readable code.
type Explanation struct {
	Code string `json:"code"`
	Text string `json:"text"`
}

// Explain produces up to MaxReasons explanations for a scored repository.
//
// Every sentence restates a measured quantity. Nothing here is inferred,
// predicted or generated: given identical features the output is byte-identical.
func Explain(now time.Time, w Window, f Features, in Input, eligibility Eligibility) []Explanation {
	candidates := make(map[string]string, len(reasonPriority))

	windowPhrase := windowPhraseFor(w)

	if f.PositiveStarDelta >= minStarsForGainReason {
		candidates[CodeStarGain] = fmt.Sprintf("Gained %s during %s.",
			pluralise(f.PositiveStarDelta, "star", "stars"), windowPhrase)
	}

	if f.AccelerationKnown {
		switch {
		case f.PreviousVelocity > 0 && f.RecentVelocity/f.PreviousVelocity >= minAccelerationRatio:
			ratio := f.RecentVelocity / f.PreviousVelocity
			candidates[CodeAccelerating] = fmt.Sprintf(
				"Star velocity is %.1f× higher than during the previous half-window.", ratio)
		case f.PreviousVelocity == 0 && f.RecentVelocity > 0 && f.PositiveStarDelta >= 5:
			candidates[CodeGrowthRestarted] = "Star growth restarted after a flat previous half-window."
		}
	}

	if f.RelativeGrowth >= minRelativeGrowthForReason {
		candidates[CodeRelativeGrowth] = fmt.Sprintf(
			"Growing quickly relative to its current size (%s added to a base of %s).",
			pluralise(f.PositiveStarDelta, "star", "stars"),
			formatCount(f.BaselineStars))
	}

	if f.PositiveForkDelta >= minForksForReason {
		candidates[CodeForkGain] = fmt.Sprintf("Received %s during the same period.",
			pluralise(f.PositiveForkDelta, "new fork", "new forks"))
	}

	if in.PushedAt != nil {
		if elapsed := now.Sub(*in.PushedAt); elapsed >= 0 && elapsed <= recentPushWindow {
			candidates[CodeRecentPush] = fmt.Sprintf("The default branch was updated %s.", humaniseAgo(elapsed))
		}
	}

	if in.LatestReleaseAt != nil {
		if elapsed := now.Sub(*in.LatestReleaseAt); elapsed >= 0 && elapsed <= recentReleaseWindow {
			candidates[CodeRecentRelease] = fmt.Sprintf("Published a release %s.", humaniseAgo(elapsed))
		}
	}

	if eligibility == EligibleProvisional {
		candidates[CodeLimitedHistory] = fmt.Sprintf(
			"History is incomplete because tracking began recently; only %s of the %s window was observed.",
			humaniseDuration(time.Duration(f.ObservedHours*float64(time.Hour))), w.Name)
	}

	// Fallbacks so that every ranked repository always says something true.
	if len(candidates) == 0 {
		if f.PositiveStarDelta > 0 {
			candidates[CodeSteadyGrowth] = fmt.Sprintf("Growing steadily during %s.", windowPhrase)
		} else {
			candidates[CodeNoRecentMovement] = fmt.Sprintf("No measured star growth during %s.", windowPhrase)
		}
	}

	out := make([]Explanation, 0, MaxReasons)
	for _, code := range reasonPriority {
		text, ok := candidates[code]
		if !ok {
			continue
		}
		out = append(out, Explanation{Code: code, Text: text})
		if len(out) == MaxReasons {
			break
		}
	}
	return out
}

// windowPhraseFor renders a window as it appears mid-sentence.
func windowPhraseFor(w Window) string {
	switch w.Name {
	case Window6h.Name:
		return "the last 6 hours"
	case Window24h.Name:
		return "the last 24 hours"
	case Window7d.Name:
		return "the last 7 days"
	default:
		return "the ranking window"
	}
}

// numberWords covers one to twelve, matching ordinary editorial style. Larger
// values are rendered as digits.
var numberWords = [...]string{
	1: "one", 2: "two", 3: "three", 4: "four", 5: "five", 6: "six",
	7: "seven", 8: "eight", 9: "nine", 10: "ten", 11: "eleven", 12: "twelve",
}

func spellOut(n int) string {
	if n >= 1 && n < len(numberWords) {
		return numberWords[n]
	}
	return formatCount(n)
}

// humaniseAgo renders an elapsed duration as "two hours ago".
func humaniseAgo(d time.Duration) string {
	if d < time.Minute {
		return "moments ago"
	}
	return humaniseDuration(d) + " ago"
}

// humaniseDuration renders a duration as "two hours" or "three days".
func humaniseDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "less than a minute"
	case d < time.Hour:
		minutes := int(math.Round(d.Minutes()))
		return fmt.Sprintf("%s %s", spellOut(minutes), plural(minutes, "minute", "minutes"))
	case d < 24*time.Hour:
		hours := int(math.Round(d.Hours()))
		if hours == 0 {
			hours = 1
		}
		return fmt.Sprintf("%s %s", spellOut(hours), plural(hours, "hour", "hours"))
	default:
		days := int(math.Round(d.Hours() / 24))
		if days == 0 {
			days = 1
		}
		return fmt.Sprintf("%s %s", spellOut(days), plural(days, "day", "days"))
	}
}

func plural(n int, singular, pluralForm string) string {
	if n == 1 {
		return singular
	}
	return pluralForm
}

// pluralise renders "312 stars" or "1 star" with thousands separators.
func pluralise(n int, singular, pluralForm string) string {
	return formatCount(n) + " " + plural(n, singular, pluralForm)
}

// FormatCount inserts thousands separators: 1240 becomes "1,240".
func FormatCount(n int) string {
	sign := ""
	if n < 0 {
		sign, n = "-", -n
	}

	digits := strconv.Itoa(n)
	if len(digits) <= 3 {
		return sign + digits
	}

	groups := make([]string, 0, len(digits)/3+1)
	if lead := len(digits) % 3; lead > 0 {
		groups = append(groups, digits[:lead])
		digits = digits[lead:]
	}
	for i := 0; i < len(digits); i += 3 {
		groups = append(groups, digits[i:i+3])
	}
	return sign + strings.Join(groups, ",")
}

func formatCount(n int) string { return FormatCount(n) }
