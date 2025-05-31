package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

type VideoEntry struct {
	Href       string `json:"href"`
	Section    string `json:"section"`
	Title      string `json:"title"`
	Index      int    `json:"index"`
	Duration   string `json:"duration"`
	Transcript string `json:"transcript,omitempty"`
	filename   string
}

var invalidRE = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func sanitizeFileName(s string) string {
	s = strings.ReplaceAll(s, "| LinkedIn Learning", "")
	s = strings.TrimSpace(s)
	return invalidRE.ReplaceAllString(s, "_")
}

const videoParseJS = `(() => {
	const sections = Array.from(document.querySelectorAll("section.classroom-toc-section"));
	const results = [];
	for (const section of sections) {
		const sectionName = section.querySelector(".classroom-toc-section__toggle-title")?.innerText.trim();
			const videos = section.querySelectorAll("li.classroom-toc-item");
		let index = 0;
		for (const video of videos) {
			const link = video.querySelector("a.classroom-toc-item__link");
			const spans = Array.from(video.querySelectorAll("span"));
			const title = Array.from(video.querySelector('.classroom-toc-item__title').childNodes)
				.find(n => n.nodeType === Node.TEXT_NODE && n.textContent.trim())
 				.textContent.trim();
			const duration = spans.map(el => el.innerText.trim())
				.find(text => text.toLowerCase().endsWith("video")) || "";
			if (!link) continue;
			index++;
			results.push({
				href: link.href,
				section: sectionName,
				title: title,
				index: index,
				duration: duration.split(' ').slice(0, -1).join('')
			});
		}
	}
	return results;
})()`

func main() {
	ssoURL := flag.String("sso", "", "URL to the enterprise SSO sign-on.")
	courseURL := flag.String("course", "", "URL of the the course to download.")
	dlTranscripts := flag.Bool("transcripts", false, "Whether or not to download transcripts.")
	saveJSON := flag.Bool("json", false, "Whether or not to output the transcript as JSON.")
	dlVideos := flag.Bool("videos", false, "Whether or not to download videos.")
	timeout := flag.Duration("timeout", time.Hour, "Timeout for the entire operation.")
	backoff := flag.Duration("backoff", time.Minute, "How often to wait between backoff retries.")
	flag.Parse()

	if !*dlVideos && !*dlTranscripts {
		log.Fatal("❌ You must specify at least one of -transcripts or -videos to download.")
	}

	ctx, cancel := newChromeDPCtx(*timeout)
	defer cancel()

	if err := ssoLogin(ctx, *ssoURL); err != nil {
		log.Fatal(err)
	}
	log.Println("✅ Logged in.")

	videos, err := parseCourseVideos(ctx, *courseURL)
	if err != nil {
		log.Fatalf("❌ Failed to extract video links: %v", err)
	}
	log.Printf("🎯 Found %d video(s) across %d sections\n", len(videos), countSections(videos))

	for i, video := range videos {
		log.Printf("▶️ [%d/%d] %v: %s \n", i+1, len(videos), video.Section, video.Title)
		if err := visitVideo(ctx, video.Href, *backoff, 0); err != nil {
			log.Printf("🙅 failed to visit video: %v", err)
			continue
		}
		if *dlTranscripts {
			if err := downloadTranscript(ctx, video, *saveJSON); err != nil {
				log.Printf("%v -> skipping.", err)
				continue
			}
		}
		if *dlVideos {
			if err := downloadVideo(ctx, video); err != nil {
				log.Printf("%v -> skipping.", err)
				continue
			}
		}
	}

	log.Println("✅ All courses info saved.")
}

func downloadTranscript(ctx context.Context, video VideoEntry, saveJSON bool) error {
	var lines []string
	if err := chromedp.Run(ctx,
		chromedp.ScrollIntoView(`button[id*="TRANSCRIPT"]`, chromedp.ByQuery),
		chromedp.Click(`button[id*="TRANSCRIPT"]`, chromedp.ByQuery),
		chromedp.Sleep(2*time.Second),
		chromedp.WaitVisible(`.content-transcript-line`, chromedp.ByQuery),
		chromedp.Evaluate(`Array.from(document.querySelectorAll('.content-transcript-line')).map(x => x.textContent.trim())`, &lines),
	); err != nil {
		return fmt.Errorf("⚠️ failed to scrape: %v", err)
	}
	video.Transcript = strings.Join(lines, "\n")

	ext := "txt"
	if saveJSON {
		ext = "json"
	}
	filename := video.filename + "." + ext
	f, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("❌ failed to create file %s: %w", filename, err)
	}
	defer func() {
		_ = f.Close()
	}()

	if saveJSON {
		if err := json.NewEncoder(f).Encode(video); err != nil {
			return fmt.Errorf("❌ failed to write JSON: %w", err)
		}
		log.Printf("💾 transcript saved: %s\n", filename)
		return nil
	}

	var sb strings.Builder
	sb.WriteString("URL: " + video.Href + "\n")
	sb.WriteString("Section: " + video.Section + "\n")
	sb.WriteString("Title: " + video.Title + "\n")
	sb.WriteString("Index: " + strconv.Itoa(video.Index) + "\n")
	sb.WriteString("Duration: " + video.Duration + "\n")
	sb.WriteString("Transcript:\n" + video.Transcript + "\n")
	if _, err := f.WriteString(sb.String()); err != nil {
		return fmt.Errorf("❌ failed to write transcript: %w", err)
	}
	log.Printf("💾 transcript saved: %s\n", filename)

	return nil
}

