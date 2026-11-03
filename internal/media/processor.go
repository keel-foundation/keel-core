package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type ProbeResult struct {
	DurationSeconds float64
	Width           int
	Height          int
	VideoCodec      string
	AudioCodec      string
	FormatName      string
	HasAudio        bool
	HasSubtitle     bool
}

type Rendition struct {
	Name         string
	Width        int
	Height       int
	VideoBitrate int
	AudioBitrate int
}

type ProcessedOutput struct {
	DurationSeconds int
	Renditions      []Rendition
	MasterPath      string
	VariantPaths    map[string]string
	MP4Path         string
	ThumbnailPath   string
	CaptionsPath    string
}

type Processor struct {
	FFmpeg             string
	FFprobe            string
	WorkDir            string
	MaxRenditionHeight int
	SegmentSeconds     int
}

type probeOutput struct {
	Format struct {
		Duration   string `json:"duration"`
		FormatName string `json:"format_name"`
	} `json:"format"`
	Streams []struct {
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
	} `json:"streams"`
}

func (p Processor) Probe(ctx context.Context, path string) (ProbeResult, error) {
	output, err := p.runCommand(ctx, p.binary(p.FFprobe, "ffprobe"), "", 1<<20,
		"-v", "error",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		path,
	)
	if err != nil {
		return ProbeResult{}, fmt.Errorf("ffprobe: %w: %s", err, output)
	}
	var parsed probeOutput
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		return ProbeResult{}, fmt.Errorf("parse ffprobe output: %w", err)
	}
	result := ProbeResult{FormatName: parsed.Format.FormatName}
	result.DurationSeconds, _ = strconv.ParseFloat(parsed.Format.Duration, 64)
	for _, stream := range parsed.Streams {
		switch stream.CodecType {
		case "video":
			if result.Width == 0 {
				result.Width = stream.Width
				result.Height = stream.Height
				result.VideoCodec = stream.CodecName
			}
		case "audio":
			if !result.HasAudio {
				result.HasAudio = true
				result.AudioCodec = stream.CodecName
			}
		case "subtitle":
			result.HasSubtitle = true
		}
	}
	if result.VideoCodec == "" || result.Width <= 0 || result.Height <= 0 {
		return ProbeResult{}, errors.New("file has no usable video stream")
	}
	return result, nil
}

func (p Processor) Process(ctx context.Context, sourcePath string, probe ProbeResult) (output ProcessedOutput, err error) {
	workDir := p.WorkDir
	if workDir == "" {
		workDir = os.TempDir()
	}
	outDir, err := os.MkdirTemp(workDir, "keeltube-media-*")
	if err != nil {
		return ProcessedOutput{}, err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(outDir)
		}
	}()
	output = ProcessedOutput{
		DurationSeconds: int(probe.DurationSeconds + 0.5),
		Renditions:      selectRenditions(probe.Width, probe.Height, p.MaxRenditionHeight),
		VariantPaths:    make(map[string]string, 8),
	}
	if output.DurationSeconds < 1 {
		output.DurationSeconds = 1
	}
	for _, rendition := range output.Renditions {
		dir := filepath.Join(outDir, rendition.Name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return ProcessedOutput{}, err
		}
		if err := p.transcodeRendition(ctx, sourcePath, dir, rendition); err != nil {
			return ProcessedOutput{}, err
		}
		output.VariantPaths[rendition.Name] = filepath.Join(dir, "index.m3u8")
	}
	masterPath := filepath.Join(outDir, "master.m3u8")
	if err := writeMasterPlaylist(masterPath, output.Renditions); err != nil {
		return ProcessedOutput{}, err
	}
	output.MasterPath = masterPath
	output.MP4Path = filepath.Join(outDir, "video.mp4")
	if err := p.transcodeMP4(ctx, sourcePath, output.MP4Path, output.Renditions[0]); err != nil {
		return ProcessedOutput{}, err
	}
	output.ThumbnailPath = filepath.Join(outDir, "thumbnail.jpg")
	if err := p.generateThumbnail(ctx, sourcePath, output.ThumbnailPath, probe.DurationSeconds); err != nil {
		return ProcessedOutput{}, err
	}
	if probe.HasSubtitle {
		captionsPath := filepath.Join(outDir, "captions.vtt")
		if err := p.extractCaptions(ctx, sourcePath, captionsPath); err == nil {
			output.CaptionsPath = captionsPath
		}
	}
	return output, nil
}

