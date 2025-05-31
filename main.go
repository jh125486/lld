package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

type VideoEntry struct {
	Href     string `json:"href"`
	Section  string `json:"section"`
	Index    int    `json:"index"`
	Duration string `json:"duration"`
}

func sanitizeFileName(s string) string {
	s = strings.ReplaceAll(s, " | LinkedIn Learning", "")
	s = strings.TrimSpace(s)
	re := regexp.MustCompile(`[^a-zA-Z0-9._-]+`)
	return re.ReplaceAllString(s, "_")
}

const videoParseJS = `(() => {
	const sections = Array.from(document.querySelectorAll("section.classroom-toc-section"));
	const results = [];
	for (const section of sections) {
	const title = section.querySelector(".classroom-toc-section__toggle-title")?.innerText.trim();
	const videos = section.querySelectorAll("li.classroom-toc-item");
	let index = 0;
	for (const video of videos) {
		const link = video.querySelector("a.classroom-toc-item__link");
		const spans = Array.from(video.querySelectorAll("span"))
		const duration = spans.map(el => el.innerText.trim())
		.find(text => text.toLowerCase().endsWith("video")) || ""
		if (!link) continue;
		index++;
		results.push({
		href: link.href,
		section: title,
		index: index,
		duration: duration.split(' ').slice(0, -1).join('')
		});
	}
	}
	return results;
})()`

func main() {
	courseURL := flag.String("course", "", "URL of the the course to download.")
	ssoURL := flag.String("sso", "", "URL to the enterprise SSO sign-on.")
	flag.Parse()

	ctx := newChromeDPCtx()

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
		log.Printf("▶️  [%d/%d] %v: %s \n", i+1, len(videos), video.Section, strings.TrimPrefix(video.Href, *courseURL))
		if err := downloadTranscript(ctx, video); err != nil {
			log.Printf("%v -> skipping.", err)
		}
	}

	log.Println("✅ All transcripts saved.")
}

func downloadTranscript(ctx context.Context, video VideoEntry) error {
	u, err := url.Parse(video.Href)
	if err != nil {
		return fmt.Errorf("❌ bad url: %w", err)
	}
	u.RawQuery = ""
	video.Href = u.String()

	if err := visitVideo(ctx, video, 0); err != nil {
		return fmt.Errorf("🙅 failed to visit video: %w", err)
	}

	var (
		title string
		lines []string
	)
	if err = chromedp.Run(ctx,
		chromedp.ScrollIntoView(`button[id*="TRANSCRIPT"]`, chromedp.ByQuery),
		chromedp.Click(`button[id*="TRANSCRIPT"]`, chromedp.ByQuery),
		chromedp.Sleep(2*time.Second),
		chromedp.WaitVisible(`.content-transcript-line`, chromedp.ByQuery),
		chromedp.Title(&title),
		chromedp.Evaluate(`Array.from(document.querySelectorAll('.content-transcript-line')).map(x => x.textContent.trim())`, &lines),
	); err != nil {
		return fmt.Errorf("⚠️ failed to scrape: %v", err)
	}

	cleanTitle := sanitizeFileName(title)
	cleanSection := sanitizeFileName(video.Section)

	filename := fmt.Sprintf("%s.%02d.%s.txt", cleanSection, video.Index, cleanTitle)
	content := fmt.Sprintln("Title:", strings.TrimSuffix(title, " | LinkedIn Learning"))
	content += fmt.Sprintln("URL:", video.Href)
	content += fmt.Sprintln("Duration:", video.Duration)
	content += fmt.Sprintln("Transcript:\n", strings.Join(lines, "\n"))

	os.WriteFile(filename, []byte(content), 0644)
	log.Printf("💾 saved %s\n", filename)

	return nil
}

func parseCourseVideos(ctx context.Context, courseURL string) ([]VideoEntry, error) {
	log.Println("📚 Parsing course structure.")
	var videos []VideoEntry
	if err := chromedp.Run(ctx,
		chromedp.Navigate(courseURL),
		chromedp.WaitVisible(`section.classroom-toc-section`, chromedp.ByQuery),
		chromedp.Sleep(1*time.Second),
		chromedp.Evaluate(videoParseJS, &videos),
	); err != nil {
		return nil, err
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

func newChromeDPCtx() context.Context {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", false),
		chromedp.Flag("disable-gpu", false),
		chromedp.Flag("start-maximized", true),
	)

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()
	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	ctx, cancel = context.WithTimeout(ctx, time.Hour)
	defer cancel()

	return ctx
}

func countSections(videos []VideoEntry) int {
	seen := make(map[string]struct{})
	for _, v := range videos {
		seen[v.Section] = struct{}{}
	}
	return len(seen)
}

const maxRetry = 6

func visitVideo(ctx context.Context, video VideoEntry, count int) error {
	var (
		rateLimited   bool
		hasTranscript bool
	)
	if err := chromedp.Run(ctx,
		chromedp.Navigate(video.Href),
		chromedp.Evaluate(`!!document.querySelector('.error-body')`, &rateLimited),
		chromedp.Evaluate(`!!document.querySelector("button[id*='TRANSCRIPT']")`, &hasTranscript),
	); err != nil {
		if count < maxRetry {
			log.Printf("❌ navigation failed (%v), retrying\n", err)
			time.Sleep(time.Minute)
			return visitVideo(ctx, video, count+1)
		}
		return fmt.Errorf("❌ navigation failed, stopping: %w", err)
	}
	if rateLimited {
		log.Println("🚧 Rate limited. Sleeping a minute and retrying...")
		time.Sleep(time.Minute)
		return visitVideo(ctx, video, count+1)
	} else if !hasTranscript {
		return fmt.Errorf("⏭️ skipping (no transcript): %s", video.Href)
	}

	return nil
}