func downloadVideo(ctx context.Context, video VideoEntry) error {
	var videoURL string
	if err := chromedp.Run(ctx,
		chromedp.WaitVisible(`video.vjs-tech`, chromedp.ByQuery),
		chromedp.AttributeValue(`video.vjs-tech`, "src", &videoURL, nil),
	); err != nil {
		return fmt.Errorf("⚠️ failed to find video: %v", err)
	}
	if videoURL == "" {
		return fmt.Errorf("⚠️ empty video URL found")
	}

	filename := video.filename + ".mp4"
	f, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("❌ failed to create file %s: %w", filename, err)
	}
	defer func() {
		_ = f.Close()
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, videoURL, http.NoBody)
	if err != nil {
		return fmt.Errorf("❌ failed to create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("❌ failed to download video: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("❌ server returned status: %s", resp.Status)
	}

	// Copy the response body to the file
	if _, err = io.Copy(f, resp.Body); err != nil {
		return fmt.Errorf("❌ failed to save video: %w", err)
	}

	log.Printf("💾 video saved: %s\n", filename)
	return nil
}

func parseCourseVideos(ctx context.Context, courseURL string) ([]VideoEntry, error) {
	log.Println("📚 Parsing course structure.")
	var videos []VideoEntry
	if err := chromedp.Run(ctx,
		chromedp.Navigate(courseURL),
		chromedp.WaitVisible(`section.classroom-toc-section`, chromedp.ByQuery),
		chromedp.Sleep(time.Second),
		chromedp.Evaluate(videoParseJS, &videos),
	); err != nil {
		return nil, err
	}
	for i, v := range videos {
		// Sigh. Sometimes LinkedIn Learning actually has bad URLs in courses.. catch them early here.
		u, err := url.Parse(v.Href)
		if err != nil {
			return nil, fmt.Errorf("❌ bad url: %w", err)
		}
		u.RawQuery = "" // Remove any query trash at the end.
		videos[i].Href = u.String()
		videos[i].filename = sanitizeFileName(fmt.Sprintf("%s.%02d.%s", v.Section, v.Index, v.Title))
	}

	return videos, nil
}

func ssoLogin(ctx context.Context, u string) error {
	log.Println("🚀 Logging in via SSO...")
	return chromedp.Run(ctx,
		chromedp.Navigate(u),
		chromedp.WaitVisible(`h3.chatbot-banner-dynamic__subheading-two`, chromedp.ByQuery),
	)
}

func newChromeDPCtx(to time.Duration) (context.Context, context.CancelFunc) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", false),
		chromedp.Flag("disable-gpu", false),
		chromedp.Flag("start-maximized", true),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, chromeCancel := chromedp.NewContext(allocCtx)
	ctx, timeoutCancel := context.WithTimeout(ctx, to)

	// Return a combined cancel function that calls all cancel funcs in reverse order.
	return ctx, func() {
		timeoutCancel()
		chromeCancel()
		allocCancel()
	}
}

func countSections(videos []VideoEntry) int {
	seen := make(map[string]struct{})
	for _, v := range videos {
		seen[v.Section] = struct{}{}
	}
	return len(seen)
}

// Eh. This is a bit of a hack, but LinkedIn Learning has a tendency to rate limit requests if you hit them too fast.
const maxRetry = 6

func visitVideo(ctx context.Context, href string, backoff time.Duration, count int) error {
	var (
		rateLimited   bool
		hasTranscript bool
	)
	if err := chromedp.Run(ctx,
		chromedp.Navigate(href),
		chromedp.Evaluate(`!!document.querySelector('.error-body')`, &rateLimited),
		chromedp.Evaluate(`!!document.querySelector("button[id*='TRANSCRIPT']")`, &hasTranscript),
	); err != nil {
		if count >= maxRetry {
			return fmt.Errorf("❌ navigation failed, stopping: %w", err)
		}
		log.Printf("❌ navigation failed (%v), retrying\n", err)
		time.Sleep(backoff)
		return visitVideo(ctx, href, backoff, count+1)
	}
	if rateLimited {
		log.Println("🚧 Rate limited. Sleeping a minute and retrying...")
		time.Sleep(backoff)
		return visitVideo(ctx, href, backoff, count+1)
	} else if !hasTranscript {
		return fmt.Errorf("⏭️ skipping (no transcript): %s", href)
	}

	return nil
}
