package media

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMetadataScanner(t *testing.T) {
	scanner := MetadataScanner{Terms: []string{"forbidden"}}
	cases := []struct {
		name    string
		input   ScanInput
		verdict string
	}{
		{"valid", ScanInput{Title: "A normal title", Description: "A normal description"}, VerdictAllow},
		{"missing title", ScanInput{Title: "   "}, VerdictBlock},
		{"policy term", ScanInput{Title: "A forbidden title"}, VerdictBlock},
		{"links", ScanInput{Title: "Title", Description: "https://a.example https://b.example https://c.example https://d.example"}, VerdictReview},
		{"control characters", ScanInput{Title: "bad\x00title"}, VerdictBlock},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := scanner.Scan(context.Background(), tc.input)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if result.Verdict != tc.verdict {
				t.Fatalf("expected %s, got %s (%+v)", tc.verdict, result.Verdict, result.Findings)
			}
		})
	}
}

func TestHTTPPolicyScanner(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("unexpected authorization header: %q", r.Header.Get("Authorization"))
		}
		var input ScanInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatalf("decode input: %v", err)
		}
		if input.Title != "Title" {
			t.Fatalf("unexpected input: %+v", input)
		}
		_ = json.NewEncoder(w).Encode(ScanResult{
			Verdict:  VerdictBlock,
			Findings: []Finding{{Code: "copyright", Message: "Matched provider", Severity: "critical", Kind: "copyright"}},
		})
	}))
	defer server.Close()
	scanner := HTTPPolicyScanner{URL: server.URL, Token: "token", Timeout: time.Second}
	result, err := scanner.Scan(context.Background(), ScanInput{Title: "Title"})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if result.Verdict != VerdictBlock || result.Scanner != "external_policy" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestHTTPPolicyScannerFailsClosed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	scanner := HTTPPolicyScanner{URL: server.URL, Timeout: time.Second}
	if _, err := scanner.Scan(context.Background(), ScanInput{Title: "Title"}); err == nil {
		t.Fatal("expected scanner error")
	}
}

func TestCompositeScannerUsesStrictestVerdict(t *testing.T) {
	scanner := CompositeScanner{Scanners: []Scanner{
		MetadataScanner{},
		staticScanner{result: ScanResult{Scanner: "static", Verdict: VerdictBlock, Findings: []Finding{{Code: "block", Severity: "critical"}}}},
	}}
	result, err := scanner.Scan(context.Background(), ScanInput{Title: "Title"})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if result.Verdict != VerdictBlock {
		t.Fatalf("expected block, got %s", result.Verdict)
	}
	if !strings.Contains(result.Scanner, "static") {
		t.Fatalf("unexpected scanner name: %q", result.Scanner)
	}
}

type staticScanner struct {
	result ScanResult
}

func (s staticScanner) Scan(context.Context, ScanInput) (ScanResult, error) {
	return s.result, nil
}

func TestSelectRenditions(t *testing.T) {
	renditions := selectRenditions(1920, 1080, 1080)
	if len(renditions) == 0 || renditions[0].Name != "1080p" {
		t.Fatalf("unexpected renditions: %+v", renditions)
	}
	if renditions[0].Width != 1920 {
		t.Fatalf("unexpected width: %d", renditions[0].Width)
	}
	for _, rendition := range renditions {
		if rendition.Height > 1080 {
			t.Fatalf("rendition exceeds max height: %+v", rendition)
		}
	}
	small := selectRenditions(320, 144, 1080)
	if len(small) != 1 || small[0].Height != 144 {
		t.Fatalf("unexpected small renditions: %+v", small)
	}
	if got := evenWidth(1920, 1080, 720); got != 1280 {
		t.Fatalf("unexpected even width: %d", got)
	}
}

func TestWriteMasterPlaylist(t *testing.T) {
	path := t.TempDir() + "/master.m3u8"
	renditions := []Rendition{
		{Name: "720p", Width: 1280, Height: 720, VideoBitrate: 2800, AudioBitrate: 128},
	}
	if err := writeMasterPlaylist(path, renditions); err != nil {
		t.Fatalf("write master: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read master: %v", err)
	}
	if !strings.Contains(string(body), "720p/index.m3u8") || !strings.Contains(string(body), "RESOLUTION=1280x720") {
		t.Fatalf("unexpected master playlist: %s", body)
	}
}
