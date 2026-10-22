package content

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/vaporon4a/movie-helper/internal/ai"
	"github.com/vaporon4a/movie-helper/internal/aiwork"
)

const researchFallback = "research_using_fallback"
const researchDeferred = "research_primary_deferred_"

func researchDeferrals(reason string) int {
	if !strings.HasPrefix(reason, researchDeferred) {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimPrefix(reason, researchDeferred))
	if err != nil {
		return 0
	}
	return max(0, min(n, 2))
}
func researchRetry(err error, now time.Time, primary bool, deferrals int) aiwork.Outcome {
	if errors.Is(err, ai.ErrDailyLimit) {
		return aiwork.Outcome{After: untilNextAIWindow(now), Reason: "local_daily_limit"}
	}
	after := 5 * time.Minute
	if retry, ok := errors.AsType[interface {
		error
		RetryAfter() time.Duration
	}](err); ok && retry.RetryAfter() > 0 {
		after = max(time.Second, retry.RetryAfter())
	}
	reason := researchFallback
	if primary {
		reason = fmt.Sprintf("%s%d", researchDeferred, min(deferrals+1, 2))
	}
	return aiwork.Outcome{After: after, Reason: reason}
}
