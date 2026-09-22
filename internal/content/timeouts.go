package content

import "time"

// Selection includes image downloads and up to two model requests. Fetch also
// reserves time for sources and the fallback; preview must still send its reply.
const (
	GeminiRequestTimeout   = 60 * time.Second
	GroqRequestTimeout     = 25 * time.Second
	GeminiSelectionTimeout = 150 * time.Second
	GroqSelectionTimeout   = 60 * time.Second
	FetchTimeout           = 4 * time.Minute
	PreviewTimeout         = FetchTimeout + 30*time.Second
)
