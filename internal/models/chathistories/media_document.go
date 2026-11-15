package chathistories

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/nekomeowww/insights-bot/internal/thirdparty/openai"
)

// documentHandling says how a shared file reaches the media model.
type documentHandling int

const (
	documentUnsupported documentHandling = iota
	// documentAsFile sends the file itself: PDF, images, audio and video.
	documentAsFile
	// documentAsText sends the file content as text.
	documentAsText
	// documentAsOfficeText extracts the text of an Office Open XML file.
	documentAsOfficeText
)

const (
	officePartLimit = 8 << 20
	// officeTotalLimit bounds what one file may decompress across all parts.
	officeTotalLimit     = 24 << 20
	officeTextRuneLimit  = 12000
	spreadsheetRowLimit  = 80
	spreadsheetSheetMax  = 3
	presentationSlideMax = 40
)

var textDocumentExtensions = map[string]bool{
	".txt": true, ".md": true, ".markdown": true, ".csv": true, ".tsv": true, ".json": true,
	".xml": true, ".yaml": true, ".yml": true, ".toml": true, ".ini": true, ".log": true,
	".html": true, ".htm": true, ".css": true, ".js": true, ".ts": true, ".tsx": true, ".jsx": true,
	".go": true, ".py": true, ".java": true, ".kt": true, ".c": true, ".h": true, ".cpp": true,
	".cs": true, ".rs": true, ".rb": true, ".php": true, ".swift": true, ".sh": true, ".ps1": true,
	".sql": true, ".lua": true, ".srt": true, ".vtt": true,
}

var officeDocumentExtensions = map[string]bool{".docx": true, ".xlsx": true, ".pptx": true}

// documentKindAndHandling decides how a shared file is described from its
// name and declared MIME type.
func documentKindAndHandling(fileName string, mimeType string) (openai.MediaKind, documentHandling) {
	extension := strings.ToLower(path.Ext(fileName))
	mimeType = strings.ToLower(mimeType)

	switch {
	case officeDocumentExtensions[extension]:
		return openai.MediaKindDocument, documentAsOfficeText
	case mimeType == "application/pdf" || extension == ".pdf":
		return openai.MediaKindDocument, documentAsFile
	case strings.HasPrefix(mimeType, "image/") && mediaMimeTypes[extension] != "":
		return openai.MediaKindPhoto, documentAsFile
	case strings.HasPrefix(mimeType, "video/") && mediaMimeTypes[extension] != "":
		return openai.MediaKindVideo, documentAsFile
	case strings.HasPrefix(mimeType, "audio/") && mediaMimeTypes[extension] != "":
		return openai.MediaKindAudio, documentAsFile
	case textDocumentExtensions[extension] || strings.HasPrefix(mimeType, "text/"):
		return openai.MediaKindDocument, documentAsText
	default:
		return "", documentUnsupported
	}
}

// documentText returns the text content of a text file, rejecting binary data.
func documentText(data []byte) (string, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return "", errors.New("file is not UTF-8 text")
	}

	text := strings.TrimSpace(string(data))
	if text == "" {
		return "", errors.New("file is empty")
	}

	return text, nil
}

// readZipPart decompresses one part, charging it to budget so a zip bomb
// spread over many parts still stops at officeTotalLimit.
func readZipPart(file *zip.File, budget *int64) ([]byte, error) {
	limit := min(int64(officePartLimit), *budget)
	if limit <= 0 {
		return nil, errors.New("office file is too large")
	}

	reader, err := file.Open()
	if err != nil {
		return nil, err
	}

	defer func() { _ = reader.Close() }()

	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s is too large", file.Name)
	}

	*budget -= int64(len(data))

	return data, nil
}

// officeTextFull reports when enough text has been collected; text past the
// rune limit is dropped anyway.
func officeTextFull(builder *strings.Builder) bool {
	return builder.Len() >= officeTextRuneLimit*utf8.UTFMax
}

// truncateRunePrefix keeps the first limit runes without converting the whole
// string.
func truncateRunePrefix(text string, limit int) string {
	count := 0

	for index := range text {
		if count == limit {
			return text[:index]
		}

		count++
	}

	return text
}

// xmlText collects the character data of elements named textElement. A
// paragraph-like element named breakElement ends a line.
func xmlText(data []byte, textElement string, breakElement string) string {
	decoder := xml.NewDecoder(bytes.NewReader(data))

	var builder strings.Builder

	inText := false

	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}

		switch t := token.(type) {
		case xml.StartElement:
			inText = t.Name.Local == textElement
		case xml.EndElement:
			if t.Name.Local == textElement {
				inText = false
			}
			if t.Name.Local == breakElement {
				builder.WriteString("\n")
			}
		case xml.CharData:
			if inText {
				builder.Write(t)
			}
		}
	}

	return builder.String()
}

var officeNumberPattern = regexp.MustCompile(`(\d+)\.xml$`)