func (p Processor) transcodeRendition(ctx context.Context, sourcePath, dir string, rendition Rendition) error {
	args := p.encodeArgs(sourcePath, rendition)
	args = append(args,
		"-f", "hls",
		"-hls_time", strconv.Itoa(p.segmentSeconds()),
		"-hls_playlist_type", "vod",
		"-hls_flags", "independent_segments",
		"-hls_segment_filename", "seg_%05d.ts",
		"index.m3u8",
	)
	output, err := p.run(ctx, dir, 64<<10, args...)
	if err != nil {
		return fmt.Errorf("ffmpeg hls %s: %w: %s", rendition.Name, err, output)
	}
	return nil
}

func (p Processor) transcodeMP4(ctx context.Context, sourcePath, outputPath string, rendition Rendition) error {
	args := p.encodeArgs(sourcePath, rendition)
	args = append(args, "-movflags", "+faststart", outputPath)
	output, err := p.run(ctx, "", 64<<10, args...)
	if err != nil {
		return fmt.Errorf("ffmpeg mp4: %w: %s", err, output)
	}
	return nil
}

func (p Processor) encodeArgs(sourcePath string, rendition Rendition) []string {
	return []string{
		"-nostdin",
		"-hide_banner",
		"-loglevel", "error",
		"-y",
		"-i", sourcePath,
		"-map_metadata", "-1",
		"-map", "0:v:0",
		"-map", "0:a?",
		"-vf", fmt.Sprintf("scale=-2:%d", rendition.Height),
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-profile:v", "main",
		"-pix_fmt", "yuv420p",
		"-crf", "21",
		"-maxrate", fmt.Sprintf("%dk", rendition.VideoBitrate),
		"-bufsize", fmt.Sprintf("%dk", rendition.VideoBitrate*2),
		"-g", "48",
		"-keyint_min", "48",
		"-sc_threshold", "0",
		"-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", p.segmentSeconds()),
		"-c:a", "aac",
		"-b:a", fmt.Sprintf("%dk", rendition.AudioBitrate),
		"-ac", "2",
		"-ar", "48000",
	}
}

func (p Processor) generateThumbnail(ctx context.Context, sourcePath, outputPath string, duration float64) error {
	seek := 1.0
	if duration > 0 && duration < 2 {
		seek = duration / 2
	}
	output, err := p.run(ctx, "", 16<<10,
		"-nostdin",
		"-hide_banner",
		"-loglevel", "error",
		"-y",
		"-ss", strconv.FormatFloat(seek, 'f', 3, 64),
		"-i", sourcePath,
		"-map_metadata", "-1",
		"-frames:v", "1",
		"-vf", "scale=1280:-2",
		"-q:v", "2",
		outputPath,
	)
	if err != nil {
		return fmt.Errorf("ffmpeg thumbnail: %w: %s", err, output)
	}
	return nil
}

func (p Processor) extractCaptions(ctx context.Context, sourcePath, outputPath string) error {
	_, err := p.run(ctx, "", 16<<10,
		"-nostdin",
		"-hide_banner",
		"-loglevel", "error",
		"-y",
		"-map_metadata", "-1",
		"-i", sourcePath,
		"-map", "0:s:0",
		"-c:s", "webvtt",
		outputPath,
	)
	return err
}

func (p Processor) run(ctx context.Context, dir string, limit int, args ...string) (string, error) {
	return p.runCommand(ctx, p.binary(p.FFmpeg, "ffmpeg"), dir, limit, args...)
}

