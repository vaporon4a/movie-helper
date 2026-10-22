package filmresearch

import "slices"

func object(properties map[string]any) map[string]any {
	required := make([]string, 0, len(properties))
	// Stable ordering is useful for prompt hashes and repeatable experiments.
	for key := range properties {
		required = append(required, key)
	}
	slices.Sort(required)
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}
func scalar(kind string) map[string]any        { return map[string]any{"type": kind} }
func array(item map[string]any) map[string]any { return map[string]any{"type": "array", "items": item} }
func ResponseSchema(audit bool) map[string]any {
	var body map[string]any
	if audit {
		body = object(map[string]any{"checks": array(object(map[string]any{"quote": scalar("string"), "rationale": scalar("string"), "index": scalar("integer"), "supported": scalar("boolean"), "neutral": scalar("boolean"), "single_construct": scalar("boolean"), "spoiler_free": scalar("boolean"), "clear": scalar("boolean")}))})
	} else {
		body = object(map[string]any{"questions": array(object(map[string]any{"family": scalar("string"), "text": scalar("string"), "context": scalar("string"), "evidence": array(object(map[string]any{"source_id": scalar("string"), "span_id": scalar("string")}))}))})
	}
	return object(map[string]any{"index": scalar("integer"), "structured": body})
}
