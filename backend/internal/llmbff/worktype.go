package llmbff

// WorkTypeFromKind maps BFF kind (quota tag) to llm-gateway work-type.
// Empty string means do not set work-type (preferred / default model).
func WorkTypeFromKind(kind string) string {
	switch kind {
	case "live_translate", "doc_translate":
		return "doc_translate"
	case "meeting_summary":
		return "meeting_summary"
	case "meeting_refine":
		return "doc_translate"
	default:
		return ""
	}
}
