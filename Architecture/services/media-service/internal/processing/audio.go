package processing

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// AudioMeta holds extracted audio metadata.
type AudioMeta struct {
	DurationMs int
	SampleRate int
	Channels   int
	Codec      string
}

// ExtractAudio extracts the audio stream from a video file as AAC in M4A container.
func ExtractAudio(ctx context.Context, inputPath, outputDir string) (outputPath string, meta *AudioMeta, err error) {
	outputPath = filepath.Join(outputDir, "audio.m4a")

	args := []string{
		"-y", "-i", inputPath,
		"-vn",            // no video
		"-c:a", "aac",    // AAC codec
		"-b:a", "128k",   // 128 kbps
		"-movflags", "+faststart",
		outputPath,
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	out, cmdErr := cmd.CombinedOutput()
	if cmdErr != nil {
		return "", nil, fmt.Errorf("ffmpeg audio extraction failed: %w\n%s", cmdErr, out)
	}

	meta, err = ProbeAudio(ctx, outputPath)
	if err != nil {
		return outputPath, nil, nil // return path even if probe fails
	}

	return outputPath, meta, nil
}

// ProbeAudio extracts audio metadata using ffprobe.
func ProbeAudio(ctx context.Context, inputPath string) (*AudioMeta, error) {
	args := []string{
		"-v", "error",
		"-select_streams", "a:0",
		"-show_entries", "stream=duration,sample_rate,channels,codec_name",
		"-of", "default=noprint_wrappers=1",
		inputPath,
	}

	out, err := exec.CommandContext(ctx, "ffprobe", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe audio: %w", err)
	}

	meta := &AudioMeta{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key, val := parts[0], parts[1]
		switch key {
		case "duration":
			dur, _ := strconv.ParseFloat(val, 64)
			meta.DurationMs = int(dur * 1000)
		case "sample_rate":
			meta.SampleRate, _ = strconv.Atoi(val)
		case "channels":
			meta.Channels, _ = strconv.Atoi(val)
		case "codec_name":
			meta.Codec = val
		}
	}
	return meta, nil
}

// GenerateWaveform generates a waveform data file (JSON array of peaks) from an audio file.
// Uses ffmpeg to extract PCM samples and computes peaks.
func GenerateWaveform(ctx context.Context, inputPath, outputDir string, numBins int) (outputPath string, err error) {
	if numBins <= 0 {
		numBins = 200
	}

	// Extract raw PCM samples
	pcmPath := filepath.Join(outputDir, "waveform.pcm")
	args := []string{
		"-y", "-i", inputPath,
		"-ac", "1",           // mono
		"-ar", "8000",        // 8kHz
		"-f", "s16le",        // 16-bit signed PCM
		"-acodec", "pcm_s16le",
		pcmPath,
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	if out, cmdErr := cmd.CombinedOutput(); cmdErr != nil {
		return "", fmt.Errorf("ffmpeg waveform extraction failed: %w\n%s", cmdErr, out)
	}

	// Read PCM and compute peaks
	data, err := os.ReadFile(pcmPath)
	if err != nil {
		return "", fmt.Errorf("read PCM: %w", err)
	}
	defer os.Remove(pcmPath)

	numSamples := len(data) / 2 // 16-bit = 2 bytes per sample
	if numSamples == 0 {
		return "", fmt.Errorf("no audio samples found")
	}

	samplesPerBin := numSamples / numBins
	if samplesPerBin < 1 {
		samplesPerBin = 1
	}

	peaks := make([]float64, numBins)
	for i := 0; i < numBins; i++ {
		start := i * samplesPerBin
		end := start + samplesPerBin
		if end > numSamples {
			end = numSamples
		}
		var maxAbs float64
		for j := start; j < end; j++ {
			idx := j * 2
			if idx+1 >= len(data) {
				break
			}
			sample := int16(data[idx]) | int16(data[idx+1])<<8
			abs := float64(sample)
			if abs < 0 {
				abs = -abs
			}
			if abs > maxAbs {
				maxAbs = abs
			}
		}
		peaks[i] = maxAbs / 32768.0
	}

	// Write JSON array
	outputPath = filepath.Join(outputDir, "waveform.json")
	var sb strings.Builder
	sb.WriteString("[")
	for i, p := range peaks {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(fmt.Sprintf("%.4f", p))
	}
	sb.WriteString("]")

	if err := os.WriteFile(outputPath, []byte(sb.String()), 0644); err != nil {
		return "", fmt.Errorf("write waveform: %w", err)
	}

	return outputPath, nil
}

// ExtractFrames extracts multiple frames from a video at equal intervals.
// Returns paths to the extracted JPEG files.
func ExtractFrames(ctx context.Context, inputPath, outputDir string, numFrames int) ([]string, error) {
	if numFrames <= 0 {
		numFrames = 5
	}

	// Get video duration
	meta, err := ProbeVideo(ctx, inputPath)
	if err != nil {
		return nil, fmt.Errorf("probe video for frames: %w", err)
	}

	durationSec := float64(meta.DurationMs) / 1000.0
	if durationSec < 1 {
		durationSec = 1
	}

	interval := durationSec / float64(numFrames+1)
	var paths []string

	for i := 1; i <= numFrames; i++ {
		timestamp := interval * float64(i)
		outPath := filepath.Join(outputDir, fmt.Sprintf("frame_%02d.jpg", i))

		args := []string{
			"-y", "-ss", fmt.Sprintf("%.2f", timestamp),
			"-i", inputPath,
			"-vframes", "1",
			"-q:v", "3",
			outPath,
		}

		cmd := exec.CommandContext(ctx, "ffmpeg", args...)
		if out, cmdErr := cmd.CombinedOutput(); cmdErr != nil {
			return paths, fmt.Errorf("extract frame %d: %w\n%s", i, cmdErr, out)
		}
		paths = append(paths, outPath)
	}

	return paths, nil
}

// GeneratePreviewGIF creates an animated GIF preview from a video.
func GeneratePreviewGIF(ctx context.Context, inputPath, outputDir string, durationSec int, height int) (string, error) {
	if durationSec <= 0 {
		durationSec = 3
	}
	if height <= 0 {
		height = 240
	}

	outputPath := filepath.Join(outputDir, "preview.gif")
	args := []string{
		"-y", "-i", inputPath,
		"-t", fmt.Sprintf("%d", durationSec),
		"-vf", fmt.Sprintf("fps=10,scale=-1:%d:flags=lanczos", height),
		"-loop", "0",
		outputPath,
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	if out, cmdErr := cmd.CombinedOutput(); cmdErr != nil {
		return "", fmt.Errorf("ffmpeg preview gif: %w\n%s", cmdErr, out)
	}

	return outputPath, nil
}

// ValidateVideoMagicBytes checks the first bytes of a file to verify it's a real video.
func ValidateVideoMagicBytes(data []byte) (string, bool) {
	if len(data) < 12 {
		return "", false
	}

	// MP4/MOV: ftyp box
	if string(data[4:8]) == "ftyp" {
		return "video/mp4", true
	}

	// WebM: EBML header
	if data[0] == 0x1A && data[1] == 0x45 && data[2] == 0xDF && data[3] == 0xA3 {
		return "video/webm", true
	}

	// AVI: RIFF....AVI
	if string(data[0:4]) == "RIFF" && string(data[8:12]) == "AVI " {
		return "video/avi", true
	}

	return "", false
}

// ValidateImageMagicBytes checks the first bytes to verify it's a real image.
func ValidateImageMagicBytes(data []byte) (string, bool) {
	if len(data) < 4 {
		return "", false
	}

	// JPEG
	if data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "image/jpeg", true
	}

	// PNG
	if data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47 {
		return "image/png", true
	}

	// GIF
	if string(data[0:3]) == "GIF" {
		return "image/gif", true
	}

	// WebP: RIFF....WEBP
	if len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "image/webp", true
	}

	return "", false
}
