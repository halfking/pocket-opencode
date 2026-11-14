package attachments

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGenerateS3Key verifies S3 key generation with proper structure
// Adapted from RedClaw PKM test suite
func TestGenerateS3Key(t *testing.T) {
	userID := uuid.New().String()
	noteID := uuid.New().String()
	filename := "test-document.pdf"

	key := GenerateS3Key(userID, noteID, filename)

	assert.Contains(t, key, "attachments/")
	assert.Contains(t, key, userID)
	assert.Contains(t, key, noteID)
	assert.Contains(t, key, "test-document.pdf")

	// Verify format: attachments/{user_id}/{note_id}/{uuid}_{filename}
	parts := key[len("attachments/"):]
	assert.Contains(t, parts, userID)
	assert.Contains(t, parts, noteID)
}

// TestSanitizeFilename verifies dangerous characters are removed
// Donated from RedClaw PKM with 6/6 passing tests
func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "normal filename",
			input:    "normal-file.txt",
			expected: "normal-file.txt",
		},
		{
			name:     "path traversal attack",
			input:    "../../../etc/passwd",
			expected: "______etc_passwd",
		},
		{
			name:     "spaces allowed",
			input:    "file with spaces.pdf",
			expected: "file with spaces.pdf",
		},
		{
			name:     "dangerous shell characters",
			input:    "file;with|dangerous&chars.doc",
			expected: "file_with_dangerous_chars.doc",
		},
		{
			name:     "command injection attempt",
			input:    "$(command).sh",
			expected: "__command_.sh",
		},
		{
			name:     "backticks",
			input:    "`rm -rf /`.txt",
			expected: "_rm -rf __.txt", // Forward slash also sanitized
		},
		{
			name:     "quotes",
			input:    "\"file's name\".doc",
			expected: "_file_s name_.doc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SanitizeFilename(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestParseContentType verifies MIME type detection
// Donated from RedClaw PKM with 8 types tested
func TestParseContentType(t *testing.T) {
	tests := []struct {
		filename    string
		contentType string
	}{
		// Images
		{"document.pdf", "application/pdf"},
		{"image.jpg", "image/jpeg"},
		{"image.JPG", "image/jpeg"},
		{"image.jpeg", "image/jpeg"},
		{"photo.png", "image/png"},
		{"animation.gif", "image/gif"},
		{"modern.webp", "image/webp"},
		{"vector.svg", "image/svg+xml"},

		// Documents
		{"report.doc", "application/msword"},
		{"report.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{"spreadsheet.xls", "application/vnd.ms-excel"},
		{"spreadsheet.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
		{"presentation.ppt", "application/vnd.ms-powerpoint"},
		{"presentation.pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation"},

		// Text
		{"notes.txt", "text/plain"},
		{"readme.md", "text/markdown"},
		{"data.csv", "text/csv"},
		{"config.json", "application/json"},
		{"data.xml", "application/xml"},

		// Archives
		{"archive.zip", "application/zip"},
		{"archive.tar", "application/x-tar"},
		{"compressed.gz", "application/gzip"},

		// Media
		{"audio.mp3", "audio/mpeg"},
		{"video.mp4", "video/mp4"},
		{"sound.wav", "audio/wav"},

		// Unknown
		{"unknown.xyz", "application/octet-stream"},
		{"no-extension", "application/octet-stream"},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			result := ParseContentType(tt.filename)
			assert.Equal(t, tt.contentType, result)
		})
	}
}

// TestExtractTextForEmbedding verifies text extraction and truncation
// Donated from RedClaw PKM
func TestExtractTextForEmbedding(t *testing.T) {
	t.Run("normal case", func(t *testing.T) {
		title := "My Important Note"
		content := "This is the content of my note with lots of information."

		text := ExtractTextForEmbedding(title, content, 1000)
		assert.Contains(t, text, title)
		assert.Contains(t, text, content)
		assert.LessOrEqual(t, len(text), 1000)
	})

	t.Run("truncation", func(t *testing.T) {
		title := "Short Title"
		longContent := string(make([]byte, 2000))

		text := ExtractTextForEmbedding(title, longContent, 500)
		assert.LessOrEqual(t, len(text), 500)
		assert.Contains(t, text, title)
	})

	t.Run("empty content", func(t *testing.T) {
		title := "Only Title"
		content := ""

		text := ExtractTextForEmbedding(title, content, 1000)
		assert.Contains(t, text, title)
	})
}

// TestValidateFilename verifies filename validation
func TestValidateFilename(t *testing.T) {
	tests := []struct {
		name      string
		filename  string
		wantError bool
	}{
		{
			name:      "valid filename",
			filename:  "document.pdf",
			wantError: false,
		},
		{
			name:      "empty filename",
			filename:  "",
			wantError: true,
		},
		{
			name:      "whitespace only",
			filename:  "   ",
			wantError: true,
		},
		{
			name:      "too long",
			filename:  string(make([]byte, 256)),
			wantError: true,
		},
		{
			name:      "null byte",
			filename:  "file\x00name.txt",
			wantError: true,
		},
		{
			name:      "unicode filename",
			filename:  "文档.pdf",
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateFilename(tt.filename)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidateFileSize verifies file size validation
func TestValidateFileSize(t *testing.T) {
	tests := []struct {
		name      string
		size      int64
		wantError bool
	}{
		{
			name:      "valid size 1KB",
			size:      1024,
			wantError: false,
		},
		{
			name:      "valid size 10MB",
			size:      10 * 1024 * 1024,
			wantError: false,
		},
		{
			name:      "valid size 50MB (max)",
			size:      52428800,
			wantError: false,
		},
		{
			name:      "zero size",
			size:      0,
			wantError: true,
		},
		{
			name:      "negative size",
			size:      -1,
			wantError: true,
		},
		{
			name:      "exceeds max",
			size:      52428801,
			wantError: true,
		},
		{
			name:      "way too large",
			size:      100 * 1024 * 1024,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateFileSize(tt.size)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestGetImageDimensions verifies dimension extraction (placeholder)
func TestGetImageDimensions(t *testing.T) {
	// Placeholder test - actual implementation would test real image data
	data := []byte{0x89, 0x50, 0x4E, 0x47} // PNG magic number
	width, height, err := GetImageDimensions(data, "image/png")

	// Current implementation returns 0,0,nil
	require.NoError(t, err)
	assert.Equal(t, 0, width)
	assert.Equal(t, 0, height)
}

// TestGenerateS3Key_Uniqueness verifies keys are unique
func TestGenerateS3Key_Uniqueness(t *testing.T) {
	userID := uuid.New().String()
	noteID := uuid.New().String()
	filename := "test.pdf"

	// Generate multiple keys
	keys := make(map[string]bool)
	for i := 0; i < 100; i++ {
		key := GenerateS3Key(userID, noteID, filename)
		assert.False(t, keys[key], "generated duplicate key")
		keys[key] = true
	}

	assert.Equal(t, 100, len(keys))
}

// BenchmarkSanitizeFilename benchmarks filename sanitization
func BenchmarkSanitizeFilename(b *testing.B) {
	filename := "../../../etc/passwd;rm -rf /;$(command)"
	for i := 0; i < b.N; i++ {
		SanitizeFilename(filename)
	}
}

// BenchmarkParseContentType benchmarks MIME type detection
func BenchmarkParseContentType(b *testing.B) {
	filename := "document.pdf"
	for i := 0; i < b.N; i++ {
		ParseContentType(filename)
	}
}