func (p Processor) runCommand(ctx context.Context, binary, dir string, limit int, args ...string) (string, error) {
	command := exec.CommandContext(ctx, binary, args...)
	if dir != "" {
		command.Dir = dir
	}
	var output limitedBuffer
	output.limit = limit
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	return output.String(), err
}

func (p Processor) binary(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func (p Processor) segmentSeconds() int {
	if p.SegmentSeconds < 2 {
		return 6
	}
	return p.SegmentSeconds
}

func selectRenditions(sourceWidth, sourceHeight, maxHeight int) []Rendition {
	standards := []Rendition{
		{Name: "2160p", Height: 2160, VideoBitrate: 12000, AudioBitrate: 192},
		{Name: "1440p", Height: 1440, VideoBitrate: 8000, AudioBitrate: 192},
		{Name: "1080p", Height: 1080, VideoBitrate: 5000, AudioBitrate: 160},
		{Name: "720p", Height: 720, VideoBitrate: 2800, AudioBitrate: 128},
		{Name: "480p", Height: 480, VideoBitrate: 1400, AudioBitrate: 128},
		{Name: "360p", Height: 360, VideoBitrate: 800, AudioBitrate: 96},
		{Name: "270p", Height: 270, VideoBitrate: 450, AudioBitrate: 96},
	}
	if maxHeight <= 0 {
		maxHeight = 1080
	}
	renditions := make([]Rendition, 0, len(standards))
	for _, rendition := range standards {
		if rendition.Height > sourceHeight || rendition.Height > maxHeight {
			continue
		}
		rendition.Width = evenWidth(sourceWidth, sourceHeight, rendition.Height)
		renditions = append(renditions, rendition)
	}
	if len(renditions) == 0 {
		height := sourceHeight
		if height%2 != 0 {
			height--
		}
		if height < 2 {
			height = 2
		}
		width := sourceWidth
		if width%2 != 0 {
			width++
		}
		if width < 2 {
			width = 2
		}
		renditions = append(renditions, Rendition{
			Name:         fmt.Sprintf("%dp", height),
			Width:        width,
			Height:       height,
			VideoBitrate: 300,
			AudioBitrate: 96,
		})
	}
	return renditions
}

func evenWidth(sourceWidth, sourceHeight, targetHeight int) int {
	if sourceHeight <= 0 || sourceWidth <= 0 {
		return 0
	}
	width := int(float64(sourceWidth)*float64(targetHeight)/float64(sourceHeight) + 0.5)
	if width%2 != 0 {
		width++
	}
	if width < 2 {
		width = 2
	}
	return width
}

func avcCodec(height int) string {
	switch {
	case height <= 480:
		return "avc1.4d401e"
	case height <= 720:
		return "avc1.4d401f"
	case height <= 1080:
		return "avc1.4d4028"
	case height <= 1440:
		return "avc1.4d402a"
	default:
		return "avc1.4d4033"
	}
}

func writeMasterPlaylist(path string, renditions []Rendition) error {
	var builder strings.Builder
	builder.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-INDEPENDENT-SEGMENTS\n")
	for _, rendition := range renditions {
		bandwidth := (rendition.VideoBitrate + rendition.AudioBitrate) * 1000
		average := bandwidth * 9 / 10
		fmt.Fprintf(&builder,
			"#EXT-X-STREAM-INF:BANDWIDTH=%d,AVERAGE-BANDWIDTH=%d,RESOLUTION=%dx%d,CODECS=\"%s,mp4a.40.2\"\n%s/index.m3u8\n",
			bandwidth, average, rendition.Width, rendition.Height, avcCodec(rendition.Height), rendition.Name,
		)
	}
	return os.WriteFile(path, []byte(builder.String()), 0o600)
}

type limitedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		b.limit = 64 << 10
	}
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		if len(p) < remaining {
			remaining = len(p)
		}
		_, _ = b.buffer.Write(p[:remaining])
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	return b.buffer.String()
}
