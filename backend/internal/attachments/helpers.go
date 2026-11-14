package attachments

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// GenerateS3Key creates a unique S3 key for an attachment
// Adapted from RedClaw PKM: removed tenant_id, kept user_id+note_id structure
// Format: attachments/{user_id}/{note_id}/{uuid}_{sanitized_filename}
func GenerateS3Key(userID, noteID string, filename string) string {
	fileID := uuid.New()
	safeFilename := SanitizeFilename(filename)
	return fmt.Sprintf("attachments/%s/%s/%s_%s",
		userID,
		noteID,
		fileID.String(),
		safeFilename,
	)
}

// SanitizeFilename removes unsafe characters from filenames
// Donated from RedClaw PKM helpers.go (tested with 6/6 passing tests)
func SanitizeFilename(filename string) string {
	// Remove path separators and other unsafe characters
	unsafe := []string{"/", "\\", "..", "~", "$", "&", "|", ";", "(", ")", "<", ">", "`", "'", "\""}
	safe := filename
	for _, char := range unsafe {
		safe = strings.ReplaceAll(safe, char, "_")
	}
	return safe
}

// ParseContentType determines content type from file extension
// Donated from RedClaw PKM helpers.go (tested with 8 file types)
func ParseContentType(filename string) string {
	lower := strings.ToLower(filename)

	switch {
	case strings.HasSuffix(lower, ".jpg"), strings.HasSuffix(lower, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(lower, ".png"):
		return "image/png"
	case strings.HasSuffix(lower, ".gif"):
		return "image/gif"
	case strings.HasSuffix(lower, ".webp"):
		return "image/webp"
	case strings.HasSuffix(lower, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(lower, ".pdf"):
		return "application/pdf"
	case strings.HasSuffix(lower, ".doc"):
		return "application/msword"
	case strings.HasSuffix(lower, ".docx"):
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case strings.HasSuffix(lower, ".xls"):
		return "application/vnd.ms-excel"
	case strings.HasSuffix(lower, ".xlsx"):
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case strings.HasSuffix(lower, ".ppt"):
		return "application/vnd.ms-powerpoint"
	case strings.HasSuffix(lower, ".pptx"):
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	case strings.HasSuffix(lower, ".txt"):
		return "text/plain"
	case strings.HasSuffix(lower, ".md"):
		return "text/markdown"
	case strings.HasSuffix(lower, ".csv"):
		return "text/csv"
	case strings.HasSuffix(lower, ".json"):
		return "application/json"
	case strings.HasSuffix(lower, ".xml"):
		return "application/xml"
	case strings.HasSuffix(lower, ".zip"):
		return "application/zip"
	case strings.HasSuffix(lower, ".tar"):
		return "application/x-tar"
	case strings.HasSuffix(lower, ".gz"):
		return "application/gzip"
	case strings.HasSuffix(lower, ".mp3"):
		return "audio/mpeg"
	case strings.HasSuffix(lower, ".mp4"):
		return "video/mp4"
	case strings.HasSuffix(lower, ".wav"):
		return "audio/wav"
	default:
		return "application/octet-stream"
	}
}

// ExtractTextForEmbedding combines title and content for embedding generation
// Donated from RedClaw PKM helpers.go
func ExtractTextForEmbedding(title, content string, maxLength int) string {
	// Combine title and content, truncate if too long
	text := fmt.Sprintf("%s\n\n%s", title, content)
	if len(text) > maxLength {
		text = text[:maxLength]
	}
	return text
}

// ValidateFilename checks if filename is valid and safe
func ValidateFilename(filename string) error {
	if strings.TrimSpace(filename) == "" {
		return fmt.Errorf("filename cannot be empty")
	}
	if len(filename) > 255 {
		return fmt.Errorf("filename too long (max 255 characters)")
	}
	// Check for null bytes
	if strings.Contains(filename, "\x00") {
		return fmt.Errorf("filename contains null bytes")
	}
	return nil
}

// ValidateFileSize checks if file size is within limits
func ValidateFileSize(sizeBytes int64) error {
	const maxSize = 52428800 // 50MB
	if sizeBytes <= 0 {
		return fmt.Errorf("file size must be positive")
	}
	if sizeBytes > maxSize {
		return fmt.Errorf("file size exceeds maximum of 50MB")
	}
	return nil
}

// GetImageDimensions extracts width/height from image metadata (placeholder)
// In production, this would use image decoding libraries
func GetImageDimensions(data []byte, contentType string) (width, height int, err error) {
	// TODO: Implement actual image dimension extraction
	// For now, return 0,0 to indicate dimensions not available
	// In production, use:
	// - image.Decode() for standard formats
	// - github.com/h2non/bimg for more formats
	return 0, 0, nil
}
