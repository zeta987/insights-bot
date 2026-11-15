package chathistories

import (
	"archive/zip"
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nekomeowww/insights-bot/internal/thirdparty/openai"
)

func makeTestZip(t *testing.T, files map[string]string) []byte {
	t.Helper()

	var buffer bytes.Buffer

	writer := zip.NewWriter(&buffer)

	for name, content := range files {
		file, err := writer.Create(name)
		require.NoError(t, err)

		_, err = file.Write([]byte(content))
		require.NoError(t, err)
	}

	require.NoError(t, writer.Close())

	return buffer.Bytes()
}

func testSpreadsheet(t *testing.T) []byte {
	t.Helper()

	return makeTestZip(t, map[string]string{
		"xl/sharedStrings.xml": `<?xml version="1.0" encoding="UTF-8"?>
<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="2" uniqueCount="2">
  <si><t>店名</t></si><si><r><t>營</t></r><r><t>收</t></r></si>
</sst>`,
		"xl/worksheets/sheet1.xml": `<?xml version="1.0" encoding="UTF-8"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>
  <row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row>
  <row r="2"><c r="A2"><v>120</v></c><c r="B2"><v>3.5</v></c><c r="C2" t="inlineStr"><is><t>內聯</t></is></c></row>
</sheetData></worksheet>`,
	})
}

func TestExtractOfficeTextReadsDocx(t *testing.T) {
	docx := makeTestZip(t, map[string]string{
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>
  <w:p><w:r><w:t>第一段</w:t></w:r><w:r><w:t xml:space="preserve"> 繼續</w:t></w:r></w:p>
  <w:p><w:r><w:t>Second paragraph</w:t></w:r></w:p>
</w:body></w:document>`,
	})

	text, err := extractOfficeText(docx, "notes.docx")

	require.NoError(t, err)
	require.Equal(t, "第一段 繼續\nSecond paragraph", text)
}

func TestExtractOfficeTextReadsSpreadsheetRows(t *testing.T) {
	text, err := extractOfficeText(testSpreadsheet(t), "report.xlsx")

	require.NoError(t, err)
	require.Equal(t, "Sheet 1:\n店名\t營收\n120\t3.5\t內聯", text)
}

func TestExtractOfficeTextOrdersSlidesNumerically(t *testing.T) {
	slide := func(text string) string {
		return `<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><a:p><a:r><a:t>` + text + `</a:t></a:r></a:p></p:sld>`
	}

	pptx := makeTestZip(t, map[string]string{
		"ppt/slides/slide10.xml": slide("十"),
		"ppt/slides/slide2.xml":  slide("二"),
		"ppt/slides/slide1.xml":  slide("一"),
	})

	text, err := extractOfficeText(pptx, "deck.pptx")

	require.NoError(t, err)
	require.Equal(t, "Slide 1:\n一\n\nSlide 2:\n二\n\nSlide 3:\n十", text)
}

func TestExtractOfficeTextRejectsNonZip(t *testing.T) {
	_, err := extractOfficeText([]byte("plain text"), "fake.docx")
	require.Error(t, err)
}

func TestDocumentKindAndHandling(t *testing.T) {
	tables := []struct {
		name     string
		mimeType string
		kind     openai.MediaKind
		handling documentHandling
	}{
		{"report.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", openai.MediaKindDocument, documentAsOfficeText},
		{"paper.PDF", "application/pdf", openai.MediaKindDocument, documentAsFile},
		{"photo.png", "image/png", openai.MediaKindPhoto, documentAsFile},
		{"clip.mp4", "video/mp4", openai.MediaKindVideo, documentAsFile},
		{"song.mp3", "audio/mpeg", openai.MediaKindAudio, documentAsFile},
		{"main.go", "application/octet-stream", openai.MediaKindDocument, documentAsText},
		{"README", "text/plain", openai.MediaKindDocument, documentAsText},
		{"archive.zip", "application/zip", "", documentUnsupported},
		{"photo.heic", "image/heic", "", documentUnsupported},
	}

	for _, table := range tables {
		kind, handling := documentKindAndHandling(table.name, table.mimeType)
		require.Equal(t, table.kind, kind, table.name)
		require.Equal(t, table.handling, handling, table.name)
	}
}

func TestDocumentTextRejectsBinary(t *testing.T) {
	text, err := documentText([]byte("\xef\xbb\xbf  hello  "))
	require.NoError(t, err)
	require.Equal(t, "hello", text)

	_, err = documentText([]byte{0x50, 0x4b, 0x03, 0x04, 0x00, 0xff})
	require.Error(t, err)
}
