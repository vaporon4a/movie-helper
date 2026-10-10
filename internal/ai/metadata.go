package ai

// Model metadata is optional for generators used by tests or local tools.
// An unknown model stays unknown; the current deployment config is not evidence.
func generatorModel(generator Generator) string {
	if named, ok := generator.(interface{ ModelName() string }); ok {
		return named.ModelName()
	}
	return ""
}
