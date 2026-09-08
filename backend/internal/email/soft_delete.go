package email

import "strings"

type PurgeSource struct {
	Subject   string
	Snippet   string
	AISummary string
}

type PurgeFields struct {
	Subject    string
	Snippet    string
	AISummary  string
	DeletedAt  int64
	BodyPurged bool
}

func BuildPurgeFields(src PurgeSource, now int64) PurgeFields {
	summary := strings.TrimSpace(src.AISummary)
	if summary == "" {
		summary = strings.TrimSpace(src.Snippet)
	}
	return PurgeFields{
		Subject:    src.Subject,
		Snippet:    "",
		AISummary:  summary,
		DeletedAt:  now,
		BodyPurged: true,
	}
}

func ShouldHidePurged(deletedAt int64, bodyPurged bool) bool {
	return bodyPurged || deletedAt > 0
}
