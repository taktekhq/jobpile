package watch

import (
	"bytes"
	"io"
	"strings"

	imap "github.com/emersion/go-imap/v2"
	message "github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset" // registers common non-UTF-8 charset decoders

	"github.com/taktekhq/jobpile/internal/textfetch"
)

// FindTextPart walks a message's BODYSTRUCTURE and returns the
// section path and part info of the best text part to preview: the
// first text/plain part if there is one, otherwise the first
// text/html part. ok is false when the message has no text part at
// all (for example a bounce that is only an attachment).
func FindTextPart(bs imap.BodyStructure) (path []int, part *imap.BodyStructureSinglePart, ok bool) {
	if bs == nil {
		return nil, nil, false
	}
	var plainPath, htmlPath []int
	var plainPart, htmlPart *imap.BodyStructureSinglePart
	bs.Walk(func(p []int, candidate imap.BodyStructure) bool {
		sp, isSingle := candidate.(*imap.BodyStructureSinglePart)
		if !isSingle || !strings.EqualFold(sp.Type, "text") {
			return true
		}
		switch strings.ToLower(sp.Subtype) {
		case "plain":
			if plainPart == nil {
				plainPath = append([]int{}, p...)
				plainPart = sp
			}
		case "html":
			if htmlPart == nil {
				htmlPath = append([]int{}, p...)
				htmlPart = sp
			}
		}
		return true
	})
	if plainPart != nil {
		return plainPath, plainPart, true
	}
	if htmlPart != nil {
		return htmlPath, htmlPart, true
	}
	return nil, nil, false
}

// DecodePart decodes raw bytes fetched for one BODY[] section, using
// the Content-Type and Content-Transfer-Encoding BODYSTRUCTURE
// already reported for that part (sp, as found by FindTextPart).
// HTML is stripped to plain text. The result is not yet collapsed or
// truncated; call CollapsePreview for that.
func DecodePart(sp *imap.BodyStructureSinglePart, raw []byte) string {
	if sp == nil {
		return string(raw)
	}

	var header message.Header
	mediaType := strings.ToLower(sp.Type) + "/" + strings.ToLower(sp.Subtype)
	header.SetContentType(mediaType, sp.Params)
	if sp.Encoding != "" {
		header.SetText("Content-Transfer-Encoding", sp.Encoding)
	}

	entity, err := message.New(header, bytes.NewReader(raw))
	if entity == nil {
		if err != nil {
			// Unknown transfer encoding with no entity to fall back
			// on: better a raw, possibly still-encoded preview than
			// none at all.
			return string(raw)
		}
		return ""
	}

	body, _ := io.ReadAll(entity.Body)
	text := string(body)
	if strings.EqualFold(sp.Subtype, "html") {
		text = textfetch.StripHTML(text)
	}
	return text
}
