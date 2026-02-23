package linkprev

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/nekomeowww/insights-bot/pkg/opengraph"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreview(t *testing.T) {
	t.Run("GeneralWebsite", func(t *testing.T) {
		assert := assert.New(t)
		require := require.New(t)

		const html = `<!DOCTYPE html>
<html>
  <head>
    <title>Nólëbase | 记录回忆，知识和畅想的地方</title>
    <meta name="description" content="记录回忆，知识和畅想的地方">
    <link rel="icon" href="/logo.svg">
    <meta name="author" content="絢香猫, 絢香音">
    <meta name="keywords" content="markdown, knowledge-base, 知识库, vitepress, obsidian, notebook, notes, nekomeowww, LittleSound">
    <meta property="og:title" content="Nólëbase">
    <meta property="og:image" content="https://nolebase.ayaka.io/og.png">
    <meta property="og:description" content="记录回忆，知识和畅想的地方">
    <meta property="og:site_name" content="Nólëbase">
  </head>
  <body></body>
</html>`

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)

			_, _ = w.Write([]byte(html))
		}))
		defer server.Close()

		meta, err := NewClient().Preview(context.Background(), server.URL)
		require.NoError(err)

		assert.Equal("Nólëbase | 记录回忆，知识和畅想的地方", meta.Title)
		assert.Equal("记录回忆，知识和畅想的地方", meta.Description)
		assert.Equal("/logo.svg", meta.Favicon)
		assert.Equal("絢香猫, 絢香音", meta.Author)
		assert.Equal([]string{
			"markdown, knowledge-base, 知识库, vitepress, obsidian, notebook, notes, nekomeowww, LittleSound",
		}, meta.Keywords)
		assert.Equal("Nólëbase", meta.OpenGraph.Title)
		assert.Equal("https://nolebase.ayaka.io/og.png", meta.OpenGraph.Image)
		assert.Equal("记录回忆，知识和畅想的地方", meta.OpenGraph.Description)
		assert.Equal("Nólëbase", meta.OpenGraph.SiteName)
	})

	t.Run("ErrorOnNon200", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)

			_, _ = w.Write([]byte("not found"))
		}))
		defer server.Close()

		_, err := NewClient().Preview(context.Background(), server.URL)
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrRequestFailed)
	})
}

func TestNewMetaFrom(t *testing.T) {
	html := `<html>
  <head>
    <title>Example Movie</title>
	<meta name="description" content="Example description">
    <link rel="icon" href="/logo.svg" type="image/svg+xml">
	<meta property="og:title" content="Example Movie" />
    <meta property="og:type" content="video.movie" />
    <meta property="og:url" content="https://example.com/movie" />
    <meta property="og:image" content="https://example.com/movie/poster.png" />
	<meta property="og:audio" content="https://example.com/bond/theme.mp3" />
    <meta property="og:description" content="Example description" />
    <meta property="og:determiner" content="the" />
    <meta property="og:locale" content="en_US" />
    <meta property="og:locale:alternate" content="fr_FR" />
    <meta property="og:locale:alternate" content="es_ES" />
    <meta property="og:site_name" content="Movie" />
    <meta property="og:video" content="https://example.com/bond/trailer.swf" />
  </head>
</html>`

	meta := newMetaFrom(lo.Must(goquery.NewDocumentFromReader(strings.NewReader(html))))
	assert.Equal(t, Meta{
		Title:       "Example Movie",
		Description: "Example description",
		Favicon:     "/logo.svg",
		Keywords:    make([]string, 0),
		OpenGraph: opengraph.OpenGraph{
			Title:       "Example Movie",
			Type:        "video.movie",
			Image:       "https://example.com/movie/poster.png",
			URL:         "https://example.com/movie",
			Audio:       "https://example.com/bond/theme.mp3",
			Description: "Example description",
			Determiner:  "the",
			Locale:      "en_US",
			LocaleAlternate: []string{
				"fr_FR",
				"es_ES",
			},
			SiteName: "Movie",
			Video:    "https://example.com/bond/trailer.swf",
		},
	}, meta)
}
