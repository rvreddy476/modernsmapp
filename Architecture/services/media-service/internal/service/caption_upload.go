package service

import (
	"errors"
	"fmt"
	"strings"

	"github.com/atpost/media-service/internal/captions"
)

// Caption uploads (POST /v1/subtitles/:mediaId).
//
// `content` is either a plain transcript (format absent or "text": the
// legacy shape, still rendered as one cue spanning the media) or the text of
// a caption FILE (format "vtt" or "srt"). A file is parsed and validated
// here and stored as normalised WebVTT: every timing re-rendered by the
// parser, every cue's text sanitised (captions.SanitizeCueText). The read
// path recognises the stored WEBVTT signature and serves those cues with
// their original timings, sanitising again on the way out.
//
// The media_subtitles.format column only admits vtt|srt, so every row is
// stored as "vtt"; the content itself says whether it is a file.

// NormalizeCaptionUpload validates one upload and returns the format and
// content to store. A refused file is ErrInvalidCaption wrapping a
// *captions.FileError, whose Code is the 400 code the handler answers with.
func NormalizeCaptionUpload(format, content string) (string, string, error) {
	var (
		cues []captions.Cue
		err  error
	)
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "text", "txt", "plain":
		return "vtt", content, nil
	case "vtt", "webvtt":
		cues, err = captions.ParseWebVTT(content)
	case "srt":
		cues, err = captions.ParseSRT(content)
	default:
		err = &captions.FileError{Code: captions.CodeCaptionFormat,
			Reason: "format must be vtt, srt or text"}
	}
	if err != nil {
		return "", "", fmt.Errorf("%w: %w", ErrInvalidCaption, err)
	}
	out := captions.RenderCompactWebVTT(cues)
	if len(out) > maxCaptionContentBytes {
		return "", "", fmt.Errorf("%w: %w", ErrInvalidCaption, &captions.FileError{
			Code:   captions.CodeCaptionTooLarge,
			Reason: fmt.Sprintf("caption file exceeds %d bytes once normalised", maxCaptionContentBytes),
		})
	}
	return "vtt", out, nil
}

// CaptionFileErrorCode is the 400 code for a refused caption file, or ""
// when err is not one.
func CaptionFileErrorCode(err error) string {
	var fe *captions.FileError
	if errors.As(err, &fe) {
		return fe.Code
	}
	return ""
}
