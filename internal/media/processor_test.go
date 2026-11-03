package media

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestProcessorGeneratesHLSAndMP4(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe is not installed")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "source.mp4")
	generate := exec.Command(ffmpeg,
		"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=10",
		"-t", "1", "-pix_fmt", "yuv420p", "-c:v", "libx264", source,
	)
	if output, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("generate source: %v: %s", err, output)
	}
	processor := Processor{
		FFmpeg:             ffmpeg,
		FFprobe:            "ffprobe",
		WorkDir:            dir,
		MaxRenditionHeight: 270,
		SegmentSeconds:     2,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	probe, err := processor.Probe(ctx, source)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if probe.Width != 320 || probe.Height != 240 || probe.DurationSeconds <= 0 {
		t.Fatalf("unexpected probe: %+v", probe)
	}
	output, err := processor.Process(ctx, source, probe)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	defer os.RemoveAll(filepath.Dir(output.MasterPath))
	for _, path := range []string{output.MasterPath, output.MP4Path, output.ThumbnailPath} {
		info, err := os.Stat(path)
		if err != nil || info.Size() == 0 {
			t.Fatalf("missing output %s: %v", path, err)
		}
	}
	if len(output.Renditions) != 1 || output.Renditions[0].Height != 240 {
		t.Fatalf("unexpected renditions: %+v", output.Renditions)
	}
	if output.VariantPaths[output.Renditions[0].Name] == "" {
		t.Fatal("missing variant playlist")
	}
}
