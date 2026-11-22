// scrapers/browser.go
package scrapers

import (
	"fmt"
	"sync"

	"github.com/mxschmitt/playwright-go"

	"github.com/yourname/job-scraper/utils"
)

// Browser is one headless Chromium shared by all Playwright scrapers. Each
// scrape task gets its own isolated context (cookies, user agent), which is
// far cheaper than launching a new Playwright driver and browser per task.
type Browser struct {
	installOnce sync.Once
	installErr  error

	mu      sync.Mutex
	pw      *playwright.Playwright
	browser playwright.Browser
}

func (b *Browser) get() (playwright.Browser, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.browser != nil && b.browser.IsConnected() {
		return b.browser, nil
	}
	b.closeLocked() // clean up a crashed browser before relaunching

	// Install the driver + Chromium lazily, so runs that never need a
	// browser (e.g. LinkedIn served over plain HTTP) skip this entirely.
	b.installOnce.Do(func() {
		b.installErr = playwright.Install(&playwright.RunOptions{Browsers: []string{"chromium"}})
	})
	if b.installErr != nil {
		return nil, fmt.Errorf("could not install playwright: %w", b.installErr)
	}

	pw, err := playwright.Run()
	if err != nil {
		return nil, fmt.Errorf("could not start playwright: %w", err)
	}
	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
		Args: []string{
			"--no-sandbox",
			"--disable-setuid-sandbox",
			"--disable-dev-shm-usage",
			"--disable-blink-features=AutomationControlled",
		},
	})
	if err != nil {
		pw.Stop()
		return nil, fmt.Errorf("could not launch browser: %w", err)
	}
	b.pw, b.browser = pw, browser
	return browser, nil
}

// NewPage opens a fresh browser context with a random user agent and returns
// a page in it with images and fonts blocked. Call the returned close func
// when done.
func (b *Browser) NewPage(extraHeaders map[string]string) (playwright.Page, func(), error) {
	browser, err := b.get()
	if err != nil {
		return nil, nil, err
	}
	ctx, err := browser.NewContext(playwright.BrowserNewContextOptions{
		UserAgent:        playwright.String(utils.RandomUserAgent()),
		Viewport:         &playwright.Size{Width: 1280, Height: 800},
		ExtraHttpHeaders: extraHeaders,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("could not create browser context: %w", err)
	}
	page, err := ctx.NewPage()
	if err != nil {
		ctx.Close()
		return nil, nil, fmt.Errorf("could not create page: %w", err)
	}
	// Only the HTML and scripts matter: skip images, fonts, media and CSS.
	// Failure here is harmless (pages just load slower).
	_ = page.Route("**/*", func(route playwright.Route) {
		if blockedResources[route.Request().ResourceType()] {
			_ = route.Abort()
			return
		}
		_ = route.Continue()
	})
	return page, func() { ctx.Close() }, nil
}

// blockedResources are request types not needed to read job cards.
var blockedResources = map[string]bool{"image": true, "font": true, "media": true, "stylesheet": true}

// Close shuts down the browser and Playwright driver, if started.
func (b *Browser) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closeLocked()
}

func (b *Browser) closeLocked() {
	if b.browser != nil {
		b.browser.Close()
		b.browser = nil
	}
	if b.pw != nil {
		b.pw.Stop()
		b.pw = nil
	}
}
