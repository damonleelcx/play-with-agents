package agent

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ledongthuc/pdf"

	"github.com/damonleelcx/play-with-agents/internal/engine"
	"github.com/damonleelcx/play-with-agents/internal/llm"
)

// ExtractText turns an upload into text the agent can read and search.
// Images (a photographed lab report, a letter) are transcribed by the vision
// model — accounted against the user like every other model call.
func (a *Agent) ExtractText(ctx context.Context, userID, filename string, data []byte) (string, error) {
	ext := strings.ToLower(filepath.Ext(filename))
	ct := http.DetectContentType(data)
	switch {
	case ext == ".pdf" || ct == "application/pdf":
		return pdfText(data)
	case ext == ".docx":
		return docxText(data)
	case strings.HasPrefix(ct, "image/"):
		return a.imageText(ctx, userID, ct, data)
	case strings.HasPrefix(ct, "text/") || ext == ".txt" || ext == ".md" || ext == ".csv" || ext == ".eml":
		return string(data), nil
	}
	return "", fmt.Errorf("unsupported file type %s — upload PDF, DOCX, text, or a photo", ct)
}

func pdfText(data []byte) (string, error) {
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("could not read PDF: %w", err)
	}
	var b strings.Builder
	for i := 1; i <= r.NumPage(); i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		t, err := p.GetPlainText(nil)
		if err == nil {
			b.WriteString(t)
			b.WriteString("\n\n")
		}
	}
	s := strings.TrimSpace(b.String())
	if s == "" {
		return "", fmt.Errorf("this PDF has no text layer (it is probably a scan) — upload a photo of the pages instead")
	}
	return s, nil
}

var reXMLTag = regexp.MustCompile(`<[^>]+>`)

func docxText(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("could not read DOCX: %w", err)
	}
	for _, f := range zr.File {
		if f.Name != "word/document.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		defer rc.Close()
		raw, err := io.ReadAll(io.LimitReader(rc, 20<<20))
		if err != nil {
			return "", err
		}
		s := strings.NewReplacer("</w:p>", "\n", "<w:tab/>", "\t", "<w:br/>", "\n").Replace(string(raw))
		s = reXMLTag.ReplaceAllString(s, "")
		s = strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&apos;", "'").Replace(s)
		return strings.TrimSpace(s), nil
	}
	return "", fmt.Errorf("DOCX has no document body")
}

func (a *Agent) imageText(ctx context.Context, userID, ct string, data []byte) (string, error) {
	url := "data:" + ct + ";base64," + base64.StdEncoding.EncodeToString(data)
	// The vision request needs the multi-part content form, which the plain
	// Message type does not carry; build it through a raw request.
	resp, err := a.Model.Chat(ctx, engine.CallMeta{Purpose: "vision", UserID: userID}, llm.Request{
		Model: a.LLM, Temperature: 0,
		Messages: []llm.Message{{Role: "user", Content: "\x00image:" + url + "\x00Transcribe all text in this image exactly, preserving tables as Markdown. " +
			"If it is a medical or legal document, keep every number, unit, date and reference range. Output only the transcription."}},
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Message.Content), nil
}
