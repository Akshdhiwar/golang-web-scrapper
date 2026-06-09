package main

import (
	"fmt"
	"log"

	"github.com/playwright-community/playwright-go"
)

func main() {
	err := playwright.Install(&playwright.RunOptions{Browsers: []string{"chromium"}})
	if err != nil {
		log.Fatalf("could not install playwright: %v", err)
	}
	pw, err := playwright.Run()
	if err != nil {
		log.Fatalf("could not start playwright: %v", err)
	}
	defer pw.Stop()

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
		log.Fatalf("could not launch browser: %v", err)
	}
	defer browser.Close()

	context, err := browser.NewContext(playwright.BrowserNewContextOptions{
		UserAgent: playwright.String("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
		Viewport: &playwright.Size{
			Width:  1920,
			Height: 1080,
		},
	})
	if err != nil {
		log.Fatalf("could not create browser context: %v", err)
	}
	defer context.Close()

	page, err := context.NewPage()
	if err != nil {
		log.Fatalf("could not create page: %v", err)
	}
	defer page.Close()

	_, err = page.Goto("https://in.indeed.com/jobs?q=developer&l=India&sort=date", playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateNetworkidle,
	})
	if err != nil {
		log.Fatalf("could not goto: %v", err)
	}

	content, err := page.Content()
	if err != nil {
		log.Fatalf("could not get content: %v", err)
	}
	
	fmt.Printf("Content length: %d\n", len(content))
}