func officePartNumber(name string) int {
	match := officeNumberPattern.FindStringSubmatch(name)
	if match == nil {
		return 0
	}

	number, _ := strconv.Atoi(match[1])

	return number
}

// numberedParts returns the zip entries under prefix in numeric order, such as
// slide1.xml, slide2.xml, …, slide10.xml.
func numberedParts(files []*zip.File, prefix string) []*zip.File {
	parts := make([]*zip.File, 0)

	for _, file := range files {
		if strings.HasPrefix(file.Name, prefix) && strings.HasSuffix(file.Name, ".xml") {
			parts = append(parts, file)
		}
	}

	sort.Slice(parts, func(i, j int) bool { return officePartNumber(parts[i].Name) < officePartNumber(parts[j].Name) })

	return parts
}

func findZipPart(files []*zip.File, name string) *zip.File {
	for _, file := range files {
		if file.Name == name {
			return file
		}
	}

	return nil
}

// spreadsheetSharedStrings reads xl/sharedStrings.xml, where cells of type "s"
// keep their text.
func spreadsheetSharedStrings(files []*zip.File, budget *int64) []string {
	part := findZipPart(files, "xl/sharedStrings.xml")
	if part == nil {
		return nil
	}

	data, err := readZipPart(part, budget)
	if err != nil {
		return nil
	}

	decoder := xml.NewDecoder(bytes.NewReader(data))
	strs := make([]string, 0)

	var current strings.Builder

	inItem, inText := false, false

	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}

		switch t := token.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "si":
				inItem = true

				current.Reset()
			case "t":
				inText = inItem
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "si":
				inItem = false
				strs = append(strs, current.String())
			case "t":
				inText = false
			}
		case xml.CharData:
			if inText {
				current.Write(t)
			}
		}
	}

	return strs
}

// spreadsheetSheetText renders the first rows of one worksheet as
// tab-separated lines.
func spreadsheetSheetText(data []byte, sharedStrings []string) string {
	decoder := xml.NewDecoder(bytes.NewReader(data))

	var builder strings.Builder

	row := make([]string, 0)
	rows := 0
	cellType := ""
	inValue := false

	var value strings.Builder

	for rows < spreadsheetRowLimit {
		token, err := decoder.Token()
		if err != nil {
			break
		}

		switch t := token.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "row":
				row = row[:0]
			case "c":
				cellType = ""

				for _, attr := range t.Attr {
					if attr.Name.Local == "t" {
						cellType = attr.Value
					}
				}

				value.Reset()
			case "v", "t":
				inValue = true
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "v", "t":
				inValue = false
			case "c":
				cell := value.String()
				if cellType == "s" {
					if index, err := strconv.Atoi(cell); err == nil && index >= 0 && index < len(sharedStrings) {
						cell = sharedStrings[index]
					}
				}

				row = append(row, strings.TrimSpace(cell))
			case "row":
				if line := strings.TrimRight(strings.Join(row, "\t"), "\t"); line != "" {
					builder.WriteString(line)
					builder.WriteString("\n")

					rows++
				}
			}
		case xml.CharData:
			if inValue {
				value.Write(t)
			}
		}
	}

	return builder.String()
}

// extractOfficeText pulls readable text out of a .docx, .xlsx or .pptx file.
func extractOfficeText(data []byte, fileName string) (string, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("open office file: %w", err)
	}

	var builder strings.Builder

	budget := int64(officeTotalLimit)

	switch strings.ToLower(path.Ext(fileName)) {
	case ".docx":
		part := findZipPart(archive.File, "word/document.xml")
		if part == nil {
			return "", errors.New("word/document.xml is missing")
		}

		content, err := readZipPart(part, &budget)
		if err != nil {
			return "", err
		}

		builder.WriteString(xmlText(content, "t", "p"))
	case ".pptx":
		for i, part := range numberedParts(archive.File, "ppt/slides/slide") {
			if i >= presentationSlideMax || officeTextFull(&builder) {
				break
			}

			content, err := readZipPart(part, &budget)
			if err != nil {
				break
			}

			fmt.Fprintf(&builder, "Slide %d:\n%s\n", i+1, xmlText(content, "t", "p"))
		}
	case ".xlsx":
		sharedStrings := spreadsheetSharedStrings(archive.File, &budget)

		for i, part := range numberedParts(archive.File, "xl/worksheets/sheet") {
			if i >= spreadsheetSheetMax || officeTextFull(&builder) {
				break
			}

			content, err := readZipPart(part, &budget)
			if err != nil {
				break
			}

			fmt.Fprintf(&builder, "Sheet %d:\n%s\n", i+1, spreadsheetSheetText(content, sharedStrings))
		}
	default:
		return "", fmt.Errorf("unsupported office file %q", fileName)
	}

	text := strings.TrimSpace(truncateRunePrefix(builder.String(), officeTextRuneLimit))
	if text == "" {
		return "", errors.New("office file has no text")
	}

	return text, nil
}
